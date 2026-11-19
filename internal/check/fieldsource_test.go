// Why: a fields loop's `&x` must have a real reference type: a Poison with
// no diagnostic there makes lower fail.
// What: TestFieldsSourceIsTypedRef, from a FuzzNoSilentPoison input.
// How: checks the generic and the concrete form and reads the type recorded
// for `&x`.

package check

import (
	"testing"

	"mk/internal/source"
)

// TestFieldsSourceIsTypedRef: a FuzzNoSilentPoison find
// (9c2443104f774598). A fields loop's `&x` was typed Poison with no
// diagnostic, in the generic form and the concrete one; lower reads that
// type when x is not a place, so `fields(&Make())` was its ICE. It is the
// reference to x's value, after any deref of x.
func TestFieldsSourceIsTypedRef(t *testing.T) {
	for _, src := range []string{
		"A[A:Fields[Format]](args:A){for a,a0 in fields(&args){}}",
		"F[T: Fields[Format]](args: &T) {\n\tfor n, v in fields(&args) {\n\t}\n}\n",
		"struct P {\n\tn: int\n}\nMake(): P {\n\treturn P(n: 1)\n}\nF(p: &&P) {\n\tfor n, v in fields(&p) {\n\t}\n\tfor n, v in fields(&Make()) {\n\t}\n}\n",
	} {
		w := newWorld(t, true)
		var b *built
		if ie := source.Guard("m", source.Span{}, func() { b = w.add("m", opt{parseErrors: true}, src) }); ie != nil {
			t.Fatalf("%q: %v", src, ie)
		}
		for _, id := range b.m.Units() {
			body := w.c.Check(b.m, id)
			for _, d := range body.Diags {
				if d.Code.String() != "E0208" {
					t.Errorf("%q: %s %s", src, d.Code, d.Message)
				}
			}
			if bad := silentPoison(w, body); len(bad) > 0 {
				t.Errorf("%q: silent Poison at %v", src, bad)
			}
		}
	}
}
