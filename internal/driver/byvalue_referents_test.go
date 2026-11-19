// Why: a by-value parameter may hold several references, and a store of a
// loan from one into another could write a dead local's address into the
// caller's reference.
// What: tests that a by-value parameter's p#1 stands for the referents of
// all its first-level references, so such a store is judged (E0604), while
// a reference or slice parameter keeps "already held".
// How: d462Only checks each program and wants E0604 alone with no clause
// offered.

package driver

import (
	"strings"
	"testing"

	"mk/internal/diag"
)

// d462Only checks src and wants only E0604, with no clause offered: `*s
// into s` needs a reference or a slice (E0301), and no clause declares a
// store between two referents of one by-value parameter.
func d462Only(t *testing.T, src string) {
	t.Helper()
	r := checkText(t, src)
	if len(r.Report.Diags) == 0 {
		t.Fatalf("want E0604, got clean")
	}
	for _, e := range r.Report.Diags {
		if e.Diag.Code != diag.E0604 {
			t.Errorf("want only E0604, got %v %s", e.Diag.Code, e.Diag.Message)
		}
		if strings.Contains(e.Diag.Message, "write `") {
			t.Errorf("a clause is offered: %s", e.Diag.Message)
		}
	}
}

const d462Two = `struct S {
    x: &&i32
    b: mut &i32
}

@@

Inner(p: mut &i32) {
    n: i32 = 7
    r: &i32 = &n
    F(S(x: &r, b: p))
}
`

// TestByValueTwoReferents: with and without var, *s.b = *s.x writes the
// address n's reference holds into the caller's reference: E0604 in F.
func TestByValueTwoReferents(t *testing.T) {
	t.Parallel()
	d462Only(t, strings.Replace(d462Two, "@@", "F(var s: S) {\n    *s.b = *s.x\n}", 1))
	d462Only(t, strings.Replace(d462Two, "@@", "F(s: S) {\n    *s.b = *s.x\n}", 1))
}

// TestByValueCell: the same store through a Cell field.
func TestByValueCell(t *testing.T) {
	t.Parallel()
	d462Only(t, `struct S {
    x: &&i32
    c: &Cell[?&i32]
}

F(s: S) {
    s.c.Store(some(*s.x))
}
`)
}

// TestByValueThroughCall: the store made by a callee's
// `*src into dst`.
func TestByValueThroughCall(t *testing.T) {
	t.Parallel()
	d462Only(t, `Put(src: mut &i32, dst: mut mut &i32) *src into dst {
    **dst = *src
}

struct S {
    x: mut &i32
    b: mut &i32
}

F(var s: S) {
    Put(mut *s.x, mut s.b)
}
`)
}

// TestReferenceKeepsShortcut: one referent, *xs or *s, so an
// element stored into another element of it is already held.
func TestReferenceKeepsShortcut(t *testing.T) {
	t.Parallel()
	r := checkText(t, `Dup(xs: mut []&i32) {
    xs[0] = xs[1]
}

DupArr(s: mut [2]&i32) {
    s[0] = s[1]
}
`)
	if len(r.Report.Diags) != 0 {
		t.Errorf("want clean, got:\n%s", diagLines(r))
	}
}
