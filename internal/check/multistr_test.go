// Why: check decodes string literals itself, so its decoder must agree with
// the parser's on multi-line literals.
// What: TestLitStringMatchesSyntax and TestMultiLineStringValues.
// How: compares the two decoders, reads a """ literal's value in
// Body.Consts, and checks a """ format string.

package check

import (
	"testing"

	"mk/internal/ast"
	"mk/internal/syntax"
	"mk/internal/types"
)

// TestLitStringMatchesSyntax: litString gives exactly syntax.StringValue's
// bytes for every string form, and fails where it fails.
func TestLitStringMatchesSyntax(t *testing.T) {
	cases := []struct {
		kind ast.TokenKind
		raw  string
	}{
		{ast.TokStr, `"plain"`},
		{ast.TokStr, `"a\tb\n\x41\u{e9}\\\""`},
		{ast.TokRawStr, `r"a\n"`},
		{ast.TokByteStr, `b"\x00\xff"`},
		{ast.TokMultiStr, "\"\"\"\n\tline one\n\tline two\n\t\"\"\""},
		{ast.TokMultiStr, "\"\"\"\n\t\tindented\n\tflush\n\n\t\"\"\""},
		{ast.TokMultiStr, "\"\"\"\n    say \"hi\" \\t\\\"\n    \"\"\""},
		{ast.TokMultiStr, "\"\"\"\n\"\"\""},
		{ast.TokMultiStr, "\"\"\"  \nx\n\"\"\""},
		{ast.TokMultiStr, "\"\"\"\n\tok\n  bad\n\t\"\"\""},
		{ast.TokMultiStr, "\"\"\"x\n\ty\n\t\"\"\""},
		{ast.TokMultiStr, "\"\"\"one line\"\"\""},
		{ast.TokMultiStr, "\"\"\"\n\t{n} items\\n\n\t\"\"\""},
	}
	for _, tc := range cases {
		want, wok := syntax.StringValue(tc.kind, tc.raw)
		got, gok := litString(tc.kind, tc.raw)
		if gok != wok || string(got) != string(want) {
			t.Errorf("%q: check %q %v, syntax %q %v", tc.raw, got, gok, want, wok)
		}
	}
}

// TestMultiLineStringValues: a """ literal's value is in Body.Consts, and a
// """ format argument is checked like a one-line one.
func TestMultiLineStringValues(t *testing.T) {
	w := newWorld(t, true)
	b := checkBody(t, w, "", "F(): str {\n\treturn \"\"\"\n\t\tfirst\n\t\t  second\n\t\t\"\"\"\n}\n")
	body := w.c.Check(b.m, b.item("F"))
	var got string
	ast.Inspect(body.Node, func(n ast.Node) bool {
		if l, ok := n.(*ast.BasicLit); ok && l.Kind == ast.TokMultiStr {
			if v := body.Consts[body.slot(l.ID())]; v != types.NoConst {
				got = w.tab.ConstString(v)
			}
		}
		return true
	})
	if got != `"first\n  second\n"` {
		t.Fatalf("value %s", got)
	}

	pre := "F(var out: Writer, i: int): WriteError!void {\n"
	runBodyCases(t, []bodyCase{
		{"multi-line format accepted", pre + "\ttry Print(mut out, \"\"\"\n\t\tcount: {i}\n\t\t\"\"\", (i))\n}\n", ""},
		{"multi-line format hole names no field", pre + "\ttry Print(mut out, \"\"\"\n\t\t{i} and {j}\n\t\t\"\"\", (i))\n}\n", "E0706@2:21"},
		{"multi-line named const", "const Fmt = \"\"\"\n\t{j}\n\t\"\"\"\n" + pre + "\ttry Print(mut out, Fmt, (i))\n}\n", "E0706@5:21"},
	})
}
