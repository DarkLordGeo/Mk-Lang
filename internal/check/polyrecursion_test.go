// Why: the facts walk must end on polymorphic recursion, where a generic
// struct reaches itself under ever larger arguments; a hang there freezes
// the compiler.
// What: TestFactsPolymorphicRecursionTerminates.
// How: checks such structs under a 20 s deadline, then reads the facts
// (Share or not) of their instances.

package check

import (
	"testing"
	"time"

	"mk/internal/types"
)

// TestFactsPolymorphicRecursionTerminates: a generic struct that reaches
// itself through a reference under growing arguments (`?&G[?T]`,
// `?&G[[2]T]`) checks, where the walk minted a new TypeID at every level
// and never ended; its instances keep their arguments' facts (G[Cell[int]]
// is not Share, G[int] is), and a rotation that brings a Cell in only
// after a few levels still reaches it.
func TestFactsPolymorphicRecursionTerminates(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ name, src string }{
		{"optional", "struct G[T] {\n    v: T\n    next: ?&G[?T]\n}\n"},
		{"array", "struct G[T] {\n    v: T\n    next: ?&G[[2]T]\n}\n"},
		{"two-ways", "struct G[T] {\n    v: T\n    a: ?&G[?T]\n    b: ?&G[[2]T]\n    c: ?&G[&T]\n}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, true)
			done := make(chan *built, 1)
			go func() { done <- w.add("main", opt{}, c.src) }()
			var b *built
			select {
			case b = <-done:
			case <-time.After(20 * time.Second):
				t.Fatal("check did not end within 20 s")
			}
			b.wantClean(t)
			tab := w.tab
			g := b.item("G")
			arg := func(x types.TypeID) []types.GenericArg { return []types.GenericArg{types.TypeArg(x)} }
			if !w.c.Facts(tab.Named(g, arg(types.Int))).Share {
				t.Error("G[int] is Share")
			}
			if w.c.Facts(tab.Named(g, arg(tab.Named(w.c.known.Cell, arg(types.Int))))).Share {
				t.Error("G[Cell[int]] is not Share")
			}
		})
	}
	w := newWorld(t, true)
	b := w.add("main", opt{}, "struct R[A, B, C] {\n    a: A\n    next: ?&R[B, C, Cell[int]]\n}\n")
	b.wantClean(t)
	r, tab := b.item("R"), w.tab
	ints := []types.GenericArg{types.TypeArg(types.Int), types.TypeArg(types.Int), types.TypeArg(types.Int)}
	if w.c.Facts(tab.Named(r, ints)).Share {
		t.Error("R[int, int, int] reaches a Cell three levels down: not Share")
	}
}
