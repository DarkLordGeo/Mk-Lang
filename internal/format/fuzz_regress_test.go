// Why: FuzzFormat found an input whose formatted text did not parse again.
// What: TestFuzzFormatBangInBracket, the regression test for that input.
// How: checks the exact formatted text of the fuzz input and that nearby
// forms (an error union, a macro habit before '!') are fixed points.

package format

import "testing"

// TestFuzzFormatBangInBracket: FuzzFormat's input `A(A:A[A ![]])`
// formats to text that parses (the error-union '!' in a bracket argument
// is tight, and the parser reads it so); the seed is
// testdata/fuzz/FuzzFormat/acd97d1c25507615.
func TestFuzzFormatBangInBracket(t *testing.T) {
	if got := mustFormat(t, "A(A:A[A ![]])\n"); got != "A(A: A[A![]])\n" {
		t.Errorf("got:\n%s", got)
	}
	wantFixed(t, "A(a: List[E![]u8])\n")
	// A macro habit's name before '!' keeps its space, or the text would
	// be E1221's `vec![...]` (syntax.MacroHabit).
	wantFixed(t, "A(a: A[vec ![]])\n")
	wantFixed(t, "A(a: A[panic !x])\n")
}
