// Why: the core loan rules decide most borrow errors users see.
// What: tests of the borrow rows, access checks, kill and purge, and scope
// ends: shared and mut conflicts, last use, replace, reborrows, the result
// boundary and pinned values.
// How: one small hand-built MIR body per rule, checked for its codes.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

func refI32(k *kit) types.TypeID { return k.tab.Ref(types.I32) }

// Shared() { var n := 1; a := &n; b := &n; Use(a); Use(b); n = 2 }: ok.
func TestR5_Shared(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Shared", nil, types.Void, types.EmptyErrors))
	n := m.user("n", types.I32, true)
	a := m.user("a", refI32(k), false)
	b := m.user("b", refI32(k), false)
	m.set(n, use(imm(types.I32, 1)))
	m.set(a, ref(m.local(n)))
	m.set(b, ref(m.local(n)))
	m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(a)))
	m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(b)))
	m.set(n, use(imm(types.I32, 2)))
	m.end(b, a, n)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// Frozen() { var n := 1; a := &n; n = 2 // <- E0502; Use(a) }.
func TestR5_ConflictTable(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Frozen", nil, types.Void, types.EmptyErrors))
	n := m.user("n", types.I32, true)
	a := m.user("a", refI32(k), false)
	m.set(n, use(imm(types.I32, 1)))
	m.set(a, ref(m.local(n)))
	bad := m.set(n, use(imm(types.I32, 2)))
	m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(a)))
	m.end(a, n)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0502, bad)
}

// LastUseEndsIt() { var p := MakePoint(); a := &p.x; Use(a); p.x = 2 }: ok.
func TestR6_LastUseEndsIt(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("LastUseEndsIt", nil, types.Void, types.EmptyErrors))
	p := m.user("p", k.Point, true)
	a := m.user("a", refI32(k), false)
	next := m.block()
	m.call(k.item("MakePoint"), nil, nil, m.local(p), next)
	m.at(next)
	m.set(a, ref(m.pl(p, fld(0, types.I32))))
	m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(a)))
	m.assign(m.pl(p, fld(0, types.I32)), use(imm(types.I32, 2)))
	m.end(a, p)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// DeferKeepsIt() { var n := 1; a := &n; defer Use(a); n = 2 // <- E0502 }.
func TestR6_DeferKeepsIt(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("DeferKeepsIt", nil, types.Void, types.EmptyErrors))
	n := m.user("n", types.I32, true)
	a := m.user("a", refI32(k), false)
	m.set(n, use(imm(types.I32, 1)))
	m.set(a, ref(m.local(n)))
	m.b.Defers = append(m.b.Defers, mir.DeferInfo{Span: m.sp()})
	bad := m.set(n, use(imm(types.I32, 2)))
	d := m.b.AddBlock(mir.OriginDefer, 0)
	m.jump(d)
	m.at(d)
	m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(a)))
	m.end(a, n)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0502, bad)
}

// cellNew declares Cell.New[T](x: T): Cell[T], automatic source H(x).
func (k *kit) cellNew() *types.Signature {
	p0 := k.tab.Param(0)
	s := k.declare("Cell.New", types.MemberKey("std.core", "Cell", "New"), types.NoReceiver,
		[]types.GenericParam{{Name: k.in.Intern("T")}}, []types.Param{k.param("x", p0)},
		k.tab.Named(k.known.Cell, []types.GenericArg{types.TypeArg(p0)}), types.EmptyErrors)
	s.Flows.Sources = []types.Source{{Param: 1}}
	return s
}

