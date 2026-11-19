// Why: error sets, try, fail, else, widening, Must and Or decide how
// failure flows through a program, and check owns their typing.
// What: TestE0801, TestE0802, TestE0804, TestE0307_NotOptional,
// TestElseForms, TestWidenRecorded, TestWidenAtGenericArgument.
// How: checks small bodies and compares codes and the recorded widenings.

package check

import (
	"testing"

	"mk/internal/ast"
	"mk/internal/diag"
)

const loadSrc = "error NotFound(id: i64)\nerror Denied\n\nLoad(id: i64): NotFound!int {\n\tif id < 0 {\n\t\tfail NotFound(id: id)\n\t}\n\treturn 1\n}\n\n"

func TestE0801(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"Load", loadSrc + "Show(id: i64): NotFound | Denied!int {\n\tn := try Load(id)\n\treturn n\n}\n", ""},
		{"try not covered", loadSrc + "Quiet(id: i64): Denied!int {\n\tn := try Load(id)\n\treturn n\n}\n", "E0801@12:7"},
		{"try in a function that cannot fail", loadSrc + "Quiet(id: i64): int {\n\treturn try Load(id)\n}\n", "E0801@12:9"},
		{"fail not covered", "error A\nerror B\n\nF(): A!void {\n\tfail B\n}\n", "E0801@5:2"},
		{"fail a non-error", "error A\n\nF(): A!void {\n\tfail 1\n}\n", "E0301@4:7"},
		{"test is not checked", loadSrc + "test \"t\" {\n\tn := try Load(1)\n}\n", ""},
		{"widening", "error A\nerror B\n\nG(): A!int {\n\treturn 1\n}\n\nH(e: A | B) {\n}\n\nF(): A | B!int {\n\tH(A)\n\tx := try G()\n\tfail A\n}\n", ""},
	})
	t.Run("missing fact", func(t *testing.T) {
		w := newWorld(t, true)
		b := checkBody(t, w, "E0801@12:7", loadSrc+"Quiet(id: i64): Denied!int {\n\tn := try Load(id)\n\treturn n\n}\n")
		f, _ := b.bodyDiags(t)[0].FactValue("missing")
		if len(f.List) != 1 || f.List[0].Str != "NotFound" {
			t.Errorf("missing %+v", f.List)
		}
	})
}

func TestE0802(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"dropped", loadSrc + "Save() {\n\tLoad(1)\n}\n", "E0802@12:2"},
		{"bound", loadSrc + "Save() {\n\tx := Load(1)\n}\n", "E0802@12:7"},
		{"passed", loadSrc + "Use(n: int) {\n}\n\nSave() {\n\tUse(Load(1))\n}\n", "E0802@15:6"},
		{"returned", loadSrc + "Save(): NotFound!int {\n\treturn Load(1)\n}\n", "E0802@12:9"},
		{"inner call under try", loadSrc + "struct Box2 {\n\tn: int\n}\n\nBox2.Get(&self): int {\n\treturn self.n\n}\n\nMk(): NotFound!Box2 {\n\treturn Box2(n: 1)\n}\n\nSave(): NotFound!int {\n\treturn try Mk().Get()\n}\n", "E0802@24:13"},
		{"consumed by match", loadSrc + "Save(): int {\n\tmatch Load(1) {\n\t\tok(v) {\n\t\t\treturn v\n\t\t}\n\t\terr(e) {\n\t\t\treturn 0\n\t\t}\n\t}\n}\n", ""},
		{"Must", loadSrc + "Save(): int {\n\treturn Load(1).Must()\n}\n", ""},
	})
}

func TestE0804(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"plain value", "G(): int {\n\treturn 1\n}\n\nF(): int {\n\treturn try G()\n}\n", "E0804@6:9"},
		{"optional", "G(): ?int {\n\treturn none\n}\n\nF(): ?int {\n\tx := try G()\n\treturn x\n}\n", "E0804@6:7"},
	})
	t.Run("proven fix", func(t *testing.T) {
		w := newWorld(t, true)
		b := checkBody(t, w, "E0804@6:9", "G(): int {\n\treturn 1\n}\n\nF(): int {\n\treturn try G()\n}\n")
		d := b.bodyDiags(t)[0]
		if len(d.Fixes) != 1 || d.Fixes[0].Class != diag.Proven || d.Fixes[0].Edits[0].Text != "" {
			t.Errorf("fix %+v", d.Fixes)
		}
	})
}

