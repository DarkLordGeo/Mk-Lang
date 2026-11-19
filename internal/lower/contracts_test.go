// Why: contracts and invariants lower to calls of their predicate items at
// the points where they must hold.
// What: TestContracts_InvariantOnDirectStore, TestContracts_ReturningInvariant,
// TestContracts_MutSelfInvariant.
// How: lowers small programs and checks where the predicate calls land.

package lower

import (
	"strings"
	"testing"
)

func TestContracts_InvariantOnDirectStore(t *testing.T) {
	l := lowerSrc(t, `struct Span {
	lo: int
	hi: int
	invariant lo <= hi
}
Make(): int {
	s := Span(lo: 1, hi: 4)
	return s.hi
}
`)
	f := l.print("Make")
	// The aggregate is stored into _s directly, then borrowed by the check.
	wantIn(t, f, "_1 = m.Span(", "= &_1", "#invariant0", "panic contract")
	if count(f, "#invariant0") != 1 {
		t.Fatalf("one invariant check:\n%s", f)
	}
}

func TestContracts_ReturningInvariant(t *testing.T) {
	l := lowerSrc(t, `error Bad
struct Span {
	lo: int
	hi: int
	invariant lo <= hi
}
Flip(s: Span): Span {
	return s
}
Maybe(s: Span, ok: bool): Bad!Span {
	if !ok {
		fail Bad
	}
	return s
}
Width(s: Span): int {
	return s.hi - s.lo
}
`)
	// The check borrows the success value before _0 is assigned.
	wantIn(t, l.print("Flip"), "= &_1", "#invariant0", "_0 = copy _1")
	m := l.print("Maybe")
	wantIn(t, m, "= &_1", "#invariant0", "_0 = ok(copy _1)")
	if count(m, "#invariant0") != 1 {
		t.Fatalf("no invariant check on the fail exit of a function returning Span:\n%s", m)
	}
	if strings.Contains(l.print("Width"), "#invariant") {
		t.Fatalf("Width returns no Span:\n%s", l.print("Width"))
	}
}

func TestContracts_MutSelfInvariant(t *testing.T) {
	l := lowerSrc(t, `error Bad
struct Span {
	lo: int
	hi: int
	invariant lo <= hi
}
Span.Grow(mut self, n: int): Bad!void {
	if n < 0 {
		fail Bad
	}
	self.hi += n
}
Span.Len(&self): int {
	return self.hi - self.lo
}
`)
	g := l.print("Span:Grow")
	// Before the fail exit and before the fall-off's success exit.
	if count(g, "#invariant0") != 2 {
		t.Fatalf("an invariant check before each exit:\n%s", g)
	}
	wantIn(t, g, "= &(*_1)", "#invariant0", "err(")
	if strings.Contains(l.print("Span:Len"), "#invariant") {
		t.Fatalf("&self takes no check:\n%s", l.print("Span:Len"))
	}
}