// Hole: Both(a: &Cell[&i32], b: &Cell[&i32], v: &i32 into a): &i32 from b;
// long: i32 = 1; c := Cell.New(&long); var out := &long; if Cond() { short:
// i32 = 2; out = Both(&c, &c, &short) // <- E0505 }; Use(out).
func TestR6_Hole(t *testing.T) {
	k := newKit(t)
	k.cellNew()
	ri := refI32(k)
	cellT := k.tab.Named(k.known.Cell, []types.GenericArg{types.TypeArg(ri)})
	rc := k.tab.Ref(cellT)
	both := k.fn("Both", []types.Param{k.param("a", rc), k.param("b", rc), {Name: k.in.Intern("v"), Type: ri, Into: 1}}, ri, types.EmptyErrors)
	both.Flows.Sources = []types.Source{{Param: 2}}
	both.Flows.Declared = true

	m := k.body(k.sig("Hole", nil, types.Void, types.EmptyErrors))
	long := m.user("long", types.I32, false)
	c := m.user("c", cellT, false)
	out := m.user("out", ri, true)
	m.set(long, use(imm(types.I32, 1)))
	t1 := m.temp(ri)
	m.set(t1, ref(m.local(long)))
	next := m.block()
	m.call(k.item("Cell.New"), []types.TypeID{ri}, []mir.Arg{argCopy(m.local(t1))}, m.local(c), next)
	m.at(next)
	m.end(t1)
	m.set(out, ref(m.local(long)))
	cond := m.temp(types.Bool)
	condNext := m.block()
	m.call(k.item("Cond"), nil, nil, m.local(cond), condNext)
	m.at(condNext)
	yes, join := m.block(), m.block()
	m.branch(cp(m.local(cond)), yes, join)
	m.at(yes)
	short := m.user("short", types.I32, false)
	m.set(short, use(imm(types.I32, 2)))
	a1, a2, a3 := m.temp(rc), m.temp(rc), m.temp(ri)
	m.set(a1, ref(m.local(c)))
	m.set(a2, ref(m.local(c)))
	bad := m.set(a3, ref(m.local(short)))
	after := m.block()
	m.call(both.Item, nil, []mir.Arg{argCopy(m.local(a1)), argCopy(m.local(a2)), argCopy(m.local(a3))}, m.local(out), after)
	m.at(after)
	m.end(a3, a2, a1, short)
	m.jump(join)
	m.at(join)
	m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(out)))
	m.end(cond, out, c, long)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0505, bad)
}

// Replace() { var n := 1; var m := 2; var r := &n; r = &m; n = 3; Use(r) }: ok.
func TestR7_Replace(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Replace", nil, types.Void, types.EmptyErrors))
	n := m.user("n", types.I32, true)
	mm := m.user("m", types.I32, true)
	r := m.user("r", refI32(k), true)
	m.set(n, use(imm(types.I32, 1)))
	m.set(mm, use(imm(types.I32, 2)))
	m.set(r, ref(m.local(n)))
	m.set(r, ref(m.local(mm)))
	m.set(n, use(imm(types.I32, 3)))
	m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(r)))
	m.end(r, mm, n)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// Held: struct Pair { a: &i32 }; var n: i32 = 1; p := Pair(a: &n); n = 2
// // <- E0502; Use(&p).
func TestR7_Held(t *testing.T) {
	k := newKit(t)
	k.structItem(k.main, "Pair", 0, false, false, field("a", refI32(k)))
	pair := k.named("Pair")
	m := k.body(k.sig("Held", nil, types.Void, types.EmptyErrors))
	n := m.user("n", types.I32, true)
	p := m.user("p", pair, false)
	tr := m.temp(refI32(k))
	m.set(n, use(imm(types.I32, 1)))
	m.set(tr, ref(m.local(n)))
	m.set(p, m.agg(mir.AggStruct, k.item("Pair"), pair, cp(m.local(tr))))
	m.end(tr)
	bad := m.set(n, use(imm(types.I32, 2)))
	m.useRef(m.local(p), pair)
	m.end(p, n)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0502, bad)
}

// ReadOut(h: &Holder): str { return h.name }: ok; the automatic source is
// B(h) (name is reached through a shared reference).
func TestR8_ReadOut(t *testing.T) {
	k := newKit(t)
	k.structItem(k.main, "Holder", 0, false, false, field("name", types.Str))
	holder := k.named("Holder")
	s := k.sig("ReadOut", []types.Param{k.param("h", k.tab.Ref(holder))}, types.Str, types.EmptyErrors)
	s.Flows.Sources = []types.Source{{Param: 1, Behind: true}}
	m := k.body(s)
	m.set(0, use(cp(m.pl(1, deref(holder), fld(0, types.Str)))))
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// Twice(r: mut i32) { UseMut(r); UseMut(r) }: ok, each a reborrow.
func TestR12_Twice(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Twice", []types.Param{k.param("r", k.tab.Mut(types.I32))}, types.Void, types.EmptyErrors))
	m.callv("UseMut", []types.TypeID{types.I32}, argReborrow(m.local(1)))
	m.callv("UseMut", []types.TypeID{types.I32}, argReborrow(m.local(1)))
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// Stored(r: mut i32) { s := r; UseMut(r) // <- E0401; UseMut(s) }.
func TestR12_Stored(t *testing.T) {
	k := newKit(t)
	mi := k.tab.Mut(types.I32)
	m := k.body(k.sig("Stored", []types.Param{k.param("r", mi)}, types.Void, types.EmptyErrors))
	s := m.user("s", mi, false)
	m.set(s, use(mv(m.local(1))))
	bad := m.callv("UseMut", []types.TypeID{types.I32}, argReborrow(m.local(1)))
	m.callv("UseMut", []types.TypeID{types.I32}, argReborrow(m.local(s)))
	m.end(s)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0401, bad)
}

// Bad(s: &Sink) { s.out[0] = 1 // <- E0507 } with struct Sink { out: mut []u8 }.
func TestR12_Bad(t *testing.T) {
	k := newKit(t)
	ms := k.tab.MutSlice(types.U8)
	k.structItem(k.main, "Sink", 0, false, false, field("out", ms))
	sink := k.named("Sink")
	m := k.body(k.sig("Bad", []types.Param{k.param("s", k.tab.Ref(sink))}, types.Void, types.EmptyErrors))
	i := m.temp(types.Int)
	m.set(i, use(imm(types.Int, 0)))
	bad := m.assign(m.pl(1, deref(sink), fld(0, ms), index(i, types.U8)), use(imm(types.U8, 1)))
	m.end(i)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0507, bad)
}

