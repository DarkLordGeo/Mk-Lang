// Why: become and range patterns are the newest syntax, and both touch
// shared AST tables.
// What: TestBecomeIsAKeyword, TestRangePatternEnds.
// How: checks that become is a keyword token after TokAsm, and that
// EachChild skips a range pattern's open ends.

package ast

import (
	"strings"
	"testing"
)

// TestBecomeIsAKeyword: TokBecome is appended after TokAsm and spells
// become, and IsKeyword and Keyword know it since the reference's Keywords
// line lists the word (TestKeywordTableMatchesReference holds the two
// equal).
func TestBecomeIsAKeyword(t *testing.T) {
	if TokBecome != TokAsm+1 || TokBecome.FixedText() != "become" || TokBecome.String() != `"become"` {
		t.Errorf("TokBecome: %d after TokAsm %d, text %q", TokBecome, TokAsm, TokBecome.FixedText())
	}
	if k, ok := Keyword("become"); !ok || k != TokBecome || !TokBecome.IsKeyword() || TokBecome.IsHabit() {
		t.Error("become must be a keyword: the reference's Keywords line lists it")
	}
}

// TestRangePatternEnds: an open end is no child, and the closed ends come
// in source order, Lo before Hi.
func TestRangePatternEnds(t *testing.T) {
	lo := &LiteralPattern{Neg: true, Lit: &BasicLit{Kind: TokInt, Raw: "1"}}
	hi := &LiteralPattern{Lit: &BasicLit{Kind: TokInt, Raw: "9"}}
	for _, tc := range []struct {
		p    *RangePattern
		want string
	}{
		{&RangePattern{Lo: lo, Hi: hi}, "LiteralPattern LiteralPattern"},
		{&RangePattern{Lo: lo}, "LiteralPattern"},
		{&RangePattern{Hi: hi}, "LiteralPattern"},
	} {
		var got []string
		EachChild(tc.p, func(c Node) { n, _ := Describe(c, nil); got = append(got, n) })
		if strings.Join(got, " ") != tc.want {
			t.Errorf("children %v, want %s", got, tc.want)
		}
	}
	if n, attrs := Describe(&RangePattern{Hi: hi}, nil); n != "RangePattern" || !strings.Contains(attrs, "open-lo") {
		t.Errorf("Describe: %s %s", n, attrs)
	}
}
