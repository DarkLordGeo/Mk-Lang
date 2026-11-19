// Why: a `for x in mut xs` loop holds a loan on xs while each element loan
// lives, and element loans must not conflict with their own loop.
// What: tests of the loop holder and element loans that skip their own
// loop's holder and element loans, with the mutLoop builder.
// How: builds the shapes lower emits: the holder is an RvSlice of origin loop
// holder, each element an RvRef of origin loop element through it, and the
// holder ends at the loop's exit.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// mutLoop builds `for x in mut xs { body(x) }` over the mut []i32 local or
// parameter xs, in the current block, and leaves the builder at the loop's
// exit block with the holder and counters ended. It returns the holder
// statement's span.
func (m *mb) mutLoop(xs mir.LocalID, body func(x mir.LocalID)) mir.SpanID {
	k := m.k
	ms := k.tab.MutSlice(types.I32)
	l := mir.LoopID(len(m.b.Loops))
	header, bodyB, exit := m.block(), m.block(), m.block()
	m.b.Loops = append(m.b.Loops, mir.LoopInfo{Header: header, Exit: exit, Span: m.sp()})
	h := m.b.AddLocal(mir.Local{Type: ms, Kind: mir.LocalLoopHolder, Loop: l, Span: m.sp()})
	m.b.Loops[l].Holder = h
	i := m.temp(types.Int)
	n := m.temp(types.Int)
	c := m.temp(types.Bool)
	hs := m.set(h, mir.Rvalue{Kind: mir.RvSlice, Borrow: mir.Exclusive, Place: m.local(xs), Origin: mir.OriginLoopHolder, Loop: l})
	m.set(i, use(imm(types.Int, 0)))
	m.set(n, mir.Rvalue{Kind: mir.RvLen, Place: m.local(h)})
	m.jump(header)
	m.at(header)
	m.set(c, mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Lt, A: cp(m.local(i)), B: cp(m.local(n))})
	m.branch(cp(m.local(c)), bodyB, exit)
	m.at(bodyB)
	x := m.user("x", k.tab.Mut(types.I32), false)
	m.set(x, mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Exclusive, Place: m.pl(h, index(i, types.I32)),
		Origin: mir.OriginLoopElement, Loop: l})
	body(x)
	m.end(x)
	m.set(i, mir.Rvalue{Kind: mir.RvBinary, Bin: mir.AddWrap, A: cp(m.local(i)), B: imm(types.Int, 1)})
	m.jump(header)
	m.at(exit)
	m.end(c, n, i, h)
	return hs
}

// All(io: Io, xs: mut []i32) { scope s := io.Scope() { for x in mut xs {
// s.go Bump(x) } } }: ok; every x is kept by the scope.
func TestR20_All(t *testing.T) {
	k := newKit(t)
	k.taskLib()
	m := k.body(k.sig("All", []types.Param{k.param("io", k.Io), k.param("xs", k.tab.MutSlice(types.I32))}, types.Void, types.EmptyErrors))
	s := m.openScope()
	m.mutLoop(2, func(x mir.LocalID) {
		m.spawn(s, "Bump", argMove(m.local(x)))
	})
	m.join(s)
	m.end(s)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// Elements kept from one activation conflict with the next loop's
// entry: var last: ?mut i32 = none; for x in mut xs { last = some(x) }; for
// y in mut xs { } // <- E0501; Use(&last).
func TestR20_TwoActivations(t *testing.T) {
	k := newKit(t)
	omi := k.tab.Optional(k.tab.Mut(types.I32))
	m := k.body(k.sig("F", []types.Param{k.param("xs", k.tab.MutSlice(types.I32))}, types.Void, types.EmptyErrors))
	last := m.user("last", omi, true)
	m.set(last, m.agg(mir.AggNone, types.NoItem, omi))
	m.mutLoop(1, func(x mir.LocalID) {
		m.set(last, m.agg(mir.AggSome, types.NoItem, omi, mv(m.local(x))))
	})
	bad := m.mutLoop(1, func(mir.LocalID) {})
	m.useRef(m.local(last), omi)
	m.end(last)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0501, bad)
}

// xs.Swap(0, 1) inside for x in mut xs is E0501 at the call, before or
// after the use of x: the loop's holder loan is live through the loop.
func TestR20_SwapInLoop(t *testing.T) {
	for _, before := range []bool{true, false} {
		k := newKit(t)
		ms := k.tab.MutSlice(types.I32)
		k.fn("Swap", []types.Param{k.param("self", ms), k.param("i", types.Int), k.param("j", types.Int)}, types.Void, types.EmptyErrors)
		m := k.body(k.sig("F", []types.Param{k.param("xs", ms)}, types.Void, types.EmptyErrors))
		var bad mir.SpanID
		swap := func() {
			bad = m.callv("Swap", nil, argReborrow(m.local(1)), mir.Arg{Operand: imm(types.Int, 0)}, mir.Arg{Operand: imm(types.Int, 1)})
		}
		m.mutLoop(1, func(x mir.LocalID) {
			if before {
				swap()
			}
			m.callv("UseMut", []types.TypeID{types.I32}, argMove(m.local(x)))
			if !before {
				swap()
			}
		})
		m.ret(mir.ExitNormal)
		m.expect(t, m.check(t), diag.E0501, bad)
	}
}
