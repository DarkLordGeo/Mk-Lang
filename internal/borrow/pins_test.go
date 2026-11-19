// Why: several diagnostics have exact anchors, facts and fixes that the
// end-to-end tests check only through the whole pipeline.
// What: hand-built pins of the borrow side of those rows: discarded linear
// temporaries, by-value parameters without var, reborrows, E0505 anchors,
// E0409 recovery, globals and the "add var" fix.
// How: one body per row, checked for the code, its span and its facts
// (fact).

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// fact returns the value of d's fact key, failing when it is absent.
func fact(t *testing.T, d *diag.Diagnostic, key string) diag.Fact {
	t.Helper()
	for _, f := range d.Facts {
		if f.Key == key {
			return f.Value
		}
	}
	t.Fatalf("%v: no fact %q", d.Code, key)
	return diag.Fact{}
}

// F() { _ = Acquire("a") // <- E0402 }: the discarded temporary ends at its
// statement still {I}; the diagnostic is anchored at the expression that
// created it, the call, which is the temporary's span, not its scope end.
func TestDiscardLinear(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	tmp := m.temp(k.Res)
	at := m.acquire(tmp)
	m.b.Locals[tmp].Span = at // lower gives a temporary its expression's span
	m.end(tmp)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0402, at)
}

// F() { Acquire("a") // <- E0402; Use(&1) }: an expression statement's
// linear value is a temporary ending with its statement, before the next
// one runs.
func TestExprStatementDropsLinear(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	tmp := m.temp(k.Res)
	at := m.acquire(tmp)
	m.b.Locals[tmp].Span = at // lower gives a temporary its expression's span
	m.end(tmp)
	one := m.temp(types.I32)
	m.set(one, use(imm(types.I32, 1)))
	m.useRef(m.local(one), types.I32)
	m.end(one)
	m.ret(mir.ExitNormal)
	r := m.check(t)
	m.expect(t, r, diag.E0402, at)
	if p := fact(t, r.Diags[0], "release"); p.Kind != diag.FactList || len(p.List) != 1 || p.List[0].Str != "Close" {
		t.Fatalf("release fact %+v, want [Close]", p)
	}
}

// F() { n := Acquire("a").id // <- E0402; Use(&n) }: reading a plain field
// of a linear temporary does not consume it; the E0402 is anchored at
// `Acquire("a")`, not at the statement's end or at n.
func TestLinearTemporaryFieldRead(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	tmp := m.temp(k.Res)
	n := m.user("n", types.I32, false)
	at := m.acquire(tmp)
	m.b.Locals[tmp].Span = at // lower gives a temporary its expression's span
	m.set(n, use(cp(m.pl(tmp, fld(0, types.I32)))))
	m.end(tmp)
	m.useRef(m.local(n), types.I32)
	m.end(n)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0402, at)
}

// F(p: Point) { p.x = 1 // <- E0404 } and F(p: Point) { p = MakePoint()
// // <- E0404 }: a by-value parameter is {I} at entry, so a field or whole
// assignment to it without `var` is E0404, with `declared_at`.
func TestE0404_ByValueParamNotVar(t *testing.T) {
	for _, whole := range []bool{false, true} {
		k := newKit(t)
		m := k.body(k.sig("F", []types.Param{k.param("p", k.Point)}, types.Void, types.EmptyErrors))
		var bad mir.SpanID
		if whole {
			next := m.block()
			bad = m.call(k.item("MakePoint"), nil, nil, m.local(1), next)
			m.at(next)
		} else {
			bad = m.assign(m.pl(1, fld(0, types.I32)), use(imm(types.I32, 1)))
		}
		m.ret(mir.ExitNormal)
		r := m.check(t)
		m.expect(t, r, diag.E0404, bad)
		if f := fact(t, r.Diags[0], "declared_at"); f.Kind != diag.FactAt {
			t.Fatalf("whole=%v: declared_at %+v", whole, f)
		}
	}
}

// F(c: bool) { x: i32; if c { x = 1 }; x = 2 // <- E0404; Use(&x) }: at the
// second assignment x is {U, I}, not {U}, so a non-var x is E0404 even
// though one path reaches it unassigned ("on some path"). With
// `var x: i32` it is ok.
func TestE0404_SecondAssignOnSomePath(t *testing.T) {
	for _, isVar := range []bool{false, true} {
		k := newKit(t)
		m := k.body(k.sig("F", []types.Param{k.param("c", types.Bool)}, types.Void, types.EmptyErrors))
		x := m.user("x", types.I32, isVar)
		yes, join := m.block(), m.block()
		m.branch(cp(m.local(1)), yes, join)
		m.at(yes)
		m.set(x, use(imm(types.I32, 1)))
		m.jump(join)
		m.at(join)
		bad := m.set(x, use(imm(types.I32, 2)))
		m.useRef(m.local(x), types.I32)
		m.end(x)
		m.ret(mir.ExitNormal)
		if isVar {
			m.expect(t, m.check(t))
		} else {
			m.expect(t, m.check(t), diag.E0404, bad)
		}
	}
}

