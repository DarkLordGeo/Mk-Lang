// Why: become (a guaranteed tail call) and range patterns have parse rules
// of their own: which operands become takes, and where a range is a pattern.
// What: TestLangBecomeParses, TestLangRangePatternsParse and
// TestLangRangeInABindIsAPattern.
// How: parses short sources and checks the nodes and diagnostics.

package syntax

import (
	"strings"
	"testing"
)

// TestLangBecomeParses: become takes a direct call, a method call, a
// generic call and a call through a fn value; any other operand, or a call
// followed by more of an expression, is E0101 and a BadStmt.
func TestLangBecomeParses(t *testing.T) {
	for _, c := range []struct{ src, want string }{
		{"become G(a, 1)", "BecomeStmt"},
		{"become t[a](a)", "BecomeStmt"},
		{"become self.Step(a)", "BecomeStmt"},
		{"become G[int](a)", "BecomeStmt"},
		{"become a + 1", "E0101"},
		{"become G(a) + 1", "E0101"},
		{"become a", "E0101"},
	} {
		r := parse(t, "F(a: int): int {\n    "+c.src+"\n}\n", Options{})
		got := r.codeList()
		if c.want == "BecomeStmt" {
			if got != "" || !strings.Contains(r.tree(), "BecomeStmt") || !strings.Contains(r.tree(), "CallExpr") {
				t.Errorf("%s: %s\n%s", c.src, got, r.tree())
			}
			continue
		}
		if got != c.want || !strings.Contains(r.tree(), "BadStmt") || !strings.Contains(strings.Join(r.codes(), "\n"), "`become` takes a call") {
			t.Errorf("%s: %v\n%s", c.src, r.codes(), r.tree())
		}
	}
	// become is a keyword: a local of that name is E0102's case, read as a name.
	if r := parse(t, "F() {\n    become := 1\n}\n", Options{}); !strings.Contains(r.codeList(), "E0102") {
		t.Errorf("become as a local: %v", r.codes())
	}
}

// TestLangRangePatternsParse: the four range forms with Int, negative Int
// and Char ends; another literal kind is E0101; "..=" is the habit E1220
// with the next value as its fix.
func TestLangRangePatternsParse(t *testing.T) {
	src := "F(x: i8): int {\n    return match x {\n        0..10 { 1 }\n        100.. { 2 }\n        ..-3 { 3 }\n        -5..-4 | 'a'..'{' { 4 }\n        _ { 0 }\n    }\n}\n"
	r := parse(t, src, Options{})
	if len(r.diags) != 0 {
		t.Fatalf("diags %v", r.codes())
	}
	tree := r.tree()
	for _, want := range []string{"RangePattern [", "RangePattern [65,70) open-hi", "open-lo", "LiteralPattern [87,89) neg"} {
		if !strings.Contains(tree, want) {
			t.Errorf("tree lacks %q:\n%s", want, tree)
		}
	}
	if n := strings.Count(tree, "RangePattern"); n != 5 {
		t.Errorf("%d range patterns:\n%s", n, tree)
	}
	for _, c := range []struct{ pat, code, fix string }{
		{`"a".."b"`, "E0101", ""},
		{"1.5..2", "E0101", ""},
		{"-'a'..'b'", "E0101", ""},
		{"1..=9", "E1220", "1..10"},
		{"-9..=-1", "E1220", "-9..0"},
		{"'a'..='z'", "E1220", "'a'..'{'"},
		{"..", "E0101", ""},
	} {
		r := parse(t, "F(x: int) {\n    match x {\n        "+c.pat+" {}\n        _ {}\n    }\n}\n", Options{})
		if !strings.Contains(r.codeList(), c.code) {
			t.Errorf("%s: %v, want %s", c.pat, r.codes(), c.code)
		}
		if c.fix != "" {
			text := applyFix(r.src.Text, r.diags[0])
			if !strings.Contains(text, "        "+c.fix+" {}") {
				t.Errorf("%s: fixed text\n%s", c.pat, text)
			}
		}
	}
}

// TestLangRangeInABindIsAPattern: a range in a destructure's
// PatField is read as a RangePattern, not as an expression, so check refuses
// it as refutable (E0306); in a call it stays E0101.
func TestLangRangeInABindIsAPattern(t *testing.T) {
	for _, src := range []string{"(x: 1..5) := e", "P(x: ..-5, y) := e", "(x: 'a'..) := e"} {
		r := parse(t, "F() {\n    "+src+"\n}\n", Options{})
		if len(r.diags) != 0 || !strings.Contains(r.tree(), "DestructureStmt") || !strings.Contains(r.tree(), "RangePattern") {
			t.Errorf("%s: %v\n%s", src, r.codes(), r.tree())
		}
	}
	r := parse(t, "F() {\n    G(x: 1..5)\n}\n", Options{})
	if r.codeList() != "E0101" || !strings.Contains(strings.Join(r.codes(), ""), rangeMsg) {
		t.Errorf("a range in a call: %v", r.codes())
	}
}
