// Why: the checked f64 to f32 Cast is on hot paths; its range check must be
// one compare and one branch before the conversion, not two compares after.
// What: TestF64ToF32CastHotPath and its disassembly scanner beforeF32Convert.
// How: compiles the cast at -O2 for x86_64 and aarch64, counts compares and
// branches before the conversion, and finds one PanicCast trap in the IR.

package codegen

import (
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// beforeF32Convert counts the compares and the conditional branches of fn
// (a disassembled function) before its first f64 to f32 conversion, and
// reports whether it has one.
func beforeF32Convert(fn string, arm bool) (compares, branches int, ok bool) {
	for _, ln := range strings.Split(fn, "\n") {
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		op := f[1]
		switch {
		case !arm && strings.HasPrefix(op, "cvtsd2ss"), !arm && strings.HasPrefix(op, "vcvtsd2ss"):
			return compares, branches, true
		case arm && op == "fcvt" && len(f) > 2 && strings.HasPrefix(f[2], "s"):
			return compares, branches, true
		case !arm && (strings.HasPrefix(op, "ucomis") || strings.HasPrefix(op, "comis") || strings.HasPrefix(op, "vucomis")):
			compares++
		case !arm && strings.HasPrefix(op, "j") && op != "jmp" && op != "jmpq":
			branches++
		case arm && (op == "fcmp" || op == "fcmpe"):
			compares++
		case arm && (strings.HasPrefix(op, "b.") || op == "cbz" || op == "cbnz" || op == "tbz" || op == "tbnz"):
			branches++
		}
	}
	return compares, branches, false
}

func TestF64ToF32CastHotPath(t *testing.T) {
	bin := needLLVM(t)
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux, types.Aarch64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			e := newTestEnv()
			body := castFn(e, "F64ToF32", types.F64, types.F32, mir.CastChecked)
			ll := emit(t, e, unitOf(e, []*mir.Body{body}), target, types.Release)
			def := regexp.MustCompile(`(?s)define [^\n]*@mk\.main\.F64ToF32\(.*?\n}\n`).FindString(ll)
			if def == "" {
				t.Fatalf("no definition of F64ToF32:\n%s", ll)
			}
			if got := strings.Count(def, "@mk.std.rt.PanicAt(i32 noundef 5,"); got != 1 {
				t.Errorf("%d PanicCast traps, want 1:\n%s", got, def)
			}
			if !strings.Contains(def, "0x47EFFFFFF0000000") {
				t.Errorf("no compare with f32RoundsToInf (0x47EFFFFFF0000000):\n%s", def)
			}
			obj := clangC(t, target, t.TempDir(), "castf32", ll, "-O2")
			dis := runTool(t, tool(bin, "llvm-objdump"), "-d", "--no-show-raw-insn", obj)
			fn := carryTestFunction(dis, "mk.main.F64ToF32")
			if fn == "" {
				t.Fatalf("no F64ToF32 in the disassembly:\n%s", dis)
			}
			c, b, ok := beforeF32Convert(fn, target.Arch == types.ArchArm64)
			if !ok {
				t.Fatalf("no f64 to f32 conversion:\n%s", fn)
			}
			if c != 1 || b != 1 {
				t.Errorf("%d compares and %d conditional branches before the conversion, want 1 and 1:\n%s", c, b, fn)
			}
		})
	}
}
