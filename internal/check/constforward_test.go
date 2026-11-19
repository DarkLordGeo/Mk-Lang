// Why: a function that passes its own `const` value parameter on to a
// callee's compile-time requires must state the clause, or the clause is
// never judged.
// What: TestForwardedConstRequires, TestMethodClauseFolds,
// TestCapturedConstArg.
// How: the stated clause is spelled with the callee's parameter as the
// argument and checked at each concrete call; a same-module clause that
// calls an imported method on a struct-typed const parameter is folded
// through the evaluator (EvalCall).

package check

import (
	"strings"
	"testing"

	"mk/internal/types"
)

// TestForwardedConstRequires: forwarding a const value parameter
// without the clause is E0706 at the inner call with the clause as a fix;
// with the clause, the outer call is judged (E0706 at W(9)).
func TestForwardedConstRequires(t *testing.T) {
	const lib = "export Lane(const i: int): int\n\trequires i >= 0 && i < 4 {\n\treturn i\n}\n\n" +
		"export struct P {\n\texport n: int\n}\n\nexport Q(const p: P)\n\trequires p.n < 4 {\n}\n\n" +
		"export F[const N: int](const i: int)\n\trequires i < N {\n}\n\n" +
		"export R(i: int)\n\trequires i < 4 {\n}\n"
	const same = "Lane2(const i: int): int\n\trequires i >= 0 && i < 4 {\n\treturn i\n}\n"
	cases := []struct{ name, src, want, clause string }{
		{"same module, not restated", same + "W(const k: int): int {\n\treturn Lane2(k)\n}\n", "E0706@6:9", "k >= 0 && k < 4"},
		{"same module, restated, bad call", same + "W(const k: int): int\n\trequires k >= 0 && k < 4 {\n\treturn Lane2(k)\n}\nZ(): int {\n\treturn W(9)\n}\n", "E0706@10:9", "k >= 0 && k < 4"},
		{"same module, restated, good call", same + "W(const k: int): int\n\trequires k >= 0 && k < 4 {\n\treturn Lane2(k)\n}\nZ(): int {\n\treturn W(2)\n}\n", "", ""},
		{"same module, a different clause", same + "W(const k: int): int\n\trequires k < 4 {\n\treturn Lane2(k)\n}\n", "E0706@7:9", "k >= 0 && k < 4"},
		{"imported, not restated", "import lib\n\nW(const k: int): int {\n\treturn lib.Lane(k)\n}\n", "E0706@4:9", "k >= 0 && k < 4"},
		{"imported, restated", "import lib\n\nW(const k: int): int\n\trequires k >= 0 && k < 4 {\n\treturn lib.Lane(k)\n}\n", "", ""},
		{"renamed parameter", "import lib\n\nW(const i: int): int\n\trequires i >= 0 && i < 4 {\n\treturn lib.Lane(i)\n}\n", "", ""},
		{"a field of a const parameter", "import lib\n\nW(const p: lib.P) {\n\tlib.Q(p)\n}\n", "E0706@4:2", "p.n < 4"},
		{"generic and value forwarded", "import lib\n\nG[const M: int](const k: int) {\n\tlib.F[M](k)\n}\n", "E0706@4:2", "k < M"},
		{"generic and value restated", "import lib\n\nG[const M: int](const k: int)\n\trequires k < M {\n\tlib.F[M](k)\n}\n", "", ""},
		// Only the generic is the caller's and the value folds: the finite
		// check judges it over the caller's finite set of M, and an
		// unrestricted M restates it (TestFiniteConstGenerics).
		{"generic forwarded, value literal", "import lib\n\nG[const M: int]() {\n\tlib.F[M](0)\n}\n", "E0706@4:2", "0 < M"},
		// A run-time contract is no compile-time clause: nothing to restate.
		{"run-time contract", "import lib\n\nW(const k: int) {\n\tlib.R(k)\n}\n", "", ""},
		// A literal argument is judged at the call as before.
		{"literal", "import lib\n\nW(): int {\n\treturn lib.Lane(3)\n}\n", "", ""},
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
				if got := fact(d, "clause"); got != tc.clause {
					t.Fatalf("clause %q, want %q", got, tc.clause)
				}
				if restate := strings.Contains(d.Message, "state it on this function"); restate != (len(d.Fixes) == 1) {
					t.Fatalf("message %q with %d fixes", d.Message, len(d.Fixes))
				}
			}
		})
	}
}

