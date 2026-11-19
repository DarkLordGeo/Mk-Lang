// Why: calls are where loans cross function boundaries: arguments move or
// borrow, results carry loans, and into clauses store them.
// What: tests of calls, stores, the boundary check and E0411, on hand-built
// MIR, with the fallible library surface lib, errs, fail, okVoid and tryPush.
// How: each test builds a small body (Guarded, Eager, Pick, Add, Mix...) and
// checks the codes Check reports.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// lib adds the fallible library surface the guarded and store cases need: error
// OutOfMemory, HeapList[T].Push(mut self, a: &Alloc, x: T into self):
// OutOfMemory!void, and Cell[T].Store(&self, x: T into self).
func (k *kit) lib() {
	if _, ok := k.byNm["OutOfMemory"]; ok {
		return
	}
	k.errorItem("OutOfMemory")
	p0 := k.tab.Param(0)
	g := []types.GenericParam{{Name: k.in.Intern("T")}}
	hl := k.named("HeapList", p0)
	push := k.declare("HeapList.Push", types.MemberKey("std.core", "HeapList", "Push"), types.ByMut, g,
		[]types.Param{{Name: k.in.Intern("self"), Type: k.tab.Mut(hl)}, k.param("a", k.tab.Ref(k.Alloc)),
			{Name: k.in.Intern("x"), Type: p0, Into: 1}}, types.Void, k.errs("OutOfMemory"))
	_ = push
	cell := k.tab.Named(k.known.Cell, []types.GenericArg{types.TypeArg(p0)})
	k.declare("Cell.Store", types.MemberKey("std.core", "Cell", "Store"), types.ByShared, g,
		[]types.Param{{Name: k.in.Intern("self"), Type: k.tab.Ref(cell)}, {Name: k.in.Intern("x"), Type: p0, Into: 1}},
		types.Void, types.EmptyErrors)
	ld := k.declare("Cell.Load", types.MemberKey("std.core", "Cell", "Load"), types.ByShared, g,
		[]types.Param{{Name: k.in.Intern("self"), Type: k.tab.Ref(cell)}}, p0, types.EmptyErrors)
	ld.Flows.Sources = []types.Source{{Param: 1, Behind: true}}
}

// errs is the error set of the named errors.
func (k *kit) errs(names ...string) types.ErrorSetID {
	var ms []types.ErrorMember
	for _, n := range names {
		if _, ok := k.byNm[n]; !ok {
			k.errorItem(n)
		}
		ms = append(ms, types.ErrorMember{Item: k.item(n)})
	}
	return k.tab.ErrorSet(ms)
}

// fail appends `fail E` with error item name: _0 = err(E), return ExitFail.
func (m *mb) fail(name string) mir.SpanID {
	es := m.k.errs(name)
	t := m.temp(m.k.tab.ErrorSetType(es))
	m.set(t, m.agg(mir.AggError, m.k.item(name), m.k.tab.ErrorSetType(es)))
	m.set(0, m.agg(mir.AggErr, types.NoItem, m.b.Locals[0].Type, cp(m.local(t))))
	m.end(t)
	return m.ret(mir.ExitFail)
}

// okVoid appends the success return of an E!void function.
func (m *mb) okVoid() mir.SpanID {
	m.set(0, m.agg(mir.AggOk, types.NoItem, m.b.Locals[0].Type))
	return m.ret(mir.ExitNormal)
}

// tryPush appends `try list.Push(a, x)` for list a mut HeapList[elem] local
// or parameter (reborrowed), a an &Alloc, x moved; the error edge returns
// ExitTry after the cleanup the caller builds with onErr. It returns the
// call's span and leaves the builder on the success block.
func (m *mb) tryPush(elem types.TypeID, list, a, x mir.LocalID, onErr func()) mir.SpanID {
	k := m.k
	ok, bad := m.block(), m.block()
	e := m.temp(k.tab.ErrorSetType(k.errs("OutOfMemory")))
	d := m.temp(types.Void)
	s := m.callErr(k.item("HeapList.Push"), []types.TypeID{elem},
		[]mir.Arg{argReborrow(m.local(list)), argCopy(m.local(a)), argMove(m.local(x))}, m.local(d), ok, mir.EdgeTry, bad, e)
	m.at(bad)
	m.set(0, m.agg(mir.AggErr, types.NoItem, m.b.Locals[0].Type, cp(m.local(e))))
	if onErr != nil {
		onErr()
	}
	m.ret(mir.ExitTry)
	m.at(ok)
	return s
}

