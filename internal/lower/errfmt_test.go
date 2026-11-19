// Why: every error has a compiler-provided Format, and Format on an error
// set at a concrete site must become a switch, not an interface call.
// What: TestProvidedBody, TestConcreteSiteSwitch.
// How: lowers each program with Validate on and checks the calls in the MIR.

package lower

import (
	"strings"
	"testing"
)

const efSrc = `error A(n: int)
error B
type S = A | B
error W(s: S, k: int)
Show(w: mut Writer, e: S): WriteError!void {
	try e.Format(w)
}
Quiet(w: mut Writer, e: S) {
	e.Format(w) else { }
}
`

// TestProvidedBody: the name and punctuation through Writer.Str, each field
// through its type's Format, and an error-set field through the switch.
func TestProvidedBody(t *testing.T) {
	l := lowerSrc(t, efSrc)
	a := l.print("#impl(std.core.Format for m.A):Format")
	wantIn(t, a, `const str "A(n: "`, "&((*_1) as m.A).0", "#impl(std.core.Format for int):Format(", `const str ")"`, "return normal")
	b := l.print("#impl(std.core.Format for m.B):Format")
	wantIn(t, b, `const str "B"`, "return normal")
	w := l.print("#impl(std.core.Format for m.W):Format")
	wantIn(t, w, `const str "W(s: "`, "Discriminant(", "switch", "#impl(std.core.Format for m.A):Format(", "#impl(std.core.Format for m.B):Format(")
	wantIn(t, w, `const str ", k: "`, "#impl(std.core.Format for int):Format(")
}

// TestConcreteSiteSwitch: e.Format(w) on a set is a switch on the code
// with one direct call per member, under try and under else alike; no
// interface call remains.
func TestConcreteSiteSwitch(t *testing.T) {
	l := lowerSrc(t, efSrc)
	for _, f := range []string{"Show", "Quiet"} {
		p := l.print(f)
		wantIn(t, p, "Discriminant(", "switch")
		if !strings.Contains(p, "#impl(std.core.Format for m.A):Format(") || !strings.Contains(p, "#impl(std.core.Format for m.B):Format(") {
			t.Errorf("%s: a member's Format is not called:\n%s", f, p)
		}
		if strings.Contains(p, "std.core.Format.method0") {
			t.Errorf("%s: an interface call remains:\n%s", f, p)
		}
	}
}
