// Why: generics in bodies (`if T is`, const arguments, compile-time
// requires) are where a wrong constant would run silently.
// What: TestIfTypeIs, TestE0705_NotConst, TestE0709_ConstArithmetic,
// TestE0706_RequiresFailed, TestE0706_RestatedClause,
// TestStageRule_RequiresInBody.
// How: checks generic functions and their calls against fakeEval and
// compares codes.

package check

import (
	"strings"
	"testing"

	"mk/internal/source"
	"mk/internal/types"

	"mk/internal/ast"
)

const memchr = "Memchr(xs: []u8, x: u8): ?int {\n\tfor i, y in xs {\n\t\tif y == x {\n\t\t\treturn some(i)\n\t\t}\n\t}\n\treturn none\n}\n\n"

func TestIfTypeIs(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"IndexOf", memchr + "IndexOf[T: Plain](xs: []T, x: &T): ?int {\n\tif T is u8 {\n\t\treturn Memchr(xs, *x)\n\t}\n\treturn none\n}\n", ""},
		{"else stays opaque", memchr + "IndexOf[T: Plain](xs: []T, x: &T): ?int {\n\tif T is u8 {\n\t\treturn Memchr(xs, *x)\n\t} else {\n\t\ty := *x + 1\n\t}\n\treturn none\n}\n", "E0703@14:13"},
		{"cannot meet the bounds", "F[T: Plain](x: T) {\n\tif T is Cell[int] {\n\t}\n}\n", "E0701@2:7"},
		{"narrowed operators", "Inc[T: Plain](x: T): T {\n\tif T is int {\n\t\treturn x + 1\n\t}\n\treturn x\n}\n", ""},
	})
	w := newWorld(t, true)
	b := checkBody(t, w, "", "Inc[T: Plain](x: T): T {\n\tif T is int {\n\t\treturn x + 1\n\t}\n\treturn x\n}\n")
	body := w.c.Check(b.m, b.item("Inc"))
	is := body.Node.(*ast.FuncDecl).Body.Stmts[0].(*ast.IfStmt)
	if u := body.Uses[body.slot(is.Cond.ID())]; u.Kind != UseBuiltin || Builtin(u.Index) != BuiltinTypeIs {
		t.Errorf("IsCond use %+v", u)
	}
}

func TestE0705_NotConst(t *testing.T) {
	const at = "At(const n: int, xs: []int): int {\n\treturn xs[n]\n}\n\n"
	runBodyCases(t, []bodyCase{
		{"literal", at + "F(xs: []int): int {\n\treturn At(1, xs)\n}\n", ""},
		{"named const", "const K: int = 2\n\n" + at + "F(xs: []int): int {\n\treturn At(K, xs)\n}\n", ""},
		{"local", at + "F(xs: []int, i: int): int {\n\tj := i\n\treturn At(j, xs)\n}\n", "E0705@7:12"},
		{"parameter", at + "F(xs: []int, i: int): int {\n\treturn At(i + 1, xs)\n}\n", "E0705@6:12"},
		{"forwarded const parameter", at + "F(const k: int, xs: []int): int {\n\treturn At(k, xs)\n}\n", ""},
		// A call is no const argument. Such a call was
		// never evaluated, and the parameter ran as 0
		// (`Lane(Id(3))`, `Lane(Two())`, `Lane(ReadCount())` read a var).
		{"call of a function", at + "Two(): int {\n\treturn 2\n}\n\nF(xs: []int): int {\n\treturn At(Two(), xs)\n}\n", "E0705@10:12"},
		{"call of a const-parameter function", at + "Id(const x: int): int {\n\treturn x\n}\n\nF(xs: []int): int {\n\treturn At(Id(3), xs)\n}\n", "E0705@10:12"},
		{"call reading a var", "var Count: int = 1\n\n" + at + "Read(): int {\n\treturn Count\n}\n\nF(xs: []int): int {\n\treturn At(Read(), xs)\n}\n", "E0705@12:12"},
		{"call of a method on a named const", at + "struct P {\n\tn: int\n}\n\nconst PP: P = P(n: 2)\n\nP.Get(&self): int {\n\treturn self.n\n}\n\nF(xs: []int): int {\n\treturn At(PP.Get(), xs)\n}\n", "E0705@16:12"},
		{"call of a const fn parameter", at + "F(const g: fn(): int, xs: []int): int {\n\treturn At(g(), xs)\n}\n", "E0705@6:12"},
		// Controls: a run-time receiver or fn value was E0705 already.
		{"call of a method on a parameter", at + "struct P {\n\tn: int\n}\n\nP.Get(&self): int {\n\treturn self.n\n}\n\nF(xs: []int, p: &P): int {\n\treturn At(p.Get(), xs)\n}\n", "E0705@14:12"},
		{"call of a fn value parameter", at + "F(xs: []int, g: fn(): int): int {\n\treturn At(g(), xs)\n}\n", "E0705@6:12"},
		{"call inside a struct literal", "struct P {\n\tn: int\n}\n\nAtP(const p: P): int {\n\treturn p.n\n}\n\nTwo(): int {\n\treturn 2\n}\n\nF(): int {\n\treturn AtP(P(n: Two()))\n}\n", "E0705@14:13"},
		// Not calls of a function: a struct literal, a conversion, SizeOf.
		{"struct literal", "struct P {\n\tn: int\n}\n\nAtP(const p: P): int {\n\treturn p.n\n}\n\nF(): int {\n\treturn AtP(P(n: 2))\n}\n", ""},
		{"conversion and SizeOf", "At8(const n: u8): int {\n\treturn int(n)\n}\n\n" + at + "F(xs: []int): int {\n\treturn At8(u8(3)) + At(SizeOf[u32](), xs)\n}\n", ""},
	})
}