// guardedSig is Guarded(a: &Alloc, list: mut HeapList[Res], r: Res, room:
// bool): Full|OutOfMemory!void.
func (k *kit) guardedSig(name string) *types.Signature {
	k.lib()
	return k.fn(name, []types.Param{k.param("a", k.tab.Ref(k.Alloc)), k.param("list", k.tab.Mut(k.named("HeapList", k.Res))),
		k.param("r", k.Res), k.param("room", types.Bool)}, types.Void, k.errs("Full", "OutOfMemory"))
}

// Guarded: if !room { fail Full }; try list.Push(a, r): ok. The call's own
// move of r is restored on its error edge, so the ExitTry return
// sees r unchanged and owned.
func TestR2_Guarded(t *testing.T) {
	k := newKit(t)
	s := k.guardedSig("Guarded")
	m := k.body(s)
	n := m.temp(types.Bool)
	m.set(n, mir.Rvalue{Kind: mir.RvUnary, Un: mir.Not, A: cp(m.local(4))})
	fail, push := m.block(), m.block()
	m.branch(cp(m.local(n)), fail, push)
	m.at(fail)
	m.end(n)
	m.fail("Full")
	m.at(push)
	m.end(n)
	m.tryPush(k.Res, 2, 1, 3, nil)
	m.okVoid()
	m.expect(t, m.check(t))
}

// Eager: try list.Push(a, r) // <- E0411; if !room { fail Full }.
func TestR2_Eager(t *testing.T) {
	k := newKit(t)
	s := k.guardedSig("Eager")
	m := k.body(s)
	bad := m.tryPush(k.Res, 2, 1, 3, nil)
	n := m.temp(types.Bool)
	m.set(n, mir.Rvalue{Kind: mir.RvUnary, Un: mir.Not, A: cp(m.local(4))})
	fail, done := m.block(), m.block()
	m.branch(cp(m.local(n)), fail, done)
	m.at(fail)
	m.end(n)
	m.fail("Full")
	m.at(done)
	m.end(n)
	m.okVoid()
	m.expect(t, m.check(t), diag.E0411, bad)
}

// Caller(a: &Alloc, list: mut HeapList[Res]): Full|OutOfMemory!void { r :=
// Acquire("a"); onerr r.Close(); try Guarded(a, list, r, Cond()) }: ok. On
// the error edge r is still owned, the onerr guard runs the Close.
func TestR2_GuardedCaller(t *testing.T) {
	k := newKit(t)
	g := k.guardedSig("Guarded")
	s := k.fn("Caller", []types.Param{k.param("a", k.tab.Ref(k.Alloc)), k.param("list", k.tab.Mut(k.named("HeapList", k.Res)))},
		types.Void, k.errs("Full", "OutOfMemory"))
	m := k.body(s)
	r := m.user("r", k.Res, false)
	m.acquire(r)
	m.b.Defers = append(m.b.Defers, mir.DeferInfo{Onerr: true, Span: m.sp()})
	c := m.temp(types.Bool)
	cn := m.block()
	m.call(k.item("Cond"), nil, nil, m.local(c), cn)
	m.at(cn)
	ok, bad := m.block(), m.block()
	e := m.temp(k.tab.ErrorSetType(g.Errors))
	d := m.temp(types.Void)
	m.callErr(g.Item, nil, []mir.Arg{argCopy(m.local(1)), argReborrow(m.local(2)), argMove(m.local(r)), argCopy(m.local(c))},
		m.local(d), ok, mir.EdgeTry, bad, e)
	m.at(bad)
	m.set(0, m.agg(mir.AggErr, types.NoItem, m.b.Locals[0].Type, cp(m.local(e))))
	closeBlk, after := m.b.AddBlock(mir.OriginOnerr, 0), m.block()
	m.term(mir.Terminator{Kind: mir.TermOnerrGuard, Value: r, Yes: closeBlk, No: after})
	guard := m.cur
	m.at(closeBlk)
	m.close(r)
	m.jump(after)
	m.at(after)
	m.end(c, r)
	m.ret(mir.ExitTry)
	m.at(ok)
	m.end(c, r)
	m.okVoid()
	res := m.check(t)
	m.expect(t, res)
	if !res.Facts.Runs(guard) {
		t.Fatalf("OnerrRuns: the guard at block %d should run the onerr copy", guard)
	}
}

