// Why: a callee's compile-time requires over a caller's const generic is
// judged over the finite set of values the caller allows; that set may be
// implied by a clause `M == e`, and no instance outside it may slip through.
// What: TestImpliedConstSet, TestSameModuleFoldPanics,
// TestForwardingCall, TestImportedEnumUnbounded,
// TestImportedValueParam.
// How: a combination at which one of the caller's own clauses folds false is
// skipped; each test pins that every instance the implied set or a skip
// leaves out is E0706 at its own site.

package check

import (
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

func TestImpliedConstSet(t *testing.T) {
	// S is a lane line: its clause lists K. S2 relates two lane counts, as
	// std.simd's WidenLo does (`M * 2 == N`).
	const lane = "S[const K: int]()\n\trequires K == 2 || K == 4 || K == 8 {\n}\n"
	const pair = "S2[const A: int, const B: int]()\n\trequires B * 2 == A {\n}\n"
	// pairR states the relation in another spelling, so a caller's
	// `M * 2 == N` is no restatement of it (restateMet): only the skip
	// accepts the related cases.
	const pairR = "S2[const A: int, const B: int]()\n\trequires A == 2 * B {\n}\n"
	cases := []struct{ name, src, want string }{
		{"implied, holds", lane + "G[const N: int, const M: int]()\n\trequires N == 4 || N == 8 || N == 16\n\trequires M == N / 2 {\n\tS[M]()\n}\n", ""},
		{"implied, commuted and parenthesized", lane + "G[const N: int, const M: int]()\n\trequires N == 4 || N == 8 || N == 16\n\trequires (N / 2 == M) {\n\tS[M]()\n}\n", ""},
		{"implied, fails for one value", lane + "G[const N: int, const M: int]()\n\trequires N == 4 || N == 8 || N == 32\n\trequires M == N / 2 {\n\tS[M]()\n}\n", "E0706@7:2"},
		// The multiplicative form bounds nothing for M: at M =
		// -9223372036854775800 its fold overflows, which a same-module site
		// reports (E0901) but an imported fn value leaves unchecked,
		// so it cannot bound M.
		{"G * k == N implies nothing", lane + "G[const N: int, const M: int]()\n\trequires N == 4 || N == 8 || N == 16\n\trequires M * 2 == N {\n\tS[M]()\n}\n", "E0706@7:2"},
		{"a conjunct implies nothing", lane + "var Ready: int = 1\nG[const N: int, const M: int]()\n\trequires N == 4 || N == 8 || N == 16\n\trequires M == N / 2 && Ready > 0 {\n\tS[M]()\n}\n", "E0706@8:2"},
		{"one step only", lane + "G[const P: int, const N: int, const M: int]()\n\trequires P == 8 || P == 16 || P == 32\n\trequires N == P / 2\n\trequires M == N / 2 {\n\tS[M]()\n}\n", "E0706@8:2"},
		{"over a type generic", lane + "G[T, const M: int]()\n\trequires M == SizeOf[T]() {\n\tS[M]()\n}\n", "E0706@6:2"},
		{"over itself", lane + "G[const M: int]()\n\trequires M == M + 0 {\n\tS[M]()\n}\n", "E0706@6:2"},
		{"e panics at one value", lane + "G[const N: int, const M: int]()\n\trequires N == 4 || N == 8\n\trequires M == 16 / (N - 4) {\n\tS[M]()\n}\n", "E0706@7:2"},
		// Each set alone has a value S rejects (3 listed, 16 implied); only
		// their intersection {2, 4} holds. The intersection is redundant
		// with the skip (the listed clause folds false at M = 16 alone), so
		// this pins the outcome, not the intersection.
		{"implied and listed intersect", lane + "G[const N: int, const M: int]()\n\trequires N == 4 || N == 8 || N == 32\n\trequires M == N / 2\n\trequires M == 2 || M == 3 || M == 4 {\n\tS[M]()\n}\n", ""},
		// The combinations: (N, M) pairs the caller's clauses rule out are
		// skipped, both listed or one implied.
		{"listed pair, related", pairR + "G[const N: int, const M: int]()\n\trequires N == 4 || N == 8\n\trequires M == 2 || M == 4\n\trequires M * 2 == N {\n\tS2[N, M]()\n}\n", ""},
		{"listed pair, unrelated", pair + "G[const N: int, const M: int]()\n\trequires N == 4 || N == 8\n\trequires M == 2 || M == 4 {\n\tS2[N, M]()\n}\n", "E0706@7:2"},
		{"implied pair", pair + "G[const N: int, const M: int]()\n\trequires N == 4 || N == 8 || N == 16\n\trequires M == N / 2 {\n\tS2[N, M]()\n}\n", ""},
		{"related by another rule", pair + "G[const N: int, const M: int]()\n\trequires N == 4 || N == 8\n\trequires M == 2 || M == 4\n\trequires M <= N / 4 {\n\tS2[N, M]()\n}\n", "E0706@8:2"},
		{"a clause that does not fold excludes nothing", pair + "var Ready: int = 1\nG[const N: int, const M: int]()\n\trequires N == 4 || N == 8\n\trequires M == 2 || M == 4\n\trequires M * 2 == N || Ready > 9 {\n\tS2[N, M]()\n}\n", "E0706@9:2"},
		// N's listed {4} meets the {8} that `M * 2 == N` implies for N from
		// M's listed {4}: an empty set, so no combination is judged. The next
		// case is the one only the skip accepts: `M < N` implies no set.
		{"empty implied-and-listed intersection", pairR + "G[const N: int, const M: int]()\n\trequires N == 4\n\trequires M == 4\n\trequires M * 2 == N {\n\tS2[N, M]()\n}\n", ""},
		{"every combination skipped, no implied set", pairR + "G[const N: int, const M: int]()\n\trequires N == 4\n\trequires M == 4\n\trequires M < N {\n\tS2[N, M]()\n}\n", ""},
		// `M * 2 == N` implies nothing for M, but N's set when M is listed
		// (e == N with e = M * 2): N is {8}.
		{"M * 2 == N implies N from a listed M", "S[const K: int]()\n\trequires K == 8 {\n}\nG[const N: int, const M: int]()\n\trequires M == 4\n\trequires M * 2 == N {\n\tS[N]()\n}\n", ""},
		// Two implied sets for M, {2, 4} from N and {2, 8} from P: only their
		// intersection {2} holds S's clause, and no clause folds with M
		// alone bound, so the skip cannot stand in for the intersection.
		// Its witness is M = 4, in the first set only.
		{"two implied sets intersect", "S[const K: int]()\n\trequires K == 2 {\n}\nG[const N: int, const P: int, const M: int]()\n\trequires N == 4 || N == 8\n\trequires P == 1 || P == 7\n\trequires M == N / 2\n\trequires M == P + 1 {\n\tS[M]()\n}\n", ""},
		{"site: two implied sets, outside one", "G[const N: int, const P: int, const M: int](): int\n\trequires N == 4 || N == 8\n\trequires P == 1 || P == 7\n\trequires M == N / 2\n\trequires M == P + 1 {\n\treturn M\n}\nH(): int {\n\treturn G[8, 1, 4]()\n}\n", "E0706@9:9"},
		// The combination cap counts the combinations before any is skipped.
		{"cap before skipping", "S3[const A: int, const B: int, const C: int]()\n\trequires A + B + C > 0 {\n}\nG[const P: int, const Q: int, const R: int]()\n" +
			"\trequires " + equalities("P", 17) + "\n\trequires " + equalities("Q", 17) + "\n\trequires " + equalities("R", 17) + "\n\trequires P == Q {\n\tS3[P, Q, R]()\n}\n", "E0706@9:2"},
		// Witnesses at concrete sites: each instance left out is E0706 there.
		{"site: M outside the implied set", "G[const N: int, const M: int](): int\n\trequires N == 16 || N == 32\n\trequires M == N / 2 {\n\treturn M\n}\nH(): int {\n\treturn G[16, 5]()\n}\n", "E0706@7:9"},
		{"site: a far M", "G[const N: int, const M: int](): int\n\trequires N == 16 || N == 32\n\trequires M == N / 2 {\n\treturn M\n}\nH(): int {\n\treturn G[16, -9223372036854775800]()\n}\n", "E0706@7:9"},
		{"site: the largest M", "G[const N: int, const M: int](): int\n\trequires N == 16 || N == 32\n\trequires M == N / 2 {\n\treturn M\n}\nH(): int {\n\treturn G[16, 9223372036854775807]()\n}\n", "E0706@7:9"},
		{"site: a value, M outside the implied set", "G[const N: int, const M: int](): int\n\trequires N == 16 || N == 32\n\trequires M == N / 2 {\n\treturn M\n}\nH(): int {\n\tf: fn(): int = G[16, 9223372036854775807]\n\treturn f()\n}\n", "E0706@7:17"},
		{"site: a skipped combination", "G[const N: int, const M: int](): int\n\trequires N == 4 || N == 8\n\trequires M == 2 || M == 4\n\trequires M * 2 == N {\n\treturn M\n}\nH(): int {\n\treturn G[4, 4]()\n}\n", "E0706@8:9"},
		{"site: a value, a skipped combination", "G[const N: int, const M: int](): int\n\trequires N == 4 || N == 8\n\trequires M == 2 || M == 4\n\trequires M * 2 == N {\n\treturn M\n}\nH(): int {\n\tf: fn(): int = G[8, 2]\n\treturn f()\n}\n", "E0706@8:17"},
		{"site: in the implied set", "G[const N: int, const M: int](): int\n\trequires N == 16 || N == 32\n\trequires M == N / 2 {\n\treturn M\n}\nH(): int {\n\treturn G[32, 16]()\n}\n", ""},
		// A method of a generic type as the caller: the receiver's N and the
		// method's M are both its generics, implied and skipped alike, and
		// the instances left out are E0706 at the method call.
		{"method of a generic type", methodCaller + "Use(): int {\n\tb := Box[8](x: 1)\n\treturn b.Half[4]() + b.Pair[4]()\n}\n", ""},
		{"site: method of a generic type", methodCaller + "Bad(): int {\n\tb := Box[8](x: 1)\n\treturn b.Half[2]() + b.Pair[2]()\n}\n", "E0706@25:23 E0706@25:9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			checkBody(t, w, tc.want, tc.src)
		})
	}
}

