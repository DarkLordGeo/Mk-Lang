// Why: running more std code through borrow found bugs and paths no test
// covered.
// What: regression tests of generic callees on an error edge, `ends`
// through fn values and interface methods, every store case into an
// abstract node, receiver borrow order and a loan held through a field.
// How: each comment holds the Mk source the hand-built MIR stands for;
// strictFacts makes the facts callbacks fail on a foreign type parameter.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// strictFacts wraps the kit's Facts so that asking for the facts of a type
// parameter, which a non-generic body does not have, fails the test: check's
// FactsIn raises an ICE there ("type parameter 0 outside the unit's 0
// generics"), so the driver's Env would.
func strictFacts(t *testing.T, k *kit, env *Env) {
	inner := env.Facts
	env.Facts = func(ty types.TypeID) types.Facts {
		if k.mentionsParamT(ty) {
			t.Errorf("Facts asked of %s, a type parameter of no generic in this body", k.tab.TypeString(ty, nil))
		}
		return inner(ty)
	}
}

func (k *kit) mentionsParamT(ty types.TypeID) bool {
	c := &checker{tab: k.tab}
	return c.mentionsParam(ty)
}

// Put(a: &Alloc, list: mut HeapList[Res]) { r := Acquire("a")
// list.Push(a, r) else { r.Close() } }: ok. Push[T] is generic; the
// restore of r on the error edge asks plain of r's type, Res, never of the
// callee's declared `x: T` (check's FactsIn raises an ICE on a parameter
// outside the body's generics).
func TestGenericCalleePlainOfArgumentType(t *testing.T) {
	{
		k := newKit(t)
		elem := "Res"
		k.lib()
		et := k.named(elem)
		hl := k.named("HeapList", et)
		s := k.fn("Put", []types.Param{k.param("a", k.tab.Ref(k.Alloc)), k.param("list", k.tab.Mut(hl))}, types.Void, types.EmptyErrors)
		m := k.body(s)
		r := m.user("r", et, false)
		m.acquire(r)
		ok, bad := m.block(), m.block()
		e := m.temp(k.tab.ErrorSetType(k.errs("OutOfMemory")))
		d := m.temp(types.Void)
		m.callErr(k.item("HeapList.Push"), []types.TypeID{et},
			[]mir.Arg{argReborrow(m.local(2)), argCopy(m.local(1)), argMove(m.local(r))}, m.local(d), ok, mir.EdgeElse, bad, e)
		m.at(bad)
		m.close(r)
		m.jump(ok)
		m.at(ok)
		m.end(d, e, r)
		m.ret(mir.ExitNormal)
		m.validate(t)
		env := k.env(s)
		strictFacts(t, k, env)
		m.expect(t, Check(m.b, env))
	}
}

// e1102Count checks every E1102 is in phase flow and returns how many there
// are; it fails on any other code.
func e1102Count(t *testing.T, res *Result) int {
	t.Helper()
	n := 0
	for _, d := range res.Diags {
		if d.Code != diag.E1102 {
			t.Fatalf("unexpected %v %s", d.Code, d.Message)
		}
		if d.Phase != diag.PhaseFlow {
			t.Fatalf("E1102 phase %v, want flow", d.Phase)
		}
		n++
	}
	return n
}

// Wrap(o: mut O, reset: fn(mut O) ends 1) ends o { reset(o) }: ok, a call
// through a fn value whose type ends its parameter ends the whole o
// (blockEndsWhole's CallValue arm); with `reset: fn(mut O)` (no ends) the
// wrapper proves nothing and is E1102 outside an unsafe-permitted module.
func TestEndsThroughFnValue(t *testing.T) {
	for _, ends := range []bool{true, false} {
		k := newKit(t)
		k.structItem(k.main, "O", 0, false, false, field("arena", k.Arena))
		ot := k.named("O")
		fn := types.FnType{Params: []types.FnParam{{Type: k.tab.Mut(ot)}}, Result: types.Void, Errors: types.EmptyErrors}
		if ends {
			fn.Ends = []types.ParamIndex{1}
		}
		ft := k.tab.Fn(fn)
		s := k.fn("Wrap", []types.Param{k.param("o", k.tab.Mut(ot)), k.param("reset", ft)}, types.Void, types.EmptyErrors)
		s.Flows.Ends = []types.ParamIndex{1}
		s.Span = source.Span{File: kitFile, Start: 3, End: 4}
		m := k.body(s)
		d := m.temp(types.Void)
		nx := m.block()
		m.term(mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallValue, Value: cp(m.local(2))},
			Args: m.b.AddArgs(argReborrow(m.local(1))), Dest: m.local(d), Target: nx, Spawn: mir.NoLocal})
		m.at(nx)
		m.end(d)
		m.ret(mir.ExitNormal)
		m.validate(t)
		if n := e1102Count(t, Check(m.b, k.env(s))); (n == 1) == ends || n > 1 {
			t.Fatalf("ends=%v: %d E1102", ends, n)
		}
	}
}

