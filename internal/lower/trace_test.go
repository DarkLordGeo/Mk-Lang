// Why: the error-return trace starts fresh at a newly built error and
// appends otherwise, and lower decides which returns carry NewError.
// What: TestNewErrorOnlyOnConstruction and failReturns.
// How: lowers fails of constructed and bound errors and try edges, and
// counts the error returns that carry NewError.

package lower

import (
	"testing"

	"mk/internal/mir"
)

// failReturns counts a body's error returns of kind k, and how many of them
// carry NewError.
func failReturns(b *mir.Body, k mir.ExitKind) (n, fresh int) {
	for i := range b.Blocks {
		t := b.Blocks[i].Term
		if t.Kind != mir.TermReturn || t.Exit != k {
			continue
		}
		n++
		if t.NewError {
			fresh++
		}
	}
	return n, fresh
}

// TestNewErrorOnlyOnConstruction pins lower's whole part of the error
// return trace: codegen starts a fresh trace at a
// return with NewError and appends to the running one otherwise, so the flag
// is set on a fail of an error constructed at the fail, and on nothing else:
// not on a fail of a bound error value (a parameter, a local bound from a
// construction), and never on a try edge.
func TestNewErrorOnlyOnConstruction(t *testing.T) {
	l := lowerSrc(t, `error NotFound(id: int)
error Denied
Load(id: int): NotFound|Denied!str {
	if id < 0 {
		fail Denied
	}
	if id > 9 {
		fail NotFound(id: id)
	}
	return "data"
}
Again(e: Denied): Denied!void {
	fail e
}
Bound(): Denied!void {
	e := Denied
	fail e
}
Show(id: int): NotFound|Denied!str {
	s := try Load(id)
	return s
}
Widen(e: Denied): NotFound|Denied!void {
	fail e
}
`)
	for _, c := range []struct {
		body        string
		kind        mir.ExitKind
		n, newError int
	}{
		{"Load", mir.ExitFail, 2, 2},
		{"Again", mir.ExitFail, 1, 0},
		{"Bound", mir.ExitFail, 1, 0},
		{"Widen", mir.ExitFail, 1, 0},
		{"Show", mir.ExitTry, 1, 0},
		{"Show", mir.ExitFail, 0, 0},
	} {
		n, fresh := failReturns(l.body(c.body), c.kind)
		if n != c.n || fresh != c.newError {
			t.Errorf("%s: %d returns of exit %v, %d with NewError; want %d and %d\n%s",
				c.body, n, c.kind, fresh, c.n, c.newError, l.print(c.body))
		}
	}
}
