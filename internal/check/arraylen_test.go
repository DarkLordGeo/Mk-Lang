// Why: an array length must be a const int of at least 0, a const argument
// that does not fold must be diagnosed (never a silent Poison), and a type
// name in value position must be an error, or lower meets an ICE.
// What: TestArrayLengthNonNegative, TestComputedArgDiagnosed,
// TestNegativeInstanceInItsOwnDeclaration, TestE0301_TypeIsNotAValue.
// How: checks small programs (`[!0]u8`, `[-1]u8`, `Buf[-1]` inside Buf) and
// compares the diagnostic codes, with no E0000 among them.

package check

import (
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/source"
	"mk/internal/types"
)

func TestArrayLengthNonNegative(t *testing.T) {
	const buf = "struct Buf[const N: int] {\n\tdata: [N]u8\n}\n"
	reject := []struct{ name, src, want string }{
		{"literal", "struct Buf {\n\tdata: [-1]u8\n}\n", "E0303@2:9"},
		{"folded", "struct Buf {\n\tdata: [0 - 1]u8\n}\n", "E0303@2:9"},
		{"complement", "struct Buf {\n\tdata: [~0]u8\n}\n", "E0303@2:9"},
		{"named const", "const N: int = -1\nstruct Buf {\n\tdata: [N]u8\n}\n", "E0303@3:9"},
		{"in a signature", "F(b: [-2]u8) {\n}\n", "E0303@1:7"},
		{"instance", buf + "struct W {\n\tb: Buf[-1]\n}\n", "E0303@5:5"},
		{"nested instance", buf + "struct Outer[const M: int] {\n\tb: Buf[M]\n}\nstruct W {\n\to: Outer[-2]\n}\n", "E0303@8:5"},
		{"alias", "type A[const N: int] = [N]u8\nstruct W {\n\to: A[-2]\n}\n", "E0303@3:5"},
		{"alias of an instance", buf + "type B[const N: int] = Buf[N]\nstruct W {\n\to: B[-2]\n}\n", "E0303@6:5"},
		{"enum payload", "enum E[const N: int] {\n\tA(x: [N]u8)\n\tB\n}\nstruct W {\n\to: E[-3]\n}\n", "E0303@6:5"},
		{"through a reference", buf + "struct W {\n\tb: ?&Buf[-1]\n}\n", "E0303@5:7"},
		{"self reference before the length", "struct Buf[const N: int] {\n\tnext: ?&Buf[-1]\n\tdata: [N]u8\n}\nstruct W {\n\tb: Buf[2]\n}\n", "E0303@2:10"},
		{"body type", "F(): int {\n\tb: [-1]u8 = []\n\treturn 0\n}\n", "E0303@2:6"},
		{"repeat count", "F(): int {\n\tb := [0; -1]\n\treturn b[0]\n}\n", "E0303@2:11"},
	}
	for _, tc := range reject {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			b := checkBody(t, w, tc.want, tc.src)
			b.layoutsClean(t)
		})
	}
	accept := []struct{ name, src, field, want string }{
		{"zero", "struct S {\n\tf: [0]u8\n}\n", "f", "[0]u8"},
		{"size of", "struct S {\n\tf: [SizeOf[u8]()]u8\n}\n", "f", ""},
		{"wrapping", "struct S {\n\tf: [1 +% 2]u8\n}\n", "f", "[3]u8"},
		{"complement wraps", "struct S {\n\tf: [~0 +% 2]u8\n}\n", "f", "[1]u8"},
		{"typed shift amount", "const K: u8 = 2\nstruct S {\n\tf: [1 << K]u8\n}\n", "f", "[4]u8"},
		{"instance", buf + "struct S {\n\tf: Buf[3]\n}\n", "f", "Buf[3]"},
		{"unused const parameter", "struct M[const N: int] {\n\tx: int\n}\nstruct S {\n\tf: M[-2]\n}\n", "f", "M[-2]"},
	}
	for _, tc := range accept {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			b := checkBody(t, w, "", tc.src)
			b.layoutsClean(t)
			if tc.want == "" {
				return
			}
			sd := b.decl("S").(*types.StructDecl)
			if got := b.ts(sd.Fields[0].Type, nil); got != tc.want {
				t.Fatalf("field %s is %s, want %s", tc.field, got, tc.want)
			}
		})
	}
}