func TestE0709_ConstArithmetic(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"N - 1", "F[const N: int]() {\n\tx: [N - 1]u8 = [0; 1]\n}\n", "E0709@2:6"},
		{"N", "F[const N: int](): [N]u8 {\n\tx: [N]u8 = [0; N]\n\treturn x\n}\n", ""},
		{"run-time use", "F[const N: int](): int {\n\treturn N - 1\n}\n", ""},
		{"run-time use typed", "F[const N: u8](): bool {\n\treturn N\n}\n", "E0301@2:9"},
	})
}

// TestE0706_RequiresFailed: a compile-time requires on a const parameter is
// evaluated at each concrete call (EvalContract, StageSignatures, the
// call's generic arguments); false is E0706 at the call with clause.
func TestE0706_RequiresFailed(t *testing.T) {
	w := newWorld(t, true)
	fe := w.withEval()
	fe.fn = func(req *types.EvalRequest) (types.EvalResult, bool) {
		if req.What != types.EvalContract || len(req.GenericArgs) != 1 {
			return types.EvalResult{}, false
		}
		n, _ := w.c.constInt(req.GenericArgs[0].Const.Value)
		var v uint64
		if n > 0 {
			v = 1
		}
		return types.EvalResult{Value: w.c.scalarConst(types.Bool, 1, v)}, true
	}
	// lib.F is imported and evaluated; the same-module F2 has no MIR in
	// stage 6, and check folds its predicate itself: F2[0]() is E0706 too.
	w.add("lib", opt{}, "export F[const N: int](): int\n\trequires N > 0 {\n\treturn N\n}\n")
	b := checkBody(t, w, "E0706@14:9 E0706@3:22", "import lib\nG(): int {\n\treturn lib.F[4]() + lib.F[0]()\n}\nH[const M: int](): int\n\trequires M > 0 {\n\treturn lib.F[M]()\n}\nF2[const N: int](): int\n\trequires N > 0 {\n\treturn N\n}\nK(): int {\n\treturn F2[0]()\n}\n")
	if ds := b.bodyDiags(t); fact(ds[0], "clause") != "N > 0" {
		t.Fatalf("clause %q", fact(ds[0], "clause"))
	}
	for _, r := range fe.requests() {
		if !strings.HasPrefix(r, "2/0 ") {
			t.Fatalf("request %s, want EvalContract at StageSignatures", r)
		}
	}
}

// TestE0706_RestatedClause: inside a generic, a call passing the caller's
// const parameter on to a callee with a compile-time requires needs the
// caller to state the substituted clause token for token.
func TestE0706_RestatedClause(t *testing.T) {
	const lib = "export F[const N: int](): int\n\trequires N > 0 {\n\treturn N\n}\n\nexport P[const A: int, const B: int](): int\n\trequires A < B {\n\treturn A\n}\n"
	cases := []struct{ name, src, want, clause string }{
		{"restated", "import lib\n\nH[const M: int](): int\n\trequires M > 0 {\n\treturn lib.F[M]()\n}\n", "", ""},
		{"not restated", "import lib\n\nH[const M: int](): int {\n\treturn lib.F[M]()\n}\n", "E0706@4:9", "M > 0"},
		{"a different clause", "import lib\n\nH[const M: int](): int\n\trequires M >= 1 {\n\treturn lib.F[M]()\n}\n", "E0706@5:9", "M > 0"},
		{"one argument a literal", "import lib\n\nH[const M: int](): int\n\trequires M < 10 {\n\treturn lib.P[M, 10]()\n}\n", "", ""},
		{"swapped", "import lib\n\nH[const M: int](): int {\n\treturn lib.P[3, M]()\n}\n", "E0706@4:9", "3 < M"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			w.withEval()
			w.add("lib", opt{}, lib)
			b := checkBody(t, w, tc.want, tc.src)
			if tc.clause != "" {
				d := b.bodyDiags(t)[0]
				if got := fact(d, "clause"); got != tc.clause || len(d.Fixes) != 1 {
					t.Fatalf("clause %q fixes %d, want %q and one fix", got, len(d.Fixes), tc.clause)
				}
			}
		})
	}
}

// TestStageRule_RequiresInBody: a requires result needed at a call inside a
// body is StageSignatures, so a clause that reads a same-module const that
// enters a same-module body is E0908 at the call.
func TestStageRule_RequiresInBody(t *testing.T) {
	w := newWorld(t, true)
	fe := w.withEval()
	fe.fn = func(req *types.EvalRequest) (types.EvalResult, bool) {
		if req.What == types.EvalContract {
			return types.EvalResult{Err: &types.EvalError{Kind: types.EvalStage, Items: []types.ItemID{req.Item}, Trace: []source.Span{req.Site}}}, true
		}
		return types.EvalResult{}, false
	}
	// The evaluator's EvalStage for a requires is the use's: E0908 at the
	// call.
	w.add("lib", opt{}, "Compute(): int {\n\treturn 4\n}\nconst Limit = Compute()\nexport F[const N: int]()\n\trequires N > Limit {\n}\n")
	checkBody(t, w, "E0908@3:2", "import lib\nG() {\n\tlib.F[4]()\n}\n")
}
