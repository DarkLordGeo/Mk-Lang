// Why: the flow predicates' type walks must end on polymorphic recursion.
// What: TestPolyRec_FlowWalksEnd.
// How: runs flows.go's contentsInto, hasMutSeen, reachesInterior and
// privateFieldChanging (bounded by recur.go) over types whose instances
// grow, under a deadline.

package check

import (
	"strings"
	"testing"
	"time"
)

// addWithin adds a module, failing when stage 5 does not end in 30 s: on
// the walks without a bound it overflowed the stack or never ended.
func addWithin(t *testing.T, w *world, src string) *built {
	t.Helper()
	done := make(chan *built, 1)
	go func() { done <- w.add("main", opt{}, src) }()
	select {
	case b := <-done:
		return b
	case <-time.After(30 * time.Second):
		t.Fatal("stage 5 did not end within 30 s")
	}
	return nil
}

// TestPolyRec_FlowWalksEnd: a function whose result or store clause has a
// polymorphically recursive type (`next: ?&G[?T]`, the same through a
// slice field or an enum variant) gets its sources: g is the one supplier,
// by H(g), since past the bound contents is ANY and hasMut true. A store
// into a `&G[int]` that reaches no interior mutability gets E0507, as
// reachesInterior answers false past the bound; one whose type argument is
// a Cell is accepted. An exported function's E0601 names the private field
// (privateFieldChanging).
func TestPolyRec_FlowWalksEnd(t *testing.T) {
	t.Parallel()
	const g = "struct G[T] {\n    v: T\n    next: ?&G[?T]\n}\n\n"
	for _, c := range []struct{ name, src, fn string }{
		{"c1", g + "Next(g: &G[int]): ?&G[?int] {\n    return g.next\n}\n", "Next"},
		{"c1s", "struct S[T] {\n    v: T\n    kids: []S[?T]\n}\n\nKids(s: &S[int]): []S[?int] {\n    return s.kids\n}\n", "Kids"},
		{"c1e", "enum E[T] {\n    Leaf(v: T)\n    Node(next: &E[?T])\n}\n\nInner(e: &E[int]): ?&E[?int] {\n    return none\n}\n", "Inner"},
		{"mut", "struct M[T] {\n    v: T\n    next: ?mut M[?T]\n}\n\nPeek(m: &M[int], k: &int): ?&M[?int] {\n    return none\n}\n", "Peek"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := addWithin(t, newWorld(t, true), c.src)
			b.wantClean(t)
			src := b.sigOf(c.fn).Flows.Sources
			if len(src) != 1 || src[0].Param != 1 || src[0].Behind || len(src[0].Path) != 0 {
				t.Errorf("%s's sources = %+v, want H(1) alone", c.fn, src)
			}
		})
	}
	t.Run("c2", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, true)
		b := addWithin(t, w, g+"Put(g: &G[int], q: &&i32) *q into g {\n}\n")
		if got := codes(w, b.diags()); got != "E0507@6:27" {
			t.Errorf("got %q, want E0507@6:27:\n%s", got, dump(w, b.diags()))
		}
	})
	// The same answer as a type that does not recur polymorphically, and
	// as a G whose instances branch through two growing fields (the walk
	// goes on past each stop, so it ends by the instance backstop).
	for name, src := range map[string]string{
		"plain":  "struct P {\n    v: int\n    next: ?&P\n}\n\nPut(g: &P, q: &&i32) *q into g {\n}\n",
		"branch": "struct G[T] {\n    v: T\n    a: ?&G[?T]\n    b: ?&G[[2]T]\n}\n\nPut(g: &G[int], q: &&i32) *q into g {\n}\n",
	} {
		t.Run("c2-"+name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			b := addWithin(t, w, src)
			if got := codes(w, b.diags()); !strings.HasPrefix(got, "E0507@") || strings.Contains(got, " ") {
				t.Errorf("got %q, want one E0507:\n%s", got, dump(w, b.diags()))
			}
		})
	}
	t.Run("c2-cell", func(t *testing.T) {
		t.Parallel()
		b := addWithin(t, newWorld(t, true), g+"Put(g: &G[Cell[?&i32]], q: &&i32) *q into g {\n}\n")
		b.wantClean(t)
	})
	t.Run("private", func(t *testing.T) {
		t.Parallel()
		w := newWorld(t, true)
		b := addWithin(t, w, "export struct G[T] {\n    export v: T\n    next: ?&G[?T]\n}\n\n"+
			"export Next(g: &G[int], h: &int): ?&G[?int] {\n    return g.next\n}\n")
		ds := b.diags()
		if got := codes(w, ds); got != "E0601@6:8" {
			t.Fatalf("got %q, want E0601@6:8:\n%s", got, dump(w, ds))
		}
		if f := fact(ds[0], "private_field"); f != "next" {
			t.Errorf("private_field = %q, want next", f)
		}
	})
}