// TestSameModuleFoldPanics: a same-module compile-time requires whose
// fold panics at a concrete call or value was left to run time, so release
// ran the body with the clause false. It is E0901 there, as an imported
// clause's evaluation is at a call. A side of && or || that its left side
// decides is not reached, at run time or here, so its panic is none. The
// imported twin, which the evaluator judges, is
// tests/conformance/codes/E0706_forward_imported (this harness evaluates no
// imported contract).
func TestSameModuleFoldPanics(t *testing.T) {
	const at = "At[const N: int, const K: int](): int\n\trequires K * 2 == N && K < 4 {\n\treturn K\n}\n" // lines 1-4
	const f = "F[const N: int](): int\n\trequires N == 0 || 8 / N > 1 {\n\treturn N\n}\n"
	const g = "G[const N: int](): int\n\trequires N != 0 && 8 / N > 1 {\n\treturn N\n}\n"
	runBodyCases(t, []bodyCase{
		{"call, overflow", at + "H(): int {\n\treturn At[16, -9223372036854775800]()\n}\n", "E0901@6:9"},
		{"value, overflow", at + "H(): int {\n\tf: fn(): int = At[16, -9223372036854775800]\n\treturn f()\n}\n", "E0901@6:17"},
		{"call, holds", at + "H(): int {\n\treturn At[4, 2]()\n}\n", ""},
		{"call, false", at + "H(): int {\n\treturn At[16, 2]()\n}\n", "E0706@6:9"},
		{"in a generic, closed arguments", at + "H[T](): int {\n\treturn At[16, -9223372036854775800]()\n}\n", "E0901@6:9"},
		{"division by zero", "D[const N: int](): int\n\trequires 8 / N > 1 {\n\treturn N\n}\nH(): int {\n\treturn D[0]()\n}\n", "E0901@6:9"},
		{"|| decided on the left", f + "H(): int {\n\treturn F[0]() + F[2]()\n}\n", ""},
		{"|| judged on the right, false", f + "H(): int {\n\treturn F[16]()\n}\n", "E0706@6:9"},
		{"&& decided on the left, false", g + "H(): int {\n\treturn G[0]()\n}\n", "E0706@6:9"},
		{"&& judged on the right, holds", g + "H(): int {\n\treturn G[2]()\n}\n", ""},
	})
}

