// Why: a const value argument that mono cannot make a constant per instance
// must be refused at check, not fail later in the driver.
// What: TestConstArgForms.
// How: checks each argument form and expects E0705 or a clean check.

package check

import (
	"strings"
	"testing"
)

// TestConstArgForms: a const value argument that mono cannot make a
// constant per instance is E0705 at check (before, every one was E0000 in
// the driver's instance-limit walk), and a closed one that check folds is
// accepted. The forms mono already folded (`N + 1`, `2 * N`,
// `P(x: N, y: 1)`, `o.v`) stay accepted.
func TestConstArgForms(t *testing.T) {
	const head = "struct P {\n\tx: int\n\ty: int\n}\n\n" + // lines 1-4
		"const K: int = 7\nconst K8: u8 = 3\nconst KP: P = P(x: 1, y: 2)\nconst Tab: [3]int = [1, 2, 3]\n\n" + // lines 6-9
		"Lane(const i: int): int {\n\treturn i\n}\n\n" + // lines 11-13
		"LaneB(const b: bool): int {\n\treturn 0\n}\n\n" + // lines 15-17
		"LaneP(const p: P): int {\n\treturn p.x\n}\n\n" // lines 19-21
	const gen = "G[const N: int, const B: bool, const N8: u8, T](): int {\n\treturn " // line 23
	// After the call's function, so the lines above do not move.
	const tail = "\nconst U8: u8 = 200\nconst NK: int = -7\n\nLane8(const i: u8): int {\n\treturn int(i)\n}\n\n" +
		"H[X](x: int): int {\n\treturn x + 1\n}\n\nstruct PF {\n\tf: fn(int): int\n}\n\n" +
		"LaneF(const p: PF): int {\n\treturn p.f(1)\n}\n\nAp[F: Plain](const f: F): int {\n\treturn 1\n}\n\n" +
		"Ar(const a: [2]fn(int): int): int {\n\treturn a[0](1)\n}\n\nRc(const r: (f: fn(int): int, n: int)): int {\n\treturn r.f(r.n)\n}\n"
	for _, tc := range []struct{ name, call, want string }{
		// Closed: folded by check, accepted.
		{"two literals", "Lane(2 + 3)", ""},
		{"negated const", "Lane(-K)", ""},
		{"conversion", "Lane(int(K8))", ""},
		{"array length", "Lane(Tab.Len())", ""},
		{"division", "Lane(K / 2)", ""},
		{"SizeOf of a closed type, plus", "Lane(SizeOf[u32]() + 1)", ""},
		{"bool not", "LaneB(!true)", ""},
		{"bool comparison", "LaneB(K > 3)", ""},
		{"struct of a closed operator", "LaneP(P(x: 1 + 2, y: 3))", ""},
		// Open forms mono folds per instance: accepted, as before.
		{"plus", "Lane(N + 1)", ""},
		{"times, literal first", "Lane(2 * N)", ""},
		{"struct of an open operator", "LaneP(P(x: N + 1, y: 1))", ""},
		{"open plus a closed operator", "Lane(N + (2 + 3))", ""},
		// Neither: E0705 at the part mono cannot fold.
		{"negated generic", "Lane(-N)", "E0705@24:14"},
		{"open division", "Lane(N / 2)", "E0705@24:14"},
		{"open shift", "Lane(N << 1)", "E0705@24:14"},
		{"open conversion", "Lane(int(N8))", "E0705@24:14"},
		{"open comparison", "LaneB(N > 0)", "E0705@24:15"},
		{"open not", "LaneB(!B)", "E0705@24:15"},
		{"SizeOf of a type parameter", "Lane(SizeOf[T]())", "E0705@24:14"},
		{"SizeOf of a type parameter, plus", "Lane(SizeOf[T]() + 1)", "E0705@24:14"},
		{"wrapping plus", "Lane(N +% 1)", "E0705@24:14"},
		{"index of a const", "Lane(Tab[1])", "E0705@24:14"},
		{"field of a named const", "Lane(KP.x)", "E0705@24:14"},
		{"field of a named const inside a struct literal", "LaneP(P(x: KP.x, y: 3))", "E0705@24:20"},
		// A builtin call's folded value is read inside an argument too
		// (`N + int(3)` was accepted before and must stay).
		{"conversion inside a struct literal", "LaneP(P(x: int(K8), y: N))", ""},
		{"conversion beside an open generic", "Lane(N + int(3))", ""},
		{"Len beside an open generic", "Lane(Tab.Len() * N)", "E0705@24:14"},
		{"Len inside a closed operator", "Lane(Tab.Len() * 2)", ""},
		// `<<` drops the bits shifted out, as the reference, the evaluator
		// and run time say; it never overflows (`K << 62`, `U8 << 1`).
		{"shift dropping bits", "Lane(K << 62)", ""},
		{"u8 shift dropping bits", "Lane8(U8 << 1)", ""},
		// An out-of-range `Cast` panics at compile time.
		{"out-of-range Cast", "Lane8(Cast[u8](NK))", "E0901@24:15"},
		// Forms accepted before stay accepted; E0705 is only
		// for the forms listed above.
		{"if with a closed condition", "Lane(if K > 3 { 1 } else { 2 })", ""},
		{"generic instance to a type-parameter const", "Ap(H[int])", ""},
		{"generic instance inside a struct literal", "LaneF(PF(f: H[int]))", ""},
		{"generic instances inside an array literal", "Ar([H[int], H[u8]])", ""},
		{"generic instance inside a record literal", "Rc((f: H[int], n: 5))", ""},
		{"open generic instance inside a struct literal", "LaneF(PF(f: H[T]))", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			w.withEval()
			b := checkBody(t, w, tc.want, head+gen+tc.call+"\n}\n"+tail)
			if !strings.HasPrefix(tc.want, "E0705") {
				return
			}
			if d := b.bodyDiags(t)[0]; !strings.Contains(d.Message, "is not a compile-time constant here") {
				t.Errorf("message %q", d.Message)
			}
		})
	}
}
