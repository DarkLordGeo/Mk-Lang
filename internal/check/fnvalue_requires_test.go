// Why: a compile-time `requires` must be judged where a fn value names the
// function, as at a call, or `const A = simd.Vec[i8, 10].WidenLo[i16, 5]`
// passes with no diagnostic.
// What: TestE0706_FnValueSameModule, TestE0706_FnValueImported,
// TestRestateClassification, TestLiteralOwner,
// TestClauseNames, TestE0706_FnValueSimd.
// How: checks fn values against a fake evaluator that answers each clause,
// and compares codes and restated clause texts.

package check

import (
	"strings"
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

// TestE0706_FnValueSameModule: a same-module predicate is folded at the
// value (foldedRequires) for each fn-value form, paired with the call form,
// which still gives one diagnostic; a generic caller restates the clause;
// a run-time clause and an instance already wrong stay silent.
func TestE0706_FnValueSameModule(t *testing.T) {
	t.Parallel()
	const f = "F[const N: int](): int\n\trequires N > 0 {\n\treturn N\n}\n\n" // lines 1-5
	const s = "struct S[const N: int] {\n\ta: int\n}\n\n" +
		"S[N].Z(): int\n\trequires N > 0 {\n\treturn N\n}\n\n" +
		"S[N].W[const M: int](self): int\n\trequires M < N {\n\treturn M\n}\n\n" // lines 1-14
	const k = "const Max = 0\n\nG(): int\n\trequires Max > 0 {\n\treturn 1\n}\n\n" // lines 1-7
	const rt = "var Ready: int = 1\n\nG(): int\n\trequires Ready > 0 {\n\treturn 1\n}\n\n" +
		"Now(): int {\n\treturn Ready\n}\n\nT(): int\n\trequires Now() > 0 {\n\treturn 1\n}\n\n" +
		"L[const N: int](): int\n\trequires N < Ready {\n\treturn N\n}\n\n"
	const v = "Valid(n: int): bool {\n\treturn n > 0\n}\n\nP[const N: int](): int\n\trequires Valid(N) {\n\treturn N\n}\n\n" // lines 1-10
	const ap = "Apply(g: fn(): int): int {\n\treturn g()\n}\n\n"                                                             // 4 lines
	runBodyCases(t, []bodyCase{
		// Inside a requires or ensures of H the clause is restated by H's
		// requires; in a struct invariant, which nothing can state it for, it
		// is unchecked.
		{"in a requires, restated", f + ap + "H[const N: int](): int\n\trequires N > 0\n\trequires Apply(F[N]) > 0 {\n\treturn N\n}\n", ""},
		{"in a requires, call, restated", f + "H[const N: int](): int\n\trequires N > 0\n\trequires F[N]() > 0 {\n\treturn N\n}\n", ""},
		{"in an ensures, restated", f + ap + "H[const N: int](): int\n\trequires N > 0\n\tensures result == Apply(F[N]) {\n\treturn N\n}\n", ""},
		{"in an ensures, finite set", f + ap + "H[const N: int](): int\n\trequires N == 1 || N == 2\n\tensures result == Apply(F[N]) {\n\treturn N\n}\n", ""},
		{"in a requires, not restated", f + ap + "H[const N: int](): int\n\trequires Apply(F[N]) > 0 {\n\treturn N\n}\n", "E0706@11:17"},
		{"in an invariant", f + ap + "struct T[const N: int] {\n\ta: int\n\tinvariant Apply(F[N]) > 0\n}\n", ""},
		{"in an invariant, call", f + "struct T[const N: int] {\n\ta: int\n\tinvariant F[N]() > 0\n}\n", ""},
		// `F[A]` (genericValue).
		{"free, value", f + "const A = F[0]\n", "E0706@6:11"},
		{"free, value in a body", f + "H(): fn(): int {\n\treturn F[0]\n}\n", "E0706@7:9"},
		{"free, call", f + "H(): int {\n\treturn F[0]()\n}\n", "E0706@7:9"},
		{"free, holds", f + "const A = F[1]\n", ""},
		// `T[A].M` (staticValue, genericFnValue).
		{"owner, value", s + "const A = S[0].Z\n", "E0706@15:11"},
		{"owner, call", s + "H(): int {\n\treturn S[0].Z()\n}\n", "E0706@16:9"},
		{"owner, holds", s + "const A = S[2].Z\n", ""},
		// `T[A].M[B]` (memberGenericValue).
		{"member, value", s + "const A = S[4].W[5]\n", "E0706@15:11"},
		{"member, call", s + "H(x: S[4]): int {\n\treturn x.W[5]()\n}\n", "E0706@16:9"},
		{"member, namespace call", s + "H(x: S[4]): int {\n\treturn S[4].W[5](x)\n}\n", "E0706@16:9"},
		{"member, holds", s + "const A = S[4].W[3]\n", ""},
		// A non-generic function whose clause names a const (the ident path).
		{"consts, value", k + "const A = G\n", "E0706@8:11"},
		{"consts, call", k + "H(): int {\n\treturn G()\n}\n", "E0706@9:9"},
		// `T.M` of a non-generic type (staticValue's non-generic return).
		{"plain owner, value", "struct P {\n\ta: int\n}\n\n" + k + "P.Z(): int\n\trequires Max > 0 {\n\treturn 1\n}\n\nconst A = P.Z\n", "E0706@17:11"},
		{"plain owner, call", "struct P {\n\ta: int\n}\n\n" + k + "P.Z(): int\n\trequires Max > 0 {\n\treturn 1\n}\n\nH(): int {\n\treturn P.Z()\n}\n", "E0706@18:9"},
		// Inside a generic the clause over the caller's const generic is
		// restated, as at a call (E0706 with the clause as a fix).
		{"generic does not restate", f + "H[const N: int](): fn(): int {\n\treturn F[N]\n}\n", "E0706@7:9"},
		{"generic call does not restate", f + "H[const N: int](): int {\n\treturn F[N]()\n}\n", "E0706@7:9"},
		{"generic restates", f + "H[const N: int](): fn(): int\n\trequires N > 0 {\n\treturn F[N]\n}\n", ""},
		{"owner generic restates", s + "H[const N: int](): fn(): int\n\trequires N > 0 {\n\treturn S[N].Z\n}\n", ""},
		{"owner generic does not restate", s + "H[const N: int](): fn(): int {\n\treturn S[N].Z\n}\n", "E0706@16:9"},
		// A run-time clause is not judged at a value or at a call, nor
		// restated in a generic: over a value parameter or a var. A call of
		// a same-module function does not fold, so `Now() > 0` (Now reading
		// a var) is left to run time too.
		{"run-time clause", "R(n: int): int\n\trequires n > 0 {\n\treturn n\n}\n\nconst A = R\n", ""},
		{"var clause, value", rt + "H(): fn(): int {\n\treturn G\n}\n", ""},
		{"var clause, call", rt + "H(): int {\n\treturn G()\n}\n", ""},
		{"call clause, value", rt + "H(): fn(): int {\n\treturn T\n}\n", ""},
		{"call clause, call", rt + "H(): int {\n\treturn T()\n}\n", ""},
		// A function called on const arguments is compile-time: in a generic
		// caller it is restated as before, at a value and at a call.
		{"function clause, generic value", v + "H[const N: int](): fn(): int {\n\treturn P[N]\n}\n", "E0706@11:9"},
		{"function clause, generic call", v + "H[const N: int](): int {\n\treturn P[N]()\n}\n", "E0706@11:9"},
		{"function clause, generic restates", v + "H[const N: int](): fn(): int\n\trequires Valid(N) {\n\treturn P[N]\n}\n", ""},
		{"var clause, generic value", rt + "H[const N: int](): fn(): int {\n\treturn L[N]\n}\n", ""},
		{"var clause, generic call", rt + "H[const N: int](): int {\n\treturn L[N]()\n}\n", ""},
		{"var clause, concrete value", rt + "const A = L[40]\n", ""},
		// Inside a fn literal the clause is restated by the function the
		// literal is written in.
		{"literal restates, value", f + "H[const N: int](): int\n\trequires N > 0 {\n\tg := fn(): int {\n\t\tf := F[N]\n\t\treturn f()\n\t}\n\treturn g()\n}\n", ""},
		{"literal restates, call", f + "H[const N: int](): int\n\trequires N > 0 {\n\tg := fn(): int {\n\t\treturn F[N]()\n\t}\n\treturn g()\n}\n", ""},
		{"literal does not restate, value", f + "H[const N: int](): int {\n\tg := fn(): int {\n\t\tf := F[N]\n\t\treturn f()\n\t}\n\treturn g()\n}\n", "E0706@8:8"},
		{"literal does not restate, call", f + "H[const N: int](): int {\n\tg := fn(): int {\n\t\treturn F[N]()\n\t}\n\treturn g()\n}\n", "E0706@8:10"},
		// An instance already wrong is its one diagnostic.
		{"wrong count", f + "const A = F[0, 1]\n", "E0304@6:11"},
		{"owner not written", s + "const A = S.Z\n", "E0711@15:11"},
	})
}

// TestE0706_FnValueImported: an imported predicate is evaluated at the
// value (evalClause with no call), for `lib.F[A]` and the non-generic
// `lib.G` and `lib.K`, whose clauses name a const; a passing value asks as
// well. The fake evaluator answers each clause from lib's state: `N > 0`
// from the request's generic argument, `Max > 0` and `Min > 0` from the
// consts' values (0 and 1), `Valid(N)` from N. `lib.Sz`'s clause answers
// EvalStage, as the evaluator does for `SizeOf[P]()` of a struct of the
// naming module: E0908 at a call, unchecked at a value. With closed
// arguments every clause is asked, whatever it names: `Ready > 0` and
// `Now() > 0` answer EvalRefused for the var (left to run time, no
// diagnostic), `N > 0 && Ready > 0` is false for N = 0 before Ready is read
// (E0706), and a refused raw access or a panic keeps its E0901 at a call. A
// clause over a parameter that is not const is never asked (no argument).
// In a generic caller nothing is asked: a clause naming lib's var is not
// restated, one naming an import's `cfg.Limit` is (the text cannot resolve
// the import), and the finite check restates a clause one of whose
// combinations is refused.
func TestE0706_FnValueImported(t *testing.T) {
	t.Parallel()
	const lib = "export F[const N: int](): int\n\trequires N > 0 {\n\treturn N\n}\n\n" +
		"export const Max = 0\n\nexport G(): int\n\trequires Max > 0 {\n\treturn 1\n}\n\n" +
		"export const Min = 1\n\nexport K(): int\n\trequires Min > 0 {\n\treturn 1\n}\n\n" +
		"export R(n: int): int\n\trequires n > 0 {\n\treturn n\n}\n\n" +
		"export var Ready: int = 1\n\nexport V(): int\n\trequires Ready > 0 {\n\treturn 1\n}\n\n" +
		"export Now(): int {\n\treturn Ready\n}\n\nexport Tick(): int\n\trequires Now() > 0 {\n\treturn 1\n}\n\n" +
		"export Sz[U](): int\n\trequires SizeOf[U]() <= 8 {\n\treturn 1\n}\n\n" +
		"export Valid(n: int): bool {\n\treturn n > 0\n}\n\nexport P[const N: int](): int\n\trequires Valid(N) {\n\treturn N\n}\n\n" +
		"export Boom(): bool {\n\treturn true\n}\n\nexport B(): int\n\trequires Boom() {\n\treturn 1\n}\n\n" +
		"export Both[const N: int](): int\n\trequires N > 0 && Ready > 0 {\n\treturn N\n}\n\n" +
		"export Peek(): bool {\n\treturn true\n}\n\nexport Raw(): int\n\trequires Peek() {\n\treturn 1\n}\n\n" +
		"export VN[const N: int](): int\n\trequires N < Ready {\n\treturn N\n}\n\n" +
		"export CN[const N: int](): int\n\trequires N < cfg.Limit {\n\treturn N\n}\n\n" +
		"export Fz[const N: int](): int\n\trequires (N == 0 && Now() > 0) || N == 1 {\n\treturn N\n}\n"
	for _, tc := range []struct {
		name, body, want string
		evals            int
	}{
		// An untyped const's initializer is typed in stage 5 too (its
		// diagnostics dropped there), so it asks twice, a value as a call.
		{"value, false", "const A = lib.F[0]\n", "E0706@3:11", 2},
		{"value, true", "const A = lib.F[1]\n", "", 2},
		{"call in a const, false", "const A = lib.F[0]()\n", "E0706@3:11", 2},
		{"value in a body, false", "H(): fn(): int {\n\treturn lib.F[0]\n}\n", "E0706@4:9", 1},
		{"value in a body, true", "H(): fn(): int {\n\treturn lib.F[3]\n}\n", "", 1},
		{"call, false", "H(): int {\n\treturn lib.F[0]()\n}\n", "E0706@4:9", 1},
		{"non-generic value, false", "const A = lib.G\n", "E0706@3:11", 2},
		{"non-generic call, false", "H(): int {\n\treturn lib.G()\n}\n", "E0706@4:9", 1},
		{"non-generic value, true", "H(): fn(): int {\n\treturn lib.K\n}\n", "", 1},
		{"non-generic call, true", "H(): int {\n\treturn lib.K()\n}\n", "", 1},
		// A clause over a parameter that is not const has no argument: no
		// diagnostic, no request.
		{"value-parameter clause, value", "const A = lib.R\n", "", 0},
		{"value-parameter clause, value in a body", "H(): fn(int): int {\n\treturn lib.R\n}\n", "", 0},
		// Refused for a run-time operation (a var read): left to run time,
		// no E0901, whether the clause names the var or a function reads it.
		{"var clause, value", "H(): fn(): int {\n\treturn lib.V\n}\n", "", 1},
		{"var clause, call", "H(): int {\n\treturn lib.V()\n}\n", "", 1},
		{"refused clause, value", "H(): fn(): int {\n\treturn lib.Tick\n}\n", "", 1},
		{"refused clause, call", "H(): int {\n\treturn lib.Tick()\n}\n", "", 1},
		// Evaluated first, so a clause false before its var is read stays
		// E0706, and one that is refused passes.
		{"var clause false first, call", "H(): int {\n\treturn lib.Both[0]()\n}\n", "E0706@4:9", 1},
		{"var clause false first, value", "H(): fn(): int {\n\treturn lib.Both[0]\n}\n", "E0706@4:9", 1},
		{"var clause refused, call", "H(): int {\n\treturn lib.Both[1]()\n}\n", "", 1},
		// A panic or a refused raw access keeps its diagnostic at a call; a
		// value is unchecked.
		{"panicking clause, call", "H(): int {\n\treturn lib.B()\n}\n", "E0901@4:9", 1},
		{"panicking clause, value", "H(): fn(): int {\n\treturn lib.B\n}\n", "", 1},
		{"raw access, call", "H(): int {\n\treturn lib.Raw()\n}\n", "E0901@4:9", 1},
		{"raw access, value", "H(): fn(): int {\n\treturn lib.Raw\n}\n", "", 1},
		// A function called on const arguments is compile-time (the
		// rejection at a call kept), at a value too, and restated in a
		// generic caller.
		{"function clause, call, false", "H(): int {\n\treturn lib.P[0]()\n}\n", "E0706@4:9", 1},
		{"function clause, value, false", "H(): fn(): int {\n\treturn lib.P[0]\n}\n", "E0706@4:9", 1},
		{"function clause, value, true", "H(): fn(): int {\n\treturn lib.P[2]\n}\n", "", 1},
		{"function clause, generic call", "H[const N: int](): int {\n\treturn lib.P[N]()\n}\n", "E0706@4:9", 0},
		{"function clause, generic value", "H[const N: int](): fn(): int {\n\treturn lib.P[N]\n}\n", "E0706@4:9", 0},
		// In a generic caller: lib's var by name is a run-time contract, not
		// restated; an import's var through the text is restated.
		{"var clause, generic call", "H[const N: int](): int {\n\treturn lib.VN[N]()\n}\n", "", 0},
		{"var clause, generic value", "H[const N: int](): fn(): int {\n\treturn lib.VN[N]\n}\n", "", 0},
		{"import var clause, generic call", "H[const N: int](): int {\n\treturn lib.CN[N]()\n}\n", "E0706@4:9", 0},
		// The finite check: one refused combination is unknown, so the
		// clause is restated; a set where every combination holds passes.
		{"finite, a refused combination", "H[const N: int](): int\n\trequires N == 0 || N == 1 {\n\treturn lib.Fz[N]()\n}\n", "E0706@5:9", 1},
		{"finite, every combination holds", "H[const N: int](): int\n\trequires N == 1 {\n\treturn lib.Fz[N]()\n}\n", "", 1},
		{"finite, a false combination", "H[const N: int](): fn(): int\n\trequires N == 1 || N == 2 {\n\treturn lib.Fz[N]\n}\n", "E0706@5:9", 2},
		// An evaluation the stage rule refuses: E0908 at a call, nothing at
		// a value.
		{"stage rule, call", "struct P {\n\ta: int\n}\n\nH(): int {\n\treturn lib.Sz[P]()\n}\n", "E0908@8:9", 1},
		{"stage rule, value", "struct P {\n\ta: int\n}\n\nH(): fn(): int {\n\treturn lib.Sz[P]\n}\n", "", 1},
		{"generic does not restate", "H[const N: int](): fn(): int {\n\treturn lib.F[N]\n}\n", "E0706@4:9", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var evals int
			w, _, ds := checkNoICEWith(t, opt{}, "import lib\n\n"+tc.body, func(w *world) {
				fe := w.withEval()
				positive := func(name string) uint64 {
					id, ok := w.items.ByKey(types.ItemKey("lib", name))
					if !ok {
						t.Fatalf("no lib.%s", name)
					}
					if n, ok := w.c.constInt(w.items.Get(id).Decl.(*types.ConstDecl).Value); ok && n > 0 {
						return 1
					}
					return 0
				}
				fe.fn = func(req *types.EvalRequest) (types.EvalResult, bool) {
					if req.What != types.EvalContract {
						return types.EvalResult{}, false
					}
					evals++
					var v uint64
					switch fn := w.in.Str(w.items.Get(w.items.Get(req.Item).Owner).Name); fn {
					case "F", "P":
						if n, ok := w.c.constInt(req.GenericArgs[0].Const.Value); ok && n > 0 {
							v = 1
						}
					case "G":
						v = positive("Max")
					case "K":
						v = positive("Min")
					case "Sz":
						return types.EvalResult{Err: &types.EvalError{Kind: types.EvalStage}}, true
					case "Tick", "V": // reads the var Ready
						return types.EvalResult{Err: &types.EvalError{Kind: types.EvalRefused, Refusal: types.RefuseGlobal}}, true
					case "Both": // N > 0 && Ready > 0: false before Ready is read
						if n, ok := w.c.constInt(req.GenericArgs[0].Const.Value); ok && n > 0 {
							return types.EvalResult{Err: &types.EvalError{Kind: types.EvalRefused, Refusal: types.RefuseGlobal}}, true
						}
					case "Fz": // (N == 0 && Now() > 0) || N == 1
						switch n, _ := w.c.constInt(req.GenericArgs[0].Const.Value); n {
						case 0:
							return types.EvalResult{Err: &types.EvalError{Kind: types.EvalRefused, Refusal: types.RefuseGlobal}}, true
						case 1:
							v = 1
						}
					case "Raw": // Peek reads through a null pointer
						return types.EvalResult{Err: &types.EvalError{Kind: types.EvalRefused, Refusal: types.RefuseRawAccess}}, true
					case "B":
						return types.EvalResult{Err: &types.EvalError{Kind: types.EvalPanic, Panic: types.PanicExplicit, Message: "boom"}}, true
					default:
						t.Errorf("EvalContract for %s's clause", fn)
					}
					return types.EvalResult{Value: w.c.scalarConst(types.Bool, 1, v)}, true
				}
				w.add("cfg", opt{}, "export var Limit: int = 1\n")
				w.add("lib", opt{}, "import cfg\n\n"+lib)
				for name, v := range map[string]uint64{"Max": 0, "Min": 1} {
					id, ok := w.items.ByKey(types.ItemKey("lib", name))
					if !ok {
						t.Fatalf("no lib.%s", name)
					}
					d := w.items.Get(id).Decl.(*types.ConstDecl)
					d.Value, d.HasValue = w.c.scalarConst(d.Type, 8, v), true
				}
			})
			var kept []*diag.Diagnostic
			for _, d := range ds {
				if d.Code != diag.E0208 {
					kept = append(kept, d)
				}
			}
			if got := codes(w, kept); got != tc.want || evals != tc.evals {
				t.Errorf("diagnostics %q with %d evaluations, want %q with %d\n%s", got, evals, tc.want, tc.evals, dump(w, kept))
			}
		})
	}
}