// TestComputedArgDiagnosed: a computed const argument check does not
// fold has a diagnostic: the fold's reason in the body's codes, else E0705.
func TestComputedArgDiagnosed(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"not on int", "struct Buf {\n\tdata: [!0]u8\n}\n", "E0308@2:9"},
		{"fuzz crasher", "struct Buf{data:[!0]u8\na00:int}\nBuf.A000(mut self){return self.data}", "E0308@1:18"},
		{"minus on bool", "struct Buf {\n\tdata: [-true]u8\n}\n", "E0308@2:9"},
		{"minus on unsigned", "const U: u8 = 1\nstruct Buf {\n\tdata: [-U]u8\n}\n", "E0308@3:9"},
		{"and on int", "struct Buf {\n\tdata: [1 && 2]u8\n}\n", "E0308@2:9"},
		{"plus on bool", "struct Buf {\n\tdata: [true + false]u8\n}\n", "E0308@2:9"},
		{"int and bool", "struct Buf {\n\tdata: [1 + true]u8\n}\n", "E0301@2:13"},
		{"typed operands", "const A: i32 = 1\nconst B: i64 = 2\nstruct Buf {\n\tdata: [A + B]u8\n}\n", "E0301@4:13"},
		{"float", "struct Buf {\n\tdata: [1.5]u8\n}\n", "E0301@2:9"},
		{"string", "struct Buf {\n\tdata: [\"ab\"]u8\n}\n", "E0301@2:9"},
		{"too large", "struct Buf {\n\tdata: [99999999999999999999 + 1]u8\n}\n", "E0303@2:9"},
		{"method call", "struct Buf {\n\tdata: [\"ab\".Len()]u8\n}\n", "E0705@2:9"},
		// A bool literal is a literal of the wrong type, as 1.5 and "ab"
		// are (E0705 before).
		{"bool name", "struct Buf {\n\tdata: [true]u8\n}\n", "E0301@2:9"},
		{"index", "const T: [2]int = [1, 2]\nstruct Buf {\n\tdata: [T[0]]u8\n}\n", "E0705@3:9"},
		{"body type", "F(): int {\n\tb: [!0]u8 = []\n\treturn int(b[0])\n}\n", "E0308@2:6"},
		{"repeat count", "F(): int {\n\tb := [0; !0]\n\treturn b[0]\n}\n", "E0308@2:11"},
		{"local", "F(n: int): int {\n\tb: [n + 1]u8 = []\n\treturn int(b[0])\n}\n", "E0705@2:6"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			o := opt{parseErrors: true}
			b := w.add("m", o, tc.src)
			var got []string
			for _, d := range append(b.diags(), b.bodyDiags(t)...) {
				if d.Code.String() != "E0208" && d.Code.String() != "E0101" {
					got = append(got, d.Code.String()+"@"+w.lc(d.At))
				}
			}
			if g := strings.Join(got, " "); !strings.Contains(g, tc.want) {
				t.Fatalf("diagnostics %q, want %s", g, tc.want)
			}
			b.layoutsClean(t)
		})
	}
}