// Pick(a: str, b: str): str from a { return b // <- E0602 }.
func TestR9_Pick(t *testing.T) {
	k := newKit(t)
	s := k.sig("Pick", []types.Param{k.param("a", types.Str), k.param("b", types.Str)}, types.Str, types.EmptyErrors)
	s.Flows.Sources = []types.Source{{Param: 1}}
	s.Flows.Declared = true
	m := k.body(s)
	bad := m.set(0, use(cp(m.local(2))))
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0602, bad)
}

// Dangling(): &i32 { n: i32 = 1; return &n // <- E0603 }.
func TestR9_Dangling(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Dangling", nil, refI32(k), types.EmptyErrors))
	n := m.user("n", types.I32, false)
	m.set(n, use(imm(types.I32, 1)))
	bad := m.set(0, ref(m.local(n)))
	m.end(n)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0603, bad)
}

// First(xs: []i32): &i32 { return &xs[0] }: ok (automatic H(xs): the
// result's loan on xs.*[_] has no target and holds xs.outer).
func TestR9_First(t *testing.T) {
	k := newKit(t)
	s := k.sig("First", []types.Param{k.param("xs", k.tab.Slice(types.I32))}, refI32(k), types.EmptyErrors)
	s.Flows.Sources = []types.Source{{Param: 1}}
	m := k.body(s)
	i := m.temp(types.Int)
	m.set(i, use(imm(types.Int, 0)))
	m.set(0, ref(m.pl(1, index(i, types.I32))))
	m.end(i)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// The boundary check is skipped when check marked the signature (E0601).
func TestBoundarySkippedAfterE0601(t *testing.T) {
	k := newKit(t)
	s := k.sig("Longer", []types.Param{k.param("a", types.Str), k.param("b", types.Str)}, types.Str, types.EmptyErrors)
	s.Flows.Sources = []types.Source{{Param: 1}}
	s.Flows.Ambiguous = true
	m := k.body(s)
	m.set(0, use(cp(m.local(2))))
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// Put(c: &Cell[&i32], v: &i32 into c) { c.Store(v) }: ok; and without the
// into, E0604 at the call.
func TestR11_Add(t *testing.T) {
	for _, into := range []bool{true, false} {
		k := newKit(t)
		k.lib()
		ri := refI32(k)
		cellT := k.tab.Named(k.known.Cell, []types.GenericArg{types.TypeArg(ri)})
		v := k.param("v", ri)
		if into {
			v.Into = 1
		}
		s := k.sig("Put", []types.Param{k.param("c", k.tab.Ref(cellT)), v}, types.Void, types.EmptyErrors)
		m := k.body(s)
		call := m.callv("Cell.Store", []types.TypeID{ri}, argCopy(m.local(1)), argCopy(m.local(2)))
		m.ret(mir.ExitNormal)
		if into {
			m.expect(t, m.check(t))
		} else {
			m.expect(t, m.check(t), diag.E0604, call)
		}
	}
}

// Deep.Mix(mut self) *self into self { *self.b = self.a }: ok; without the
// clause, E0604 at the write.
func TestR11_DeepMixClause(t *testing.T) {
	for _, clause := range []bool{true, false} {
		k := newKit(t)
		ri := refI32(k)
		mri := k.tab.Mut(ri)
		k.structItem(k.main, "Deep", 0, false, false, field("a", ri), field("b", mri))
		deep := k.named("Deep")
		s := k.method("Deep", "Mix", types.ByMut, deep, nil, types.Void, types.EmptyErrors)
		if clause {
			s.Flows.Stores = []types.Store{{From: 1, Into: 1}}
		}
		m := k.body(s)
		w := m.assign(m.pl(1, deref(deep), fld(1, mri), deref(ri)), use(cp(m.pl(1, deref(deep), fld(0, ri)))))
		m.ret(mir.ExitNormal)
		if clause {
			m.expect(t, m.check(t))
		} else {
			m.expect(t, m.check(t), diag.E0604, w)
		}
	}
}

// Store(c: &Cell[?&i32], p: &Point into c) { c.Store(some(&p.x)) }; Leak() {
// cell := Cell[?&i32].New(none); if Cond() { pt := Point(x: 1, y: 2);
// Store(&cell, &pt) // <- E0505 }; if r := cell.Load() { Use(r) } }. The
// Load is modelled as Use(&cell), which keeps cell live.
func TestR11_StoreLeak(t *testing.T) {
	k := newKit(t)
	k.lib()
	k.cellNew()
	ori := k.tab.Optional(refI32(k))
	cellT := k.tab.Named(k.known.Cell, []types.GenericArg{types.TypeArg(ori)})
	st := k.fn("Store", []types.Param{k.param("c", k.tab.Ref(cellT)), {Name: k.in.Intern("p"), Type: k.tab.Ref(k.Point), Into: 1}},
		types.Void, types.EmptyErrors)
	m := k.body(k.sig("Leak", nil, types.Void, types.EmptyErrors))
	cell := m.user("cell", cellT, false)
	none := m.temp(ori)
	m.set(none, m.agg(mir.AggNone, types.NoItem, ori))
	nx := m.block()
	m.call(k.item("Cell.New"), []types.TypeID{ori}, []mir.Arg{argCopy(m.local(none))}, m.local(cell), nx)
	m.at(nx)
	m.end(none)
	c := m.temp(types.Bool)
	cn := m.block()
	m.call(k.item("Cond"), nil, nil, m.local(c), cn)
	m.at(cn)
	yes, join := m.block(), m.block()
	m.branch(cp(m.local(c)), yes, join)
	m.at(yes)
	pt := m.user("pt", k.Point, false)
	m.set(pt, m.agg(mir.AggStruct, k.item("Point"), k.Point, imm(types.I32, 1), imm(types.I32, 2)))
	a1, a2 := m.temp(k.tab.Ref(cellT)), m.temp(k.tab.Ref(k.Point))
	m.set(a1, ref(m.local(cell)))
	bad := m.set(a2, ref(m.local(pt)))
	m.callv("Store", nil, argCopy(m.local(a1)), argCopy(m.local(a2)))
	_ = st
	m.end(a2, a1, pt)
	m.jump(join)
	m.at(join)
	m.useRef(m.local(cell), cellT)
	m.end(c, cell)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0505, bad)
}

// Keep(q: Point, out: mut &i32) { *out = &q.x // <- E0505 }: a loan on a
// by-value parameter's own storage stored into an abstract node.
func TestParamStorageIntoAbstractIsE0505(t *testing.T) {
	k := newKit(t)
	ri := refI32(k)
	m := k.body(k.sig("Keep", []types.Param{k.param("q", k.Point), k.param("out", k.tab.Mut(ri))}, types.Void, types.EmptyErrors))
	r := m.temp(ri)
	bad := m.set(r, ref(m.pl(1, fld(0, types.I32))))
	m.assign(m.pl(2, deref(ri)), use(cp(m.local(r))))
	m.end(r)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0505, bad)
}

// Keep2(out: mut &Point) { *out = &MakePoint() // <- E0506 }.
func TestTemporaryIntoAbstractIsE0506(t *testing.T) {
	k := newKit(t)
	rp := k.tab.Ref(k.Point)
	m := k.body(k.sig("Keep2", []types.Param{k.param("out", k.tab.Mut(rp))}, types.Void, types.EmptyErrors))
	tp := m.temp(k.Point)
	nx := m.block()
	m.call(k.item("MakePoint"), nil, nil, m.local(tp), nx)
	m.at(nx)
	r := m.temp(rp)
	bad := m.set(r, ref(m.local(tp)))
	m.assign(m.pl(1, deref(rp)), use(cp(m.local(r))))
	m.end(r, tp)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0506, bad)
}
