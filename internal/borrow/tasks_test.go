// Why: spawned calls, scopes, globals and `ends` each change how long a loan
// lives, and the errors at their edges are easy to miss.
// What: tests of spawned calls, joins, globals, `ends` and mut loops, with
// the taskLib, openScope, spawn and join builders.
// How: hand-built scope bodies with s.go calls, checked for E0504, E0505,
// E0503, E1102, E0605 and E0507.

package borrow

import (
	"testing"

	"mk/internal/diag"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// taskLib declares Bump(out: mut i32) and NewScope(): Scope (the binding of
// `scope s := io.Scope()`).
func (k *kit) taskLib() {
	if _, ok := k.byNm["Bump"]; ok {
		return
	}
	k.fn("Bump", []types.Param{k.param("out", k.tab.Mut(types.I32))}, types.Void, types.EmptyErrors)
	k.fn("NewScope", nil, k.Scope, types.EmptyErrors)
}

// openScope binds a scope local and continues in a new block.
func (m *mb) openScope() mir.LocalID {
	s := m.scopeLocal(m.k.Scope)
	nx := m.block()
	m.call(m.k.item("NewScope"), nil, nil, m.local(s), nx)
	m.at(nx)
	return s
}

// spawn appends `s.go fn(args)` and continues in a new block.
func (m *mb) spawn(s mir.LocalID, fn string, args ...mir.Arg) mir.SpanID {
	nx := m.block()
	d := m.temp(types.Void)
	sp := m.call(m.k.item(fn), nil, args, m.local(d), nx)
	m.b.Blocks[m.cur].Term.Spawn = s
	m.at(nx)
	return sp
}

func (m *mb) join(s mir.LocalID) {
	blk := &m.b.Blocks[m.cur]
	blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StJoin, Local: s, Span: m.sp()})
}

// Peek(io: Io) { var n: i32 = 0; scope s := io.Scope() { s.go Bump(mut n);
// Use(&n) // <- E0504 } }; and after the scope, Use(&n) is fine.
func TestR17_Peek(t *testing.T) {
	k := newKit(t)
	k.taskLib()
	m := k.body(k.sig("Peek", []types.Param{k.param("io", k.Io)}, types.Void, types.EmptyErrors))
	n := m.user("n", types.I32, true)
	m.set(n, use(imm(types.I32, 0)))
	s := m.openScope()
	tm := m.temp(k.tab.Mut(types.I32))
	m.set(tm, mutRef(m.local(n)))
	m.spawn(s, "Bump", argMove(m.local(tm)))
	m.end(tm)
	rn := m.temp(refI32(k))
	bad := m.set(rn, ref(m.local(n)))
	m.callv("Use", []types.TypeID{types.I32}, argCopy(m.local(rn)))
	m.end(rn)
	m.join(s)
	m.end(s)
	m.useRef(m.local(n), types.I32)
	m.end(n)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0504, bad)
}

// Parallel(io: Io, a: []i32, b: []i32): i64 with two spawns of Sum into two
// locals, read after the scope: ok.
func TestR17_Parallel(t *testing.T) {
	k := newKit(t)
	k.taskLib()
	si := k.tab.Slice(types.I32)
	k.fn("Sum", []types.Param{k.param("xs", si), k.param("out", k.tab.Mut(types.I64))}, types.Void, types.EmptyErrors)
	m := k.body(k.sig("Parallel", []types.Param{k.param("io", k.Io), k.param("a", si), k.param("b", si)}, types.I64, types.EmptyErrors))
	left := m.user("left", types.I64, true)
	right := m.user("right", types.I64, true)
	m.set(left, use(imm(types.I64, 0)))
	m.set(right, use(imm(types.I64, 0)))
	s := m.openScope()
	ml := m.temp(k.tab.Mut(types.I64))
	m.set(ml, mutRef(m.local(left)))
	m.spawn(s, "Sum", argCopy(m.local(2)), argMove(m.local(ml)))
	m.end(ml)
	mr := m.temp(k.tab.Mut(types.I64))
	m.set(mr, mutRef(m.local(right)))
	m.spawn(s, "Sum", argCopy(m.local(3)), argMove(m.local(mr)))
	m.end(mr)
	m.join(s)
	m.end(s)
	m.set(0, mir.Rvalue{Kind: mir.RvBinary, Bin: mir.Add, A: cp(m.local(left)), B: cp(m.local(right))})
	m.end(right, left)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t))
}

// Helper(s: &Scope, x: mut i32) { s.go Bump(x) // <- E0604 }: a helper's
// arguments need `into s`; with it, ok.
func TestR17_Helper(t *testing.T) {
	for _, into := range []bool{false, true} {
		k := newKit(t)
		k.taskLib()
		x := k.param("x", k.tab.Mut(types.I32))
		if into {
			x.Into = 1
		}
		m := k.body(k.sig("Helper", []types.Param{k.param("s", k.tab.Ref(k.Scope)), x}, types.Void, types.EmptyErrors))
		sp := m.spawn(1, "Bump", argMove(m.local(2)))
		m.ret(mir.ExitNormal)
		if into {
			m.expect(t, m.check(t))
		} else {
			m.expect(t, m.check(t), diag.E0604, sp)
		}
	}
}

