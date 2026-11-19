// Why: an intrinsic is only worth having if LLVM's backend emits the
// instruction it stands for.
// What: TestSHA1_IntrinsicsObject: std.raw's SHA-1 intrinsics become
// sha1rnds4 with each round group's immediate, sha1nexte, sha1msg1 and
// sha1msg2 on x86_64, and sha1c, sha1p, sha1m, sha1h, sha1su0 and sha1su1 on
// aarch64, as sha256_obj_test.go does for SHA-256.
// How: Sha1Rounds4's round group is a const argument, so each group is its
// own function; objects are disassembled.

package codegen

import (
	"fmt"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// s1Object emits, for target, UseSha1Rounds4G0 to G3 (the round group an
// immediate), UseSha1NextE and UseSha1Schedule4, each passing its
// parameters to the intrinsic and returning the result, compiles the IR
// with clang at -O0 and -O2, and returns the -O2 object's disassembly.
func s1Object(t *testing.T, target types.Target) string {
	t.Helper()
	bin := needLLVM(t)
	ll := s1IR(t, target)
	dir := t.TempDir()
	clangC(t, target, dir, "o0", ll, "-O0")
	obj := clangC(t, target, dir, "o2", ll, "-O2")
	return runTool(t, tool(bin, "llvm-objdump"), "-d", "--no-show-raw-insn", obj)
}

// s1IR is s1Object's unit as release LLVM IR for target, with no LLVM
// tool: the intrinsics' helpers are mk.intr.raw.Sha1Rounds4.0 to .3,
// mk.intr.raw.Sha1NextE and mk.intr.raw.Sha1Schedule4.
func s1IR(t *testing.T, target types.Target) string {
	t.Helper()
	e := newTestEnv()
	w := objArr(e, 4, types.U32)
	var bodies []*mir.Body
	var uses []types.ItemID
	add := func(fnName string, id types.ItemID, n int, extra ...mir.Operand) {
		ps := make([]types.TypeID, n)
		for i := range ps {
			ps[i] = w
		}
		f := e.body(e.fn(fnName, ps, w, 0))
		var ops []mir.Operand
		for i := range ps {
			ops = append(ops, mir.Copy(place(mir.LocalID(i+1))))
		}
		v := f.call(id, nil, w, append(ops, extra...)...)
		f.assign(place(0), use(mir.Copy(place(v))), f.at(21, 1))
		f.ret(f.at(21, 1))
		bodies = append(bodies, f.b)
	}
	rounds := e.stdFn("std.raw", "", "Sha1Rounds4", 0, []types.TypeID{w, w, types.U8}, w)
	nextE := e.stdFn("std.raw", "", "Sha1NextE", 0, []types.TypeID{w, w}, w)
	sched := e.stdFn("std.raw", "", "Sha1Schedule4", 0, []types.TypeID{w, w, w, w}, w)
	uses = append(uses, rounds, nextE, sched)
	for g := uint64(0); g < 4; g++ {
		add(fmt.Sprintf("UseSha1Rounds4G%d", g), rounds, 2, mir.Imm(types.U8, g))
	}
	add("UseSha1NextE", nextE, 2)
	add("UseSha1Schedule4", sched, 4)
	u := unitOf(e, bodies)
	for _, id := range uses {
		u.Funcs = append(u.Funcs, Func{Symbol: "-", Key: e.items.Get(id).Key.String(), Item: id, Pack: -1})
	}
	return emit(t, e, u, target, types.Release)
}

// TestSHA1_IntrinsicsObject: each intrinsic, called from a function
// without the feature (so through its helper compiled with it), compiles
// for x86_64-linux, x86_64-windows and aarch64-linux (sha2), and the -O2
// object holds its instructions; on x86_64 each round group's sha1rnds4
// carries its own immediate, 0 to 3.
func TestSHA1_IntrinsicsObject(t *testing.T) {
	x86 := map[string]int{"sha1rnds4": 4, "sha1nexte": 1, "sha1msg1": 1, "sha1msg2": 1}
	for _, c := range []struct {
		target types.Target
		want   map[string]int
	}{
		{types.X86_64Linux, x86},
		{types.X86_64Windows, x86},
		{types.Aarch64Linux, map[string]int{"sha1c": 1, "sha1p": 1, "sha1m": 1, "sha1h": 1, "sha1su0": 1, "sha1su1": 1}},
	} {
		t.Run(c.target.Name, func(t *testing.T) {
			t.Parallel()
			var names []string
			for m := range c.want {
				names = append(names, m)
			}
			dis := s1Object(t, c.target)
			got := objMnemonics(dis, names)
			t.Logf("%v", got)
			for m, n := range c.want {
				if got[m] < n {
					t.Errorf("%s: %d %s, want at least %d (all: %v)", c.target.Name, got[m], m, n, got)
				}
			}
			if c.target.Arch != types.ArchX64 {
				return
			}
			imms := map[string]bool{}
			for _, ln := range strings.Split(dis, "\n") {
				if f := strings.Fields(ln); len(f) >= 3 && f[1] == "sha1rnds4" {
					imms[strings.TrimSuffix(f[2], ",")] = true
				}
			}
			for g := 0; g < 4; g++ {
				if !imms[fmt.Sprintf("$0x%x", g)] {
					t.Errorf("no sha1rnds4 with immediate %d (immediates seen: %v)", g, imms)
				}
			}
		})
	}
}
