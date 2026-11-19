// Why: three ABI defects must stay fixed, and the clang records must stay
// tied to the probe that wrote them.
// What: TestABINestedPackedIsMemory (SysV MEMORY for a leaf unaligned at its
// absolute offset), TestABIVariadicWithoutArguments and
// TestABIProbeHashMatches.
// How: lowers suite rows by name (suiteRow), and hashes the probe named in
// each record, found under the repository or MK_EVIDENCE.

package abi

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"mk/internal/types"
)

// suiteRow returns the suite row called name.
func suiteRow(t *testing.T, w *world, name string) row {
	t.Helper()
	for _, r := range w.rows {
		if r.name == name {
			return r
		}
	}
	t.Fatalf("no suite row %s", name)
	return row{}
}

// TestABINestedPackedIsMemory: on x86_64 System V a packed struct nested at
// an odd offset (InP at 1 in OutP, in an array in OutA, as the payload of
// ?InP, and in a packed parent OutPP) has a u32 leaf at an absolute offset of
// 1, so the value is MEMORY: byval as a parameter, sret as a result, and on
// the stack once five INTEGER registers are taken. The same InP at offset 4
// (OutP4) stays INTEGER. Win64 and AAPCS64 have no unaligned rule.
func TestABINestedPackedIsMemory(t *testing.T) {
	w := loadSuite(t)
	sysv := types.X86_64Linux
	for _, name := range []string{"OutP", "OutA", "OutPP", "OptInP"} {
		p := suiteRow(t, w, "p_"+name)
		s := lowerRow(w, sysv, p)
		if s.Params[0].Kind != Indirect || !s.Params[0].Byval || s.Params[0].Align != 8 {
			t.Errorf("SysV p_%s: param %+v, want Indirect byval align 8", name, s.Params[0])
		}
		if c := Classify(w.tt, w.layout, sysv, p.params[0]); c.Kind != types.AbiIndirect || !c.Byval {
			t.Errorf("SysV Classify(%s) = %+v, want indirect byval", name, c)
		}
		r := suiteRow(t, w, "r_"+name)
		if s := lowerRow(w, sysv, r); s.Ret.Kind != Indirect || s.Ret.Align != 1 {
			t.Errorf("SysV r_%s: result %+v, want Indirect (sret) align 1", name, s.Ret)
		}
		// AAPCS64 has no unaligned rule: a 5-byte composite is one i64.
		if s := lowerRow(w, types.Aarch64Linux, p); s.Params[0].Kind != Direct {
			t.Errorf("AAPCS64 p_%s: param %+v, want Direct", name, s.Params[0])
		}
	}
	exh := lowerRow(w, sysv, suiteRow(t, w, "exh_OptInP"))
	if last := exh.Params[5]; last.Kind != Indirect || !last.Byval {
		t.Errorf("SysV exh_OptInP: ?InP %+v, want byval on the stack (not r9)", last)
	}
	ok := lowerRow(w, sysv, suiteRow(t, w, "p_OutP4"))
	if p := ok.Params[0]; p.Kind != Direct || len(p.Parts) != 1 || p.Parts[0] != intPart(8) {
		t.Errorf("SysV p_OutP4: %+v, want Direct i64 (InP at an aligned offset)", p)
	}
}

