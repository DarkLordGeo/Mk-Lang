// Why: Slice[T]'s methods apply to slices, arrays and implementers, and a
// const argument may call a function; both are typed in stage 6.
// What: TestSliceMethods, TestConstArgCall, TestInterfaceOwnedSelf.
// How: checks programs over the corpus prelude (sliceWorld) and compares
// codes and recorded uses.

package check

import (
	"testing"

	"mk/internal/ast"
	"mk/internal/types"
)

// sliceWorld is a world whose std.core is the corpus prelude, which holds
// the reference's Slice[T] methods.
func sliceWorld(t *testing.T) *world {
	return newWorldCore(t, true, corpusPrelude(t))
}

func TestSliceMethods(t *testing.T) {
	const less = "Less(a: &int, b: &int): bool {\n\treturn *a < *b\n}\n\n"
	cases := []bodyCase{
		{"on mut []T", less + "F(xs: mut []int) {\n\txs.Sort(Less)\n\txs.Swap(0, 1)\n}\n", ""},
		{"on []T", less + "F(xs: []int): bool {\n\treturn xs.Has(&1)\n}\n", ""},
		{"on an array", less + "F() {\n\tvar a := [3, 1, 2]\n\ta.Sort(Less)\n}\n", ""},
		{"on an implementer", less + "F(var l: List[int]) {\n\tl.Sort(Less)\n\tl.Reverse()\n}\n", ""},
		{"own member wins", "F(l: &List[int]): int {\n\treturn l.Len()\n}\n", ""},
		{"element type checked", "LessS(a: &str, b: &str): bool {\n\treturn true\n}\n\nF(xs: mut []int) {\n\txs.Sort(LessS)\n}\n", "E0301@6:10"},
		{"no such method", "F(xs: mut []int) {\n\txs.Shuffle()\n}\n", "E0203@2:5"},
		{"not a slice", "F(n: int) {\n\tn.Sort()\n}\n", "E0203@2:4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checkBody(t, sliceWorld(t), tc.want, tc.src)
		})
	}
	t.Run("use and receiver", func(t *testing.T) {
		w := sliceWorld(t)
		b := checkBody(t, w, "", less+"F(xs: mut []int, var l: List[int]) {\n\txs.Sort(Less)\n\tl.Sort(Less)\n}\n")
		body := w.c.Check(b.m, b.item("F"))
		fd := body.Node.(*ast.FuncDecl)
		for i, want := range []Adjust{{}, {Kind: AdjRefMut}} {
			call := fd.Body.Stmts[i].(*ast.ExprStmt).X.(*ast.CallExpr)
			use := body.Uses[body.slot(call.ID())]
			it := w.items.Get(use.Item)
			if use.Kind != UseMethod || it.Owner != w.rs.Known().Slice || w.in.Str(it.Name) != "Sort" {
				t.Errorf("stmt %d: use %+v (%s)", i, use, it.Key)
			}
			fe := call.Fun.(*ast.FieldExpr)
			if a := body.Adjust[body.slot(fe.X.ID())]; a != want {
				t.Errorf("stmt %d: receiver adjust %+v, want %+v", i, a, want)
			}
			if g := body.Generic[use.Inst]; len(g) != 1 || g[0].Type != types.Int {
				t.Errorf("stmt %d: generic args %v, want [int]", i, g)
			}
		}
	})
}