// interface Resettable { Reset(mut self) ends self }; Wrap[T: Resettable](o:
// mut T) ends o { o.Reset() }: ok, the interface method's signature (read
// through Env.Method) ends the whole o; a Reset without ends leaves Wrap an
// unproven primitive, E1102.
func TestEndsThroughInterfaceMethod(t *testing.T) {
	for _, ends := range []bool{true, false} {
		k := newKit(t)
		rs := k.items.AddItem(&types.Item{Module: k.main.ID, Name: k.in.Intern("Resettable"), Kind: types.KindInterface,
			Key: types.ItemKey("main", "Resettable"), Decl: &types.InterfaceDecl{}})
		reset := types.Signature{Name: k.in.Intern("Reset"), Receiver: types.ByMut,
			Params: []types.Param{{Name: k.in.Intern("self"), Type: k.tab.Mut(k.tab.SelfType())}}, Result: types.Void, Errors: types.EmptyErrors}
		if ends {
			reset.Flows.Ends = []types.ParamIndex{1}
		}
		p0 := k.tab.Param(0)
		s := k.sig("Wrap", []types.Param{k.param("o", k.tab.Mut(p0))}, types.Void, types.EmptyErrors)
		s.Generics = []types.GenericParam{{Name: k.in.Intern("T")}}
		s.Flows.Ends = []types.ParamIndex{1}
		s.Span = source.Span{File: kitFile, Start: 3, End: 4}
		m := k.body(s)
		m.b.GenericCount = 1
		d := m.temp(types.Void)
		nx := m.block()
		m.term(mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallInterface, Iface: rs, Method: 0, SelfType: p0},
			Args: m.b.AddArgs(argReborrow(m.local(1))), Dest: m.local(d), Target: nx, Spawn: mir.NoLocal})
		m.at(nx)
		m.end(d)
		m.ret(mir.ExitNormal)
		m.validate(t)
		env := k.env(s)
		env.Method = func(it types.ItemID, i uint32) *types.Signature {
			if it == rs && i == 0 {
				return &reset
			}
			return nil
		}
		if n := e1102Count(t, Check(m.b, env)); (n == 1) == ends || n > 1 {
			t.Fatalf("ends=%v: %d E1102", ends, n)
		}
	}
}

