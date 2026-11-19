// Why: rules that interact (boundary checks, spawns, joins, field writes,
// fn values) need tests of their own beyond the one-rule cases.
// What: tests of boundary suppression, spawns with the callee's own into,
// ends with a live holder, `if T is`, linear temporaries on a try edge,
// field writes, {U, M} joins, Exchange(r, r), from-path coverage and flows
// through fn values.
// How: one hand-built MIR body per shape, checked for its codes.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// A failed boundary check reports alone; the E0508 inside the
// return statement's span is dropped.
func TestFailedBoundaryReportsAlone(t *testing.T) {
	k := newKit(t)
	mi := k.tab.Mut(types.I32)
	m := k.body(k.sig("F", nil, mi, types.EmptyErrors))
	n := m.user("n", types.I32, false)
	tm := m.temp(mi)
	m.set(n, use(imm(types.I32, 1)))
	inner := m.set(tm, mutRef(m.local(n)))
	ret := m.set(0, use(mv(m.local(tm))))
	is, rs := m.b.Span(inner), m.b.Span(ret)
	m.b.Spans[ret] = source.Span{File: rs.File, Start: is.Start, End: rs.End}
	m.end(tm, n)
	m.ret(mir.ExitNormal)
	r := m.check(t)
	if len(r.Diags) != 1 || r.Diags[0].Code != diag.E0603 || r.Diags[0].At != m.b.Span(ret) {
		t.Fatalf("want only E0603 at the return: %v", got(r))
	}
}

// Put(v: &i32 into m, m: &Mutex[&i32]) spawned with &short, &m puts the
// loan on short into m, which outlives the scope: E0505 at &short. With
// short declared before m (so m ends first), ok.
func TestR17_SpawnAppliesCalleeInto(t *testing.T) {
	for _, shortFirst := range []bool{false, true} {
		k := newKit(t)
		k.taskLib()
		ri := refI32(k)
		mt := k.tab.Named(k.known.Mutex, []types.GenericArg{types.TypeArg(ri)})
		k.fn("Put", []types.Param{{Name: k.in.Intern("v"), Type: ri, Into: 2}, k.param("m", k.tab.Ref(mt))}, types.Void, types.EmptyErrors)
		m := k.body(k.sig("Bad", []types.Param{k.param("io", k.Io)}, types.Void, types.EmptyErrors))
		var short mir.LocalID
		if shortFirst {
			short = m.user("short", types.I32, false)
			m.set(short, use(imm(types.I32, 2)))
		}
		long := m.user("long", types.I32, false)
		mm := m.user("m", mt, false)
		m.set(long, use(imm(types.I32, 1)))
		tl := m.temp(ri)
		m.set(tl, ref(m.local(long)))
		m.set(mm, m.agg(mir.AggStruct, k.known.Mutex, mt, imm(types.U32, 0), cp(m.local(tl))))
		m.end(tl)
		if !shortFirst {
			short = m.user("short", types.I32, false)
			m.set(short, use(imm(types.I32, 2)))
		}
		s := m.openScope()
		a1, a2 := m.temp(ri), m.temp(k.tab.Ref(mt))
		bad := m.set(a1, ref(m.local(short)))
		m.set(a2, ref(m.local(mm)))
		m.spawn(s, "Put", argCopy(m.local(a1)), argCopy(m.local(a2)))
		m.end(a2, a1)
		m.join(s)
		m.end(s)
		if !shortFirst {
			m.end(short)
		}
		m.useRef(m.local(mm), mt)
		m.end(mm, long)
		if shortFirst {
			m.end(short)
		}
		m.ret(mir.ExitNormal)
		if shortFirst {
			m.expect(t, m.check(t))
		} else {
			m.expect(t, m.check(t), diag.E0505, bad)
		}
	}
}

