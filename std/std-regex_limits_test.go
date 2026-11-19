// Why: Compile refuses patterns nested too deep, and that limit differs from
// Go's, so the model cannot judge it alone.
// What: Compile's TooLarge at 1,000 levels of nesting, over groups of
// every kind, and the cases where that rule and Go's maxHeight part.
// How: nested patterns at the edges of the limit, Go's verdict pinned beside Mk's.

package std

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
)

// TestSTDREGEX_NestingLimit: a group inside 1,000 others is TooLarge
// in Mk whatever its kind; Go counts the height of its syntax tree, which
// has no node for (?:..) and one for a capture and for a concatenation.
// Three patterns where the two rules part, and the edges of Mk's limit, with
// Go's verdict pinned beside Mk's, so a change on either side shows here.
func TestSTDREGEX_NestingLimit(t *testing.T) {
	nest := func(open string, n int, inner string) string {
		return strings.Repeat(open, n) + inner + strings.Repeat(")", n)
	}
	cases := []struct {
		name, pat string
		mk        string // "ok" or "large", as rxSyntax prints
		goOK      bool
	}{
		{"1,000 (?: groups", nest("(?:", 1000, "a"), "ok", true},
		{"1,001 (?: groups", nest("(?:", 1001, "a"), "large", true},
		{"5,000 (?: groups", nest("(?:", 5000, "a"), "large", true},
		{"1,000 captures", nest("(", 1000, ""), "ok", false},
		{"1,001 captures", nest("(", 1001, ""), "large", false},
		{"999 captures", nest("(", 999, ""), "ok", true},
		{"600 (b captures", nest("(b", 600, ""), "ok", false},
		{"499 (b captures", nest("(b", 499, ""), "ok", true},
	}
	var pats []string
	for _, c := range cases {
		pats = append(pats, c.pat)
	}
	exe := regexBuild(t, fmt.Sprintf(rxSyntax, rxStrs(pats)))
	got := strings.Split(strings.TrimSuffix(regexExec(t, exe), "\n"), "\n")
	if len(got) != len(cases) {
		t.Fatalf("%d lines for %d patterns:\n%s", len(got), len(cases), strings.Join(got, "\n"))
	}
	for i, c := range cases {
		f := strings.Fields(got[i])
		if len(f) < 2 || f[1] != c.mk {
			t.Errorf("%s: Mk %q, want %s", c.name, got[i], c.mk)
		}
		_, err := regexp.Compile(c.pat)
		if (err == nil) != c.goOK {
			t.Errorf("%s: Go %v, pinned as accepted=%v (the documented difference moved)", c.name, err, c.goOK)
		}
	}
}