// Same() { var n := 1; Exchange(mut n, mut n) // <- E0501 }: the second
// borrow conflicts with the first, which the call still uses.
func TestR13_Same(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Same", nil, types.Void, types.EmptyErrors))
	mi := k.tab.Mut(types.I32)
	n := m.user("n", types.I32, true)
	m.set(n, use(imm(types.I32, 1)))
	t1, t2 := m.temp(mi), m.temp(mi)
	m.set(t1, mutRef(m.local(n)))
	bad := m.set(t2, mutRef(m.local(n)))
	m.callv("Exchange", []types.TypeID{types.I32}, argMove(m.local(t1)), argMove(m.local(t2)))
	m.end(t2, t1, n)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0501, bad)
}

// Passes() { Use(&MakePoint()) }: ok.
func TestR14_Passes(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Passes", nil, types.Void, types.EmptyErrors))
	tp := m.temp(k.Point)
	next := m.block()
	m.call(k.item("MakePoint"), nil, nil, m.local(tp), next)
	m.at(next)
	m.useRef(m.local(tp), k.Point)
	m.end(tp)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// TooLong() { r := &MakePoint() // <- E0506; Use(r) }: the temporary ends at
// its statement while r still holds its loan.
func TestR14_TooLong(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("TooLong", nil, types.Void, types.EmptyErrors))
	rp := k.tab.Ref(k.Point)
	r := m.user("r", rp, false)
	tp := m.temp(k.Point)
	next := m.block()
	m.call(k.item("MakePoint"), nil, nil, m.local(tp), next)
	m.at(next)
	bad := m.set(r, ref(m.local(tp)))
	m.end(tp)
	m.callv("Use", []types.TypeID{k.Point}, argCopy(m.local(r)))
	m.end(r)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0506, bad)
}

// Pinned(a: &i32, b: &i32) { var r := a; q := &r; r = b // <- E0502; Use(q) }.
func TestR15_Pinned(t *testing.T) {
	k := newKit(t)
	ri := refI32(k)
	m := k.body(k.sig("Pinned", []types.Param{k.param("a", ri), k.param("b", ri)}, types.Void, types.EmptyErrors))
	r := m.user("r", ri, true)
	q := m.user("q", k.tab.Ref(ri), false)
	m.set(r, use(cp(m.local(1))))
	m.set(q, ref(m.local(r)))
	bad := m.set(r, use(cp(m.local(2))))
	m.callv("Use", []types.TypeID{ri}, argCopy(m.local(q)))
	m.end(q, r)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0502, bad)
}

// Reads(): &i32 { return &Limit } with const Limit: i32 = 10: ok, a static
// loan passes the boundary.
func TestR16_Reads(t *testing.T) {
	k := newKit(t)
	lim := k.items.AddItem(&types.Item{Module: k.main.ID, Name: k.in.Intern("Limit"), Kind: types.KindConst,
		Key: types.ItemKey("main", "Limit"), Decl: &types.ConstDecl{Type: types.I32}})
	m := k.body(k.sig("Reads", nil, refI32(k), types.EmptyErrors))
	m.set(0, mir.Rvalue{Kind: mir.RvGlobalRef, Item: lim})
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}