// TestStoreCasesTotal: every loan kind stored into an abstract node
// (`*out = v`, out: mut &i32) meets exactly one case of the store rule. Each
// row is one body with q: &i32 and n: Point parameters:
//
//	static    *out = &G            (global G: i32)          ok
//	abstract  *out = q             q not declared into out  E0604
//	declared  *out = q             q: &i32 into out         ok
//	inner     *out = *out          out's own inner loan      ok
//	local     x := 1; *out = &x                             E0505 at &x
//	param     *out = &n.x          by-value storage          E0505 at &n.x
//	temp      *out = &MakePoint().x                         E0506 at the borrow
//	deref     *out = &(*q)         through a deref, q into out: no target, ok
func TestStoreCasesTotal(t *testing.T) {
	type row struct {
		name  string
		build func(k *kit, m *mb, out, q, n mir.LocalID) (diag.Code, mir.SpanID)
		into  bool
	}
	ri := func(k *kit) types.TypeID { return refI32(k) }
	store := func(m *mb, out mir.LocalID, r mir.LocalID) {
		m.assign(m.pl(out, deref(ri(m.k))), use(cp(m.local(r))))
	}
	rows := []row{
		{"static", func(k *kit, m *mb, out, q, n mir.LocalID) (diag.Code, mir.SpanID) {
			g := k.items.AddItem(&types.Item{Module: k.main.ID, Name: k.in.Intern("G"), Kind: types.KindGlobal,
				Key: types.ItemKey("main", "G"), Decl: &types.GlobalDecl{Type: types.I32}})
			r := m.temp(ri(k))
			m.set(r, mir.Rvalue{Kind: mir.RvGlobalRef, Item: g})
			store(m, out, r)
			m.end(r)
			return 0, 0
		}, false},
		{"abstract", func(k *kit, m *mb, out, q, n mir.LocalID) (diag.Code, mir.SpanID) {
			return diag.E0604, m.assign(m.pl(out, deref(ri(k))), use(cp(m.local(q))))
		}, false},
		{"declared", func(k *kit, m *mb, out, q, n mir.LocalID) (diag.Code, mir.SpanID) {
			m.assign(m.pl(out, deref(ri(k))), use(cp(m.local(q))))
			return 0, 0
		}, true},
		{"inner", func(k *kit, m *mb, out, q, n mir.LocalID) (diag.Code, mir.SpanID) {
			r := m.temp(ri(k))
			m.set(r, use(cp(m.pl(out, deref(ri(k))))))
			store(m, out, r)
			m.end(r)
			return 0, 0
		}, false},
		{"local", func(k *kit, m *mb, out, q, n mir.LocalID) (diag.Code, mir.SpanID) {
			x := m.user("x", types.I32, false)
			m.set(x, use(imm(types.I32, 1)))
			r := m.temp(ri(k))
			s := m.set(r, ref(m.local(x)))
			store(m, out, r)
			m.end(r, x)
			return diag.E0505, s
		}, false},
		{"param", func(k *kit, m *mb, out, q, n mir.LocalID) (diag.Code, mir.SpanID) {
			r := m.temp(ri(k))
			s := m.set(r, ref(m.pl(n, fld(0, types.I32))))
			store(m, out, r)
			m.end(r)
			return diag.E0505, s
		}, false},
		{"temp", func(k *kit, m *mb, out, q, n mir.LocalID) (diag.Code, mir.SpanID) {
			tp := m.temp(k.Point)
			nx := m.block()
			m.call(k.item("MakePoint"), nil, nil, m.local(tp), nx)
			m.at(nx)
			r := m.temp(ri(k))
			s := m.set(r, ref(m.pl(tp, fld(0, types.I32))))
			store(m, out, r)
			m.end(r, tp)
			return diag.E0506, s
		}, false},
		{"deref", func(k *kit, m *mb, out, q, n mir.LocalID) (diag.Code, mir.SpanID) {
			r := m.temp(ri(k))
			m.set(r, ref(m.pl(q, deref(types.I32))))
			store(m, out, r)
			m.end(r)
			return 0, 0
		}, true},
	}
	for _, rw := range rows {
		t.Run(rw.name, func(t *testing.T) {
			k := newKit(t)
			q := k.param("q", refI32(k))
			if rw.into {
				q.Into = 2
			}
			s := k.sig("Keep", []types.Param{q, k.param("out", k.tab.Mut(refI32(k))), k.param("n", k.Point)}, types.Void, types.EmptyErrors)
			m := k.body(s)
			code, at := rw.build(k, m, 2, 1, 3)
			m.ret(mir.ExitNormal)
			if code == 0 {
				m.expect(t, m.check(t))
			} else {
				m.expect(t, m.check(t), code, at)
			}
		})
	}
}