// TestForwardingCall: inside a generic, a callee clause that reads an
// argument no clause could spell was left to the entry check, which a
// release build never runs.
//   - An enum value (`At[N, Mode.A]`, `At[N, MA]`) of a same-module callee
//     is spelled, so the clause is judged over the caller's finite sets;
//     an imported callee's is judged over them as it is
//     (tests/conformance/codes/E0706_forward_imported).
//   - A clause that still cannot be judged rejects the call,
//     since the callee's body may rely on it through an implied set or a
//     skipped combination, unless it is the callee's sole clause
//     over the one const generic it names (soleClause): that one keeps
//     the sole-clause exit. A skip can rest on two clauses over
//     one generic (`M == 3 || M == 24` beside `M < 4`; the imported callee
//     is conformance E0706_forward_skip_one). One E0706 per call or value.
func TestForwardingCall(t *testing.T) {
	const m = "enum Mode {\n\tA\n\tB\n}\n\nconst MA: Mode = Mode.A\n"                                                                // lines 1-6
	const at = m + "At[const K: int, const D: Mode](): int\n\trequires K >= 0 && K < 4 && D == Mode.A {\n\treturn K\n}\n"            // lines 7-10
	const w = "struct W[T] {\n\ta: T\n\tb: T\n\tc: T\n}\nAt[const K: int](): int\n\trequires K >= 0 && K < 4 {\n\treturn K\n}\n"     // lines 1-9
	const implied = w + "Pick[const N: int, const M: int](): int\n\trequires N == 2\n\trequires M == N + 1 {\n\treturn At[M]()\n}\n" // lines 10-14
	const listed = w + "Pick[const N: int, const M: int](): int\n\trequires N == 2\n\trequires M == 3 {\n\treturn At[M]()\n}\n"
	const skipped = w + "Pick[const N: int, const M: int](): int\n\trequires N == 2 || N == 24\n\trequires M == 1 || M == 3\n\trequires N + M < 10 {\n\treturn At[M]()\n}\n" // lines 10-15
	const skipOne = w + "Pick[const M: int](): int\n\trequires M == 3 || M == 24\n\trequires M < 4 {\n\treturn At[M]()\n}\n"                                                 // lines 10-14 (q09)
	const twoOver = w + "Pick[const M: int](): int\n\trequires M >= 0 && M < 4\n\trequires M != 2 {\n\treturn At[M]()\n}\n"                                                  // lines 10-14
	const otherGen = w + "Pick[const N: int, const M: int](): int\n\trequires N == 2\n\trequires M == 3 {\n\treturn At[M]() + N\n}\n"                                        // lines 10-14
	runBodyCases(t, []bodyCase{
		{"variant, false for one N", at + "Pick[const N: int](): int\n\trequires N == 2 || N == 24 {\n\treturn At[N, Mode.A]()\n}\n", "E0706@13:9"},
		{"enum const, false for one N", at + "Pick[const N: int](): int\n\trequires N == 2 || N == 24 {\n\treturn At[N, MA]()\n}\n", "E0706@13:9"},
		{"variant, holds for every N", at + "Pick[const N: int](): int\n\trequires N == 2 || N == 3 {\n\treturn At[N, Mode.A]()\n}\n", ""},
		{"variant, false for every N", at + "Pick[const N: int](): int\n\trequires N == 2 || N == 3 {\n\treturn At[N, Mode.B]()\n}\n", "E0706@13:9"},
		{"variant, value", at + "Pick[const N: int](): fn(): int\n\trequires N == 2 || N == 24 {\n\treturn At[N, Mode.A]\n}\n", "E0706@13:9"},
		{"variant, restated", at + "Pick[const N: int](): int\n\trequires N == 2 || N == 24\n\trequires N >= 0 && N < 4 && Mode.A == Mode.A {\n\treturn At[N, Mode.A]()\n}\n", ""},
		{"implied clause unjudged, call", implied + "Fwd[T](): int {\n\treturn Pick[2, SizeOf[W[T]]()]()\n}\n", "E0706@16:9"},
		{"implied clause unjudged, value", implied + "Fwd[T](): fn(): int {\n\treturn Pick[2, SizeOf[W[T]]()]\n}\n", "E0706@16:9"},
		{"skip clause unjudged", skipped + "Fwd[T](): int {\n\treturn Pick[2, SizeOf[W[T]]()]()\n}\n", "E0706@17:9"},
		{"implied clause, spelled argument", implied + "Fwd[T](): int {\n\treturn Pick[2, 3]()\n}\n", ""},
		// A skip over one generic, at a call and at a value;
		// one E0706 for Pick's two clauses.
		{"skip by a one-generic clause, call", skipOne + "Fwd[T](): int {\n\treturn Pick[SizeOf[W[T]]()]()\n}\n", "E0706@16:9"},
		{"skip by a one-generic clause, value", skipOne + "Fwd[T](): int {\n\tf := Pick[SizeOf[W[T]]()]\n\treturn f()\n}\n", "E0706@16:7"},
		{"skip by a one-generic clause, concrete", skipOne + "Fwd(): int {\n\treturn Pick[3]()\n}\n", ""},
		{"two clauses over one generic", twoOver + "Fwd[T](): int {\n\treturn Pick[SizeOf[W[T]]()]()\n}\n", "E0706@16:9"},
		// The sole-clause exit, unchanged: the sole clause
		// over M, beside a clause over another generic.
		{"listed clause unjudged", listed + "Fwd[T](): int {\n\treturn Pick[2, SizeOf[W[T]]()]()\n}\n", ""},
		{"sole clause unjudged, N spelled", otherGen + "Fwd[T](): int {\n\treturn Pick[2, SizeOf[W[T]]()]()\n}\n", ""},
		// A clause whose text has a form exprText cannot spell ("...", an
		// array type in brackets) may name any generic, so it is never
		// sole; its own body's E0706 is at 13:9.
		{"a ... clause is never sole", w + "Pick[const N: int, const M: int](): int\n\trequires N == 2\n\trequires M == SizeOf[[N]u8]() + 1 {\n\treturn At[M]()\n}\n" +
			"Fwd[T](): int {\n\treturn Pick[2, SizeOf[W[T]]()]()\n}\n", "E0706@13:9 E0706@16:9"},
	})
}

