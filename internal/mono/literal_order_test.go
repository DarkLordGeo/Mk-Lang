// Why: the explicit-stack constID in canon.go must give the same literal
// order as the recursive form it replaced.
// What: TestLiteralShallowOrder and constIDOld, the old recursive walk.
// How: canonicalizes the same nested const with both walks and compares the
// results.

package mono

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// constIDOld is the earlier recursive constID, verbatim apart from its name.
func (l *literals) constIDOld(c types.ConstID) types.ConstID {
	if l == nil || c == types.NoConst {
		return c
	}
	if r, ok := l.memo[c]; ok {
		return r
	}
	l.memo[c] = c
	ct := l.env.Consts()
	v := ct.Get(c)
	var relocs []types.Reloc
	changed := false
	for i, r := range v.Relocs {
		nr := r
		switch r.Kind {
		case types.RelocFn:
			nr.Fn.Args = l.args(r.Fn.Args)
			nr.Fn.Item = l.item(r.Fn.Item, nr.Fn.Args)
		case types.RelocData:
			nr.Data = l.constIDOld(r.Data)
		}
		if !changed && !sameReloc(nr, r) {
			changed = true
			relocs = append(make([]types.Reloc, 0, len(v.Relocs)), v.Relocs[:i]...)
		}
		if changed {
			relocs = append(relocs, nr)
		}
	}
	out := c
	if changed {
		out = ct.Intern(types.ConstValue{Type: v.Type, Align: v.Align, Bytes: append([]byte(nil), v.Bytes...), Relocs: relocs})
	}
	l.memo[c] = out
	return out
}

// TestLiteralShallowOrder: parent = {fn litA @0, data child @8},
// child = {fn litB @0}, litA and litB equal bodies (one content key).
func TestLiteralShallowOrder(t *testing.T) {
	w := newWorld()
	tab := w.tab
	refInt := tab.Ref(types.Int)
	fnT := tab.Fn(types.FnType{Params: []types.FnParam{{Type: refInt}}, Result: types.Bool})
	mk := func(name string) types.ItemID {
		f := w.fn(name, 0, []types.TypeID{refInt}, types.Bool)
		lit := w.literal(f, 0, []types.TypeID{refInt}, types.Bool)
		lb := w.body(lit)
		lb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: mir.Imm(types.Bool, 1)})
		lb.ret()
		return lit
	}
	litA, litB := mk("FA"), mk("FB")
	if keyOf(w, litA, nil).Text != keyOf(w, litB, nil).Text {
		t.Fatalf("setup: literals have different content keys")
	}
	child := w.consts.Intern(types.ConstValue{Type: fnT, Align: 8, Bytes: make([]byte, 8),
		Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocFn, Fn: types.FnRef{Item: litB}}}})
	parent := w.consts.Intern(types.ConstValue{Type: types.U64, Align: 8, Bytes: make([]byte, 16),
		Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocFn, Fn: types.FnRef{Item: litA}}, {Offset: 8, Kind: types.RelocData, Data: child}}})
	lOld, lNew := newLiterals(w), newLiterals(w)
	old, nw := lOld.constIDOld(parent), lNew.constID(parent)
	name := func(c types.ConstID) string {
		v := w.consts.Get(c)
		s := w.items.Get(v.Relocs[0].Fn.Item).Key.String()
		if len(v.Relocs) > 1 {
			s += " / " + w.items.Get(w.consts.Get(v.Relocs[1].Data).Relocs[0].Fn.Item).Key.String()
		}
		return s
	}
	t.Logf("old: %d (%s), new: %d (%s)", old, name(old), nw, name(nw))
	if old != nw {
		t.Errorf("constID differs from the recursive form on a 2-level value: old %s, new %s", name(old), name(nw))
	}
}
