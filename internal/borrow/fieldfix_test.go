// Why: E0501 on a view method's automatic `from self` is fixed by writing a
// narrower `from self.f` clause, and the fix must be offered only when it is
// right.
// What: TestE0501_FieldClauseFix, for access.go fieldClauseFix with
// Env.FieldClause, which the driver fills from check's (*Checker).FieldClause.
// How: a Pager view whose Peek is called twice, with and without the clause
// callback, private and exported.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// view struct Pager { text: []u8; at: int }
// Pager.Peek(mut self): []u8 { self.at += 1; return self.text }
// Both(t: []u8): int { var b := Pager(text: t, at: 0); x := b.Peek()
// y := b.Peek() // <- E0501 ; return x.Len() + y.Len() }: the second call's
// mut borrow of b conflicts with the loan x holds through Peek's automatic
// `from self`. The fix writes check's clause on Peek: proven for a
// private Peek, compiles for an exported one, and absent with no clause
// or no callback.
func TestE0501_FieldClauseFix(t *testing.T) {
	at := source.Span{File: kitFile, Start: 1000, End: 1000}
	for _, tc := range []struct {
		name     string
		hook     bool
		clause   bool
		exported bool
		fix      bool
		want     diag.FixClass
	}{
		{"private", true, true, false, true, diag.Proven},
		{"exported", true, true, true, true, diag.Compiles},
		{"no clause", true, false, false, false, 0},
		{"no callback", false, false, false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := newKit(t)
			su := k.tab.Slice(types.U8)
			k.structItem(k.main, "Pager", 0, true, false, field("text", su), field("at", types.Int))
			pager := k.named("Pager")
			peek := k.method("Pager", "Peek", types.ByMut, pager, nil, su, types.EmptyErrors)
			peek.Flows.Sources = []types.Source{{Param: 1}}
			m := k.body(k.sig("Both", []types.Param{k.param("t", su)}, types.Int, types.EmptyErrors))
			b := m.user("b", pager, true)
			m.set(b, m.agg(mir.AggStruct, k.item("Pager"), pager, cp(m.local(1)), imm(types.Int, 0)))
			mp := k.tab.Mut(pager)
			x, y := m.user("x", su, false), m.user("y", su, false)
			// cover gives the call just made a span holding its receiver's
			// borrow, as lower's call span holds `b` in `b.Peek()`.
			cover := func(site mir.SpanID) {
				s, c := m.b.Span(site), m.b.Span(m.b.Blocks[m.cur].Term.Span)
				m.b.Blocks[m.cur].Term.Span = m.b.AddSpan(source.Span{File: s.File, Start: s.Start, End: c.End})
			}
			t1 := m.temp(mp)
			s1 := m.set(t1, mutRef(m.local(b)))
			n1 := m.block()
			m.call(peek.Item, nil, []mir.Arg{argMove(m.local(t1))}, m.local(x), n1)
			cover(s1)
			m.at(n1)
			t2 := m.temp(mp)
			bad := m.set(t2, mutRef(m.local(b)))
			n2 := m.block()
			m.call(peek.Item, nil, []mir.Arg{argMove(m.local(t2))}, m.local(y), n2)
			cover(bad)
			m.at(n2)
			lx, ly := m.temp(types.Int), m.temp(types.Int)
			m.set(lx, mir.Rvalue{Kind: mir.RvLen, Place: m.local(x)})
			m.set(ly, mir.Rvalue{Kind: mir.RvLen, Place: m.local(y)})
			m.set(0, mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Add, A: cp(m.local(lx)), B: cp(m.local(ly))})
			m.end(ly, lx, t2, y, t1, x, b)
			m.ret(mir.ExitNormal)
			m.validate(t)
			env := k.env(m.sig)
			if tc.hook {
				env.FieldClause = func(id types.ItemID) (FieldClause, bool) {
					if id != peek.Item || !tc.clause {
						return FieldClause{}, false
					}
					return FieldClause{Clause: "from self.text", At: at, Exported: tc.exported}, true
				}
			}
			res := Check(m.b, env)
			m.expect(t, res, diag.E0501, bad)
			fixes := res.Diags[0].Fixes
			if !tc.fix {
				if len(fixes) != 0 {
					t.Fatalf("want no fix, got %+v", fixes)
				}
				return
			}
			if len(fixes) != 1 || fixes[0].Class != tc.want || len(fixes[0].Edits) != 1 ||
				fixes[0].Edits[0].Text != " from self.text" || fixes[0].Edits[0].Span != at {
				t.Fatalf("want one %v fix inserting ` from self.text` at %v, got %+v", tc.want, at, fixes)
			}
		})
	}
}
