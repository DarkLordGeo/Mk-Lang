// Why: the JSON report spells fix classes as words tools depend on.
// What: TestFixClassSpelling.
// How: checks FixClass.String for Proven, Compiles and Alternatives.

package diag

import "testing"

// TestFixClassSpelling: the JSON spelling of each fix class is exactly
// proven, compiles, alternatives.
func TestFixClassSpelling(t *testing.T) {
	for c, want := range map[FixClass]string{Proven: "proven", Compiles: "compiles", Alternatives: "alternatives"} {
		if got := c.String(); got != want {
			t.Errorf("FixClass(%d).String() = %q, want %q", c, got, want)
		}
	}
}
