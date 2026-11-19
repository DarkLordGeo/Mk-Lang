// Why: function exits are where moved or borrowed parameters must be back in
// order, on success and on error.
// What: tests of the restore of moved arguments on an error edge, of
// parameters at returns and of the E0411 sites.
// How: hand-built bodies with fail, try and else exits, checked for E0411,
// E0401 and E0402.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// listT is List[i32]: a unique (view), non-plain by-value parameter type.
func listT(k *kit) types.TypeID { return k.named("List", types.I32) }

// F(var p: List[i32], q: List[i32]): Full!void { p = q // <- E0411; fail
// Full } (an assignment changes p).
func TestAssignVarParam(t *testing.T) {
	k := newKit(t)
	lt := listT(k)
	s := k.fn("F", []types.Param{{Name: k.in.Intern("p"), Type: lt, Var: true}, k.param("q", lt)}, types.Void, k.errs("Full"))
	m := k.body(s)
	bad := m.set(1, use(mv(m.local(2))))
	m.fail("Full")
	m.expect(t, m.check(t), diag.E0411, bad)
}

// F(var p: List[i32]): Full!void { r := mut p // <- E0411; UseMut(r); fail
// Full }.
func TestMutBorrowVarParam(t *testing.T) {
	k := newKit(t)
	lt := listT(k)
	s := k.fn("F", []types.Param{{Name: k.in.Intern("p"), Type: lt, Var: true}}, types.Void, k.errs("Full"))
	m := k.body(s)
	r := m.user("r", k.tab.Mut(lt), false)
	bad := m.set(r, mutRef(m.local(1)))
	m.callv("UseMut", []types.TypeID{lt}, argMove(m.local(r)))
	m.end(r)
	m.fail("Full")
	m.expect(t, m.check(t), diag.E0411, bad)
}

// struct W { m: Mutex[Res] }; F(p: W): Full!void { r := &p // <- E0411;
// Use(r); fail Full } (an inline Mutex of a non-plain T borrowed at
// all).
func TestInlineMutexBorrow(t *testing.T) {
	k := newKit(t)
	mres := k.tab.Named(k.known.Mutex, []types.GenericArg{types.TypeArg(k.Res)})
	k.structItem(k.main, "W", 0, false, false, field("m", mres))
	wt := k.named("W")
	m := k.body(k.fn("F", []types.Param{k.param("p", wt)}, types.Void, k.errs("Full")))
	r := m.user("r", k.tab.Ref(wt), false)
	bad := m.set(r, ref(m.local(1)))
	m.callv("Use", []types.TypeID{wt}, argCopy(m.local(r)))
	m.end(r)
	// p is linear: consume it before the success-less exit so only E0411
	// remains (a fail exit returns ownership anyway).
	m.fail("Full")
	m.expect(t, m.check(t), diag.E0411, bad)
}

// F(r: Res): Full!void { defer r.Close(); fail Full }: the defer copy on
// the fail exit consumes r, an E0411 site.
func TestDeferConsumesParam(t *testing.T) {
	k := newKit(t)
	m := k.body(k.fn("F", []types.Param{k.param("r", k.Res)}, types.Void, k.errs("Full")))
	m.b.Defers = append(m.b.Defers, mir.DeferInfo{Span: m.sp()})
	es := k.errs("Full")
	tm := m.temp(k.tab.ErrorSetType(es))
	m.set(tm, m.agg(mir.AggError, k.item("Full"), k.tab.ErrorSetType(es)))
	m.set(0, m.agg(mir.AggErr, types.NoItem, m.b.Locals[0].Type, cp(m.local(tm))))
	m.end(tm)
	d := m.b.AddBlock(mir.OriginDefer, 0)
	m.jump(d)
	m.at(d)
	bad := m.close(1)
	m.ret(mir.ExitFail)
	m.expect(t, m.check(t), diag.E0411, bad)
}