// Arena.Reset(mut self) ends self while a Cell put into the arena is
// still used: the exemption skips only loans no other live root reaches, so
// the reset's borrow is E0501. With the cell dead, the reset is ok.
func TestR18_ResetWithLiveCell(t *testing.T) {
	for _, live := range []bool{true, false} {
		k := newKit(t)
		reset := k.method("Arena", "Reset", types.ByMut, k.Arena, nil, types.Void, types.EmptyErrors)
		reset.Flows.Ends = []types.ParamIndex{1}
		mi := k.tab.Mut(types.I32)
		put := k.method("Arena", "PutI", types.ByShared, k.Arena, nil, mi, types.EmptyErrors)
		put.Flows.Sources = []types.Source{{Param: 1}}
		free := k.method("Arena", "Free", types.ByValue, k.Arena, nil, types.Void, types.EmptyErrors)
		free.Flows.Ends = []types.ParamIndex{1}
		m := k.body(k.sig("F", []types.Param{k.param("a", k.Arena)}, types.Void, types.EmptyErrors))
		a := m.user("arena", k.Arena, true)
		m.set(a, use(mv(m.local(1))))
		ta := m.temp(k.tab.Ref(k.Arena))
		c := m.user("c", mi, false)
		m.set(ta, ref(m.local(a)))
		nx := m.block()
		m.call(put.Item, nil, []mir.Arg{argCopy(m.local(ta))}, m.local(c), nx)
		m.at(nx)
		m.end(ta)
		tr := m.temp(k.tab.Mut(k.Arena))
		bad := m.set(tr, mutRef(m.local(a)))
		m.callv("Arena.Reset", nil, argMove(m.local(tr)))
		m.end(tr)
		if live {
			m.callv("UseMut", []types.TypeID{types.I32}, argMove(m.local(c)))
		}
		m.end(c)
		m.callv("Arena.Free", nil, argMove(m.local(a)))
		m.end(a)
		m.ret(mir.ExitNormal)
		if live {
			m.expect(t, m.check(t), diag.E0501, bad)
		} else {
			m.expect(t, m.check(t))
		}
	}
}

// F[T](x: T) { if T is i32 { y := narrow(x) } else { Take(x) } } is
// ok; with no else the branch that does not narrow leaves x initialized and
// the join is E0402 at x.
func TestIfTypeIsConsumes(t *testing.T) {
	for _, withElse := range []bool{true, false} {
		k := newKit(t)
		p0 := k.tab.Param(0)
		s := k.genFn("F", []types.Param{k.param("x", p0)}, types.Void)
		m := k.body(s)
		m.b.GenericCount = 1
		yes, no, join := m.block(), m.block(), m.block()
		m.term(mir.Terminator{Kind: mir.TermTypeTest, Param: 0, Type: types.I32, Yes: yes, No: no})
		m.at(yes)
		y := m.user("y", types.I32, false)
		m.set(y, mir.Rvalue{Kind: mir.RvCast, Cast: mir.CastNarrow, A: mv(m.local(1)), Type: types.I32})
		m.end(y)
		m.jump(join)
		m.at(no)
		if withElse {
			m.callv("Take", []types.TypeID{p0}, argMove(m.local(1)))
		}
		m.jump(join)
		m.at(join)
		m.ret(mir.ExitNormal)
		r := m.check(t)
		if withElse {
			m.expect(t, r)
		} else {
			m.expect(t, r, diag.E0402, m.b.Locals[1].Span)
		}
	}
}

// An early try with a live linear local is E0402 at it: f :=
// Acquire("a"); try G(); f.Close().
func TestR2_LinearTemporaryOnTryEdge(t *testing.T) {
	k := newKit(t)
	k.lib()
	oom := k.errs("OutOfMemory")
	g := k.fn("G", nil, types.Void, oom)
	m := k.body(k.fn("F", nil, types.Void, oom))
	f := m.user("f", k.Res, false)
	m.acquire(f)
	ok, bad := m.block(), m.block()
	e := m.temp(k.tab.ErrorSetType(oom))
	d := m.temp(types.Void)
	m.callErr(g.Item, nil, nil, m.local(d), ok, mir.EdgeTry, bad, e)
	m.at(bad)
	m.set(0, m.agg(mir.AggErr, types.NoItem, m.b.Locals[0].Type, cp(m.local(e))))
	m.end(e, f)
	m.ret(mir.ExitTry)
	m.at(ok)
	m.close(f)
	m.end(f)
	m.okVoid()
	m.expect(t, m.check(t), diag.E0402, m.b.Locals[f].Span)
}

// A field write to a non-var local is E0404; a field write of linear type is
// E0402.
func TestE0404_FieldOfNonVar(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	p := m.user("p", k.Point, false)
	m.set(p, m.agg(mir.AggStruct, k.item("Point"), k.Point, imm(types.I32, 1), imm(types.I32, 2)))
	bad := m.assign(m.pl(p, fld(0, types.I32)), use(imm(types.I32, 3)))
	m.end(p)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0404, bad)
}

func TestE0402_FieldOfLinearType(t *testing.T) {
	k := newKit(t)
	bt := k.both()
	m := k.body(k.sig("F", []types.Param{{Name: k.in.Intern("b"), Type: bt, Var: true}}, types.Void, types.EmptyErrors))
	r := m.user("r", k.Res, false)
	m.acquire(r)
	bad := m.assign(m.pl(1, fld(0, k.Res)), use(mv(m.local(r))))
	m.end(r)
	m.callv("Take", []types.TypeID{bt}, argMove(m.local(1)))
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0402, bad)
}

