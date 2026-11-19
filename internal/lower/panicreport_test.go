// Why: a panic prints a report naming its site and cause, and lower
// supplies both.
// What: TestTrapSiteIsCheckedExpression,
// TestContracts_PanicNamesClause.
// How: each trap's site is a statement's span, which codegen renders, so a
// trapping rvalue stored straight into a binding must carry the checked
// expression's span; a contract panic passes its clause kind as the message.

package lower

import (
	"testing"

	"mk/internal/mir"
)

// TestTrapSiteIsCheckedExpression (lowered with Validate on): `b := Cast[u8](n)` reported its
// cast at b (tests/programs/panics/cast.mk wants 3:10, got 3:5), and
// `t := s[0..n]` its str boundary at t. An rvalue with an implicit check
// stored by a binding or a direct assignment now carries the value's span;
// any other rvalue keeps the statement's (borrow anchors at it).
func TestTrapSiteIsCheckedExpression(t *testing.T) {
	l := lowerSrc(t, `F(n: int, s: str): int {
	b := Cast[u8](n + 1)
	var c: u8 = 0
	c = Cast[u8](n)
	t := s[0..n]
	d := n + 1
	e := -n
	k := n
	w := n < 1
	var q := 0
	q = n * 2
	_ = w
	return int(b) + int(c) + t.Len() + d + e + k + q
}
`)
	b := l.body("F")
	at := map[string][]string{}
	for _, blk := range b.Blocks {
		for _, st := range blk.Stmts {
			if st.Kind != mir.StAssign || st.Place.Proj.Len != 0 {
				continue
			}
			lc := b.Locals[st.Place.Local]
			if lc.Kind != mir.LocalUser {
				continue
			}
			name := l.w.in.Str(lc.Name)
			at[name] = append(at[name], l.w.lc(b.Span(st.Span)))
		}
	}
	for _, c := range []struct {
		name string
		want []string
	}{
		{"b", []string{"2:7"}},          // CastChecked: the Cast call
		{"c", []string{"3:2", "4:6"}},   // the initializer, then the Cast of the assignment
		{"t", []string{"5:7"}},          // str RvSlice: the slice expression
		{"d", []string{"6:7"}},          // Add: the binary expression
		{"e", []string{"7:7"}},          // Neg: the unary expression
		{"k", []string{"8:2"}},          // a plain copy: the statement
		{"w", []string{"9:2"}},          // a compare never traps: the statement
		{"q", []string{"10:2", "11:6"}}, // Mul through an assignment
	} {
		wantList(t, c.name, at[c.name], c.want...)
	}
}

// TestContracts_PanicNamesClause: lower passes requires, ensures or
// invariant as the contract panic's message, so the report is
// `panic: contract: requires at ...` (tests/programs/panics/contract.mk;
// before, the panic had no message and printed `panic: contract`).
func TestContracts_PanicNamesClause(t *testing.T) {
	l := lowerSrc(t, `struct Span {
	lo: int
	hi: int
	invariant lo <= hi
}
Half(n: int): int
	requires n > 1
	ensures result > 0
{
	return n / 2
}
Make(n: int): Span {
	return Span(lo: 0, hi: n)
}
Span.Grow(mut self, n: int) {
	self.hi += n
}
`)
	h := l.print("Half")
	if count(h, `panic contract const str "requires"`) != 1 || count(h, `panic contract const str "ensures"`) != 1 {
		t.Fatalf("Half: one requires and one ensures panic, each naming its clause:\n%s", h)
	}
	for _, k := range []string{"Make", "Span:Grow"} {
		b := l.print(k)
		if count(b, "panic contract") == 0 || count(b, "panic contract") != count(b, `panic contract const str "invariant"`) {
			t.Fatalf("%s: every invariant panic names its clause:\n%s", k, b)
		}
	}
}