// F(r: mut i32) { s := mut *r; UseMut(s); UseMut(r) }: ok. A reborrow of
// a `mut T` reference goes through a deref (k ≥ 1), so the non-var r is no
// E0508, and the call's ArgReborrow of r is not either.
func TestE0508_ReborrowOfMutRefIsNot(t *testing.T) {
	k := newKit(t)
	mi := k.tab.Mut(types.I32)
	m := k.body(k.sig("F", []types.Param{k.param("r", mi)}, types.Void, types.EmptyErrors))
	s := m.user("s", mi, false)
	m.set(s, mutRef(m.pl(1, deref(types.I32))))
	m.callv("UseMut", []types.TypeID{types.I32}, argMove(m.local(s)))
	m.end(s)
	m.callv("UseMut", []types.TypeID{types.I32}, argReborrow(m.local(1)))
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// F() { var out := &long; { short := 2; out = &short // <- E0505 }; Use(out)
// }: the E0505 is at the loan's creation, `&short`, not at the scope end;
// the scope end is the ends_at fact, out the holder, and its next use the
// kept_live_by location.
func TestE0505_AnchorIsCreationNotScopeEnd(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	out := m.user("out", refI32(k), true)
	long := m.user("long", types.I32, false)
	m.set(long, use(imm(types.I32, 1)))
	m.set(out, ref(m.local(long)))
	short := m.user("short", types.I32, false)
	m.set(short, use(imm(types.I32, 2)))
	loan := m.set(out, ref(m.local(short)))
	end := m.end(short)
	use := m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(out)))
	m.end(long, out)
	m.ret(mir.ExitNormal)
	r := m.check(t)
	m.expect(t, r, diag.E0505, loan)
	d := r.Diags[0]
	if f := fact(t, d, "ends_at"); f.At != m.b.Span(end) {
		t.Fatalf("ends_at %v, want the scope end %v", f.At, m.b.Span(end))
	}
	if f := fact(t, d, "kept_live_by"); f.At != m.b.Span(use) {
		t.Fatalf("kept_live_by %v, want the use %v", f.At, m.b.Span(use))
	}
	if f := fact(t, d, "holder"); f.Str != "out" {
		t.Fatalf("holder %q, want out", f.Str)
	}
	if f := fact(t, d, "loan"); f.At != m.b.Span(loan) || f.Str != "short" {
		t.Fatalf("loan fact %+v, want short at the borrow", f)
	}
}

// F(j: &Both) { x := j.a // <- E0409; x.Close() }: the read of a linear
// field through a reference is E0409 alone. The recovery marks x moved with
// no move site, so neither the Close (a use of x) nor x's scope end adds an
// E0401 or E0402.
func TestE0409_Recovery(t *testing.T) {
	k := newKit(t)
	bt := k.both()
	m := k.body(k.sig("F", []types.Param{k.param("j", k.tab.Ref(bt))}, types.Void, types.EmptyErrors))
	x := m.user("x", k.Res, false)
	bad := m.set(x, use(mv(m.pl(1, deref(bt), fld(0, k.Res)))))
	m.close(x)
	m.end(x)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0409, bad)
}

// global declares `var <name>: t` in main.
func (k *kit) global(name string, t types.TypeID) types.ItemID {
	return k.items.AddItem(&types.Item{Module: k.main.ID, Name: k.in.Intern(name), Kind: types.KindGlobal,
		Key: types.ItemKey("main", name), Decl: &types.GlobalDecl{Type: t}})
}

// var G: i32; F() { UseMut(mut G) // <- E0507 }: a global is reached through
// the shared *(&G), so a mut borrow of it is E0507 with the place fact.
func TestMutGlobalIsE0507(t *testing.T) {
	k := newKit(t)
	g := k.global("G", types.I32)
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	tg := m.temp(refI32(k))
	tm := m.temp(k.tab.Mut(types.I32))
	m.set(tg, mir.Rvalue{Kind: mir.RvGlobalRef, Item: g})
	bad := m.set(tm, mutRef(m.pl(tg, deref(types.I32))))
	m.callv("UseMut", []types.TypeID{types.I32}, argMove(m.local(tm)))
	m.end(tm, tg)
	m.ret(mir.ExitNormal)
	r := m.check(t)
	m.expect(t, r, diag.E0507, bad)
	fact(t, r.Diags[0], "place")
}

// var G: Res; F() { x := G // <- E0409; x.Close() }: moving a non-plain
// value out of a global goes through *(&G), a place with steps, so it is
// E0409 alone, and the recovery leaves x silent.
func TestMoveNonPlainGlobalIsE0409(t *testing.T) {
	k := newKit(t)
	g := k.global("G", k.Res)
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	tg := m.temp(k.tab.Ref(k.Res))
	x := m.user("x", k.Res, false)
	m.set(tg, mir.Rvalue{Kind: mir.RvGlobalRef, Item: g})
	bad := m.set(x, use(mv(m.pl(tg, deref(k.Res)))))
	m.close(x)
	m.end(x, tg)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0409, bad)
}