// methodCaller is a generic type with two methods generic over M beside
// the receiver's N: Half with M implied by N, Pair with listed sets whose
// combinations its third clause narrows.
const methodCaller = "struct Box[const N: int] {\n\tx: int\n}\n" +
	"S[const K: int](): int\n\trequires K == 2 || K == 4 {\n\treturn K\n}\n" +
	"S2[const A: int, const B: int](): int\n\trequires A == 2 * B {\n\treturn B\n}\n" +
	"Box[N].Half[const M: int](self): int\n\trequires N == 4 || N == 8\n\trequires M == N / 2 {\n\treturn S[M]() + S2[N, M]()\n}\n" +
	"Box[N].Pair[const M: int](self): int\n\trequires N == 4 || N == 8\n\trequires M == 2 || M == 4\n\trequires M * 2 == N {\n\treturn S2[N, M]()\n}\n"

// TestImportedValueParam is the fall-through to
// soleClause: an imported callee's enum value is unspelled but closed, so
// a clause over it and a const value parameter is judged at the call with
// the parameter bound to the call's argument (this harness evaluates
// no imported contract, so the false case is conformance
// E0706_forward_imported's ValueParam). A forwarded const value parameter
// beside the unspelled value is E0706 "cannot be judged here", sole clause
// or not, where it kept the sole-clause exit before.
// TestImportedEnumUnbounded: lib.At[N, lib.MB] reads lib's enum value
// D, which no clause of the caller can spell. When the caller's sets do
// not bound N to values where the clause holds, the call is E0706 "cannot
// be judged here", never a demand to restate `N >= 0 && N < 4 && D ==
// Mode.A`: that text keeps lib's D and Mode, and a caller with its own D
// and Mode met it by text and ran At with its clause false.
func TestImportedEnumUnbounded(t *testing.T) {
	t.Parallel()
	const lib = "export enum Mode {\n\tA\n\tB\n}\n\nexport const MA: Mode = Mode.A\n\nexport const MB: Mode = Mode.B\n\n" +
		"export At[const K: int, const D: Mode](): int\n\trequires K >= 0 && K < 4 && D == Mode.A {\n\treturn K\n}\n"
	const own = "import lib\n\nenum Mode {\n\tA\n\tB\n}\n\n" // lines 1-7
	for _, tc := range []struct{ name, src, want string }{
		{"no set (t3)", "import lib\n\nH[const N: int](): int\n\trequires N >= 0 && N < 4 {\n\treturn lib.At[N, lib.MB]()\n}\n", "E0706@5:9"},
		{"restated with the caller's own D and Mode (t2)", own + "H[const N: int, const D: Mode](): int\n\trequires N >= 0 && N < 4 && D == Mode.A {\n\treturn lib.At[N, lib.MB]()\n}\n", "E0706@10:9"},
		{"listed, false at each, restated (r4_t5)", own + "H[const N: int, const D: Mode](): int\n\trequires N == 0 || N == 1\n\trequires N >= 0 && N < 4 && D == Mode.A {\n\treturn lib.At[N, lib.MB]()\n}\n", "E0706@11:9"},
		{"listed, holds at each (r4_t6)", "import lib\n\nH[const N: int](): int\n\trequires N == 0 || N == 1 {\n\treturn lib.At[N, lib.MA]()\n}\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			fe := w.withEval()
			// At's clause, evaluated as mk's evaluator would: K in 0..3 and
			// D's tag that of Mode.A (0).
			fe.fn = func(req *types.EvalRequest) (types.EvalResult, bool) {
				if req.What != types.EvalContract || len(req.GenericArgs) != 2 {
					return types.EvalResult{}, false
				}
				k, _ := w.c.constInt(req.GenericArgs[0].Const.Value)
				d := w.consts.Get(req.GenericArgs[1].Const.Value).Bytes
				var v uint64
				if k >= 0 && k < 4 && len(d) > 0 && d[0] == 0 {
					v = 1
				}
				return types.EvalResult{Value: w.c.scalarConst(types.Bool, 1, v)}, true
			}
			w.add("lib", opt{}, lib)
			// lib's consts get their variant's tag by hand, as
			// TestImportedValueParam does.
			for name, tag := range map[string]uint64{"MA": 0, "MB": 1} {
				id, ok := w.items.ByKey(types.ItemKey("lib", name))
				if !ok {
					t.Fatal("no lib." + name)
				}
				d := w.items.Get(id).Decl.(*types.ConstDecl)
				d.Value, d.HasValue = w.c.scalarConst(d.Type, 1, tag), true
			}
			b := checkBody(t, w, tc.want, tc.src)
			for _, d := range append(b.diags(), b.bodyDiags(t)...) {
				if d.Code == diag.E0706 && (!strings.Contains(d.Message, "cannot be judged here") || strings.Contains(d.Message, "state it")) {
					t.Errorf("E0706 message %q, want \"cannot be judged here\" and no demand to restate", d.Message)
				}
			}
		})
	}
}

