// Why: running real Mk programs through check and lower found borrow bugs
// that hand-built MIR had missed; each fix needs a test that holds it.
// What: regression tests of the length read of RvLen, a bounds check's
// length read, the argument slot restore, E0409's destination hygiene, the
// reborrow slot's path type, the direct write's carrier condition, `into`
// chains (the cookbook's AddChild) and `*q into p` of plain contents.
// How: each test's comment holds the Mk source its hand-built MIR stands for;
// boundsCheck adds lower's bounds check before an element borrow.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// boundsCheck appends lower's bounds check of index local i against the
// length of place p: n = Len(p); c = Lt(i, n); switch c [0: panic bounds],
// and continues in the in-bounds block.
func (m *mb) boundsCheck(p mir.Place, i mir.LocalID) {
	n := m.temp(types.Int)
	c := m.temp(types.Bool)
	m.set(n, mir.Rvalue{Kind: mir.RvLen, Place: p})
	m.set(c, mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Lt, A: cp(m.local(i)), B: cp(m.local(n))})
	bad, ok := m.block(), m.block()
	m.branch(cp(m.local(c)), ok, bad)
	m.at(bad)
	m.term(mir.Terminator{Kind: mir.TermPanic, Panic: types.PanicBounds})
	m.at(ok)
	m.end(c, n)
}

// Indexes(xs: mut []i32) { a := mut xs[0]; b := mut xs[1] // <- E0501
// ; *a += *b }, with lower's bounds checks: the second check's Len(xs)
// while a is live is no E0504.
func TestR4_IndexesBoundsCheckIsNoAccess(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Indexes", []types.Param{k.param("xs", k.tab.MutSlice(types.I32))}, types.Void, types.EmptyErrors))
	mi := k.tab.Mut(types.I32)
	i0, i1 := m.temp(types.Int), m.temp(types.Int)
	a, b := m.user("a", mi, false), m.user("b", mi, false)
	m.set(i0, use(imm(types.Int, 0)))
	m.boundsCheck(m.local(1), i0)
	m.set(a, mutRef(m.pl(1, index(i0, types.I32))))
	m.set(i1, use(imm(types.Int, 1)))
	m.boundsCheck(m.local(1), i1)
	bad := m.set(b, mutRef(m.pl(1, index(i1, types.I32))))
	m.assign(m.pl(a, deref(types.I32)), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Add,
		A: cp(m.pl(a, deref(types.I32))), B: cp(m.pl(b, deref(types.I32)))})
	m.end(b, a, i1, i0)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0501, bad)
}

// Inside(xs: mut []int) { for x in mut xs { *x += xs.Len() // <- E0504 } }:
// a user Len reads the length of xs.*, which the loop's holder loan covers;
// an element loan does not (TestR4_IndexesBoundsCheckIsNoAccess).
func TestR20_LenInsideLoopIsE0504(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Inside", []types.Param{k.param("xs", k.tab.MutSlice(types.I32))}, types.Void, types.EmptyErrors))
	var bad mir.SpanID
	m.mutLoop(1, func(x mir.LocalID) {
		n := m.temp(types.Int)
		bad = m.set(n, mir.Rvalue{Kind: mir.RvLen, Place: m.local(1)})
		m.end(n)
	})
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0504, bad)
}