// A return from inside an else handler is a success exit, so a move
// before it is no E0411; a try inside the else is an error exit.
func TestElseReturnIsSuccess(t *testing.T) {
	for _, exit := range []mir.ExitKind{mir.ExitElse, mir.ExitTry} {
		k := newKit(t)
		lt := listT(k)
		s := k.fn("F", []types.Param{k.param("p", lt)}, types.Void, k.errs("Full"))
		m := k.body(s)
		bad := m.callv("Take", []types.TypeID{lt}, argMove(m.local(1)))
		if exit == mir.ExitElse {
			m.okVoid()
			m.b.Blocks[m.cur].Term.Exit = mir.ExitElse
			m.expect(t, m.check(t))
		} else {
			es := k.errs("Full")
			tm := m.temp(k.tab.ErrorSetType(es))
			m.set(tm, m.agg(mir.AggError, k.item("Full"), k.tab.ErrorSetType(es)))
			m.set(0, m.agg(mir.AggErr, types.NoItem, m.b.Locals[0].Type, cp(m.local(tm))))
			m.end(tm)
			m.ret(mir.ExitTry)
			m.expect(t, m.check(t), diag.E0411, bad)
		}
	}
}

// Stash(r: mut i32 into s, s: mut HeapList[mut i32], a: &Alloc):
// OutOfMemory!void; the caller's `var n := 1; m := mut n; Stash(m, mut
// list, a) else { *m = 2 // <- E0401 }`: a mut local moved into an into
// parameter stays moved on the error edge. Here list is the caller's own
// parameter, so the store of the loan on n into it is also E0505 at mut n.
func TestRestoreOnlyByValueParams(t *testing.T) {
	k := newKit(t)
	k.lib()
	mi := k.tab.Mut(types.I32)
	hl := k.named("HeapList", mi)
	stash := k.fn("Stash", []types.Param{{Name: k.in.Intern("r"), Type: mi, Into: 2}, k.param("s", k.tab.Mut(hl)),
		k.param("a", k.tab.Ref(k.Alloc))}, types.Void, k.errs("OutOfMemory"))
	m := k.body(k.fn("Caller", []types.Param{k.param("a", k.tab.Ref(k.Alloc)), k.param("list", k.tab.Mut(hl))}, types.Void, types.EmptyErrors))
	n := m.user("n", types.I32, true)
	mm := m.user("m", mi, false)
	m.set(n, use(imm(types.I32, 1)))
	ln := m.set(mm, mutRef(m.local(n)))
	ok, bad := m.block(), m.block()
	e := m.temp(k.tab.ErrorSetType(stash.Errors))
	d := m.temp(types.Void)
	m.callErr(stash.Item, nil, []mir.Arg{argMove(m.local(mm)), argReborrow(m.local(2)), argCopy(m.local(1))},
		m.local(d), ok, mir.EdgeElse, bad, e)
	m.at(bad)
	w := m.assign(m.pl(mm, deref(types.I32)), use(imm(types.I32, 2)))
	m.jump(ok)
	m.at(ok)
	m.end(e, mm, n)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0401, w, diag.E0505, ln)
}

// A linear by-value parameter still holding its value at a normal exit is
// E0402 at the parameter; at a fail exit ownership returns to the caller.
func TestE0402_LinearParamAtExit(t *testing.T) {
	for _, exit := range []mir.ExitKind{mir.ExitNormal, mir.ExitFail} {
		k := newKit(t)
		m := k.body(k.fn("F", []types.Param{k.param("r", k.Res)}, types.Void, k.errs("Full")))
		if exit == mir.ExitNormal {
			m.okVoid()
			m.expect(t, m.check(t), diag.E0402, m.b.Locals[1].Span)
		} else {
			m.fail("Full")
			m.expect(t, m.check(t))
		}
	}
}

// Facts: GuardDecisions is OnerrRuns indexed by block.
func TestOnerrRunsFacts(t *testing.T) {
	f := Facts{OnerrRuns: []OnerrRun{{Block: 2, Run: true}, {Block: 4, Run: false}}}
	g := f.GuardDecisions(6)
	if !g[2] || g[4] || g[0] || len(g) != 6 {
		t.Fatalf("GuardDecisions %v", g)
	}
	if !f.Runs(2) || f.Runs(4) {
		t.Fatalf("Runs")
	}
}