// Helper(s: &Scope) { var n: i32 = 0; s.go Bump(mut n) // <- E0505 }.
func TestR17_LocalSpawnedFromHelper(t *testing.T) {
	k := newKit(t)
	k.taskLib()
	m := k.body(k.sig("Helper", []types.Param{k.param("s", k.tab.Ref(k.Scope))}, types.Void, types.EmptyErrors))
	n := m.user("n", types.I32, true)
	m.set(n, use(imm(types.I32, 0)))
	tm := m.temp(k.tab.Mut(types.I32))
	bad := m.set(tm, mutRef(m.local(n)))
	m.spawn(1, "Bump", argMove(m.local(tm)))
	m.end(tm, n)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0505, bad)
}

// Tie: struct Tie { a: Cell[?&Tie] }; Moves() { t := Tie(a: Cell.New(none));
// t.a.Store(some(&t)); Take(t) // <- E0503 } (a self-loan never moves).
func TestR18_Tie(t *testing.T) {
	k := newKit(t)
	k.lib()
	k.cellNew()
	tie := k.items.AddItem(&types.Item{Module: k.main.ID, Name: k.in.Intern("Tie"), Kind: types.KindStruct,
		Key: types.ItemKey("main", "Tie")})
	tieT := k.tab.Named(tie, nil)
	ort := k.tab.Optional(k.tab.Ref(tieT))
	cellT := k.tab.Named(k.known.Cell, []types.GenericArg{types.TypeArg(ort)})
	k.items.Get(tie).Decl = &types.StructDecl{Fields: []types.FieldDecl{{Name: k.in.Intern("a"), Type: cellT}}}
	m := k.body(k.sig("Moves", nil, types.Void, types.EmptyErrors))
	tv := m.user("t", tieT, false)
	none := m.temp(ort)
	c := m.temp(cellT)
	m.set(none, m.agg(mir.AggNone, types.NoItem, ort))
	nx := m.block()
	m.call(k.item("Cell.New"), []types.TypeID{ort}, []mir.Arg{argCopy(m.local(none))}, m.local(c), nx)
	m.at(nx)
	m.set(tv, m.agg(mir.AggStruct, tie, tieT, mv(m.local(c))))
	m.end(c, none)
	rt := m.temp(k.tab.Ref(tieT))
	sm := m.temp(ort)
	ra := m.temp(k.tab.Ref(cellT))
	m.set(rt, ref(m.local(tv)))
	m.set(sm, m.agg(mir.AggSome, types.NoItem, ort, cp(m.local(rt))))
	m.set(ra, ref(m.pl(tv, fld(0, cellT))))
	m.callv("Cell.Store", []types.TypeID{ort}, argCopy(m.local(ra)), argCopy(m.local(sm)))
	m.end(ra, sm, rt)
	bad := m.callv("Take", []types.TypeID{tieT}, argMove(m.local(tv)))
	m.end(tv)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0503, bad)
}

// ends: struct O { arena: Arena }; O.Clear(mut self) ends self {
// self.arena.Reset() } // <- E1102 in a module that is not unsafe-permitted
// while O.Clear2(mut self) ends self { self.Reset2() } reborrows the
// whole self and is ok; Clear in a permitted module is ok.
func TestFieldEndsIsNoProof(t *testing.T) {
	for _, tc := range []struct {
		name      string
		whole     bool
		permitted bool
		e1102     bool
	}{{"field", false, false, true}, {"whole", true, false, false}, {"permitted", false, true, false}} {
		t.Run(tc.name, func(t *testing.T) {
			k := newKit(t)
			k.structItem(k.main, "O", 0, false, false, field("arena", k.Arena))
			ot := k.named("O")
			reset := k.method("Arena", "Reset", types.ByMut, k.Arena, nil, types.Void, types.EmptyErrors)
			reset.Flows.Ends = []types.ParamIndex{1}
			reset2 := k.method("O", "Reset2", types.ByMut, ot, nil, types.Void, types.EmptyErrors)
			reset2.Flows.Ends = []types.ParamIndex{1}
			s := k.method("O", "Clear", types.ByMut, ot, nil, types.Void, types.EmptyErrors)
			s.Flows.Ends = []types.ParamIndex{1}
			s.Span = source.Span{File: kitFile, Start: 3, End: 4}
			m := k.body(s)
			if tc.whole {
				m.callv("O.Reset2", nil, argReborrow(m.local(1)))
			} else {
				ta := m.temp(k.tab.Mut(k.Arena))
				m.set(ta, mutRef(m.pl(1, deref(ot), fld(0, k.Arena))))
				m.callv("Arena.Reset", nil, argMove(m.local(ta)))
				m.end(ta)
			}
			m.ret(mir.ExitNormal)
			m.validate(t)
			env := k.env(s)
			if tc.permitted {
				env.Module = &types.Module{Path: "main", UnsafePermitted: true}
			}
			res := Check(m.b, env)
			n := 0
			for _, d := range res.Diags {
				if d.Code == diag.E1102 {
					n++
					if d.Phase != diag.PhaseFlow {
						t.Fatalf("E1102 phase %v, want flow", d.Phase)
					}
				}
			}
			if (n == 1) != tc.e1102 || len(res.Diags) != n {
				t.Fatalf("diagnostics %v, want E1102 %v", got(res), tc.e1102)
			}
		})
	}
}