// TestNegativeInstanceInItsOwnDeclaration is a FuzzCheck ICE
// (kept input ffede09c4eb7f4e1, `struct Buf[const N:int]{:[N Buf[-1`):
// an instance with a negative length written inside its own declaration is
// checked once the declaration is done (negativeInstance's queue), so the
// field keeps the type `Buf[-1]`, and laying out Buf[-1] or anything holding
// it reached `[-1]u8` and check's ICE. Now E0303 at the instance, E0907
// (Buf[-1] holds itself by value) and no E0000. The second group is the
// E0907 half alone: an instance of a declaration inside that declaration,
// by value, holds itself, though the identity instance never comes back
// (`struct Buf[T] { x: Buf[int] }` was accepted).
func TestNegativeInstanceInItsOwnDeclaration(t *testing.T) {
	const own = "struct Buf[const N: int] {\n\tx: Buf[-1]\n\ty: [N]u8\n}\n"
	cases := []struct{ name, src, want string }{
		{"field", own, "E0303@2:5 E0907@1:8"},
		{"array element", "struct Buf[const N: int] {\n\tx: [N]Buf[-1]\n}\n", "E0303@2:8 E0907@1:8"},
		{"length after the instance", "struct Buf[const N: int] {\n\ty: [N]u8\n\tx: Buf[-1]\n}\n", "E0303@3:5 E0907@1:8"},
		{"user after", own + "struct W {\n\tb: Buf[2]\n}\n", "E0303@2:5 E0907@1:8"},
		{"user before", "struct W {\n\tb: Buf[2]\n}\n" + own, "E0303@5:5 E0907@4:8"},
		{"fuzz input", "struct Buf[const N:int]{:[N Buf[-1", "parse"},
		{"closed instance of itself", "struct Buf[T] {\n\tx: Buf[int]\n\ty: T\n}\n", "E0907@1:8"},
		{"open instance of itself", "struct R[T, U] {\n\tx: ?R[T, int]\n}\n", "E0907@1:8"},
		{"through another declaration", "struct P[T] {\n\tq: ?Q[T]\n}\nstruct Q[T] {\n\tp: P[int]\n}\n", "E0907@1:8"},
		{"behind a reference is finite", "struct Buf[T] {\n\tx: ?&Buf[int]\n\ty: T\n}\n", ""},
		{"nested arguments are finite", "struct S[T] {\n\tx: T\n}\nstruct W {\n\ts: S[S[int]]\n}\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			if tc.want == "parse" {
				// Parse errors: the fuzz harness checks anyway, as here.
				b := w.add("m", opt{parseErrors: true}, tc.src)
				for _, d := range b.diags() {
					if d.Code == diag.E0000 {
						t.Fatalf("ICE: %s", d.Message)
					}
				}
				b.layoutsClean(t)
				return
			}
			b := checkCodes(t, w, "m", opt{}, tc.want, tc.src)
			b.layoutsClean(t)
		})
	}
}

// layoutsClean lays out every non-generic struct and enum of the module
// under a guard: no ICE (the old arrayLen one on a negative length).
func (b *built) layoutsClean(t *testing.T) {
	t.Helper()
	for _, ii := range b.info.Items {
		it := b.w.items.Get(ii.Item)
		if (it.Kind != types.KindStruct && it.Kind != types.KindEnum) || len(allGenerics(it.Decl)) > 0 {
			continue
		}
		nt := b.w.tab.Named(it.ID, nil)
		if ie := source.Guard(it.Key.String(), it.Span, func() { b.w.c.Layout(nt) }); ie != nil {
			t.Fatalf("ICE laying out %s: %v", it.Key, ie)
		}
	}
}

// TestE0301_TypeIsNotAValue: a name of a type, an interface or a type
// parameter in value position is E0301, never a silent Poison (FuzzLower's
// `scope b := Scope {}` reached lower's "a scope's value is not a call").
func TestE0301_TypeIsNotAValue(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"scope over a type", "A(a0: Io) {\n\tscope b := Scope {\n\t}\n}\n", "E0301@2:13"},
		{"struct", "struct P {\n\tx: int\n}\nA(): int {\n\tq := P\n\treturn 0\n}\n", "E0301@5:7"},
		{"type parameter", "A[T](): int {\n\tq := T\n\treturn 0\n}\n", "E0301@2:7"},
		{"imported struct", "import lib\nA(): int {\n\tq := lib.P\n\treturn 0\n}\n", "E0301@3:7"},
		{"broken const is its own diagnostic", "const N: Nope = 1\nA(): int {\n\tq := N\n\treturn 0\n}\n", "E0201@1:10"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			w.add("lib", opt{}, "export struct P {\n\tx: int\n}\n")
			checkBody(t, w, tc.want, tc.src)
		})
	}
}
