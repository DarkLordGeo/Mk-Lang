// Why: fuzzers found inputs that crashed lower or the evaluator after check
// accepted them; each must stay fixed.
// What: TestFuzzValueAsMemberTypeArg and TestFuzzMistypedContractArg.
// How: checks each kept fuzz input and its relatives and compares codes.

package check

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/types"
)

// TestFuzzValueAsMemberTypeArg is corpus FuzzCheck's 77640a14944bb5b4,
// `const A00000=simd.Vec[i8,10].WidenLo[0,0]`, and its relatives: a value
// written for a type parameter in a member's bracket (a fn value, a method
// call, the namespace form) is a bracket resolve leaves to check, so nothing
// reported it, and the fn value's type `fn(Vec[i8, 10]): Vec[<error>, 0]`
// reached the MIR validator's rule M11. Each is E0301 at the argument, and
// the call or value is Poison. So is an index of a value that is no name
// (`0[0]`, `H()[0]`) in any type-argument bracket, a free function's too,
// which typeOf read as Poison with no diagnostic.
func TestFuzzValueAsMemberTypeArg(t *testing.T) {
	const s = "struct S {\n\ta: int\n}\nS.G[U, const M: int](self): int {\n\treturn 1\n}\n"
	for _, tc := range []struct{ name, src, want string }{
		{"fn value", s + "const A = S.G[0, 8]\n", "E0301@7:15"},
		{"fn value read by another const", s + "const A = S.G[0, 8]\nconst B = A\n", "E0301@7:15"},
		{"method call", s + "A(s: S): int {\n\treturn s.G[0, 8]()\n}\n", "E0301@8:13"},
		{"namespace call", s + "A(s: S): int {\n\treturn S.G[0, 8](s)\n}\n", "E0301@8:13"},
		{"operator", s + "A(s: S): int {\n\treturn s.G[1 + 1, 8]()\n}\n", "E0301@8:13"},
		// An index of a value that is no name, in any type-argument bracket
		// (resolve walks its head as a value and reports nothing).
		{"index of a literal", s + "const A = S.G[0[0], 8]\n", "E0301@7:15"},
		{"index of a call", s + "H(): [2]int {\n\treturn [0; 2]\n}\nconst A = S.G[H()[0], 8]\n", "E0301@10:15"},
		{"index in parentheses", s + "A(s: S): int {\n\treturn s.G[(0[0]), 8]()\n}\n", "E0301@8:14"},
		{"index, a free function", s + "F[U](x: int): int {\n\treturn x\n}\nconst A = F[0[0]]\n", "E0301@10:13"},
		// Unchanged: a type is accepted, an unknown name is resolve's.
		{"a type", s + "A(s: S): int {\n\treturn s.G[int, 8]()\n}\n", ""},
		{"unknown name", s + "const A = S.G[Nope, 8]\n", "E0201@7:15"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w, ds := checkNoICE(t, tc.src)
			var kept []*diag.Diagnostic
			for _, d := range ds {
				if d.Code != diag.E0208 && d.Code != diag.E0306 {
					kept = append(kept, d)
				}
			}
			if got := codes(w, kept); got != tc.want {
				t.Errorf("diagnostics %q, want %q\n%s", got, tc.want, dump(w, kept))
			}
		})
	}
}