// Lens(xs: mut []i32) { a := mut xs[0]; n := xs.Len(); *a = 1 }: ok, the
// length is not an element (rustc's Shallow(ArrayLength)).
func TestLenBesideElementLoan(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Lens", []types.Param{k.param("xs", k.tab.MutSlice(types.I32))}, types.Void, types.EmptyErrors))
	i0 := m.temp(types.Int)
	a := m.user("a", k.tab.Mut(types.I32), false)
	n := m.user("n", types.Int, false)
	m.set(i0, use(imm(types.Int, 0)))
	m.set(a, mutRef(m.pl(1, index(i0, types.I32))))
	m.set(n, mir.Rvalue{Kind: mir.RvLen, Place: m.local(1)})
	m.assign(m.pl(a, deref(types.I32)), use(imm(types.I32, 1)))
	m.end(n, a, i0)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// The guarded Caller with lower's argument slot:
// r := Acquire("a"); onerr r.Close(); try Guarded(a, list, r, Cond()) reads
// r into a slot t = move r before Cond() runs, and passes move t. The error
// edge restores r, not t, so t's scope end is no E0402 and the guard runs
// the Close: ok.
func TestSlotRestoredOnErrorEdge(t *testing.T) {
	k := newKit(t)
	g := k.guardedSig("Guarded")
	s := k.fn("Caller", []types.Param{k.param("a", k.tab.Ref(k.Alloc)), k.param("list", k.tab.Mut(k.named("HeapList", k.Res)))},
		types.Void, k.errs("Full", "OutOfMemory"))
	m := k.body(s)
	r := m.user("r", k.Res, false)
	m.acquire(r)
	m.b.Defers = append(m.b.Defers, mir.DeferInfo{Onerr: true, Span: m.sp()})
	slot := m.temp(k.Res)
	m.set(slot, use(mv(m.local(r))))
	c := m.temp(types.Bool)
	cn := m.block()
	m.call(k.item("Cond"), nil, nil, m.local(c), cn)
	m.at(cn)
	ok, bad := m.block(), m.block()
	e := m.temp(k.tab.ErrorSetType(g.Errors))
	d := m.temp(types.Void)
	m.callErr(g.Item, nil, []mir.Arg{argCopy(m.local(1)), argReborrow(m.local(2)), argMove(m.local(slot)), argCopy(m.local(c))},
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
	m.end(c, slot, r)
	m.ret(mir.ExitTry)
	m.at(ok)
	m.end(c, slot, r)
	m.okVoid()
	res := m.check(t)
	m.expect(t, res)
	if !res.Facts.Runs(guard) {
		t.Fatalf("OnerrRuns: the guard at block %d should run the onerr copy", guard)
	}
}

// for r in rs { Use(&r) } over rs: []Res: `r = copy rs[i]` is E0409,
// and r's scope end adds no E0402 (error hygiene): E0409 alone.
func TestE0409_ForCopyNonPlain(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Each", []types.Param{k.param("rs", k.tab.Slice(k.Res))}, types.Void, types.EmptyErrors))
	i := m.temp(types.Int)
	r := m.user("r", k.Res, false)
	m.set(i, use(imm(types.Int, 0)))
	bad := m.set(r, use(cp(m.pl(1, index(i, k.Res)))))
	m.useRef(m.local(r), k.Res)
	m.end(r, i)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0409, bad)
}

// Bump(x: mut i32) { UseMut(x); *x += 1 }: the reborrow slot's path x.* has
// the referent's type, i32, so the later plain copy of *x is no E0409.
func TestReborrowSlotPathHasReferentType(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("Bump", []types.Param{k.param("x", k.tab.Mut(types.I32))}, types.Void, types.EmptyErrors))
	m.callv("UseMut", []types.TypeID{types.I32}, argReborrow(m.local(1)))
	m.assign(m.pl(1, deref(types.I32)), mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Add,
		A: cp(m.pl(1, deref(types.I32))), B: imm(types.I32, 1)})
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// The direct write through a List[&i32] that holds v's loan:
// Local(v: &i32) { var out := Wrap(v); s := ItemsMut(mut out); s[0] = v }:
// ok. The view's element loan reaches v's referent node, an i32, which is no
// carrier and receives nothing (before the fix: E0604 "a loan of v into v").
func TestViewElementLoanNotWritten(t *testing.T) {
	k := newKit(t)
	ri := refI32(k)
	list := k.named("List", ri)
	wrap := k.fn("Wrap", []types.Param{k.param("v", ri)}, list, types.EmptyErrors)
	wrap.Flows.Sources = []types.Source{{Param: 1}}
	ms := k.tab.MutSlice(ri)
	im := k.fn("ItemsMut", []types.Param{k.param("self", k.tab.Mut(list))}, ms, types.EmptyErrors)
	im.Flows.Sources = []types.Source{{Param: 1}}
	m := k.body(k.sig("Local", []types.Param{k.param("v", ri)}, types.Void, types.EmptyErrors))
	out := m.user("out", list, true)
	n1 := m.block()
	m.call(wrap.Item, nil, []mir.Arg{argCopy(m.local(1))}, m.local(out), n1)
	m.at(n1)
	mo := m.temp(k.tab.Mut(list))
	m.set(mo, mutRef(m.local(out)))
	s := m.temp(ms)
	n2 := m.block()
	m.call(im.Item, nil, []mir.Arg{argMove(m.local(mo))}, m.local(s), n2)
	m.at(n2)
	i := m.temp(types.Int)
	m.set(i, use(imm(types.Int, 0)))
	m.assign(m.pl(s, index(i, ri)), use(cp(m.local(1))))
	m.end(i, s, mo, out)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// F(p: &Cell[str], a: &Cell[str] into p, s: str into a) { p.Store(s) }: ok,
// s reaches p through a (`into` chains, the cookbook's AddChild); without
// `a into p` the store is E0604.
func TestIntoChainDeclaresStore(t *testing.T) {
	for _, chain := range []bool{true, false} {
		k := newKit(t)
		k.lib()
		cs := k.tab.Ref(k.tab.Named(k.known.Cell, []types.GenericArg{types.TypeArg(types.Str)}))
		a := k.param("a", cs)
		if chain {
			a.Into = 1
		}
		sv := k.param("s", types.Str)
		sv.Into = 2
		m := k.body(k.sig("F", []types.Param{k.param("p", cs), a, sv}, types.Void, types.EmptyErrors))
		bad := m.callv("Cell.Store", []types.TypeID{types.Str}, argCopy(m.local(1)), argCopy(m.local(3)))
		m.ret(mir.ExitNormal)
		if chain {
			m.expect(t, m.check(t))
		} else {
			m.expect(t, m.check(t), diag.E0604, bad)
		}
	}
}

// G(a: &Cell[str], s: str into a): str from a { return s }: ok, the result
// from a covers what was declared into a (the caller computes it from a's
// argument after the call's stores); with no `into`, E0602.
func TestFromCoversDeclaredInto(t *testing.T) {
	for _, into := range []bool{true, false} {
		k := newKit(t)
		k.lib()
		cs := k.tab.Ref(k.tab.Named(k.known.Cell, []types.GenericArg{types.TypeArg(types.Str)}))
		sv := k.param("s", types.Str)
		if into {
			sv.Into = 1
		}
		s := k.sig("G", []types.Param{k.param("a", cs), sv}, types.Str, types.EmptyErrors)
		s.Flows.Sources = []types.Source{{Param: 1}}
		s.Flows.Declared = true
		m := k.body(s)
		bad := m.set(0, use(cp(m.local(2))))
		m.ret(mir.ExitNormal)
		if into {
			m.expect(t, m.check(t))
		} else {
			m.expect(t, m.check(t), diag.E0602, bad)
		}
	}
}

// Grow(a: &Arena, buf: []u8) { next := mut a.Slice(n); CopyFrom[u8](next,
// buf) }: `*src into self` of a []u8 stores bytes, which hold no loan, so the
// store into the arena's node is no E0604 (std.fs ReadBytes); at a generic
// T = &i32 the store stands.
func TestStoreOfPlainContentsAtCall(t *testing.T) {
	for _, elem := range []types.TypeID{types.U8, types.NoType} {
		k := newKit(t)
		p0 := k.tab.Param(0)
		g := []types.GenericParam{{Name: k.in.Intern("T")}}
		cf := k.declare("CopyFrom", types.ItemKey("main", "CopyFrom"), types.NoReceiver, g,
			[]types.Param{k.param("self", k.tab.MutSlice(p0)), k.param("src", k.tab.Slice(p0))}, types.Void, types.EmptyErrors)
		cf.Flows.Stores = []types.Store{{From: 2, Into: 1}}
		e := elem
		if e == types.NoType {
			e = refI32(k)
		}
		sl := k.fn("Fresh", []types.Param{k.param("a", k.tab.Ref(k.Arena))}, k.tab.MutSlice(e), types.EmptyErrors)
		sl.Flows.Sources = []types.Source{{Param: 1}}
		m := k.body(k.sig("Grow", []types.Param{k.param("a", k.tab.Ref(k.Arena)), k.param("buf", k.tab.Slice(e))}, types.Void, types.EmptyErrors))
		next := m.user("next", k.tab.MutSlice(e), false)
		n1 := m.block()
		m.call(sl.Item, nil, []mir.Arg{argCopy(m.local(1))}, m.local(next), n1)
		m.at(n1)
		bad := m.callv("CopyFrom", []types.TypeID{e}, argReborrow(m.local(next)), argCopy(m.local(2)))
		m.end(next)
		m.ret(mir.ExitNormal)
		if elem == types.U8 {
			m.expect(t, m.check(t))
		} else {
			m.expect(t, m.check(t), diag.E0604, bad)
		}
	}
}

// With lower's bounds checks: Twice(xs: mut []i32) { a := mut xs[0..];
// b := mut xs[0..] // <- E0501 }: the second slice's bounds check reads the
// length of xs.*, which a's loan covers, but a bounds check is part of the
// borrow it guards, so it adds no E0504 at the line.
func TestSubsliceBoundsCheckAddsNothing(t *testing.T) {
	k := newKit(t)
	ms := k.tab.MutSlice(types.I32)
	m := k.body(k.sig("Twice", []types.Param{k.param("xs", ms)}, types.Void, types.EmptyErrors))
	lo := m.temp(types.Int)
	a, b := m.user("a", ms, false), m.user("b", ms, false)
	m.set(lo, use(imm(types.Int, 0)))
	m.boundsCheck(m.local(1), lo)
	m.set(a, mir.Rvalue{Kind: mir.RvSlice, Borrow: mir.Exclusive, Place: m.local(1), HasLo: true, A: cp(m.local(lo))})
	m.boundsCheck(m.local(1), lo)
	bad := m.set(b, mir.Rvalue{Kind: mir.RvSlice, Borrow: mir.Exclusive, Place: m.local(1), HasLo: true, A: cp(m.local(lo))})
	m.useRef(m.local(a), ms)
	m.useRef(m.local(b), ms)
	m.end(b, a, lo)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0501, bad)
}

// Both(ys: mut []i32, xs: mut []i32) { for y in mut ys { xs[ys.Len()] = 1 //
// <- E0504 } }: the user Len of ys sits in the block of xs's bounds check, as
// the comparison's left operand; only the right operand's length read is the
// bounds check's, so the loop holder's conflict on ys stays.
func TestR20_LenInIndexInsideLoopIsE0504(t *testing.T) {
	k := newKit(t)
	ms := k.tab.MutSlice(types.I32)
	m := k.body(k.sig("Both", []types.Param{k.param("ys", ms), k.param("xs", ms)}, types.Void, types.EmptyErrors))
	var bad mir.SpanID
	m.mutLoop(1, func(y mir.LocalID) {
		n := m.temp(types.Int)
		bad = m.set(n, mir.Rvalue{Kind: mir.RvLen, Place: m.local(1)})
		m.boundsCheck(m.local(2), n)
		m.assign(m.pl(2, index(n, types.I32)), use(imm(types.I32, 1)))
		m.end(n)
	})
	m.ret(mir.ExitNormal)
	r := m.check(t)
	m.expect(t, r, diag.E0504, bad)
	t.Log(r.Diags[0].Message)
}