// TestRestateClassification: in a generic caller, where nothing can be
// evaluated, a same-module clause is classified by its resolved names, so a
// name is bound by its own file's imports: `cfg.Ready` under `import cfg =
// consts` is a const and restated, under `import cfg` a var and not, though
// another file of the module imports the other one.
func TestRestateClassification(t *testing.T) {
	t.Parallel()
	const fn = "F[const N: int](): int\n\trequires N < cfg.Ready {\n\treturn N\n}\n\nH[const N: int](): int {\n\treturn F[N]()\n}\n"
	for _, tc := range []struct{ name, a, b, want string }{
		{"aliased const", "import cfg = consts\n\n" + fn, "import cfg\n\nX(): int {\n\treturn cfg.Ready\n}\n", "E0706@9:9"},
		{"var", "import cfg\n\n" + fn, "import cfg = consts\n\nX(): int {\n\treturn cfg.Ready\n}\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			w.add("cfg", opt{}, "export var Ready: int = 1\n")
			w.add("consts", opt{}, "export const Ready: int = 5\n")
			b := w.add("m", opt{}, tc.a, tc.b)
			var ds []*diag.Diagnostic
			for _, d := range append(b.diags(), b.bodyDiags(t)...) {
				if d.Code != diag.E0208 {
					ds = append(ds, d)
				}
			}
			if got := codes(w, ds); got != tc.want {
				t.Fatalf("diagnostics %q, want %q\n%s", got, tc.want, dump(w, ds))
			}
		})
	}
}