// F() { x := 1; x = 2 // <- E0404 } carries the "add var" fix at x's
// declaration; F() { Point(x, y) := MakePoint(); x = 2 // <- E0404 }
// does not: x is bound by a pattern, whose binding statement does not start
// at x's declaration, and `var` cannot be inserted there.
func TestFixAddVar_OmittedForPatternBinding(t *testing.T) {
	for _, pattern := range []bool{false, true} {
		k := newKit(t)
		m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
		x := m.user("x", types.I32, false)
		var bad mir.SpanID
		if pattern {
			y := m.user("y", types.I32, false)
			tp := m.temp(k.Point)
			next := m.block()
			stmt := m.call(k.item("MakePoint"), nil, nil, m.local(tp), next)
			m.at(next)
			// The binding statements carry the destructure's span, which
			// starts before x's own (the pattern's x is inside it).
			m.b.Locals[x].Span = m.sp()
			blk := &m.b.Blocks[m.cur]
			blk.Stmts = append(blk.Stmts,
				mir.Statement{Kind: mir.StAssign, Place: m.local(x), Rvalue: use(cp(m.pl(tp, fld(0, types.I32)))), Span: stmt},
				mir.Statement{Kind: mir.StAssign, Place: m.local(y), Rvalue: use(cp(m.pl(tp, fld(1, types.I32)))), Span: stmt})
			m.end(tp)
			bad = m.set(x, use(imm(types.I32, 2)))
			m.end(y)
		} else {
			m.b.Locals[x].Span = m.set(x, use(imm(types.I32, 1)))
			bad = m.set(x, use(imm(types.I32, 2)))
		}
		m.end(x)
		m.ret(mir.ExitNormal)
		r := m.check(t)
		m.expect(t, r, diag.E0404, bad)
		if n := len(r.Diags[0].Fixes); pattern && n != 0 || !pattern && n != 1 {
			t.Fatalf("pattern=%v: %d fixes %+v", pattern, n, r.Diags[0].Fixes)
		}
	}
}

// F() { p := MakePoint(); var last := &p; for i in 0..2 { last =
// &MakePoint() // <- E0506 }; Use(last) }: a temporary of a `for` body
// ends with its iteration, and last, live after the loop, still holds the
// loan on it: E0506 at the borrow, once, with the scope end as ends_at. With
// Use(last) inside the body instead it is ok.
func TestE0506_ForIterationEnd(t *testing.T) {
	for _, useAfter := range []bool{true, false} {
		k := newKit(t)
		rp := k.tab.Ref(k.Point)
		m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
		p := m.user("p", k.Point, false)
		last := m.user("last", rp, true)
		i := m.user("i", types.Int, true)
		cond := m.temp(types.Bool)
		n0 := m.block()
		m.call(k.item("MakePoint"), nil, nil, m.local(p), n0)
		m.at(n0)
		m.set(last, ref(m.local(p)))
		m.set(i, use(imm(types.Int, 0)))
		header, body, exit := m.block(), m.block(), m.block()
		m.b.Loops = append(m.b.Loops, mir.LoopInfo{Header: header, Exit: exit, Holder: mir.NoLocal, Span: m.sp()})
		m.jump(header)
		m.at(header)
		m.set(cond, mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Lt, A: cp(m.local(i)), B: imm(types.Int, 2)})
		m.branch(cp(m.local(cond)), body, exit)
		m.at(body)
		tp := m.temp(k.Point)
		n1 := m.block()
		m.call(k.item("MakePoint"), nil, nil, m.local(tp), n1)
		m.at(n1)
		loan := m.set(last, ref(m.local(tp)))
		if !useAfter {
			m.callv("Use", []types.TypeID{k.Point}, argCopy(m.local(last)))
		}
		end := m.end(tp)
		m.set(i, mir.Rvalue{Kind: mir.RvBinary, Bin: mir.AddWrap, A: cp(m.local(i)), B: imm(types.Int, 1)})
		m.jump(header)
		m.at(exit)
		if useAfter {
			m.callv("Use", []types.TypeID{k.Point}, argCopy(m.local(last)))
		}
		m.end(cond, i, last, p)
		m.ret(mir.ExitNormal)
		r := m.check(t)
		if !useAfter {
			m.expect(t, r)
			continue
		}
		m.expect(t, r, diag.E0506, loan)
		if f := fact(t, r.Diags[0], "ends_at"); f.At != m.b.Span(end) {
			t.Fatalf("ends_at %v, want the iteration's end of the temporary %v", f.At, m.b.Span(end))
		}
	}
}
