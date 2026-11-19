// Why: user locals keep one loan set per field, and the rules that keep a
// root split or make it whole are easy to get subtly wrong.
// What: tests of locals.go's per-field held sets for locals. The compiled
// witnesses in tests/soundness/witness/S2-* are the end-to-end tests; these
// pin the pieces.
// How: hand-built bodies with field writes, checked with the split-union
// self-check on.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// Every test of this package runs with put checking that a split root's
// held set is exactly the union of its fields.
func init() { SetSplitUnionCheck(true) }

// TestFieldWriteStrongAndWeak: `t.b = &y` replaces field b's set, so
// a read of t.a after y ends is clean; t.b is not.
func TestFieldWriteStrongAndWeak(t *testing.T) {
	for _, readB := range []bool{false, true} {
		k := newKit(t)
		pair := fldPair(k)
		ri := refI32(k)
		m := k.body(k.sig("Caller", nil, types.Void, types.EmptyErrors))
		x := m.user("x", types.I32, false)
		m.set(x, use(imm(types.I32, 1)))
		tx := m.temp(ri)
		m.set(tx, ref(m.local(x)))
		tv := m.user("t", pair, true)
		m.set(tv, mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: k.item("Pair"), Ops: m.b.AddOperands(cp(m.local(tx)), cp(m.local(tx)))})
		y := m.user("y", types.I32, false)
		m.set(y, use(imm(types.I32, 2)))
		ty := m.temp(ri)
		bad := m.set(ty, ref(m.local(y)))
		m.assign(m.pl(tv, fld(1, ri)), use(cp(m.local(ty))))
		m.end(ty, y)
		f := uint32(0)
		if readB {
			f = 1
		}
		m.callv("Use", []types.TypeID{types.I32}, argCopy(m.pl(tv, fld(f, ri))))
		m.end(tv, tx, x)
		m.ret(mir.ExitNormal)
		if readB {
			m.expect(t, m.check(t), diag.E0505, bad)
		} else {
			m.expect(t, m.check(t))
		}
	}
}