const iterSrc = "struct C {\n\tn: int\n}\n\nC.Next(mut self): ?int {\n\treturn none\n}\n\nerror E\n\nF2(): E!int {\n\treturn 1\n}\n\nF3(): E!?int {\n\treturn none\n}\n\n"

func TestE0307_NotOptional(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"Next", iterSrc + "G(var c: C): int {\n\tif x := c.Next() {\n\t\treturn x\n\t}\n\treturn 0\n}\n", ""},
		{"error union", iterSrc + "G(): int {\n\tif x := F2() {\n\t\treturn x\n\t}\n\treturn 0\n}\n", "E0307@20:10"},
		{"for binding", iterSrc + "G(n: int) {\n\tfor x := n {\n\t}\n}\n", "E0307@20:11"},
	})
}

func TestElseForms(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"try F() else on E!?T", iterSrc + "G(): E!int {\n\tx := try F3() else {\n\t\treturn 0\n\t}\n\treturn x\n}\n", ""},
		{"else binds the error", iterSrc + "G(): int {\n\tx := F2() else e {\n\t\tmatch e {\n\t\t\tE {\n\t\t\t\treturn 0\n\t\t\t}\n\t\t}\n\t}\n\treturn x\n}\n", ""},
		{"else on an optional", iterSrc + "G(var c: C): int {\n\tx := c.Next() else {\n\t\treturn 0\n\t}\n\treturn x\n}\n", ""},
		{"Or is E0207", "F(x: ?Cell[int], d: Cell[int]): Cell[int] {\n\treturn x.Or(d)\n}\n", "E0207@2:11"},
	})
}

func TestWidenRecorded(t *testing.T) {
	w := newWorld(t, true)
	b := checkBody(t, w, "", "error A\nerror B\n\nH(e: A | B) {\n}\n\nF() {\n\tH(A)\n}\n")
	body := w.c.Check(b.m, b.item("F"))
	call := body.Node.(*ast.FuncDecl).Body.Stmts[0].(*ast.ExprStmt).X.(*ast.CallExpr)
	if a := body.Adjust[body.slot(call.Args[0].Value.ID())]; a.Kind != AdjWiden || a.To == 0 {
		t.Errorf("adjust %+v", a)
	}
}

// TestWidenAtGenericArgument: an E!T argument for a
// parameter E'!T whose T inference binds is widened when E is within E'
// (AdjWiden on the argument) and is E0301 when it is not. An E!T
// argument is also E0802 (an E!T is used only under try, else or
// match) unless E0301 already stands at it.
func TestWidenAtGenericArgument(t *testing.T) {
	pre := "error A\nerror B\nL1(): A!int { return 1 }\nL2(): (A | B)!int { return 1 }\n"
	runBodyCases(t, []bodyCase{
		{"narrower set", pre + "Get[T](r: A!T): int { return 0 }\nF(): int {\n\treturn Get(L2())\n}\n", "E0301@7:13"},
		{"same set", pre + "Get[T](r: A!T): int { return 0 }\nF(): int {\n\treturn Get(L1())\n}\n", "E0802@7:13"},
		{"other element", pre + "Get[T](r: A!T, x: T): int { return 0 }\nF(): int {\n\treturn Get(L1(), true)\n}\n", "E0301@7:19 E0802@7:13"},
		{"plain parameter", pre + "Get2(r: (A | B)!int): int { return 0 }\nF(): int {\n\treturn Get2(L1())\n}\n", "E0802@7:14"},
		{"same type", pre + "Get2(r: A!int): int { return 0 }\nF(): int {\n\treturn Get2(L1())\n}\n", "E0802@7:14"},
		{"fn value", pre + "F(g: fn(A!int): int): int {\n\treturn g(L1())\n}\n", "E0802@6:11"},
		{"try is fine", pre + "Get2(r: int): int { return 0 }\nF(): A!int {\n\treturn Get2(try L1())\n}\n", ""},
	})
	w := newWorld(t, true)
	b := checkBody(t, w, "E0802@7:13", pre+"Get[T](r: (A | B)!T): int { return 0 }\nF(): int {\n\treturn Get(L1())\n}\n")
	body := w.c.Check(b.m, b.item("F"))
	call := body.Node.(*ast.FuncDecl).Body.Stmts[0].(*ast.ReturnStmt).Value.(*ast.CallExpr)
	if a := body.Adjust[body.slot(call.Args[0].Value.ID())]; a.Kind != AdjWiden || a.To == 0 {
		t.Errorf("adjust %+v", a)
	}
}
