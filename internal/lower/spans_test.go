// Why: borrow anchors its diagnostics at the source position each MIR item
// carries, so a wrong span puts an error on the wrong code.
// What: TestSpansForDiagnostics, TestSpans_ReceiverBorrowAtCall.
// How: lowers a small program and compares the line:column of its locals,
// loops, statements and terminators with the source.

package lower

import (
	"fmt"
	"testing"

	"mk/internal/mir"
)

func TestSpansForDiagnostics(t *testing.T) {
	l := lowerSrc(t, `Log() {
}
Get(xs: []int, i: int): int {
	return xs[i]
}
F(xs: []int, i: int): int {
	defer Log()
	var s := 0
	for x in xs {
		s += x
	}
	if i < 0 {
		return Get(xs, 0)
	}
	k := xs[i]
	s += k
	return s
}
`)
	b := l.body("F")
	at := func(id mir.SpanID) string { return l.w.lc(b.Span(id)) }
	name := func(lc mir.Local) string { return l.w.in.Str(lc.Name) }
	for _, lc := range b.Locals {
		switch {
		case lc.Kind == mir.LocalUser && name(lc) == "s":
			if got := at(lc.Span); got != "8:6" {
				t.Errorf("user local s at %s, want its Ident 8:6", got)
			}
		case lc.Kind == mir.LocalUser && name(lc) == "k":
			if got := at(lc.Span); got != "15:2" {
				t.Errorf("user local k at %s, want 15:2", got)
			}
		}
	}
	// Hidden loop locals (the iterated slice, its length, the counter) are
	// at the for header.
	hidden := 0
	for _, lc := range b.Locals {
		if lc.Kind == mir.LocalTemp && lc.Span == b.Loops[0].Span {
			hidden++
		}
	}
	if at(b.Loops[0].Span) != "9:2" || hidden < 3 {
		t.Errorf("loop at %s with %d hidden locals at its header, want 9:2 and 3 or more", at(b.Loops[0].Span), hidden)
	}
	var calls, returns, panics, ends []string
	var copies []string
	for _, blk := range b.Blocks {
		if blk.Origin == mir.OriginDefer {
			if blk.Term.Kind == mir.TermCall {
				copies = append(copies, at(blk.Term.Span))
			}
			continue
		}
		for _, st := range blk.Stmts {
			if st.Kind == mir.StScopeEnd && b.Locals[st.Local].Kind == mir.LocalUser && name(b.Locals[st.Local]) == "s" {
				ends = append(ends, at(st.Span))
			}
		}
		switch blk.Term.Kind {
		case mir.TermCall:
			calls = append(calls, at(blk.Term.Span))
		case mir.TermReturn:
			returns = append(returns, at(blk.Term.Span))
		case mir.TermPanic:
			panics = append(panics, at(blk.Term.Span))
		}
	}
	// TermCall: the call expression. TermReturn: the return statements.
	// TermPanic: the checked index expression. Cleanup copies: the defer
	// body's own call, on both returns. A local's scope end on a return:
	// the return statement.
	wantList(t, "calls", calls, "13:10")
	wantList(t, "returns", returns, "13:3", "17:2")
	wantList(t, "panics", panics, "15:7")
	wantList(t, "defer copies", copies, "7:8", "7:8")
	wantList(t, "scope ends of s", ends, "13:3", "17:2")
}

func wantList(t *testing.T, what string, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s at %v, want %v", what, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s at %v, want %v", what, got, want)
			return
		}
	}
}

// TestSpans_ReceiverBorrowAtCall pins the RvRef span: an automatic
// receiver borrow carries the receiver's call ("the receiver's call for an
// automatic borrow"), an explicit &p or mut p its own borrow expression, so
// borrow reports E0505 at the call for one and at the & for the other.
func TestSpans_ReceiverBorrowAtCall(t *testing.T) {
	l := lowerSrc(t, `struct P {
	x: int
}
P.Get(&self): int {
	return self.x
}
P.Bump(mut self) {
	self.x += 1
}
Read(p: &P): int {
	return p.x
}
F(): int {
	var p := P(x: 1)
	n := p.Get()
	p.Bump()
	return n + Read(&p)
}
`)
	b := l.body("F")
	var refs []string
	for _, blk := range b.Blocks {
		for _, st := range blk.Stmts {
			if st.Kind == mir.StAssign && st.Rvalue.Kind == mir.RvRef {
				sp := b.Span(st.Span)
				refs = append(refs, fmt.Sprintf("%s+%d", l.w.lc(sp), sp.End-sp.Start))
			}
		}
	}
	// The whole calls p.Get() (15:7, 7 bytes) and p.Bump() (16:2, 8 bytes),
	// not their receiver p (1 byte); &p at 17:18, 2 bytes.
	wantList(t, "RvRef spans", refs, "15:7+7", "16:2+8", "17:18+2")
}