func TestImportedValueParam(t *testing.T) {
	t.Parallel()
	const lib = "export enum Mode {\n\tA\n\tB\n}\n\nexport const MA: Mode = Mode.A\n\n" +
		"export GT[T, const D: Mode](const k: int): int\n\trequires k >= 0 && k < 4 && D == Mode.A {\n\treturn k\n}\n\n" +
		"export G2[T, const D: Mode](const k: int): int\n\trequires k >= 0 && k < 4 && D == Mode.A\n\trequires D != Mode.B {\n\treturn k\n}\n"
	for _, tc := range []struct{ name, src, want string }{
		{"value parameter inside it", "import lib\n\nH[U](): int {\n\treturn lib.GT[U, lib.MA](3)\n}\n", ""},
		{"forwarded value parameter, sole clause (R5-S1)", "import lib\n\nH[U](const j: int): int\n\trequires j >= 0 && j < 4 {\n\treturn lib.GT[U, lib.MA](j)\n}\n", "E0706@5:9"},
		{"forwarded value parameter, two clauses over D", "import lib\n\nH[U](const j: int): int\n\trequires j >= 0 && j < 4 {\n\treturn lib.G2[U, lib.MA](j)\n}\n", "E0706@5:9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			w.withEval()
			w.add("lib", opt{}, lib)
			// lib.MA gets Mode.A's tag by hand, as TestUnspelledForwarded does.
			id, ok := w.items.ByKey(types.ItemKey("lib", "MA"))
			if !ok {
				t.Fatal("no lib.MA")
			}
			d := w.items.Get(id).Decl.(*types.ConstDecl)
			d.Value, d.HasValue = w.c.scalarConst(d.Type, 1, 0), true
			checkBody(t, w, tc.want, tc.src)
		})
	}
}