// TestConstArgCall: `[F()]u8` calls a function at compile time. A function
// of the same module is E0908 at once; an imported one is an EvalCall
// request at StageSignatures whose value becomes the argument.
func TestConstArgCall(t *testing.T) {
	t.Run("same module", func(t *testing.T) {
		w := newWorld(t, true)
		b := checkBody(t, w, "E0908@6:8", "Four(): int {\n\treturn 4\n}\n\nZero(): int {\n\tbuf: [Four()]u8 = [0; 4]\n\treturn int(buf[0])\n}\n")
		ds := b.bodyDiags(t)
		if got := fact(ds[0], "items"); got != "[m.Four]" {
			t.Errorf("items %s", got)
		}
	})
	t.Run("in a signature", func(t *testing.T) {
		w := newWorld(t, true)
		checkCodes(t, w, "m", opt{}, "E0908@5:7", "Four(): int {\n\treturn 4\n}\n\nG(b: [Four()]u8) {\n}\n")
	})
	t.Run("imported", func(t *testing.T) {
		w := newWorld(t, true)
		fe := w.withEval()
		w.add("lib", opt{}, "export Four(): int {\n\treturn 4\n}\n\nexport Twice(n: int): int {\n\treturn 2 * n\n}\n\nexport Flag(): bool {\n\treturn true\n}\n")
		fe.answers["lib.Four"] = types.EvalResult{Value: w.c.scalarConst(types.Int, 8, 4)}
		fe.answers["lib.Twice"] = types.EvalResult{Value: w.c.scalarConst(types.Int, 8, 6)}
		b := checkBody(t, w, "E0301@6:6", "import lib\n\nF(): int {\n\tbuf: [lib.Four()]u8 = [0; 4]\n\tc: [lib.Twice(3)]u8 = [0; 6]\n\td: [lib.Flag()]u8 = [0; 1]\n\treturn int(buf[0]) + int(c[0]) + int(d[0])\n}\n")
		_ = b
		var calls []string
		for _, r := range fe.requests() {
			calls = append(calls, r)
		}
		if len(calls) != 2 || calls[0] != "1/0 lib.Four" || calls[1] != "1/0 lib.Twice" {
			t.Errorf("requests %q, want EvalCall at StageSignatures of Four and Twice", calls)
		}
		for _, r := range fe.asked {
			if r.Item == w.mods["lib"].item("Twice") && (len(r.Args) != 1 || !constIs(w, r.Args[0], 3)) {
				t.Errorf("Twice's argument %v", r.Args)
			}
		}
	})
	t.Run("fallible or generic callee", func(t *testing.T) {
		w := newWorld(t, true)
		fe := w.withEval()
		w.add("lib", opt{}, "export error Bad\n\nexport Maybe(): Bad!int {\n\treturn 4\n}\n\nexport Gen[T](): int {\n\treturn 4\n}\n")
		// Neither is Poison without a diagnostic: the
		// fallible one's type is not int, the generic one waits for mono.
		checkBody(t, w, "E0301@3:7 E0705@3:27", "import lib\n\nF(a: [lib.Maybe()]u8, b: [lib.Gen[int]()]u8) {\n}\n")
		if r := fe.requests(); len(r) != 0 {
			t.Errorf("requests %q: neither call is evaluated", r)
		}
	})
	t.Run("no evaluator", func(t *testing.T) {
		w := newWorld(t, true)
		w.add("lib", opt{}, "export Four(): int {\n\treturn 4\n}\n")
		b := checkBody(t, w, "E0901@4:8", "import lib\n\nF(): int {\n\tbuf: [lib.Four()]u8 = [0; 4]\n\treturn int(buf[0])\n}\n")
		if r := fact(b.bodyDiags(t)[0], "refused"); r != "no-evaluator" {
			t.Errorf("refused %q", r)
		}
	})
}

func constIs(w *world, id types.ConstID, n int64) bool {
	v, ok := w.c.constInt(id)
	return ok && v == n
}

// TestInterfaceOwnedSelf: a `Type.Name` function owned by an interface I
// (as std's `Slice[T].Sort` is) has `self: I[A]`, whose calls of I's own
// methods type as a bound's (UseIfaceMethod), with I's parameters := A.
// In a function with no generics (I has no type parameters) such a call
// is E0702 (IfaceCallLowers): it would be an interface call in an instance
// body, an internal error in lower; S[T]'s functions keep their calls.
func TestInterfaceOwnedSelf(t *testing.T) {
	w := newWorld(t, true)
	b := checkBody(t, w, "E0203@9:7 E0209@8:2 E0304@16:2 E0702@13:14 E0702@8:7",
		"interface I {\n\tQ(&self): int\n}\n\nI.M(self) {\n\t_ = self\n\tx := self\n\tself.Q()\n\tself.Nope()\n}\n\nI.N(&self): int {\n\treturn self.Q()\n}\n\nI[T].G(&self) {\n}\n\n"+
			"interface S[T] {\n\tItems(&self): []T\n}\n\nS[T].Count(&self): int {\n\treturn self.Items().Len()\n}\n\nS[T].First(&self): T {\n\treturn self.Items()[0]\n}\n")
	body := w.c.Check(b.m, b.item("I:N"))
	ret := body.Node.(*ast.FuncDecl).Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	if u := body.Uses[body.slot(ret.ID())]; u.Kind != UseIfaceMethod || u.Item != b.item("I") || u.Index != 0 {
		t.Errorf("self.Q(): %+v", u)
	}
}
