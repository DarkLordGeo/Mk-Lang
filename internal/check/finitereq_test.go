// Why: a callee's compile-time requires that names a const generic of the
// caller has no one value inside the generic, yet must be judged there, not
// left to run time.
// What: TestFiniteConstGenerics, TestCombinationLimitStated,
// TestImportedCalleeOpenT.
// How: when every value argument folds, the clause is judged over the
// finite set of values the caller's own clauses allow, else restated.

package check

import (
	"strconv"
	"strings"
	"testing"

	"mk/internal/types"
)

func TestFiniteConstGenerics(t *testing.T) {
	const lib = "export F[const N: int](const i: int)\n\trequires i >= 0 && i < N {\n}\n"
	const same = "S[const N: int](const i: int)\n\trequires i >= 0 && i < N {\n}\n"
	cases := []struct{ name, src, want, clause string }{
		// std.simd utf8.mk's shape: two clauses, intersected to {16, 32, 64}.
		{"imported, restricted, holds", "import lib\n\nG[const M: int]()\n\trequires M == 2 || M == 4 || M == 8 || M == 16 || M == 32 || M == 64\n\trequires M == 16 || M == 32 || M == 64 {\n\tlib.F[M](0)\n\tlib.F[M](2)\n\tlib.F[M](15)\n}\n", "", ""},
		{"imported, restricted, fails for one value", "import lib\n\nG[const M: int]()\n\trequires M == 2 || M == 4 {\n\tlib.F[M](3)\n}\n", "E0706@5:2", "3 >= 0 && 3 < M"},
		{"imported, unrestricted", "import lib\n\nG[const M: int]() {\n\tlib.F[M](0)\n}\n", "E0706@4:2", "0 >= 0 && 0 < M"},
		{"imported, unrestricted, restated", "import lib\n\nG[const M: int]()\n\trequires 0 >= 0 && 0 < M {\n\tlib.F[M](0)\n}\n", "", ""},
		{"imported, literal first, parenthesized", "import lib\n\nG[const M: int]()\n\trequires (4 == M) || (M == 8) {\n\tlib.F[M](3)\n}\n", "", ""},
		{"imported, not a disjunction of equalities", "import lib\n\nG[const M: int]()\n\trequires M > 0 && M < 4 {\n\tlib.F[M](0)\n}\n", "E0706@5:2", "0 >= 0 && 0 < M"},
		{"same module, restricted, holds", same + "G[const M: int]()\n\trequires M == 2 || M == 4 {\n\tS[M](1)\n}\n", "", ""},
		{"same module, restricted, fails", same + "G[const M: int]()\n\trequires M == 2 || M == 4 {\n\tS[M](2)\n}\n", "E0706@6:2", "2 >= 0 && 2 < M"},
		{"same module, unrestricted", same + "G[const M: int]() {\n\tS[M](0)\n}\n", "E0706@5:2", "0 >= 0 && 0 < M"},
		// One caller generic bound to two callee generics takes one
		// value per combination, so A=4, B=2 is never tried.
		{"PM-R1 same caller generic twice", "S2[const A: int, const B: int](const i: int)\n\trequires i >= 0 && A <= B {\n}\nG[const M: int]()\n\trequires M == 2 || M == 4 {\n\tS2[M, M](0)\n}\n", "", ""},
		{"PM-R1 two caller generics", "S2[const A: int, const B: int](const i: int)\n\trequires i >= 0 && A <= B {\n}\nG[const M: int, const P: int]()\n\trequires M == 2 || M == 4\n\trequires P == 2 || P == 4 {\n\tS2[M, P](0)\n}\n", "E0706@7:2", "0 >= 0 && M <= P"},
		// More than maxFiniteCombos (17^3 = 4,913) is restated, and
		// the message says why; 16^3 = 4,096 is judged.
		{"PM-R3 too many combinations", "S3[const A: int, const B: int, const C: int](const i: int)\n\trequires i >= 0 && i < A + B + C {\n}\nG[const P: int, const Q: int, const R: int]()\n" +
			"\trequires " + equalities("P", 17) + "\n\trequires " + equalities("Q", 17) + "\n\trequires " + equalities("R", 17) + " {\n\tS3[P, Q, R](0)\n}\n", "E0706@8:2", "0 >= 0 && 0 < P + Q + R"},
		{"PM-R3 at the limit", "S3[const A: int, const B: int, const C: int](const i: int)\n\trequires i >= 0 && i < A + B + C {\n}\nG[const P: int, const Q: int, const R: int]()\n" +
			"\trequires " + equalities("P", 16) + "\n\trequires " + equalities("Q", 16) + "\n\trequires " + equalities("R", 16) + " {\n\tS3[P, Q, R](0)\n}\n", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			fe := w.withEval()
			// lib.F's clause, evaluated: i >= 0 && i < N.
			fe.fn = func(req *types.EvalRequest) (types.EvalResult, bool) {
				if req.What != types.EvalContract || len(req.Args) != 1 || len(req.GenericArgs) != 1 || req.GenericArgs[0].Const.Kind != types.CAValue {
					return types.EvalResult{}, false
				}
				i, _ := w.c.constInt(req.Args[0])
				n, _ := w.c.constInt(req.GenericArgs[0].Const.Value)
				var v uint64
				if i >= 0 && i < n {
					v = 1
				}
				return types.EvalResult{Value: w.c.scalarConst(types.Bool, 1, v)}, true
			}
			w.add("lib", opt{}, lib)
			b := checkBody(t, w, tc.want, tc.src)
			if tc.clause != "" {
				d := b.bodyDiags(t)[0]
				if got := fact(d, "clause"); got != tc.clause {
					t.Fatalf("clause %q, want %q", got, tc.clause)
				}
				if !strings.Contains(d.Message, "state it on this function") || len(d.Fixes) != 1 {
					t.Fatalf("message %q with %d fixes", d.Message, len(d.Fixes))
				}
			}
		})
	}
}

