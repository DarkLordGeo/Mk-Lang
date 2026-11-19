// Why: a demand to restate a callee's clause comes with a fix, and applying
// it must give a module that checks clean.
// What: TestRestatedFixIsProven and TestRestatedFixNotProven.
// How: the fix (requireRestatedWhy) is proven when the clause keeps no name
// of the callee; it goes after the signature's last contract or at its end,
// never before the body's `{` with a newline (which left the `{` on a line
// of its own, E0101). The test applies it and checks again.

package check

import (
	"strings"
	"testing"

	"mk/internal/diag"
)

func TestRestatedFixIsProven(t *testing.T) {
	t.Parallel()
	const f = "F(const n: int): int\n\trequires n > 0 {\n\treturn n\n}\n\n" // lines 1-4
	for _, tc := range []struct{ name, h, want string }{
		{"no contract", "H(const k: int): int {\n\treturn F(k)\n}\n", "H(const k: int): int\n    requires k > 0 {"},
		{"after a contract", "H(const k: int): int\n\trequires k < 9 {\n\treturn F(k)\n}\n", "\trequires k < 9\n    requires k > 0 {"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			src := f + tc.h
			b := w.add("m", opt{}, src)
			ds := b.bodyDiags(t)
			if len(ds) != 1 || ds[0].Code != diag.E0706 || len(ds[0].Fixes) != 1 {
				t.Fatalf("want one E0706 with a fix:\n%s", dump(w, ds))
			}
			fx := ds[0].Fixes[0]
			if fx.Class != diag.Proven || len(fx.Edits) != 1 {
				t.Fatalf("fix %+v, want one proven edit", fx)
			}
			e := fx.Edits[0]
			fixed := src[:e.Span.Start] + e.Text + src[e.Span.End:]
			if !strings.Contains(fixed, tc.want) {
				t.Fatalf("fixed source lacks %q:\n%s", tc.want, fixed)
			}
			checkBody(t, newWorld(t, true), "", fixed)
		})
	}
}

// A clause that keeps a callee module's name is matched by meaning, so its
// fix stays compiles; a signature with a cpu clause and no contract has no
// span for the keyword, so it gets no fix (restateAt).
func TestRestatedFixNotProven(t *testing.T) {
	t.Parallel()
	const f = "const Lim: int = 4\n\nF(const n: int): int\n\trequires n < Lim {\n\treturn n\n}\n\n"
	w := newWorld(t, true)
	b := w.add("m", opt{}, f+"H(const k: int): int {\n\treturn F(k)\n}\n")
	ds := b.bodyDiags(t)
	if len(ds) != 1 || len(ds[0].Fixes) != 1 || ds[0].Fixes[0].Class != diag.Compiles {
		t.Fatalf("want one E0706 with a compiles fix:\n%s", dump(w, ds))
	}
}