// TestFuzzMistypedContractArg is corpus FuzzCheck's 3ba3419e29f7060e,
// `a.LoAd(0)` on a std.atomic.Atomic[i64]: the literal 0 for `const o:
// Order` is E0301, but its recorded int const went on to the evaluator as
// the argument of `requires o.CanLoad()`, which binds &Order to it only
// when its type is Order: eval's ICE "contract argument 0 ... does
// not match &P". A mistyped argument now does not fold, so the clause is
// not evaluated and E0301 is the one diagnostic. A well-typed call still
// evaluates it, the receiver auto-referenced or not. The fake evaluator
// applies eval's check.
func TestFuzzMistypedContractArg(t *testing.T) {
	const lib = "export struct O {\n\tv: u8\n}\n\nexport const A: O = O(v: 1)\nexport const B: O = O(v: 2)\n\n" +
		"export O.CanLoad(self): bool {\n\treturn self.v != 2\n}\n\n" +
		"export struct Atom[T: Plain] {\n\tx: T\n}\n\nexport Atom[T].New(x: T): Atom[T] {\n\treturn Atom(x: x)\n}\n\n" +
		"export Atom[T].Load(&self, const o: O): T\n\trequires o.CanLoad() {\n\treturn self.x\n}\n"
	for _, tc := range []struct {
		name, body, want string
		evals            int
	}{
		{"int for a struct const", "P(a: lib.Atom[i64]): i64 {\n\treturn a.Load(0)\n}\n", "E0301@4:16", 0},
		{"through a reference", "P(a: &lib.Atom[i64]): i64 {\n\treturn a.Load(1)\n}\n", "E0301@4:16", 0},
		{"bool", "P(a: lib.Atom[i64]): i64 {\n\treturn a.Load(true)\n}\n", "E0301@4:16", 0},
		{"another struct const", "struct Q {\n\tv: u8\n}\n\nconst K: Q = Q(v: 1)\n\nP(a: lib.Atom[i64]): i64 {\n\treturn a.Load(K)\n}\n", "E0301@10:16", 0},
		// Well-typed: evaluated, false is E0706, the receiver by value
		// (auto-referenced) or by reference.
		{"by value, true", "P(a: lib.Atom[i64]): i64 {\n\treturn a.Load(lib.A)\n}\n", "", 1},
		{"by value, false", "P(a: lib.Atom[i64]): i64 {\n\treturn a.Load(lib.B)\n}\n", "E0706@4:9", 1},
		{"local, false", "P(): i64 {\n\ta := lib.Atom[i64].New(1)\n\treturn a.Load(lib.B)\n}\n", "E0706@5:9", 1},
		// Another diagnostic in the body leaves a well-typed call evaluated.
		{"another error in the body", "P(a: lib.Atom[i64]): i64 {\n\t_ = lib.Atom[i64](x: 1)\n\treturn a.Load(lib.B)\n}\n", "E0204@4:6 E0706@5:9", 1},
		{"by reference, false", "P(a: &lib.Atom[i64]): i64 {\n\treturn a.Load(lib.B)\n}\n", "E0706@4:9", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var evals int
			w, _, ds := checkNoICEWith(t, opt{}, "import lib\n\n"+tc.body, func(w *world) {
				fe := w.withEval()
				fe.fn = func(req *types.EvalRequest) (types.EvalResult, bool) {
					if req.What != types.EvalContract {
						return types.EvalResult{}, false
					}
					evals++
					pd := w.items.Get(req.Item).Decl.(*types.FuncDecl)
					if len(req.Args) != 1 || len(pd.Sig.Params) != 1 {
						t.Fatalf("EvalContract with %d arguments for %d parameters", len(req.Args), len(pd.Sig.Params))
					}
					cv := w.consts.Get(req.Args[0])
					if pt := w.tab.Get(pd.Sig.Params[0].Type); pt.Kind != types.KRef || cv.Type != pt.Elem {
						t.Fatalf("contract argument of type %s for %s (eval's ICE)", w.tab.TypeString(cv.Type, nil), w.tab.TypeString(pd.Sig.Params[0].Type, nil))
					}
					var v uint64
					if len(cv.Bytes) > 0 && cv.Bytes[0] != 2 {
						v = 1
					}
					return types.EvalResult{Value: w.c.scalarConst(types.Bool, 1, v)}, true
				}
				w.add("lib", opt{}, lib)
				for name, v := range map[string]uint64{"A": 1, "B": 2} {
					id, ok := w.items.ByKey(types.ItemKey("lib", name))
					if !ok {
						t.Fatalf("no lib.%s", name)
					}
					d := w.items.Get(id).Decl.(*types.ConstDecl)
					d.Value, d.HasValue = w.c.scalarConst(d.Type, 1, v), true
				}
			})
			if got := codes(w, ds); got != tc.want || evals != tc.evals {
				t.Errorf("diagnostics %q with %d evaluations, want %q with %d\n%s", got, evals, tc.want, tc.evals, dump(w, ds))
			}
		})
	}
}
