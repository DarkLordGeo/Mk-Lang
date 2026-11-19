// Why: statements carry check's rules on dropped values, bindings, loops
// and reachability.
// What: TestE0209_DroppedValue, TestE0309_RecordNames, TestE0805, TestE0803,
// TestE0806, TestStatements, TestDivergesRecorded,
// TestCompoundAssignOperators.
// How: checks small bodies and compares codes and the recorded divergence.

package check

import (
	"testing"

	"mk/internal/ast"
	"mk/internal/diag"
)

func TestE0209_DroppedValue(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"dropped int", "G(): int {\n\treturn 1\n}\n\nF() {\n\tG()\n}\n", "E0209@6:2"},
		{"discard", "G(): int {\n\treturn 1\n}\n\nF() {\n\t_ = G()\n}\n", ""},
		{"void call", "G() {\n}\n\nF() {\n\tG()\n}\n", ""},
		{"error union is E0802", "error E\n\nG(): E!int {\n\treturn 1\n}\n\nF() {\n\tG()\n\t_ = G()\n}\n", "E0802@8:2 E0802@9:6"},
		{"linear value is borrow's", "linear struct L {\n\tn: int\n}\n\nG(): L {\n\treturn L(n: 1)\n}\n\nF() {\n\tG()\n}\n", ""},
		{"Ignored", "F(x: int) {\n\tx + 1\n}\n", "E0209@2:2"},
	})
	t.Run("proven fix", func(t *testing.T) {
		w := newWorld(t, true)
		b := checkBody(t, w, "E0209@6:2", "G(): int {\n\treturn 1\n}\n\nF() {\n\tG()\n}\n")
		d := b.bodyDiags(t)[0]
		if len(d.Fixes) != 1 || d.Fixes[0].Class != diag.Proven || d.Fixes[0].Edits[0].Text != "_ = " {
			t.Errorf("fix %+v", d.Fixes)
		}
	})
}

func TestE0309_RecordNames(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"record", "F(r: (a: int, b: bool)): int {\n\t(a, b) := r\n\treturn a\n}\n", ""},
		{"renamed field", "F(r: (a: int, b: bool)): int {\n\t(a: x, b: _) := r\n\treturn x\n}\n", ""},
		{"unknown name", "F(r: (a: int, b: bool)): int {\n\t(a, c) := r\n\treturn a\n}\n", "E0309@2:6"},
		{"missing", "F(r: (a: int, b: bool)): int {\n\t(a) := r\n\treturn a\n}\n", "E0305@2:2"},
		{"rest", "F(r: (a: int, b: bool)): int {\n\t(a, ..) := r\n\treturn a\n}\n", ""},
		{"struct", pointSrc + "F(p: Point): int {\n\tPoint(x, ..) := p\n\treturn x\n}\n", ""},
		{"wrong struct", pointSrc + "struct Q {\n\tx: int\n}\n\nF(p: Point): int {\n\tQ(x) := p\n\treturn x\n}\n", "E0301@11:2"},
		{"rest over a non-plain field", "struct H {\n\tx: int\n\tc: Cell[int]\n}\n\nF(h: H): int {\n\tH(x, ..) := h\n\treturn x\n}\n", "E0408@7:2"},
		{"refutable", "F(r: (a: ?int, b: bool)): bool {\n\t(a: none, b) := r\n\treturn b\n}\n", "E0306@2:6"},
	})
	t.Run("proven rename", func(t *testing.T) {
		w := newWorld(t, true)
		b := checkBody(t, w, "E0309@2:6", "F(r: (a: int, b: bool)): int {\n\t(a, c) := r\n\treturn a\n}\n")
		d := b.bodyDiags(t)[0]
		if len(d.Fixes) != 1 || d.Fixes[0].Edits[0].Text != "b" {
			t.Errorf("fix %+v", d.Fixes)
		}
	})
}

