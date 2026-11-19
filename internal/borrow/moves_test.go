// Why: initialization and move states are the base of every linear-value
// and use-after-move error.
// What: tests of operands and destinations, init states, moves and joins:
// copies, moves, reads, late and partial init, leaks, destructuring, fields
// and indexes.
// How: hand-built MIR in the shapes lower emits, checked for E0401 to E0404,
// E0409, E0501 and E0508.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// Copies() { a := Point(x: 1, y: 2); b := a; Use(&a); Use(&b) }: ok.
func TestR1_Copies(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Copies", nil, types.Void, types.EmptyErrors))
	a := m.user("a", k.Point, false)
	b := m.user("b", k.Point, false)
	m.set(a, m.agg(mir.AggStruct, k.item("Point"), k.Point, imm(types.I32, 1), imm(types.I32, 2)))
	m.set(b, use(cp(m.local(a))))
	m.useRef(m.local(a), k.Point)
	m.useRef(m.local(b), k.Point)
	m.end(b, a)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// Moves() { f := Acquire("a"); g := f; f.Close() // <- E0401; g.Close() }.
func TestR1_Moves(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Moves", nil, types.Void, types.EmptyErrors))
	f := m.user("f", k.Res, false)
	g := m.user("g", k.Res, false)
	m.acquire(f)
	m.set(g, use(mv(m.local(f))))
	bad := m.close(f)
	m.close(g)
	m.end(g, f)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0401, bad)
}

// Reads() { f := Acquire("a"); defer f.Close(); Use(&f) }: ok. The defer
// copy is a block of origin defer on the exit edge.
func TestR2_Reads(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Reads", nil, types.Void, types.EmptyErrors))
	f := m.user("f", k.Res, false)
	m.acquire(f)
	dsp := m.sp()
	m.b.Defers = append(m.b.Defers, mir.DeferInfo{Span: dsp})
	m.useRef(m.local(f), k.Res)
	dblk := m.b.AddBlock(mir.OriginDefer, 0)
	m.jump(dblk)
	m.at(dblk)
	m.close(f)
	m.end(f)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// Partly(c: bool) { f := Acquire("a") // <- E0402; if c { f.Close() } }.
func TestR2_Partly(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Partly", []types.Param{k.param("c", types.Bool)}, types.Void, types.EmptyErrors))
	f := m.user("f", k.Res, false)
	m.acquire(f)
	yes, join := m.block(), m.block()
	m.branch(cp(m.local(1)), yes, join)
	m.at(yes)
	m.close(f)
	m.jump(join)
	m.at(join)
	m.end(f)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0402, m.b.Locals[f].Span)
}

// Over() { var f := Acquire("a"); f = Acquire("b") // <- E0402; f.Close() }.
func TestR2_Over(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Over", nil, types.Void, types.EmptyErrors))
	f := m.user("f", k.Res, true)
	m.acquire(f)
	bad := m.acquire(f)
	m.close(f)
	m.end(f)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0402, bad)
}

// lateBody is Late/TooLate: x: i32; if c { x = 1 } [else { x = 2 }]; return x.
func lateBody(t *testing.T, withElse bool) (*mb, mir.SpanID) {
	k := newKit(t)
	m := k.body(k.sig("Late", []types.Param{k.param("c", types.Bool)}, types.I32, types.EmptyErrors))
	x := m.user("x", types.I32, false)
	yes, no, join := m.block(), m.block(), m.block()
	m.branch(cp(m.local(1)), yes, no)
	m.at(yes)
	m.set(x, use(imm(types.I32, 1)))
	m.jump(join)
	m.at(no)
	if withElse {
		m.set(x, use(imm(types.I32, 2)))
	}
	m.jump(join)
	m.at(join)
	r := m.set(0, use(cp(m.local(x))))
	m.end(x)
	m.ret(mir.ExitNormal)
	return m, r
}

func TestR3_Late(t *testing.T) {
	m, _ := lateBody(t, true)
	m.expect(t, m.check(t))
}

// TooLate(c: bool): i32 { x: i32; if c { x = 1 }; return x // <- E0403 }:
// the absent entry on the else path joins as {U, I}.
func TestR3_TooLate(t *testing.T) {
	m, r := lateBody(t, false)
	m.expect(t, m.check(t), diag.E0403, r)
}

