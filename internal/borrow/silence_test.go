// Why: check's codes stay check's, so a body built as if check had passed it
// must get no borrow diagnostic and no ICE.
// What: those silence tests, and tests of an interface call's flows through
// Env.Method, a carrier const's RvConstRef and a TermPanic message operand.
// How: hand-built bodies of the check-only cases, checked for no output.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/types"
)

// Dup() { var n := 0; arr := [mut n; 2]; Use(&arr) }: the E0408 is
// check's; borrow neither reports the repeat nor raises an ICE.
func TestRvRepeatNonPlainSilent(t *testing.T) {
	k := newKit(t)
	mi := k.tab.Mut(types.I32)
	arrT := k.tab.Array(k.lenArg(2), mi)
	m := k.body(k.sig("Dup", nil, types.Void, types.EmptyErrors))
	n := m.user("n", types.I32, true)
	m.set(n, use(imm(types.I32, 0)))
	tm := m.temp(mi)
	m.set(tm, mutRef(m.local(n)))
	arr := m.user("arr", arrT, false)
	m.set(arr, mir.Rvalue{Kind: mir.RvRepeat, A: mv(m.local(tm)), Len: k.lenArg(2)})
	m.useRef(m.local(arr), arrT)
	m.end(arr, tm, n)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// Racy(io: Io) { c := Cell[i32].New(0); scope s := io.Scope() { s.go
// Count(&c) } }: the verdict is check's one E1002 (the .go argument is not
// Send); borrow adds nothing, since c outlives the scope.
func TestR17_RacyBorrowSilent(t *testing.T) {
	k := newKit(t)
	k.taskLib()
	ci := k.tab.Named(k.known.Cell, []types.GenericArg{types.TypeArg(types.I32)})
	k.fn("Count", []types.Param{k.param("c", k.tab.Ref(ci))}, types.Void, types.EmptyErrors)
	k.fn("NewCell", nil, ci, types.EmptyErrors)
	m := k.body(k.sig("Racy", []types.Param{k.param("io", k.Io)}, types.Void, types.EmptyErrors))
	c := m.user("c", ci, false)
	nx := m.block()
	m.call(k.item("NewCell"), nil, nil, m.local(c), nx)
	m.at(nx)
	s := m.openScope()
	r := m.temp(k.tab.Ref(ci))
	m.set(r, ref(m.local(c)))
	m.spawn(s, "Count", argCopy(m.local(r)))
	m.end(r)
	m.join(s)
	m.end(s, c)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// TestNoSharedCodeEmitted: none of check's E0408, E0807, E1001 and E1002 is
// in borrow's code list (TestCodeTable ties that list to the source),
// and the bodies of check's E0408 and E1002 cases get no borrow diagnostic
// (TestRvRepeatNonPlainSilent, TestR17_RacyBorrowSilent). E0807's case,
// Two() { a := Acquire("a"); b := Acquire("b"); onerr { a.Close();
// b.Close() } }, never reaches lower; its two values consumed on the normal
// path give borrow nothing to say.
func TestNoSharedCodeEmitted(t *testing.T) {
	for _, c := range borrowCodes {
		switch c {
		case diag.E0408, diag.E0807, diag.E1001, diag.E1002:
			t.Errorf("%v is check's, not borrow's", c)
		}
	}
	k := newKit(t)
	m := k.body(k.sig("Two", nil, types.Void, types.EmptyErrors))
	a, b := m.user("a", k.Res, false), m.user("b", k.Res, false)
	m.acquire(a)
	m.acquire(b)
	m.close(b)
	m.close(a)
	m.end(b, a)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// Sink is `interface Sink { Put(&self, v: &i32 into self) }`; F[T: Sink](c:
// &T, v: &i32) { c.Put(v) // <- E0604 }: the interface call's flows come
// from Env.Method, so the store of v into c is undeclared; with the
// clause `v: &i32 into c` it is ok.
func TestCallInterfaceFlowsFromMethod(t *testing.T) {
	for _, declared := range []bool{false, true} {
		k := newKit(t)
		ri := refI32(k)
		sink := k.items.AddItem(&types.Item{Module: k.main.ID, Name: k.in.Intern("Sink"), Kind: types.KindInterface,
			Key: types.ItemKey("main", "Sink"), Decl: &types.InterfaceDecl{}})
		put := types.Signature{Name: k.in.Intern("Put"), Receiver: types.ByShared,
			Params: []types.Param{{Name: k.in.Intern("self"), Type: k.tab.Ref(k.tab.SelfType())}, {Name: k.in.Intern("v"), Type: ri, Into: 1}},
			Result: types.Void, Errors: types.EmptyErrors}
		p0 := k.tab.Param(0)
		v := k.param("v", ri)
		if declared {
			v.Into = 1
		}
		s := k.sig("F", []types.Param{k.param("c", k.tab.Ref(p0)), v}, types.Void, types.EmptyErrors)
		s.Generics = []types.GenericParam{{Name: k.in.Intern("T")}}
		m := k.body(s)
		m.b.GenericCount = 1
		d := m.temp(types.Void)
		nx := m.block()
		bad := m.term(mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallInterface, Iface: sink, Method: 0, SelfType: p0},
			Args: m.b.AddArgs(argCopy(m.local(1)), argCopy(m.local(2))), Dest: m.local(d), Target: nx, Spawn: mir.NoLocal})
		m.at(nx)
		m.end(d)
		m.ret(mir.ExitNormal)
		m.validate(t)
		env := k.env(s)
		env.Method = func(it types.ItemID, i uint32) *types.Signature {
			if it == sink && i == 0 {
				return &put
			}
			return nil
		}
		res := Check(m.b, env)
		if declared {
			m.expect(t, res)
		} else {
			m.expect(t, res, diag.E0604, bad)
		}
	}
}

// Name(): str { return Greeting } with const Greeting: str: ok, the const's
// value holds its static node (a carrier const, RvConstRef), which
// passes the boundary.
func TestRvConstRefOfCarrierConst(t *testing.T) {
	k := newKit(t)
	g := k.items.AddItem(&types.Item{Module: k.main.ID, Name: k.in.Intern("Greeting"), Kind: types.KindConst,
		Key: types.ItemKey("main", "Greeting"), Decl: &types.ConstDecl{Type: types.Str}})
	m := k.body(k.sig("Name", nil, types.Str, types.EmptyErrors))
	m.set(0, mir.Rvalue{Kind: mir.RvConstRef, Item: g})
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// F() { msg: str; panic(msg) // <- E0403 }: a TermPanic message is a use.
func TestTermPanicMessageIsUse(t *testing.T) {
	k := newKit(t)
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	msg := m.user("msg", types.Str, false)
	bad := m.term(mir.Terminator{Kind: mir.TermPanic, Panic: types.PanicExplicit, HasMsg: true, Msg: cp(m.local(msg))})
	m.expect(t, m.check(t), diag.E0403, bad)
}