func TestE0805(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"falls off", "F(x: int): int {\n\tif x > 0 {\n\t\treturn 1\n\t}\n}\n", "E0805@5:1"},
		{"both branches", "F(x: int): int {\n\tif x > 0 {\n\t\treturn 1\n\t} else {\n\t\treturn 2\n\t}\n}\n", ""},
		{"forever", "F(): int {\n\tfor {\n\t}\n}\n", ""},
		{"forever with break", "F(): int {\n\tfor {\n\t\tbreak\n\t}\n}\n", "E0805@5:1"},
		{"panic", "F(): int {\n\tPanic(\"no\")\n}\n", ""},
		{"void", "F() {\n}\n", ""},
		{"fn literal", "F(): fn(): int {\n\treturn fn(): int {\n\t}\n}\n", "E0805@3:2"},
		{"labeled break out of an inner loop", "F(): int {\n\touter: for {\n\t\tfor {\n\t\t\tbreak outer\n\t\t}\n\t}\n}\n", "E0805@7:1"},
	})
	t.Run("proven fix", func(t *testing.T) {
		w := newWorld(t, true)
		b := checkBody(t, w, "E0209@2:2 E0805@3:1", "F(x: int): int {\n\tx + 1\n}\n")
		var d *diag.Diagnostic
		for _, x := range b.bodyDiags(t) {
			if x.Code == diag.E0805 {
				d = x
			}
		}
		if len(d.Fixes) != 1 || d.Fixes[0].Edits[0].Text != "return " || fact(d, "result") != "int" {
			t.Errorf("%s", dump(w, []*diag.Diagnostic{d}))
		}
	})
}

const fallible = "error E\n\nG(): E!int {\n\treturn 1\n}\n\nH(): E!void {\n}\n\n"

func TestE0803(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"value needed", fallible + "F(): int {\n\tx := G() else {\n\t}\n\treturn x\n}\n", "E0803@11:11"},
		{"handler diverges", fallible + "F(): int {\n\tx := G() else {\n\t\treturn 0\n\t}\n\treturn x\n}\n", ""},
		{"void dropped", fallible + "F() {\n\tH() else {\n\t}\n}\n", ""},
		{"non-void dropped", fallible + "F() {\n\tG() else {\n\t}\n}\n", "E0209@11:2"},
		{"else on a plain value", "F(x: int): int {\n\ty := x else {\n\t\treturn 0\n\t}\n\treturn y\n}\n", "E0307@2:7"},
		{"else x on an optional", "F(x: ?int): int {\n\ty := x else e {\n\t\treturn 0\n\t}\n\treturn y\n}\n", "E0307@2:14"},
		{"try", fallible + "F(): E!int {\n\tx := try G()\n\treturn x\n}\n", ""},
		{"return F()", fallible + "F(): E!int {\n\treturn G()\n}\n", "E0802@11:9"},
	})
}

func TestE0806(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"return", "F() {\n\tdefer {\n\t\treturn\n\t}\n}\n", "E0806@3:3"},
		{"try", fallible + "F(): E!void {\n\tdefer {\n\t\tx := try G()\n\t}\n}\n", "E0806@12:8"},
		{"fail", fallible + "F(): E!void {\n\tonerr {\n\t\tfail E\n\t}\n}\n", "E0806@12:3"},
		{"break leaves", "F() {\n\tfor {\n\t\tdefer {\n\t\t\tbreak\n\t\t}\n\t\tbreak\n\t}\n}\n", "E0806@4:4"},
		{"break inside", "F() {\n\tdefer {\n\t\tfor {\n\t\t\tbreak\n\t\t}\n\t}\n}\n", ""},
		{"fn literal excluded", "F() {\n\tdefer {\n\t\tg := fn(): int {\n\t\t\treturn 1\n\t\t}\n\t}\n}\n", ""},
	})
}