// F() { x := 1; Use(&x) }: the first binding finds the zero entry {U}, so
// no E0404.
func TestInitZeroEntryIsUnassigned(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	x := m.user("x", types.I32, false)
	m.set(x, use(imm(types.I32, 1)))
	m.useRef(m.local(x), types.I32)
	m.end(x)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// for i in 0..2 { y := i; Use(&y) }: y, purged at the end of one
// iteration, is {U} again at its binding in the next.
func TestLoopLocalRebindAfterPurge(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	i := m.user("i", types.Int, true)
	y := m.user("y", types.Int, false)
	cond := m.temp(types.Bool)
	m.set(i, use(imm(types.Int, 0)))
	header, body, exit := m.block(), m.block(), m.block()
	m.b.Loops = append(m.b.Loops, mir.LoopInfo{Header: header, Exit: exit, Holder: mir.NoLocal, Span: m.sp()})
	m.jump(header)
	m.at(header)
	m.set(cond, mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Lt, A: cp(m.local(i)), B: imm(types.Int, 2)})
	m.branch(cp(m.local(cond)), body, exit)
	m.at(body)
	m.set(y, use(cp(m.local(i))))
	m.useRef(m.local(y), types.Int)
	m.end(y)
	m.set(i, mir.Rvalue{Kind: mir.RvBinary, Bin: mir.AddWrap, A: cp(m.local(i)), B: imm(types.Int, 1)})
	m.jump(header)
	m.at(exit)
	m.end(cond, i)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// x := Acquire("a"); Take(x); x = Acquire("b") // <- E0404.
func TestE0404_SecondAssignAfterMove(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	x := m.user("x", k.Res, false)
	m.acquire(x)
	m.callv("Take", []types.TypeID{k.Res}, argMove(m.local(x)))
	bad := m.acquire(x)
	m.close(x)
	m.end(x)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0404, bad)
}

// n := 1; r := mut n // <- E0508; UseMut(r).
func TestE0508_MutOfNonVar(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	n := m.user("n", types.I32, false)
	r := m.user("r", k.tab.Mut(types.I32), false)
	m.set(n, use(imm(types.I32, 1)))
	bad := m.set(r, mutRef(m.local(n)))
	m.callv("UseMut", []types.TypeID{types.I32}, argMove(m.local(r)))
	m.end(r, n)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0508, bad)
}

// both declares struct Both { a: Res, b: Res }.
func (k *kit) both() types.TypeID {
	k.structItem(k.main, "Both", 0, false, false, field("a", k.Res), field("b", k.Res))
	return k.named("Both")
}

// Leak(p: Both): Res { return p.a // <- E0409 }.
func TestR4_Leak(t *testing.T) {
	k := newKit(t)
	bt := k.both()
	m := k.body(k.sig("Leak", []types.Param{k.param("p", bt)}, k.Res, types.EmptyErrors))
	bad := m.set(0, use(mv(m.pl(1, fld(0, k.Res)))))
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0409, bad)
}

// Release(p: Both) { Both(a, b) := p; a.Close(); b.Close() }: ok (lowered
// as t = move p; a = move t.a; b = move t.b).
func TestR4_DestructureConsumes(t *testing.T) {
	k := newKit(t)
	bt := k.both()
	m := k.body(k.sig("Release", []types.Param{k.param("p", bt)}, types.Void, types.EmptyErrors))
	tmp := m.temp(bt)
	a := m.user("a", k.Res, false)
	b := m.user("b", k.Res, false)
	m.set(tmp, use(mv(m.local(1))))
	m.set(a, use(mv(m.pl(tmp, fld(0, k.Res)))))
	m.set(b, use(mv(m.pl(tmp, fld(1, k.Res)))))
	m.close(a)
	m.close(b)
	m.end(b, a, tmp)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// Disjoint() { var p := Point(x: 1, y: 2); a := mut p.x; b := mut p.y;
// *a += 1; *b += 1 }: ok.
func TestR4_Fields(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Disjoint", nil, types.Void, types.EmptyErrors))
	p := m.user("p", k.Point, true)
	mi := k.tab.Mut(types.I32)
	a := m.user("a", mi, false)
	b := m.user("b", mi, false)
	m.set(p, m.agg(mir.AggStruct, k.item("Point"), k.Point, imm(types.I32, 1), imm(types.I32, 2)))
	m.set(a, mutRef(m.pl(p, fld(0, types.I32))))
	m.set(b, mutRef(m.pl(p, fld(1, types.I32))))
	m.assign(m.pl(a, deref(types.I32)), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Add, A: cp(m.pl(a, deref(types.I32))), B: imm(types.I32, 1)})
	m.assign(m.pl(b, deref(types.I32)), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Add, A: cp(m.pl(b, deref(types.I32))), B: imm(types.I32, 1)})
	m.end(b, a, p)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// Indexes(xs: mut []i32) { a := mut xs[0]; b := mut xs[1] // <- E0501;
// *a += *b }.
func TestR4_Indexes(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Indexes", []types.Param{k.param("xs", k.tab.MutSlice(types.I32))}, types.Void, types.EmptyErrors))
	mi := k.tab.Mut(types.I32)
	i0 := m.temp(types.Int)
	i1 := m.temp(types.Int)
	a := m.user("a", mi, false)
	b := m.user("b", mi, false)
	m.set(i0, use(imm(types.Int, 0)))
	m.set(a, mutRef(m.pl(1, index(i0, types.I32))))
	m.set(i1, use(imm(types.Int, 1)))
	bad := m.set(b, mutRef(m.pl(1, index(i1, types.I32))))
	m.assign(m.pl(a, deref(types.I32)), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Add,
		A: cp(m.pl(a, deref(types.I32))), B: cp(m.pl(b, deref(types.I32)))})
	m.end(b, a, i1, i0)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0501, bad)
}