// x: Res; if c { x = Acquire("a"); x.Close() }: the join is {U, M},
// which counts: E0402 at x.
func TestR2_JoinUM(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", []types.Param{k.param("c", types.Bool)}, types.Void, types.EmptyErrors))
	x := m.user("x", k.Res, false)
	yes, join := m.block(), m.block()
	m.branch(cp(m.local(1)), yes, join)
	m.at(yes)
	m.acquire(x)
	m.close(x)
	m.jump(join)
	m.at(join)
	m.end(x)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0402, m.b.Locals[x].Span)
}

// Exchange(r, r) with r: mut i32 reborrows r twice; the second slot
// conflicts with the first, which the call is still forming: E0501.
func TestExchangeSameLocal(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", []types.Param{k.param("r", k.tab.Mut(types.I32))}, types.Void, types.EmptyErrors))
	bad := m.callv("Exchange", []types.TypeID{types.I32}, argReborrow(m.local(1)), argReborrow(m.local(1)))
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0501, bad)
}

// struct T { a: &i32, b: &i32 }; F(t: &T): &i32 from t.a { return t.b
// // <- E0602 }; `return t.a` is ok; and a call result that holds an
// untagged t.inner is covered by from t.a.
func TestFromFieldTagMismatch(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field uint32
		call  bool
		bad   bool
	}{{"b", 1, false, true}, {"a", 0, false, false}, {"call", 0, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			k := newKit(t)
			ri := refI32(k)
			k.structItem(k.main, "T", 0, false, false, field("a", ri), field("b", ri))
			tt := k.named("T")
			g := k.fn("G", []types.Param{k.param("x", k.tab.Ref(tt))}, ri, types.EmptyErrors)
			g.Flows.Sources = []types.Source{{Param: 1, Behind: true}}
			s := k.sig("F", []types.Param{k.param("t", k.tab.Ref(tt))}, ri, types.EmptyErrors)
			s.Flows.Sources = []types.Source{{Param: 1, Behind: true, Path: []types.FieldStep{{Field: 0, Type: ri}}}}
			s.Flows.Declared = true
			m := k.body(s)
			var ret mir.SpanID
			if tc.call {
				nx := m.block()
				ret = m.call(g.Item, nil, []mir.Arg{argCopy(m.local(1))}, m.local(0), nx)
				m.at(nx)
			} else {
				ret = m.set(0, use(cp(m.pl(1, deref(tt), fld(tc.field, ri)))))
			}
			m.ret(mir.ExitNormal)
			if tc.bad {
				m.expect(t, m.check(t), diag.E0602, ret)
			} else {
				m.expect(t, m.check(t))
			}
		})
	}
}

// A call through a fn value reads its flows from the fn type:
// f: fn(&i32 into $2, &Cell[&i32]) stores the loan on short into c,
// which outlives short: E0505 at &short.
func TestFlowsThroughFnValue(t *testing.T) {
	k := newKit(t)
	k.lib()
	ri := refI32(k)
	cellT := k.tab.Named(k.known.Cell, []types.GenericArg{types.TypeArg(ri)})
	fnT := k.tab.Fn(types.FnType{Params: []types.FnParam{{Type: ri, Into: 2}, {Type: k.tab.Ref(cellT)}}, Result: types.Void})
	k.cellNew()
	m := k.body(k.sig("F", []types.Param{k.param("f", fnT)}, types.Void, types.EmptyErrors))
	long := m.user("long", types.I32, false)
	c := m.user("c", cellT, false)
	m.set(long, use(imm(types.I32, 1)))
	tl := m.temp(ri)
	m.set(tl, ref(m.local(long)))
	nx := m.block()
	m.call(k.item("Cell.New"), []types.TypeID{ri}, []mir.Arg{argCopy(m.local(tl))}, m.local(c), nx)
	m.at(nx)
	m.end(tl)
	short := m.user("short", types.I32, false)
	m.set(short, use(imm(types.I32, 2)))
	a1, a2 := m.temp(ri), m.temp(k.tab.Ref(cellT))
	bad := m.set(a1, ref(m.local(short)))
	m.set(a2, ref(m.local(c)))
	after := m.block()
	d := m.temp(types.Void)
	m.term(mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallValue, Value: cp(m.local(1))},
		Args: m.b.AddArgs(argCopy(m.local(a1)), argCopy(m.local(a2))), Dest: m.local(d), Target: after, Spawn: mir.NoLocal})
	m.at(after)
	m.end(a2, a1, short)
	m.useRef(m.local(c), cellT)
	m.end(c, long)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0505, bad)
}