func TestStatements(t *testing.T) {
	runBodyCases(t, []bodyCase{
		{"for range", "F(n: u8): u8 {\n\tvar s: u8 = 0\n\tfor i in 0..n {\n\t\ts += i\n\t}\n\treturn s\n}\n", ""},
		{"for over a slice", "F(xs: []int, ys: &[3]u8): int {\n\tvar s := 0\n\tfor x in xs {\n\t\ts += x\n\t}\n\tfor i, y in ys {\n\t\ts += i + int(y)\n\t}\n\treturn s\n}\n", ""},
		{"for by reference", "F(xs: []int): int {\n\tvar s := 0\n\tfor x in &xs {\n\t\ts += *x\n\t}\n\treturn s\n}\n", ""},
		{"for over a str", "F(s: str) {\n\tfor c in s {\n\t}\n}\n", "E0301@2:11"},
		// A source that is not a sequence is reported, never Poison.
		{"for over an int", "F() {\n\tfor x in 5 {\n\t}\n}\n", "E0301@2:11"},
		{"for over a bool", "F(b: bool) {\n\tfor x in b {\n\t}\n}\n", "E0301@2:11"},
		{"for over an optional", "F(o: ?int) {\n\tfor x in o {\n\t}\n}\n", "E0301@2:11"},
		{"for over a record", "F(r: (a: int)) {\n\tfor x in &r {\n\t}\n}\n", "E0301@2:12"},
		{"for over a struct", "struct Q {\n\tn: int\n}\n\nF(q: Q) {\n\tfor v in q {\n\t}\n}\n", "E0301@6:11"},
		{"for over a type parameter", "F[T](x: T) {\n\tfor v in x {\n\t}\n}\n", "E0703@2:11"},
		{"range of bool", "F(a: bool, b: bool) {\n\tfor i in a..b {\n\t}\n}\n", "E0301@2:11"},
		{"if binding", "F(x: ?int): int {\n\tif v := x {\n\t\treturn v\n\t}\n\tif r := &x {\n\t\treturn *r\n\t}\n\treturn 0\n}\n", ""},
		{"if binding on a plain value", "F(x: int): int {\n\tif v := x {\n\t\treturn v\n\t}\n\treturn 0\n}\n", "E0307@2:10"},
		{"while", "F(var n: int) {\n\tfor n > 0 {\n\t\tn -= 1\n\t}\n}\n", ""},
		{"not a place", "G(): int {\n\treturn 1\n}\n\nF() {\n\tG() = 2\n}\n", "E0301@6:2"},
		{"compound on str", "F(var s: str) {\n\ts += \"x\"\n}\n", "E0308@2:2"},
		{"return in void", "F() {\n\treturn 1\n}\n", "E0301@2:9"},
		{"bare return in int", "F(): int {\n\treturn\n}\n", "E0301@2:2"},
		{"match arms all return", "enum C {\n\tA\n\tB\n}\n\nF(c: C): int {\n\tmatch c {\n\t\t.A {\n\t\t\treturn 1\n\t\t}\n\t\t.B {\n\t\t\treturn 2\n\t\t}\n\t}\n}\n", ""},
	})
}

func TestDivergesRecorded(t *testing.T) {
	w := newWorld(t, true)
	b := checkBody(t, w, "", "F(x: int): int {\n\tif x > 0 {\n\t\treturn 1\n\t}\n\treturn 2\n}\n")
	body := w.c.Check(b.m, b.item("F"))
	fd := body.Node.(*ast.FuncDecl)
	if body.Diverges.Has(fd.Body.Stmts[0].ID(), body.Base) || !body.Diverges.Has(fd.Body.Stmts[1].ID(), body.Base) || !body.Diverges.Has(fd.Body.ID(), body.Base) {
		t.Errorf("Diverges: if %v, return %v, body %v", body.Diverges.Has(fd.Body.Stmts[0].ID(), body.Base),
			body.Diverges.Has(fd.Body.Stmts[1].ID(), body.Base), body.Diverges.Has(fd.Body.ID(), body.Base))
	}
}

// TestCompoundAssignOperators: the wrapping, bitwise and shift compound
// forms are defined on integers only, and a shift amount is any integer
// type, as for the infix operators.
func TestCompoundAssignOperators(t *testing.T) {
	f64 := func(stmt string) string { return "F() {\n\tvar x: f64 = 1.0\n\t" + stmt + "\n}\n" }
	runBodyCases(t, []bodyCase{
		{"&= on f64", f64("x &= 2.0"), "E0308@3:2"},
		{"|= on f64", f64("x |= 2.0"), "E0308@3:2"},
		{"^= on f64", f64("x ^= 2.0"), "E0308@3:2"},
		{"<<= on f64", f64("x <<= 1"), "E0308@3:2"},
		{">>= on f64", f64("x >>= 1"), "E0308@3:2"},
		{"+%= on f64", f64("x +%= 1.0"), "E0308@3:2"},
		{"-%= on f64", f64("x -%= 1.0"), "E0308@3:2"},
		{"*%= on f64", f64("x *%= 1.0"), "E0308@3:2"},
		{"+= on f64", f64("x += 1.5"), ""},
		{"&= on bool", "F() {\n\tvar b := true\n\tb &= false\n}\n", "E0308@3:2"},
		{"shift by another integer type", "F(n: int) {\n\tvar x: u8 = 1\n\tx <<= n\n\tx >>= n\n}\n", ""},
		{"shift by a float", "F(f: f64) {\n\tvar x: u8 = 1\n\tx <<= f\n}\n", "E0308@3:2"},
		{"+= on u8", "F() {\n\tvar x: u8 = 1\n\tx += 1\n\tx +%= 255\n\tx &= 3\n}\n", ""},
		{"+= on a parameter", "F[T](var x: T, y: T) {\n\tx += y\n}\n", "E0703@2:2"},
	})
}
