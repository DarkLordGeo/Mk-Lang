// Why: without "tune-cpu", LLVM lowers a rotate to shld, which is slower.
// What: TestTuneCPU and TestTunePerLevel: every Mk function and every
// support IR function carries "tune-cpu" next to "target-cpu" (levelTunes).
// How: a rotate (raw.RotL64, llvm.fshl(x, x, c)) is compiled by clang -O2 -S
// and must be rol (rorxq at x86-64-v3 and v4), not shld.

package codegen

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// rotBody: Rot(x: u64): u64 { return raw.RotL64(x, 13) }
func rotBody(e *testEnv) *mir.Body {
	rot := e.fnIn("std.raw", "", "RotL64", []types.TypeID{types.U64, types.Int}, types.U64, true)
	f := e.body(e.fn("Rot", []types.TypeID{types.U64}, types.U64, 0))
	f.callTo(direct(rot), place(0), mir.NoLocal, f.at(2, 10), mir.Copy(place(1)), mir.Imm(types.Int, 13))
	f.ret(f.at(2, 3))
	return f.b
}

func TestTuneCPU(t *testing.T) {
	for cpu, want := range levelTunes {
		if got := tuneCPU(cpu); got != want {
			t.Errorf("tuneCPU(%s) = %q, want %q", cpu, got, want)
		}
	}
	if got := tuneCPU("znver4"); got != "znver4" {
		t.Errorf("tuneCPU(znver4) = %q, want znver4", got)
	}
	define := regexp.MustCompile(`(?m)^define .*$`)
	attrGroup := regexp.MustCompile(`(?m)^attributes #\d+ = \{.*\}$`)
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			e := newTestEnv()
			u := unitOf(e, []*mir.Body{rotBody(e)})
			var pack, sup bytes.Buffer
			cfg := Config{Target: target, Profile: types.Release, CPU: "x86-64-v2"}
			if _, err := EmitPack(u, 0, e, cfg, &pack); err != nil {
				t.Fatal(err)
			}
			if err := EmitSupport(&Unit{}, nil, cfg, &sup); err != nil {
				t.Fatal(err)
			}
			for name, ll := range map[string]string{"pack": pack.String(), "support": sup.String()} {
				defs := define.FindAllString(ll, -1)
				if len(defs) == 0 {
					t.Fatalf("%s: no define", name)
				}
				// Every attribute group naming target-cpu also names tune-cpu.
				for _, g := range attrGroup.FindAllString(ll, -1) {
					if strings.Contains(g, `"target-cpu"="x86-64-v2"`) && !strings.Contains(g, `"tune-cpu"="generic"`) {
						t.Errorf("%s: %s has no tune-cpu", name, g)
					}
				}
				if !strings.Contains(ll, `"tune-cpu"="generic"`) {
					t.Errorf("%s: no tune-cpu at all", name)
				}
			}
			// clang -O2 -S: the rotate is rol, never shld.
			bin := needLLVM(t)
			dir := t.TempDir()
			src := filepath.Join(dir, "rot.ll")
			if err := os.WriteFile(src, pack.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			asm := filepath.Join(dir, "rot.s")
			runTool(t, tool(bin, "clang"), "--target="+target.LLVMTriple, "-O2", "-S", "-Wno-override-module", "-o", asm, src)
			b, err := os.ReadFile(asm)
			if err != nil {
				t.Fatal(err)
			}
			s := string(b)
			if strings.Contains(s, "shld") || !strings.Contains(s, "rolq") {
				t.Errorf("raw.RotL64 is not rolq at -O2:\n%s", s)
			}
		})
	}
}

// levelTunes is the "tune-cpu" of each x86-64 ISA level and of "generic":
// x86-64 and v2 are tuned as generic (their own tuning turns a constant
// rotate into shld), v3 and v4 as themselves, as clang's
// -march=x86-64-v3 tunes C (it passes no -tune-cpu).
var levelTunes = map[string]string{
	"generic":   "generic",
	"x86-64":    "generic",
	"x86-64-v2": "generic",
	"x86-64-v3": "x86-64-v3",
	"x86-64-v4": "x86-64-v4",
}

// TestTunePerLevel: at each --cpu level, every function definition
// (its attributes are written inline) and every attribute group of the
// pack and of the support pack that names the level as "target-cpu" names
// levelTunes' "tune-cpu", and no other tune; at v3 and v4 clang -O2 still
// compiles raw.RotL64(x, 13) without shld (rorxq, BMI2).
func TestTunePerLevel(t *testing.T) {
	attrGroup := regexp.MustCompile(`(?m)^(?:define .*|attributes #\d+ = \{.*\})$`)
	tuneAttr := regexp.MustCompile(`"tune-cpu"="([^"]*)"`)
	for _, cpu := range []string{"x86-64", "x86-64-v2", "x86-64-v3", "x86-64-v4"} {
		want := levelTunes[cpu]
		t.Run(cpu, func(t *testing.T) {
			e := newTestEnv()
			u := unitOf(e, []*mir.Body{rotBody(e)})
			var pack, sup bytes.Buffer
			cfg := Config{Target: types.X86_64Windows, Profile: types.Release, CPU: cpu}
			if _, err := EmitPack(u, 0, e, cfg, &pack); err != nil {
				t.Fatal(err)
			}
			if err := EmitSupport(&Unit{}, nil, cfg, &sup); err != nil {
				t.Fatal(err)
			}
			for name, ll := range map[string]string{"pack": pack.String(), "support": sup.String()} {
				n := 0
				for _, g := range attrGroup.FindAllString(ll, -1) {
					if !strings.Contains(g, `"target-cpu"="`+cpu+`"`) {
						continue
					}
					n++
					m := tuneAttr.FindAllStringSubmatch(g, -1)
					if len(m) != 1 || m[0][1] != want {
						t.Errorf("%s: %s: tune-cpu %v, want exactly %q", name, g, m, want)
					}
				}
				if n == 0 {
					t.Errorf("%s: no definition or attribute group names target-cpu %s", name, cpu)
				}
			}
			if cpu != "x86-64-v3" && cpu != "x86-64-v4" {
				return
			}
			bin := needLLVM(t)
			dir := t.TempDir()
			src := filepath.Join(dir, "rot.ll")
			if err := os.WriteFile(src, pack.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			asm := filepath.Join(dir, "rot.s")
			runTool(t, tool(bin, "clang"), "--target="+types.X86_64Windows.LLVMTriple, "-O2", "-S", "-Wno-override-module", "-o", asm, src)
			b, err := os.ReadFile(asm)
			if err != nil {
				t.Fatal(err)
			}
			if s := string(b); strings.Contains(s, "shld") || !strings.Contains(s, "rorxq") {
				t.Errorf("raw.RotL64 at %s is not rorxq at -O2:\n%s", cpu, s)
			}
		})
	}
}