// TestMethodClauseFolds: `requires o.CanLoad()` in this module, o
// a const parameter of an imported struct type, is folded through EvalCall
// of the imported method on the call's struct const; false is E0706 at the
// outer call, true is clean, and a same-module method stays unchecked.
func TestMethodClauseFolds(t *testing.T) {
	const lib = "export struct O {\n\tv: u8\n}\n\nexport const A: O = O(v: 1)\nexport const B: O = O(v: 2)\n\n" +
		"export O.CanLoad(self): bool {\n\treturn self.v != 2\n}\n\n" +
		"export Load(const o: O)\n\trequires o.CanLoad() {\n}\n"
	src := "import lib\n\nLd(const o: lib.O)\n\trequires o.CanLoad() {\n\tlib.Load(o)\n}\n\nZ() {\n\tLd(lib.A)\n\tLd(lib.B)\n}\n"
	w := newWorld(t, true)
	fe := w.withEval()
	var calls int
	fe.fn = func(req *types.EvalRequest) (types.EvalResult, bool) {
		if req.What != types.EvalCall || len(req.Args) != 1 {
			return types.EvalResult{}, false
		}
		if w.items.Get(req.Item).Key.String() != "lib.O:CanLoad" {
			t.Errorf("EvalCall of %s", w.items.Get(req.Item).Key)
		}
		calls++
		cv := w.consts.Get(req.Args[0])
		var v uint64
		if len(cv.Bytes) > 0 && cv.Bytes[0] != 2 {
			v = 1
		}
		return types.EvalResult{Value: w.c.scalarConst(types.Bool, 1, v)}, true
	}
	w.add("lib", opt{}, lib)
	// The driver evaluates an imported module's consts before its importers
	// check; here lib.A and lib.B get O(v: 1) and O(v: 2) by hand.
	for name, v := range map[string]uint64{"A": 1, "B": 2} {
		id, ok := w.items.ByKey(types.ItemKey("lib", name))
		if !ok {
			t.Fatalf("no lib.%s", name)
		}
		d := w.items.Get(id).Decl.(*types.ConstDecl)
		d.Value, d.HasValue = w.c.scalarConst(d.Type, 1, v), true
	}
	b := checkBody(t, w, "E0706@10:2", src)
	if calls != 2 {
		t.Fatalf("%d EvalCall requests, want 2 (one per outer call)", calls)
	}
	if got := fact(b.bodyDiags(t)[0], "clause"); got != "o.CanLoad()" {
		t.Fatalf("clause %q", got)
	}

	// The same clause over a method of this module is left unchecked.
	w2 := newWorld(t, true)
	w2.withEval()
	checkBody(t, w2, "", "struct O {\n\tv: u8\n}\n\nconst B: O = O(v: 2)\n\nO.CanLoad(self): bool {\n\treturn self.v != 2\n}\n\nLd(const o: O)\n\trequires o.CanLoad() {\n}\n\nZ() {\n\tLd(B)\n}\n")
}

// TestCapturedConstArg: inside a `fn` literal a captured const
// parameter is no const argument (E0705 at it): the literal is
// compiled once, and without the rule the call reached mono as "const
// parameter argument is not a constant". Outside a literal it still is.
func TestCapturedConstArg(t *testing.T) {
	const lib = "export Lane(const i: int): int {\n\treturn i\n}\n"
	const call = "Call(const f: fn(): int): int {\n\treturn f()\n}\n\n"
	for _, tc := range []struct{ name, src, want string }{
		{"captured", "import lib\n\n" + call + "W(const k: int): int {\n\treturn Call(fn(): int { return lib.Lane(k) })\n}\n", "E0705@8:42"},
		{"direct", "import lib\n\n" + call + "W(const k: int): int {\n\treturn lib.Lane(k)\n}\n", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			w.withEval()
			w.add("lib", opt{}, lib)
			checkBody(t, w, tc.want, tc.src)
		})
	}
}