// TestLiteralOwner: inside a fn literal the restated clause is the
// owner function's: the fix is placed at the end of that function's
// signature (before, before its body), and the finite check reads that
// function's clauses (finiteSet through ownerSig), so
// `requires N == 1 || N == 2` covers `F[N]()` against `N > 0`.
func TestLiteralOwner(t *testing.T) {
	t.Parallel()
	const f = "F[const N: int](): int\n\trequires N > 0 {\n\treturn N\n}\n\n"
	t.Run("fix on the owner", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, true)
		src := f + "H[const N: int](): int {\n\tg := fn(): int {\n\t\treturn F[N]()\n\t}\n\treturn g()\n}\n"
		b := w.add("m", opt{}, src)
		ds := b.bodyDiags(t)
		if len(ds) != 1 || ds[0].Code != diag.E0706 || len(ds[0].Fixes) != 1 {
			t.Fatalf("want one E0706 with a fix:\n%s", dump(w, ds))
		}
		at := ds[0].Fixes[0].Edits[0].Span.Start
		if want := uint32(strings.Index(src, " {\n\tg :=")); at != want {
			t.Errorf("fix at offset %d, want %d (the end of H's signature)", at, want)
		}
	})
	t.Run("finite set of the owner", func(t *testing.T) {
		t.Parallel()
		checkBody(t, newWorld(t, true), "", f+"H[const N: int](): int\n\trequires N == 1 || N == 2 {\n\tg := fn(): int {\n\t\treturn F[N]()\n\t}\n\treturn g()\n}\n")
	})
}