// equalities is `x == 1 || ... || x == n`.
func equalities(x string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		if i > 1 {
			b.WriteString(" || ")
		}
		b.WriteString(x + " == " + strconv.Itoa(i))
	}
	return b.String()
}

// TestCombinationLimitStated: the E0706 of a call over more
// combinations than the finite check evaluates says so.
func TestCombinationLimitStated(t *testing.T) {
	w := newWorld(t, true)
	src := "S3[const A: int, const B: int, const C: int](const i: int)\n\trequires i >= 0 && i < A + B + C {\n}\nG[const P: int, const Q: int, const R: int]()\n" +
		"\trequires " + equalities("P", 17) + "\n\trequires " + equalities("Q", 17) + "\n\trequires " + equalities("R", 17) + " {\n\tS3[P, Q, R](0)\n}\n"
	b := checkBody(t, w, "E0706@8:2", src)
	if d := b.bodyDiags(t)[0]; !strings.Contains(d.Message, "more than 4,096 combinations") {
		t.Fatalf("message %q does not state the limit", d.Message)
	}
}

// TestImportedCalleeOpenT: an imported callee generic over a type T
// and N, called with the caller's open T, is judged over N's set as the
// same-module callee is when its clause does not name T (the evaluator
// gets a closed stand-in for T); a clause that names T is restated.
func TestImportedCalleeOpenT(t *testing.T) {
	const lib = "export F[T, const N: int](const i: int)\n\trequires i >= 0 && i < N {\n}\n" +
		"export H[T, const N: int](const i: int)\n\trequires i >= 0 && i < N + SizeOf[T]() {\n}\n"
	cases := []struct{ name, src, want, clause string }{
		{"imported, open T, holds", "import lib\n\nG[T, const M: int]()\n\trequires M == 2 || M == 4 {\n\tlib.F[T, M](0)\n}\n", "", ""},
		{"imported, open T, fails for one value", "import lib\n\nG[T, const M: int]()\n\trequires M == 2 || M == 4 {\n\tlib.F[T, M](3)\n}\n", "E0706@5:2", "3 >= 0 && 3 < M"},
		{"imported, closed T, holds", "import lib\n\nG[const M: int]()\n\trequires M == 2 || M == 4 {\n\tlib.F[u8, M](1)\n}\n", "", ""},
		// SizeOf[T]() reads T; the restated clause spells it with the
		// call's type argument (it was printed as "...()").
		{"imported, clause names T", "import lib\n\nG[T, const M: int]()\n\trequires M == 2 || M == 4 {\n\tlib.H[T, M](0)\n}\n", "E0706@5:2", "0 >= 0 && 0 < M + SizeOf[T]()"},
		{"same module, open T, holds", "S[T, const N: int](const i: int)\n\trequires i >= 0 && i < N {\n}\nG[T, const M: int]()\n\trequires M == 2 || M == 4 {\n\tS[T, M](0)\n}\n", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			fe := w.withEval()
			fe.fn = func(req *types.EvalRequest) (types.EvalResult, bool) {
				if req.What != types.EvalContract || len(req.Args) != 1 || len(req.GenericArgs) != 2 ||
					req.GenericArgs[0].IsConst || !w.c.closed(req.GenericArgs[0].Type) || req.GenericArgs[1].Const.Kind != types.CAValue {
					return types.EvalResult{}, false
				}
				i, _ := w.c.constInt(req.Args[0])
				n, _ := w.c.constInt(req.GenericArgs[1].Const.Value)
				var v uint64
				if i >= 0 && i < n {
					v = 1
				}
				return types.EvalResult{Value: w.c.scalarConst(types.Bool, 1, v)}, true
			}
			w.add("lib", opt{}, lib)
			b := checkBody(t, w, tc.want, tc.src)
			if tc.clause != "" {
				if got := fact(b.bodyDiags(t)[0], "clause"); got != tc.clause {
					t.Fatalf("clause %q, want %q", got, tc.clause)
				}
			}
		})
	}
}
