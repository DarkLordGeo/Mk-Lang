// Why: an open range pattern after a field's colon (`a: 10..`) must keep its
// space, or the formatted text reads differently.
// What: TestLangRangeAfterColon.
// How: a match over a struct pattern with open ranges is a fixed point.

package format

import "testing"

func TestLangRangeAfterColon(t *testing.T) {
	in := "F(p: P): int {\n    return match p {\n        P(a: 10.., b: ..0) { 1 }\n        _ { 0 }\n    }\n}\n"
	if got := mustFormat(t, in); got != in {
		t.Errorf("formatted:\n%s\nwant:\n%s", got, in)
	}
}
