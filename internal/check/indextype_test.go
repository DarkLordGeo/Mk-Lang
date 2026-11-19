// Why: a type written as an index must be E0301; a silent Poison there
// reaches lower as an ICE.
// What: TestE0301_TypeInIndex.
// How: checks `xs[[]int]` and the fuzz input and expects E0301.

package check

import (
	"testing"

	"mk/internal/diag"
)

// TestE0301_TypeInIndex: a type written as an index (`xs[[]int]`) is E0301,
// as `xs[int]` is. The FuzzCheck input `F[T](x:T){0[[]x](0)}` was a silent
// Poison that lower reported as an ICE ("check gap").
func TestE0301_TypeInIndex(t *testing.T) {
	for _, tc := range []bodyCase{
		{"fuzz input", "F[T](x:T){0[[]x](0)}\n", "E0301@1:13"},
		{"slice of a value", "F[T](x: T, xs: []int) {\n\t_ = xs[[]int]\n}\n", "E0301@2:9"},
		{"type parameter", "F[T](x: T) {\n\t_ = 0[[]T]()\n}\n", "E0301@2:8"},
		{"name of a type", "F[T](x: T, xs: []int) {\n\t_ = xs[int]\n}\n", "E0301@2:9"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			b := w.add("m", opt{}, tc.src)
			body := w.c.Check(b.m, b.item("F"))
			var ds []*diag.Diagnostic
			for _, d := range append(b.diags(), body.Diags...) {
				if d.Code != diag.E0208 {
					ds = append(ds, d)
				}
			}
			if got := codes(w, ds); got != tc.want {
				t.Fatalf("diagnostics %q, want %q\n%s", got, tc.want, dump(w, ds))
			}
		})
	}
}
