// Why: the E1220 fix for an inclusive range must not propose a value that
// does not fit the type.
// What: TestLangInclusiveAtTypeMax: "..=" ending at an integer
// type's maximum has no proven fix, since the next value (256 for 255) is no
// value of a u8 (E0303) and the parser does not know the scrutinee's type.
// How: parses the range and checks that the alternatives name both rewrites.

package syntax

import (
	"strings"
	"testing"
)

func TestLangInclusiveAtTypeMax(t *testing.T) {
	for _, c := range []struct {
		pat   string
		proof bool
	}{
		{"0..=255", false},
		{"0..=127", false},
		{"0..=65535", false},
		{"0..=4294967295", false},
		{"0..=9223372036854775807", false},
		{"0..=254", true},
		{"-128..=-1", true},
	} {
		r := parse(t, "F(x: u8) {\n    match x {\n        "+c.pat+" {}\n        _ {}\n    }\n}\n", Options{})
		if len(r.diags) != 1 || r.diags[0].Code.String() != "E1220" {
			t.Fatalf("%s: %v, want E1220", c.pat, r.codes())
		}
		d := r.diags[0]
		if got := len(d.Fixes) > 0; got != c.proof {
			t.Errorf("%s: proven fix %v, want %v", c.pat, got, c.proof)
		}
		if !c.proof && (len(d.Alternatives) != 2 || !strings.Contains(d.Alternatives[0], "leave it out")) {
			t.Errorf("%s: alternatives %q", c.pat, d.Alternatives)
		}
	}
}
