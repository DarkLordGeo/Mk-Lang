// Why: a match narrows a value to one variant, and takes move payloads out
// piece by piece; both change which loans and linear values are live.
// What: tests of takes and variant narrowing, E0405 and E0406, with the
// discr and switchOn builders.
// How: hand-built match and destructuring bodies, checked for their codes.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

func (m *mb) discr(d mir.LocalID, p mir.Place) mir.SpanID {
	return m.set(d, mir.Rvalue{Kind: mir.RvDiscriminant, Place: p})
}

// switchOn is a switch on a discriminant local over (value, target) pairs.
func (m *mb) switchOn(d mir.LocalID, def mir.BlockID, cases ...mir.SwitchCase) mir.SpanID {
	return m.term(mir.Terminator{Kind: mir.TermSwitch, On: cp(m.local(d)), Cases: m.b.AddCases(cases...), Default: def})
}

// enum E { A(&i32), B(Res) }; F() { var n := 1; e := E.A(&n); match e { A(r)
// => { n = 2 // <- E0502; Use(r) }, B(x) => x.Close() } }.
func TestVariantNarrowingPlainPayloadKeepsLoans(t *testing.T) {
	k := newKit(t)
	ri := refI32(k)
	k.enumItem("E", 0, []fieldSpec{field("0", ri)}, []fieldSpec{field("0", k.Res)})
	et := k.named("E")
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	n := m.user("n", types.I32, true)
	e := m.user("e", et, false)
	tr := m.temp(ri)
	tm := m.temp(et)
	d := m.temp(types.U64)
	m.set(n, use(imm(types.I32, 1)))
	m.set(tr, ref(m.local(n)))
	rv := m.agg(mir.AggEnum, k.item("E"), et, cp(m.local(tr)))
	rv.Variant = 0
	m.set(e, rv)
	m.end(tr)
	m.set(tm, use(mv(m.local(e))))
	m.discr(d, m.local(tm))
	ba, bb, join := m.block(), m.block(), m.block()
	m.switchOn(d, bb, mir.SwitchCase{Value: 0, Target: ba})
	m.at(ba)
	r := m.user("r", ri, false)
	m.set(r, use(cp(m.pl(tm, down(mir.SelEnum, 0, et), fld(0, ri)))))
	bad := m.set(n, use(imm(types.I32, 2)))
	m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(r)))
	m.end(r)
	m.jump(join)
	m.at(bb)
	x := m.user("x", k.Res, false)
	m.set(x, use(mv(m.pl(tm, down(mir.SelEnum, 1, et), fld(0, k.Res)))))
	m.close(x)
	m.end(x)
	m.jump(join)
	m.at(join)
	m.end(d, tm, e, n)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0502, bad)
}

// F(o: ?Res) { match o { none => {}, _ => {} } } is E0402 at o's temporary:
// the Default edge's variant is some, whose payload is linear.
func TestVariantNarrowingDefaultEdge(t *testing.T) {
	k := newKit(t)
	or := k.tab.Optional(k.Res)
	m := k.body(k.sig("F", []types.Param{k.param("o", or)}, types.Void, types.EmptyErrors))
	tm := m.temp(or)
	d := m.temp(types.U64)
	m.set(tm, use(mv(m.local(1))))
	m.discr(d, m.local(tm))
	bn, bs, join := m.block(), m.block(), m.block()
	m.switchOn(d, bs, mir.SwitchCase{Value: 0, Target: bn})
	m.at(bn)
	m.jump(join)
	m.at(bs)
	m.jump(join)
	m.at(join)
	m.end(d, tm)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0402, m.b.Locals[tm].Span)
}