// Grow(list: mut List[int]): OutOfMemory!void { try
// list.Push(list.Len()) }: ok. The receiver borrow of Len, &(*list), is
// formed, read and ended before the reborrow of list for Push (borrow
// operands are formed after all others).
func TestR13_Grow(t *testing.T) {
	k := newKit(t)
	k.lib()
	p0 := k.tab.Param(0)
	g := []types.GenericParam{{Name: k.in.Intern("T")}}
	lt := k.named("List", p0)
	k.declare("List.Len", types.MemberKey("std.core", "List", "Len"), types.ByShared, g,
		[]types.Param{{Name: k.in.Intern("self"), Type: k.tab.Ref(lt)}}, types.Int, types.EmptyErrors)
	k.declare("List.Push", types.MemberKey("std.core", "List", "Push"), types.ByMut, g,
		[]types.Param{{Name: k.in.Intern("self"), Type: k.tab.Mut(lt)}, {Name: k.in.Intern("x"), Type: p0, Into: 1}},
		types.Void, k.errs("OutOfMemory"))
	li := k.named("List", types.Int)
	s := k.sig("Grow", []types.Param{k.param("list", k.tab.Mut(li))}, types.Void, k.errs("OutOfMemory"))
	m := k.body(s)
	rcv := m.temp(k.tab.Ref(li))
	n := m.temp(types.Int)
	m.set(rcv, ref(m.pl(1, deref(li))))
	nx := m.block()
	m.call(k.item("List.Len"), []types.TypeID{types.Int}, []mir.Arg{argCopy(m.local(rcv))}, m.local(n), nx)
	m.at(nx)
	m.end(rcv)
	ok, bad := m.block(), m.block()
	e := m.temp(k.tab.ErrorSetType(k.errs("OutOfMemory")))
	d := m.temp(types.Void)
	m.callErr(k.item("List.Push"), []types.TypeID{types.Int}, []mir.Arg{argReborrow(m.local(1)), argCopy(m.local(n))},
		m.local(d), ok, mir.EdgeTry, bad, e)
	m.at(bad)
	m.set(0, m.agg(mir.AggErr, types.NoItem, m.b.Locals[0].Type, cp(m.local(e))))
	m.end(d, e, n)
	m.ret(mir.ExitTry)
	m.at(ok)
	m.end(d, e, n)
	m.okVoid()
	m.expect(t, m.check(t))
}

// StillHolds(a: &Arena) { var t := Text.New(a); h := Holder(name:
// t.Str()); n := ReadOut(&h); t.Clear() // <- E0501
// ; Use(&n) }, with struct Holder { name: str } and ReadOut(h: &Holder):
// str returning h.name (automatic `from h` behind: the str is a copy of what
// h holds). n holds the loan of t through h, so t.Clear()'s mut t conflicts.
// Without the final Use(&n) n is dead and the Clear is fine.
func TestR8_StillHolds(t *testing.T) {
	for _, useAfter := range []bool{true, false} {
		k := newKit(t)
		text := k.named("Text")
		holder := k.structItem(k.main, "Holder", 0, false, false, field("name", types.Str))
		ht := k.tab.Named(holder, nil)
		tn := k.fn("Text.New", []types.Param{k.param("a", k.tab.Ref(k.Arena))}, text, types.EmptyErrors)
		tn.Flows.Sources = []types.Source{{Param: 1}}
		ts := k.method("Text", "Str", types.ByShared, text, nil, types.Str, types.EmptyErrors)
		ts.Flows.Sources = []types.Source{{Param: 1}}
		k.method("Text", "Clear", types.ByMut, text, nil, types.Void, types.EmptyErrors)
		ro := k.fn("ReadOut", []types.Param{k.param("h", k.tab.Ref(ht))}, types.Str, types.EmptyErrors)
		ro.Flows.Sources = []types.Source{{Param: 1, Behind: true}}
		m := k.body(k.sig("StillHolds", []types.Param{k.param("a", k.tab.Ref(k.Arena))}, types.Void, types.EmptyErrors))
		tl := m.user("t", text, true)
		h := m.user("h", ht, false)
		n := m.user("n", types.Str, false)
		nx := m.block()
		m.call(tn.Item, nil, []mir.Arg{argCopy(m.local(1))}, m.local(tl), nx)
		m.at(nx)
		rt := m.temp(k.tab.Ref(text))
		str := m.temp(types.Str)
		m.set(rt, ref(m.local(tl)))
		nx = m.block()
		m.call(ts.Item, nil, []mir.Arg{argCopy(m.local(rt))}, m.local(str), nx)
		m.at(nx)
		m.set(h, m.agg(mir.AggStruct, holder, ht, cp(m.local(str))))
		m.end(str, rt)
		rh := m.temp(k.tab.Ref(ht))
		m.set(rh, ref(m.local(h)))
		nx = m.block()
		m.call(ro.Item, nil, []mir.Arg{argCopy(m.local(rh))}, m.local(n), nx)
		m.at(nx)
		m.end(rh)
		mt := m.temp(k.tab.Mut(text))
		bad := m.set(mt, mutRef(m.local(tl)))
		m.callv("Text.Clear", nil, argMove(m.local(mt)))
		m.end(mt)
		if useAfter {
			m.useRef(m.local(n), types.Str)
		}
		m.end(n, h, tl)
		m.ret(mir.ExitNormal)
		if useAfter {
			m.expect(t, m.check(t), diag.E0501, bad)
		} else {
			m.expect(t, m.check(t))
		}
	}
}
