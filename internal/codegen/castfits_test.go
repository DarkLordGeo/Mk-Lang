// Why: a checked float-to-integer Cast is on hot paths; its range check must
// be one conditional branch before the conversion, not one per bound.
// What: TestFloatCastOneBranch over every float and integer width, with
// floatCastCases and the disassembly scanner branchesBeforeConvert.
// How: compiles each cast at -O2 for x86_64 and aarch64, counts branches before
// the conversion, and finds both exact tests of floatFits on the cold path.

package codegen

import (
	"regexp"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// floatCastCases are the checked casts of each float type to each integer
// type, named <from>To<to>.
func floatCastCases(e *testEnv) ([]*mir.Body, []string) {
	floats := []struct {
		name string
		t    types.TypeID
	}{{"F32", types.F32}, {"F64", types.F64}}
	ints := []struct {
		name string
		t    types.TypeID
	}{{"I8", types.I8}, {"I16", types.I16}, {"I32", types.I32}, {"I64", types.I64}, {"Int", types.Int},
		{"U8", types.U8}, {"U16", types.U16}, {"U32", types.U32}, {"U64", types.U64}, {"Uint", types.Uint}}
	var bodies []*mir.Body
	var names []string
	for _, f := range floats {
		for _, i := range ints {
			n := f.name + "To" + i.name
			bodies = append(bodies, castFn(e, n, f.t, i.t, mir.CastChecked))
			names = append(names, n)
		}
	}
	return bodies, names
}

// branchesBeforeConvert counts the conditional branches of fn (a
// disassembled function) before its first float-to-integer conversion, and
// reports whether it has one.
func branchesBeforeConvert(fn string, arm bool) (int, bool) {
	n := 0
	for _, ln := range strings.Split(fn, "\n") {
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		op := f[1]
		switch {
		case !arm && strings.Contains(op, "cvtts"), arm && strings.HasPrefix(op, "fcvtz"):
			return n, true
		case !arm && strings.HasPrefix(op, "j") && op != "jmp" && op != "jmpq":
			n++
		case arm && (strings.HasPrefix(op, "b.") || op == "cbz" || op == "cbnz" || op == "tbz" || op == "tbnz"):
			n++
		}
	}
	return n, false
}

func TestFloatCastOneBranch(t *testing.T) {
	bin := needLLVM(t)
	for _, target := range []types.Target{types.X86_64Windows, types.X86_64Linux, types.Aarch64Linux} {
		t.Run(target.Name, func(t *testing.T) {
			e := newTestEnv()
			bodies, names := floatCastCases(e)
			ll := emit(t, e, unitOf(e, bodies), target, types.Release)
			for _, name := range names {
				def := regexp.MustCompile(`(?s)define [^\n]*@mk\.main\.` + name + `\(.*?\n}\n`).FindString(ll)
				if def == "" {
					t.Fatalf("no definition of %s", name)
				}
				// The cold path keeps both exact tests, each its own trap.
				if got := strings.Count(def, "@mk.std.rt.PanicAt(i32 noundef 5,"); got != 2 {
					t.Errorf("%s: %d PanicCast traps, want 2:\n%s", name, got, def)
				}
			}
			dir := t.TempDir()
			obj := clangC(t, target, dir, "casts", ll, "-O2")
			dis := runTool(t, tool(bin, "llvm-objdump"), "-d", "--no-show-raw-insn", obj)
			arm := target.Arch == types.ArchArm64
			for _, name := range names {
				fn := carryTestFunction(dis, "mk.main."+name)
				if fn == "" {
					t.Fatalf("no %s in the disassembly:\n%s", name, dis)
				}
				n, ok := branchesBeforeConvert(fn, arm)
				if !ok {
					t.Errorf("%s: no conversion instruction:\n%s", name, fn)
					continue
				}
				if n != 1 {
					t.Errorf("%s: %d conditional branches before the conversion, want 1:\n%s", name, n, fn)
				}
			}
		})
	}
}
