// Why: parser defects found by fuzzing and dogfooding must stay fixed.
// What: regression tests, each named after the defect it closes.
// How: parses the failing source and checks the tree and diagnostics.

package syntax

import (
	"strings"
	"testing"

	"mk/internal/diag"
)

// TestFuzzFormatBangInBracket: inside a bracket argument '!' is the
// error-union operator, so `List[E![]u8]` and `A[A![]]`, as mk fmt
// writes them, parse; they were "Mk has no macros" (FuzzFormat's input
// `A(A:A[A ![]])`). In operand position the macro form keeps its error, and
// in a bracket the named macro forms (E1221) and panic! (E1210) keep theirs.
func TestFuzzFormatBangInBracket(t *testing.T) {
	for _, src := range []string{
		"A(a: List[E![]u8])\n",
		"A(a: List[E!(int, int)])\n",
		"A(A: A[A![]])\n",
		"F() {\n    t := Task[IoError![]u8].New()\n}\n",
	} {
		if r := parse(t, src, Options{AllowBodiless: true}); len(r.diags) != 0 {
			t.Errorf("%q: diagnostics:\n  %s", src, strings.Join(r.codes(), "\n  "))
		}
	}
	runCases(t, []pcase{
		{name: "macro in a call", src: "F() {\n    G(foo!(x))\n}\n", codes: "E0101", msg: "Mk has no macros"},
		{name: "named macro in an index", src: "F() {\n    a := xs[vec![1]]\n}\n", codes: "E1221", msg: "Mk has no macros: `vec!` is List"},
		{name: "panic in an index", src: "F() {\n    a := xs[panic!(1)]\n}\n", codes: "E1210"},
	})
}

// TestPackUniverseNameRename: E0102 on a top-level name or a type
// parameter spelled as a universe or primitive name suggests a free
// PascalCase name, never `SizeOf_`, which E0207 refuses; a local or a
// parameter keeps `name_` (snake_case allows it).
func TestPackUniverseNameRename(t *testing.T) {
	for _, c := range []struct{ src, alt string }{
		{"SizeOf(n: int): int {\n    return n\n}\n", "rename it to a free PascalCase name"},
		{"struct Cast {}\n", "rename it to a free PascalCase name"},
		{"F[SizeOf](x: SizeOf) {}\n", "rename it to a free PascalCase name"},
		{"F(int: u8) {}\n", "rename it to `int_`"},
		{"F() {\n    u8 := 1\n}\n", "rename it to `u8_`"},
	} {
		r := parse(t, c.src, Options{})
		if len(r.diags) != 1 || r.diags[0].Code != diag.E0102 || len(r.diags[0].Alternatives) != 1 || r.diags[0].Alternatives[0] != c.alt {
			t.Errorf("%q: want one E0102 with %q:\n  %s", c.src, c.alt, strings.Join(r.codes(), "\n  "))
			for _, d := range r.diags {
				t.Logf("  %v %q", d.Code, d.Alternatives)
			}
		}
	}
}