// TestClauseNames: the text classifier reads no name inside a literal:
// `b'n'`, `b"x"`, a raw `r"C:\"` (no escape, so the quote after the
// backslash ends it) and a str with an escaped quote.
func TestClauseNames(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ text, want string }{
		{`sep != b'n' && x`, "sep x"},
		{`name != r"C:\" && Ready > 0`, "name Ready"},
		{`s != b"Ready" && n`, "s n"},
		{`s != "a\"Ready" && k`, "s k"},
		{`o.CanLoad() && lib.Max > 0x1F`, "o .CanLoad lib .Max"},
		// A quote inside a multi-line literal.
		{"s != \"\"\"\n    say \"Ready\"\n    \"\"\" && k", "s k"},
	} {
		var got []string
		for _, n := range clauseNames(tc.text) {
			s := n.name
			if n.member {
				s = "." + s
			}
			got = append(got, s)
		}
		if strings.Join(got, " ") != tc.want {
			t.Errorf("clauseNames(%s) = %q, want %q", tc.text, strings.Join(got, " "), tc.want)
		}
	}
}

// TestE0706_FnValueSimd is the item's program and its relatives over the
// real std.simd and evaluator (mk check over the overlay): each fn-value
// form of a Vec method is judged as its call is.
func TestE0706_FnValueSimd(t *testing.T) {
	t.Parallel()
	clause := " requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64"
	for _, tc := range []struct{ name, src, want string }{
		{"WidenLo value, 10 lanes", "const A = simd.Vec[i8, 10].WidenLo[i16, 5]~", "main.mk:3:11: E0706"},
		{"WidenLo call, 10 lanes", "F(v: simd.Vec[i8, 10]): simd.Vec[i16, 5] {~    return v.WidenLo[i16, 5]()~}~", "main.mk:4:12: E0706"},
		{"WidenLo value, wrong M", "const A = simd.Vec[i8, 16].WidenLo[i16, 4]~", "main.mk:3:11: E0706"},
		{"WidenLo value, holds", "const A = simd.Vec[i8, 16].WidenLo[i16, 8]~", ""},
		{"Splat value, 3 lanes", "const A = simd.Vec[u8, 3].Splat~", "main.mk:3:11: E0706"},
		{"Splat value, 16 lanes", "const A = simd.Vec[u8, 16].Splat~", ""},
		{"Splat value in a generic, restated", "G[const N: int](): fn(x: u8): simd.Vec[u8, N]" + clause + " {~    return simd.Vec[u8, N].Splat~}~", ""},
		{"Splat value in a generic, not restated", "G[const N: int](): fn(x: u8): simd.Vec[u8, N] {~    return simd.Vec[u8, N].Splat~}~", "main.mk:4:12: E0706"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := "import std.simd~~" + tc.src + "~export Main(sys: &System) {~}~"
			out, code := mkOverlay(t, strings.ReplaceAll(src, "~", string(rune(10))), "check")
			if tc.want == "" && code != 0 || tc.want != "" && (code == 0 || !strings.Contains(out, tc.want)) {
				t.Fatalf("exit %d, want %q:%c%s", code, tc.want, rune(10), out)
			}
		})
	}
}