// Stash(io: Io): Canceled!void with var Latest: Mutex[?&i32]: storing
// some(&n) through the guard into the global is E0605. The guard
// is modelled as a local Guard view holding the global's static loan.
func TestR16_Stash(t *testing.T) {
	k := newKit(t)
	ori := k.tab.Optional(refI32(k))
	mt := k.tab.Named(k.known.Mutex, []types.GenericArg{types.TypeArg(ori)})
	latest := k.items.AddItem(&types.Item{Module: k.main.ID, Name: k.in.Intern("Latest"), Kind: types.KindGlobal,
		Key: types.ItemKey("main", "Latest"), Decl: &types.GlobalDecl{Type: mt}})
	gt := k.named("Guard", ori)
	p0 := k.tab.Param(0)
	g := []types.GenericParam{{Name: k.in.Intern("T")}}
	lock := k.declare("Mutex.Lock", types.MemberKey("std.core", "Mutex", "Lock"), types.ByShared, g,
		[]types.Param{{Name: k.in.Intern("self"), Type: k.tab.Ref(k.tab.Named(k.known.Mutex, []types.GenericArg{types.TypeArg(p0)}))}},
		k.named("Guard", p0), types.EmptyErrors)
	lock.Flows.Sources = []types.Source{{Param: 1}}
	k.declare("Guard.Store", types.MemberKey("std.core", "Guard", "Store"), types.ByMut, g,
		[]types.Param{{Name: k.in.Intern("self"), Type: k.tab.Mut(k.named("Guard", p0))}, {Name: k.in.Intern("x"), Type: p0, Into: 1}},
		types.Void, types.EmptyErrors)
	k.declare("Guard.Unlock", types.MemberKey("std.core", "Guard", "Unlock"), types.ByValue, g,
		[]types.Param{{Name: k.in.Intern("self"), Type: k.named("Guard", p0)}}, types.Void, types.EmptyErrors)
	m := k.body(k.sig("Stash", nil, types.Void, types.EmptyErrors))
	n := m.user("n", types.I32, false)
	gl := m.user("g", gt, true)
	m.set(n, use(imm(types.I32, 1)))
	tg := m.temp(k.tab.Ref(mt))
	m.set(tg, mir.Rvalue{Kind: mir.RvGlobalRef, Item: latest})
	nx := m.block()
	m.call(lock.Item, []types.TypeID{ori}, []mir.Arg{argCopy(m.local(tg))}, m.local(gl), nx)
	m.at(nx)
	m.end(tg)
	rn := m.temp(refI32(k))
	sm := m.temp(ori)
	mg := m.temp(k.tab.Mut(gt))
	m.set(rn, ref(m.local(n)))
	m.set(sm, m.agg(mir.AggSome, types.NoItem, ori, cp(m.local(rn))))
	m.set(mg, mutRef(m.local(gl)))
	bad := m.callv("Guard.Store", []types.TypeID{ori}, argMove(m.local(mg)), argCopy(m.local(sm)))
	m.end(mg, sm, rn)
	m.callv("Guard.Unlock", []types.TypeID{ori}, argMove(m.local(gl)))
	m.end(gl, n)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0605, bad)
}

// A write to a var global goes through *(&G), a shared step: E0507.
func TestGlobalWritesAreE0507(t *testing.T) {
	k := newKit(t)
	gi := k.items.AddItem(&types.Item{Module: k.main.ID, Name: k.in.Intern("G"), Kind: types.KindGlobal,
		Key: types.ItemKey("main", "G"), Decl: &types.GlobalDecl{Type: types.I32}})
	m := k.body(k.sig("F", nil, types.Void, types.EmptyErrors))
	tg := m.temp(refI32(k))
	m.set(tg, mir.Rvalue{Kind: mir.RvGlobalRef, Item: gi})
	bad := m.assign(m.pl(tg, deref(types.I32)), use(imm(types.I32, 1)))
	m.end(tg)
	m.ret(mir.ExitNormal)
	m.expect(t, m.check(t), diag.E0507, bad)
}
