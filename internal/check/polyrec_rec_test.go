// Why: the polymorphic-recursion bound must count no anonymous record, or
// plain programs trip it.
// What: TestPolyRec_RecordsDoNotTripTheBound and
// TestPolyRec_E0607ReasonNamesTheRecursion.
// How: checks programs over records and growing generic instances
// (recur.go) and compares codes and reasons.

package check

import (
	"fmt"
	"strings"
	"testing"
)

// TestPolyRec_RecordsDoNotTripTheBound: 67 chained structs, each holding
// `?&(x: S{i+1}, y1: int, ..., yi: int)` (records of strictly growing size,
// no recursion), check clean: with borrow's record bucket the
// 67th growth stopped contentsInto and F's `b` had a second candidate
// (E0601). A record that does recur through a generic instance
// (`next: ?&(a: G[?T], b: int)`) still ends: E0507 on its store clause.
func TestPolyRec_RecordsDoNotTripTheBound(t *testing.T) {
	t.Parallel()
	var b strings.Builder
	for i := 1; i <= 66; i++ {
		fmt.Fprintf(&b, "struct S%d {\n    n: ?&(x: S%d", i, i+1)
		for j := 1; j <= i; j++ {
			fmt.Fprintf(&b, ", y%d: int", j)
		}
		b.WriteString(")\n}\n\n")
	}
	b.WriteString("struct S67 {\n    z: int\n}\n\nF(a: &S1, b: &i32): &i32 {\n    _ = a\n    return b\n}\n")
	w := newWorld(t, true)
	addWithin(t, w, b.String()).wantClean(t)

	w = newWorld(t, true)
	crec := addWithin(t, w, "struct G[T] {\n    v: T\n    next: ?&(a: G[?T], b: int)\n}\n\nPut(g: &G[int], q: &&i32) *q into g {\n}\n")
	if got := codes(w, crec.diags()); got != "E0507@6:27" {
		t.Errorf("crec: got %q, want E0507@6:27:\n%s", got, dump(w, crec.diags()))
	}
}

// TestPolyRec_E0607ReasonNamesTheRecursion:
// `from w.g` with g a G[int] whose instances grow along two fields is
// refused (hasMut is true at the bound), and the reason is
// "recursive-type", not "mut", when no mut can occur in the type; with a
// mut field it stays "mut".
func TestPolyRec_E0607ReasonNamesTheRecursion(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, field, want string }{
		{"shared", "", "recursive-type"},
		{"mut", "    export m: ?mut i32\n", "mut"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			b := addWithin(t, w, "export struct G[T] {\n    export v: T\n"+c.field+"    export a: ?&G[?T]\n    export b: ?&G[[2]T]\n}\n\n"+
				"export struct W {\n    export g: G[int]\n}\n\nexport Get(w: &W): G[int] from w.g {\n    return w.g\n}\n")
			ds := b.diags()
			if len(ds) != 1 || ds[0].Code.String() != "E0607" || fact(ds[0], "reason") != c.want {
				t.Fatalf("want one E0607 reason %q, got:\n%s", c.want, dump(w, ds))
			}
		})
	}
}