// match e { A(r) => r.Close(), _ => {} } over enum { A(Res), B, C }: ok; the
// Default edge's variants carry nothing.
func TestVariantNarrowingDefaultEdgeComplement(t *testing.T) {
	k := newKit(t)
	k.enumItem("E", 0, []fieldSpec{field("0", k.Res)}, nil, nil)
	et := k.named("E")
	m := k.body(k.sig("F", []types.Param{k.param("e", et)}, types.Void, types.EmptyErrors))
	tm := m.temp(et)
	d := m.temp(types.U64)
	m.set(tm, use(mv(m.local(1))))
	m.discr(d, m.local(tm))
	ba, bo, join := m.block(), m.block(), m.block()
	m.switchOn(d, bo, mir.SwitchCase{Value: 0, Target: ba})
	m.at(ba)
	r := m.user("r", k.Res, false)
	m.set(r, use(mv(m.pl(tm, down(mir.SelEnum, 0, et), fld(0, k.Res)))))
	m.close(r)
	m.end(r)
	m.jump(join)
	m.at(bo)
	m.jump(join)
	m.at(join)
	m.end(d, tm)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// enum E { V(?Res), W }; F(e: E) { match e { V(some(r)) => r.Close(), V(none)
// => {}, W => {} } }: ok; the inner test is on t.V.0 in its own block.
func TestVariantNarrowingNested(t *testing.T) {
	k := newKit(t)
	or := k.tab.Optional(k.Res)
	k.enumItem("E", 0, []fieldSpec{field("0", or)}, nil)
	et := k.named("E")
	m := k.body(k.sig("F", []types.Param{k.param("e", et)}, types.Void, types.EmptyErrors))
	tm := m.temp(et)
	d := m.temp(types.U64)
	d2 := m.temp(types.U64)
	m.set(tm, use(mv(m.local(1))))
	m.discr(d, m.local(tm))
	bv, bw, bsome, bnone, join := m.block(), m.block(), m.block(), m.block(), m.block()
	m.switchOn(d, bw, mir.SwitchCase{Value: 0, Target: bv})
	m.at(bv)
	inner := m.pl(tm, down(mir.SelEnum, 0, et), fld(0, or))
	m.discr(d2, inner)
	m.switchOn(d2, bnone, mir.SwitchCase{Value: 1, Target: bsome})
	m.at(bsome)
	r := m.user("r", k.Res, false)
	m.set(r, use(mv(m.pl(tm, down(mir.SelEnum, 0, et), fld(0, or), down(mir.SelSome, 0, or), fld(0, k.Res)))))
	m.close(r)
	m.end(r)
	m.jump(join)
	m.at(bnone)
	m.jump(join)
	m.at(bw)
	m.jump(join)
	m.at(join)
	m.end(d2, d, tm)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// struct Job { r: Res, id: i32 }; Run(j: Job) { Job(r, id) := j; Use(&id);
// r.Close() }: ok, with t = move j; r = move t.r; id = copy t.id.
func TestTakeThenPlainCopy(t *testing.T) {
	k := newKit(t)
	k.structItem(k.main, "Job", 0, false, false, field("r", k.Res), field("id", types.I32))
	job := k.named("Job")
	m := k.body(k.sig("Run", []types.Param{k.param("j", job)}, types.Void, types.EmptyErrors))
	tm := m.temp(job)
	r := m.user("r", k.Res, false)
	id := m.user("id", types.I32, false)
	m.set(tm, use(mv(m.local(1))))
	m.set(r, use(mv(m.pl(tm, fld(0, k.Res)))))
	m.set(id, use(cp(m.pl(tm, fld(1, types.I32)))))
	m.useRef(m.local(id), types.I32)
	m.close(r)
	m.end(id, r, tm)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// A whole use of a taken temporary is a use of a moved value.
func TestTakenTemporaryWholeUseIsE0401(t *testing.T) {
	k := newKit(t)
	k.structItem(k.main, "Job", 0, false, false, field("r", k.Res), field("id", types.I32))
	job := k.named("Job")
	m := k.body(k.sig("Run", []types.Param{k.param("j", job)}, types.Void, types.EmptyErrors))
	tm := m.temp(job)
	r := m.user("r", k.Res, false)
	m.set(tm, use(mv(m.local(1))))
	m.set(r, use(mv(m.pl(tm, fld(0, k.Res)))))
	bad := m.callv("Take", []types.TypeID{job}, argMove(m.local(tm)))
	m.close(r)
	m.end(r, tm)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0401, bad)
}

// Both(a, b) := p with the second take in a later block with one
// predecessor: the T survives the single-predecessor edge.
func TestTakeInLaterSinglePredBlock(t *testing.T) {
	k := newKit(t)
	bt := k.both()
	m := k.body(k.sig("Release", []types.Param{k.param("p", bt)}, types.Void, types.EmptyErrors))
	tm := m.temp(bt)
	a := m.user("a", k.Res, false)
	b := m.user("b", k.Res, false)
	m.set(tm, use(mv(m.local(1))))
	m.set(a, use(mv(m.pl(tm, fld(0, k.Res)))))
	nx := m.block()
	m.jump(nx)
	m.at(nx)
	m.set(b, use(mv(m.pl(tm, fld(1, k.Res)))))
	m.close(a)
	m.close(b)
	m.end(b, a, tm)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// f := Acquire("a"); defer f.Close(); Take(f) // <- E0406.
func TestE0406_AtTheMove(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	f := m.user("f", k.Res, false)
	m.acquire(f)
	m.b.Defers = append(m.b.Defers, mir.DeferInfo{Span: m.sp()})
	bad := m.callv("Take", []types.TypeID{k.Res}, argMove(m.local(f)))
	d := m.b.AddBlock(mir.OriginDefer, 0)
	m.jump(d)
	m.at(d)
	m.close(f)
	m.end(f)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0406, bad)
}

// f := Acquire("a"); for { if Cond() { break }; Take(f) // <- E0405 }: the
// only move is in an earlier iteration; the join at the header is also
// E0402 at f.
func TestE0405_PreviousIteration(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	f := m.user("f", k.Res, false)
	c := m.temp(types.Bool)
	m.acquire(f)
	header, body, exit := m.block(), m.block(), m.block()
	m.b.Loops = append(m.b.Loops, mir.LoopInfo{Header: header, Exit: exit, Holder: mir.NoLocal, Span: m.sp()})
	m.jump(header)
	m.at(header)
	cn := m.block()
	m.call(k.item("Cond"), nil, nil, m.local(c), cn)
	m.at(cn)
	m.branch(cp(m.local(c)), exit, body)
	m.at(body)
	bad := m.callv("Take", []types.TypeID{k.Res}, argMove(m.local(f)))
	m.jump(header)
	m.at(exit)
	m.end(c, f)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0405, bad, diag.E0402, m.b.Locals[f].Span)
}
