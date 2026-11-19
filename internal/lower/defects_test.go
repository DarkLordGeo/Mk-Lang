// Why: a Scope method called on a Scope[E], a void value bound to a name and
// a destructured value must lower to MIR that validates; programs written in
// Mk met an ICE at each.
// What: TestChatCancelOnScopeOf, TestProcmanBindVoidElse,
// TestProcmanDestructureValue.
// How: a Scope method (Cancel) on a Scope[E] borrows the Scope header, field
// 0 of Scope[E]'s layout, never the whole Scope[E] for a &Scope. A void
// expression's value is the void constant, which a binding assigns, never a
// record aggregate of void. Each test lowers the shape with Validate on.

package lower

import "testing"

func TestChatCancelOnScopeOf(t *testing.T) {
	l := lowerSrc(t, `error Wake

Stop(w: &Scope[Wake]) {
    w.Cancel()
}

Body(io: Io): Wake!void {
    try scope w := io.ScopeOf[Wake]() {
        Stop(&w)
        w.Cancel()
    }
}
`)
	wantIn(t, l.print("Stop"), "&(*_1).0", "Scope:Cancel")
	wantIn(t, l.print("Body"), "Stop", ".0", "Scope:Cancel")
}

func TestProcmanBindVoidElse(t *testing.T) {
	l := lowerSrc(t, `error Full

Push(n: int): Full!void {
    if n < 0 {
        fail Full
    }
}

Keep(n: int) {
    pushed := Push(n) else { }
    _ = pushed
    q := Push(n) else { return }
    _ = q
    Assert(n == n)
}
`)
	wantIn(t, l.print("Keep"), "Push", "Push")
}

func TestProcmanDestructureValue(t *testing.T) {
	l := lowerSrc(t, `error Bad(line: int, col: int)

Where(e: Bad, c: bool): int {
    (line, col) := match e {
        Bad(line, col) { (line, col) }
    }
    (line: a, col: b) := if c { (line: line + 1, col: col) } else { (line: 0, col: 0) }
    return a + b
}
`)
	wantIn(t, l.print("Where"), "switch", "return")
}
