// Why: loans a spawned call's arguments take must end with the scope, not
// stay stored in a carrier argument.
// What: TestSpawnLoansEndWithTheScope.
// How: a `scope s { s.go Pump(&m, &y) }` body on hand-built MIR, then a mut
// borrow of m after the block.

package borrow

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// TestSpawnLoansEndWithTheScope: a spawned call's argument loans last
// until the block exits, and s alone holds them. In `scope s { s.go Pump(&m,
// &y) }` over a `Mutex[&i32]` local m (a carrier written through `&`), the
// loans are not stored into m too, so `mut m` after the block is not E0502
// against a loan m holds on itself.
func TestSpawnLoansEndWithTheScope(t *testing.T) {
	k := newKit(t)
	k.taskLib()
	mt := k.tab.Named(k.known.Mutex, []types.GenericArg{types.TypeArg(refI32(k))})
	k.fn("Pump", []types.Param{k.param("c", k.tab.Ref(mt)), k.param("n", refI32(k))}, types.Void, types.EmptyErrors)
	m := k.body(k.sig("Run", []types.Param{k.param("io", k.Io)}, types.Void, types.EmptyErrors))
	z := m.user("z", types.I32, false)
	m.set(z, use(imm(types.I32, 1)))
	tz := m.temp(refI32(k))
	m.set(tz, ref(m.local(z)))
	mm := m.user("m", mt, true)
	m.set(mm, m.agg(mir.AggStruct, k.known.Mutex, mt, imm(types.U32, 0), cp(m.local(tz))))
	m.end(tz)
	y := m.user("y", types.I32, false)
	m.set(y, use(imm(types.I32, 2)))
	s := m.openScope()
	a1, a2 := m.temp(k.tab.Ref(mt)), m.temp(refI32(k))
	m.set(a1, ref(m.local(mm)))
	m.set(a2, ref(m.local(y)))
	m.spawn(s, "Pump", argCopy(m.local(a1)), argCopy(m.local(a2)))
	m.end(a2, a1)
	m.join(s)
	m.end(s)
	w := m.temp(k.tab.Mut(mt))
	m.set(w, mutRef(m.local(mm)))
	m.end(w, y, mm, z)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}