// TestABIVariadicWithoutArguments: Lower reports a variadic signature when
// the function is variadic and no variadic argument is given (the
// declaration of extern fn printf(fmt: *u8, ...), or the call printf("hi")),
// on every target, and the fixed parameters lower as in a non-variadic one.
func TestABIVariadicWithoutArguments(t *testing.T) {
	w := newWorld()
	fmtPtr := w.tt.RawPtr(types.U8)
	for _, target := range abiTargets() {
		decl := Lower(w.tt, w.layout, target, []types.TypeID{fmtPtr}, 1, true, types.I32)
		if !decl.Variadic || decl.Fixed != 1 || len(decl.Params) != 1 {
			t.Errorf("%s: printf declaration lowered as %+v, want variadic with 1 fixed", target.Name, decl)
		}
		plain := Lower(w.tt, w.layout, target, []types.TypeID{fmtPtr}, 1, false, types.I32)
		if plain.Variadic {
			t.Errorf("%s: non-variadic signature reported variadic", target.Name)
		}
		if toClass(plain.Params[0]) != toClass(decl.Params[0]) || toClass(plain.Ret) != toClass(decl.Ret) {
			t.Errorf("%s: fixed parameters lower differently when variadic", target.Name)
		}
		call := Lower(w.tt, w.layout, target, []types.TypeID{fmtPtr, types.I64}, 1, true, types.I32)
		if !call.Variadic || call.Fixed != 1 {
			t.Errorf("%s: printf call with one variadic argument lowered as %+v", target.Name, call)
		}
	}
	// A non-variadic signature must give every parameter as fixed.
	defer func() {
		if recover() == nil {
			t.Error("Lower accepted fixed < len(params) for a non-variadic signature")
		}
	}()
	Lower(w.tt, w.layout, types.X86_64Linux, []types.TypeID{fmtPtr, types.I64}, 1, false, types.I32)
}

// moduleRoot is the repository root, two levels above this package.
const moduleRoot = "../.."

// evidenceVar names the evidence directory in a record's "# probe:" line.
const evidenceVar = "$MK_EVIDENCE"

// evidenceRoot is the evidence directory: MK_EVIDENCE, or Mk-evidence next
// to the repository when it is unset.
func evidenceRoot() string {
	if d := os.Getenv("MK_EVIDENCE"); d != "" {
		return d
	}
	return filepath.Join(moduleRoot, "..", "Mk-evidence")
}

// probeHash is the SHA-256 of a probe source, CRLF read as LF (so a checkout
// that converts line endings hashes the same), as the probe computes it.
func probeHash(b []byte) string {
	sum := sha256.Sum256([]byte(strings.ReplaceAll(string(b), "\r\n", "\n")))
	return hex.EncodeToString(sum[:])
}

// TestABIProbeHashMatches: every clang record names the probe that wrote it
// by a path that exists (relative to the repository root, or under
// $MK_EVIDENCE) and records its SHA-256, which equals the file's. So the
// records, the only evidence that abi agrees with clang, can be regenerated
// from the probe they name, and a probe edited without rerunning it is
// caught.
func TestABIProbeHashMatches(t *testing.T) {
	for _, target := range abiTargets() {
		t.Run(target.Name, func(t *testing.T) {
			o, err := readOracle(oraclePath(target))
			if err != nil {
				if target.Arch == types.ArchX64 {
					t.Fatalf("required clang record: %v", err)
				}
				t.Skipf("no clang record: %v", err)
			}
			var path, hash string
			for _, h := range o.header {
				if v, ok := strings.CutPrefix(h, "# probe: "); ok {
					path, _, _ = strings.Cut(v, " ")
				}
				if v, ok := strings.CutPrefix(h, "# probe-sha256: "); ok {
					hash = strings.TrimSpace(v)
				}
			}
			if path == "" || hash == "" {
				t.Fatalf("record header lacks # probe: or # probe-sha256: %q", o.header)
			}
			// The probe writes C, so it lives outside the repository,
			// named as $MK_EVIDENCE/abi-oracle/probe.go. A machine
			// without the evidence directory cannot check the hash.
			rest, outside := strings.CutPrefix(path, evidenceVar+"/")
			if outside {
				path = filepath.Join(evidenceRoot(), filepath.FromSlash(rest))
			} else {
				path = filepath.Join(moduleRoot, filepath.FromSlash(path))
			}
			b, err := os.ReadFile(path)
			if err != nil {
				if outside && os.IsNotExist(err) {
					t.Skipf("the probe %s is outside the repository and not on this machine", path)
				}
				t.Fatalf("the probe named by the record: %v", err)
			}
			if got := probeHash(b); got != hash {
				t.Errorf("probe %s has SHA-256 %s, the record says %s: rerun the probe", path, got, hash)
			}
		})
	}
}
