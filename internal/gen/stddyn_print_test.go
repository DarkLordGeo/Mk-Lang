// Why: gen dyn.Of's output puts one table slot per line, which relies on how
// the printer breaks a line that ends inside '(' or '['.
// What: TestStdDynPrintBracketGroups, for print.go's bracket groups.
// How: lexes unindented source, prints its tokens and compares the
// indented text.

package gen

import (
	"testing"

	"mk/internal/ast"
	"mk/internal/syntax"
)

// TestStdDynPrintBracketGroups: a line left inside brackets indents what
// follows by one level, however many it opened; leading closers come off
// first, so "))" lines up with the line that opened them; lines without a
// break inside brackets print as before.
func TestStdDynPrintBracketGroups(t *testing.T) {
	lex := func(s string) []tok {
		lt, _ := syntax.Lex(0, s)
		var ts []tok
		for _, k := range lt {
			switch {
			case k.Kind == ast.TokWhitespace || k.Kind == ast.TokTerm || k.Kind == ast.TokEOF:
			case hasText(k.Kind):
				ts = append(ts, tok{kind: k.Kind, text: k.Text(s)})
			default:
				ts = append(ts, tok{kind: k.Kind})
			}
		}
		return ts
	}
	for _, c := range []struct{ name, in, want string }{
		{"record type", "view struct D {\nobj: R\ntable: (\na: fn(&R): int,\nb: fn(mut R)\n)\n}",
			"view struct D {\n    obj: R\n    table: (\n        a: fn(&R): int,\n        b: fn(mut R)\n    )\n}"},
		{"two opened on one line", "F() {\nreturn D(obj: R.Of(x), table: (\na: D.A[T],\nb: D.B[T]\n))\n}",
			"F() {\n    return D(obj: R.Of(x), table: (\n        a: D.A[T],\n        b: D.B[T]\n    ))\n}"},
		{"nested groups", "F(\na: int,\nb: (\nc: int\n)\n) {\n}",
			"F(\n    a: int,\n    b: (\n        c: int\n    )\n) {\n}"},
		{"brackets", "const X = [\n1,\n2\n]",
			"const X = [\n    1,\n    2\n]"},
		{"literal body inside a call", "F() {\nxs.Sort(fn(a: int, b: int): bool {\nreturn a < b\n})\ny()\n}",
			"F() {\n    xs.Sort(fn(a: int, b: int): bool {\n        return a < b\n    })\n    y()\n}"},
		{"closed on one line", "F() {\nx := g(a, (b, c))\ny()\n}", "F() {\n    x := g(a, (b, c))\n    y()\n}"},
		{"continuation after a group", "F(a: int): int\nrequires a > 0\n{\nreturn a\n}",
			"F(a: int): int\n    requires a > 0\n{\n    return a\n}"},
	} {
		var p printer
		p.tokens(lex(c.in))
		if got := p.b.String(); got != c.want {
			t.Errorf("%s: print(%q) =\n%s\nwant\n%s", c.name, c.in, got, c.want)
		}
	}
}
