// Why: std's unsafe code and its allocator run at compile time through
// intrinsics and overrides, and anything the evaluator cannot model must be
// refused with E0901, never miscomputed.
// What: tests of intrinsics, overrides, refusals and EvalArena, against std
// keys declared in the test world: raw pointer rules, pointer comparison,
// zeroed padding, and arena memory in results.
// How: hand-built bodies call std.raw keys as bodiless intrinsics; arena pages
// come from the overridden PageAlloc and PageFree.

package eval

import (
	"sync"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// raw declares a bodiless std intrinsic std.raw.<name>.
func (w *world) raw(name string, result types.TypeID, params ...types.TypeID) types.ItemID {
	if f, ok := w.items.ByKey(types.ItemKey("std.raw", name)); ok {
		return f
	}
	f := w.fn("std.raw", name, result, params...)
	fd := w.items.Get(f).Decl.(*types.FuncDecl)
	fd.Bodiless, fd.Intrinsic = true, true
	return f
}

// callG terminates blk with fn[targs](args) into dest, then next.
func (x *bb) callG(blk mir.BlockID, fn types.ItemID, targs []types.TypeID, dest mir.Place, next mir.BlockID, args ...mir.Operand) mir.SpanID {
	var ga []types.GenericArg
	for _, t := range targs {
		ga = append(ga, types.TypeArg(t))
	}
	var as []mir.Arg
	for _, a := range args {
		as = append(as, mir.Arg{Operand: a})
	}
	return x.term(blk, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: fn, Args: x.b.AddGenericArgs(ga...)}},
		Args: x.b.AddArgs(as...), Dest: dest, Target: next, Spawn: mir.NoLocal})
}

// seq is a body under construction as a chain of blocks, one call each.
type seq struct {
	*bb
	cur mir.BlockID
}

func (s *seq) call(fn types.ItemID, targs []types.TypeID, dest mir.Place, args ...mir.Operand) mir.SpanID {
	next := s.block()
	sp := s.callG(s.cur, fn, targs, dest, next, args...)
	s.cur = next
	return sp
}

func wantRefusal(t *testing.T, r types.EvalResult, want types.Refusal) {
	t.Helper()
	if r.Err == nil || r.Err.Kind != types.EvalRefused || r.Err.Refusal != want {
		t.Fatalf("got %+v, want refused %v", r.Err, want)
	}
}

func TestE0901_RefusedIo(t *testing.T) {
	w := newWorld(t)
	io, iot := w.structType("std.core", "Io", types.Int, types.Int)
	w.known.Io = io
	useIo := w.fn("lib", "UseIo", types.Int, iot)
	x := w.body(useIo, types.Int, iot)
	x.assign(0, lp(0), use(imm(types.Int, 1)))
	x.ret(0)
	x.done()
	w.forbidBody[useIo] = true
	caller := w.fn("lib", "Caller", types.Int)
	y := w.body(caller, types.Int)
	v := y.local(iot)
	b1 := y.block()
	y.assign(0, lp(v), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: io, Ops: y.b.AddOperands(imm(types.Int, 0), imm(types.Int, 0))})
	y.call(0, useIo, lp(0), b1, cp(v))
	y.ret(b1)
	y.done()
	wantRefusal(t, w.call(w.evaluator(0), caller, 0), types.RefuseIo)
	// A request whose callee takes Io is refused before anything runs.
	r := w.call(w.evaluator(0), useIo, 0, w.consts.Intern(types.ConstValue{Type: iot, Align: 8, Bytes: make([]byte, 16)}))
	wantRefusal(t, r, types.RefuseIo)
	if r.Steps != 0 {
		t.Errorf("refused request ran %d steps", r.Steps)
	}
}

func TestE0901_RefusedSyscall(t *testing.T) {
	w := newWorld(t)
	sc := w.fn("std.sys", "Syscall", types.Int, types.Int)
	fd := w.items.Get(sc).Decl.(*types.FuncDecl)
	fd.Bodiless, fd.Intrinsic = true, true
	f := w.fn("lib", "F", types.Int)
	x := w.body(f, types.Int)
	b1 := x.block()
	x.call(0, sc, lp(0), b1, imm(types.Int, 60))
	x.ret(b1)
	x.done()
	wantRefusal(t, w.call(w.evaluator(0), f, 0), types.RefuseSyscall)
}

func TestE0901_RefusedExtern(t *testing.T) {
	w := newWorld(t)
	ext := w.fn("lib", "puts", types.Int)
	fd := w.items.Get(ext).Decl.(*types.FuncDecl)
	fd.Bodiless, fd.Sig.Extern = true, true
	f := w.fn("lib", "F", types.Int)
	x := w.body(f, types.Int)
	b1 := x.block()
	x.call(0, ext, lp(0), b1)
	x.ret(b1)
	x.done()
	wantRefusal(t, w.call(w.evaluator(0), f, 0), types.RefuseExtern)
	// An intrinsic eval does not know is an ICE, not a refusal.
	unk := w.raw("Unknown", types.Int)
	g := w.fn("lib", "G", types.Int)
	y := w.body(g, types.Int)
	b2 := y.block()
	y.call(0, unk, lp(0), b2)
	y.ret(b2)
	y.done()
	if msg := iceOf(func() { w.call(w.evaluator(0), g, 0) }); msg == "" {
		t.Errorf("unknown intrinsic is not an ICE")
	}
}

func TestE0901_RefusedGlobal(t *testing.T) {
	w := newWorld(t)
	g := w.item("main", "Counter", types.KindGlobal, &types.GlobalDecl{Type: types.Int})
	ref := w.tab.Ref(types.Int)
	read := w.constItem("main", "ReadIt", types.Int, 0)
	x := w.body(read, types.Int)
	p := x.local(ref)
	x.assign(0, lp(p), mir.Rvalue{Kind: mir.RvGlobalRef, Item: g})
	x.assign(0, lp(0), use(mir.Copy(x.b.Place(p, mir.Projection{Kind: mir.ProjDeref, Type: types.Int}))))
	x.ret(0)
	x.done()
	e := w.evaluator(0)
	wantRefusal(t, e.Eval(constReq(w, read, types.StageBodies)), types.RefuseGlobal)
	// Taking the address is allowed; a result holding it is refused.
	keep := w.constItem("main", "Keep", ref, 0)
	y := w.body(keep, ref)
	y.assign(0, lp(0), mir.Rvalue{Kind: mir.RvGlobalRef, Item: g})
	y.ret(0)
	y.done()
	wantRefusal(t, e.Eval(constReq(w, keep, types.StageBodies)), types.RefuseGlobal)
}

func TestRawCopyOfPartialPointer(t *testing.T) {
	w := newWorld(t)
	pu8 := w.tab.RawPtr(types.U8)
	ppu8 := w.tab.RawPtr(pu8)
	buf := w.array(8, types.U8)
	of := w.raw("Of", pu8, w.tab.Ref(types.U8))
	ofMut := w.raw("OfMut", pu8, w.tab.Mut(types.U8))
	cast := w.raw("PtrCast", pu8, pu8)
	cpy := w.raw("Copy", types.Void, pu8, pu8, types.Int)
	read := w.raw("Read", types.U64, pu8)
	for _, readT := range []types.TypeID{pu8, types.U64} {
		f := w.fn("lib", "Cut"+w.tab.TypeString(readT, nil), readT)
		x := &seq{bb: w.body(f, readT)}
		xv, px, b := x.local(types.U8), x.local(pu8), x.local(buf)
		rx, ppx, pb, src, dst, pr := x.local(w.tab.Ref(types.U8)), x.local(ppu8), x.local(w.tab.Mut(buf)), x.local(pu8), x.local(pu8), x.local(w.tab.RawPtr(readT))
		x.assign(0, lp(xv), use(imm(types.U8, 9)))
		x.assign(0, lp(b), mir.Rvalue{Kind: mir.RvRepeat, A: imm(types.U8, 0), Len: types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, 8)}})
		x.assign(0, lp(rx), mir.Rvalue{Kind: mir.RvRef, Place: lp(xv)})
		x.call(of, []types.TypeID{types.U8}, lp(px), cp(rx))
		x.assign(x.cur, lp(ppx), mir.Rvalue{Kind: mir.RvRef, Place: lp(px)}) // &px as a raw pointer below
		x.call(cast, []types.TypeID{pu8, types.U8}, lp(src), cp(ppx))
		x.assign(x.cur, lp(pb), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Exclusive, Place: lp(b)})
		x.call(ofMut, []types.TypeID{buf}, lp(dst), cp(pb))
		x.call(cpy, []types.TypeID{types.U8}, mir.LocalPlace(x.local(types.Void)), cp(dst), cp(src), imm(types.Int, 4))
		x.call(cast, []types.TypeID{types.U8, readT}, lp(pr), cp(dst))
		at := x.call(read, []types.TypeID{readT}, lp(0), cp(pr))
		x.ret(x.cur)
		// The test MIR is loose about raw pointer types (std.raw is generic);
		// it skips validation.
		w.bodies[f] = &Body{MIR: x.b, State: BodyReady}
		r := w.call(w.evaluator(0), f, 0)
		wantRefusal(t, r, types.RefusePointerBytes)
		if r.Err.At != x.b.Span(at) {
			t.Errorf("Read[%s] refused at %v, want the read", w.tab.TypeString(readT, nil), r.Err.At)
		}
	}
}

// arenaWorld declares a test std: PageAlloc and PageFree (overridden), an
// arena record over one page, EvalArena's function, and returns the arena
// record type.
func (w *world) arenaWorld() (types.TypeID, types.ItemID) {
	pu8 := w.tab.RawPtr(types.U8)
	w.pageAlloc = w.fn("std.rt", "PageAlloc", pu8, types.Int)
	w.pageFree = w.fn("std.rt", "PageFree", types.Void, pu8, types.Int)
	_, rec := w.structType("std.core", "Arena", pu8, types.Int)
	ref := w.tab.Ref(rec)
	prec := w.tab.RawPtr(rec)
	cast := w.raw("PtrCast", pu8, pu8)
	write := w.raw("Write", types.Void, prec, rec)
	mk := w.fn("std.core", "MakeEvalArena", ref)
	x := &seq{bb: w.body(mk, ref)}
	page, hdr, ph, v := x.local(pu8), x.local(pu8), x.local(prec), x.local(rec)
	x.call(w.pageAlloc, nil, lp(page), imm(types.Int, 4096))
	x.call(w.pageAlloc, nil, lp(hdr), imm(types.Int, 16))
	x.call(cast, []types.TypeID{types.U8, rec}, lp(ph), cp(hdr))
	x.assign(x.cur, lp(v), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: w.tab.Get(rec).Item, Ops: x.b.AddOperands(cp(page), imm(types.Int, 0))})
	x.call(write, []types.TypeID{rec}, mir.LocalPlace(x.local(types.Void)), cp(ph), cp(v))
	x.assign(x.cur, lp(0), mir.Rvalue{Kind: mir.RvRef, Place: x.b.Place(ph, mir.Projection{Kind: mir.ProjDeref, Type: rec})})
	x.ret(x.cur)
	w.bodies[mk] = &Body{MIR: x.b, State: BodyReady}
	w.evalArena = mk
	return rec, mk
}

// buildStr declares BuildStr(a: &Arena, n: int): str writing 'a'+i at
// page[i] for i < n and returning raw.Str(page, n).
func (w *world) buildStr(rec types.TypeID) types.ItemID {
	pu8 := w.tab.RawPtr(types.U8)
	ref := w.tab.Ref(rec)
	off := w.raw("Offset", pu8, pu8, types.Int)
	write := w.raw("Write", types.Void, pu8, types.U8)
	mkStr := w.raw("Str", types.Str, pu8, types.Int)
	f := w.fn("lib", "BuildStr", types.Str, ref, types.Int)
	x := &seq{bb: w.body(f, types.Str, ref, types.Int)}
	base, i, cnd, p, ch, ci := x.local(pu8), x.local(types.Int), x.local(types.Bool), x.local(pu8), x.local(types.U8), x.local(types.Int)
	head, body := x.block(), x.block()
	x.assign(0, lp(base), use(mir.Copy(x.b.Place(1, mir.Projection{Kind: mir.ProjDeref, Type: rec}, mir.Projection{Kind: mir.ProjField, A: 0, Type: pu8}))))
	x.assign(0, lp(i), use(imm(types.Int, 0)))
	x.gotoB(0, head)
	x.assign(head, lp(cnd), bin(mir.Lt, cp(i), cp(2)))
	x.cur = body
	x.call(off, []types.TypeID{types.U8}, lp(p), cp(base), cp(i))
	x.assign(x.cur, lp(ci), bin(mir.Add, cp(i), imm(types.Int, 'a')))
	x.assign(x.cur, lp(ch), mir.Rvalue{Kind: mir.RvCast, Cast: mir.CastChecked, A: cp(ci), Type: types.U8})
	x.call(write, []types.TypeID{types.U8}, mir.LocalPlace(x.local(types.Void)), cp(p), cp(ch))
	x.assign(x.cur, lp(i), bin(mir.Add, cp(i), imm(types.Int, 1)))
	x.gotoB(x.cur, head)
	exit := x.block()
	x.branch(head, cp(cnd), body, exit)
	x.cur = exit
	x.call(mkStr, nil, lp(0), cp(base), cp(2))
	x.ret(x.cur)
	w.bodies[f] = &Body{MIR: x.b, State: BodyReady}
	return f
}

// strFromArena declares const name: str = BuildStr(EvalArena(), n).
func (w *world) strFromArena(name string, build types.ItemID, rec types.TypeID, n int64) types.ItemID {
	k := w.constItem("main", name, types.Str, 0)
	x := &seq{bb: w.body(k, types.Str)}
	a := x.local(w.tab.Ref(rec))
	next := x.block()
	x.term(x.cur, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallBuiltin, Builtin: mir.BuiltinEvalArena}, Dest: lp(a), Target: next, Spawn: mir.NoLocal})
	x.cur = next
	x.call(build, nil, lp(0), cp(a), imm(types.Int, n))
	x.ret(x.cur)
	x.done()
	return k
}

func TestStrPointeeArrayInterned(t *testing.T) {
	w := newWorld(t)
	rec, _ := w.arenaWorld()
	build := w.buildStr(rec)
	k := w.strFromArena("S", build, rec, 37)
	before := w.tab.Len()
	r := w.evaluator(0).Eval(constReq(w, k, types.StageBodies))
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	v := w.consts.Get(r.Value)
	if len(v.Relocs) != 1 || types.LoadUint(v.Bytes[8:], 8) != 37 {
		t.Fatalf("S = %+v", v)
	}
	p := w.consts.Get(v.Relocs[0].Data)
	if pt := w.tab.Get(p.Type); pt.Kind != types.KArray || pt.Elem != types.U8 || w.intOf(pt.Len.Value) != 37 {
		t.Errorf("pointee type %s, want [37]u8", w.tab.TypeString(p.Type, nil))
	}
	if string(p.Bytes) != "abcdefghijklmnopqrstuvwxyz{|}~\x7f\x80\x81\x82\x83\x84\x85"[:37] {
		t.Errorf("pointee bytes %q", p.Bytes)
	}
	if grew := w.tab.Len() - before; grew != 1 {
		t.Errorf("Types grew by %d, want only [37]u8 (EG-08)", grew)
	}
}

func TestArenaSliceBecomesStatic(t *testing.T) {
	w := newWorld(t)
	rec, _ := w.arenaWorld()
	build := w.buildStr(rec)
	k1 := w.strFromArena("S1", build, rec, 3)
	k2 := w.strFromArena("S2", build, rec, 3)
	e := w.evaluator(0)
	r1 := e.Eval(constReq(w, k1, types.StageBodies))
	r2 := e.Eval(constReq(w, k2, types.StageBodies))
	if r1.Err != nil || r2.Err != nil || r1.Value != r2.Value {
		t.Fatalf("%+v %+v: equal arena strs must intern to one static value", r1, r2)
	}
	if p := w.consts.Get(w.consts.Get(r1.Value).Relocs[0].Data); string(p.Bytes) != "abc" {
		t.Errorf("static data %q", p.Bytes)
	}
}

func TestRawSameAndAllocatorCheck(t *testing.T) {
	w := newWorld(t)
	pi := w.tab.RawPtr(types.Int)
	of := w.raw("Of", pi, w.tab.Ref(types.Int))
	same := w.raw("Same", types.Bool, pi, pi)
	// Check(wrong: bool): int compares two locals' pointers with raw.Same
	// and ==, then, std-shaped, panics with a message when the allocator
	// identity check fails.
	f := w.fn("lib", "Check", types.Int, types.Bool)
	x := &seq{bb: w.body(f, types.Int, types.Bool)}
	a, b, pa, pb, s1, s2, e1, ok := x.local(types.Int), x.local(types.Int), x.local(pi), x.local(pi), x.local(types.Bool), x.local(types.Bool), x.local(types.Bool), x.local(types.Bool)
	ra, rb := x.local(w.tab.Ref(types.Int)), x.local(w.tab.Ref(types.Int))
	x.assign(0, lp(a), use(imm(types.Int, 1)))
	x.assign(0, lp(b), use(imm(types.Int, 2)))
	x.assign(0, lp(ra), mir.Rvalue{Kind: mir.RvRef, Place: lp(a)})
	x.assign(0, lp(rb), mir.Rvalue{Kind: mir.RvRef, Place: lp(b)})
	x.call(of, []types.TypeID{types.Int}, lp(pa), cp(ra))
	x.call(of, []types.TypeID{types.Int}, lp(pb), cp(rb))
	x.call(same, []types.TypeID{types.Int}, lp(s1), cp(pa), cp(pb))
	x.call(same, []types.TypeID{types.Int}, lp(s2), cp(pa), cp(pa))
	x.assign(x.cur, lp(e1), bin(mir.Eq, cp(pa), cp(pb)))
	// ok = !s1 && s2 && !e1, as bits: s2 &^ (s1 | e1)
	x.assign(x.cur, lp(ok), bin(mir.BitOr, cp(s1), cp(e1)))
	x.assign(x.cur, lp(ok), mir.Rvalue{Kind: mir.RvUnary, Un: mir.Not, A: cp(ok)})
	x.assign(x.cur, lp(ok), bin(mir.BitAnd, cp(ok), cp(s2)))
	x.assign(x.cur, lp(0), mir.Rvalue{Kind: mir.RvCast, Cast: mir.CastLossless, A: cp(ok), Type: types.Int})
	bad, good := x.block(), x.block()
	x.branch(x.cur, cp(1), bad, good)
	x.term(bad, mir.Terminator{Kind: mir.TermPanic, Panic: types.PanicExplicit, HasMsg: true, Msg: mir.Const(types.Str, w.strConst("wrong allocator"))})
	x.ret(good)
	w.bodies[f] = &Body{MIR: x.b, State: BodyReady}
	tr := w.consts.Intern(types.ConstValue{Type: types.Bool, Align: 1, Bytes: []byte{1}})
	fl := w.consts.Intern(types.ConstValue{Type: types.Bool, Align: 1, Bytes: []byte{0}})
	if r := w.call(w.evaluator(0), f, 0, fl); r.Err != nil || w.intOf(r.Value) != 1 {
		t.Errorf("Same(a, b) false, Same(a, a) true, == agrees: %+v %d", r.Err, r.Value)
	}
	r := w.call(w.evaluator(0), f, 0, tr)
	if r.Err == nil || r.Err.Kind != types.EvalPanic || r.Err.Panic != types.PanicExplicit || r.Err.Message != "wrong allocator" {
		t.Errorf("allocator check: %+v, want an explicit panic, not a refusal", r.Err)
	}
}

func TestZeroedMemoryUnderReuse(t *testing.T) {
	w := newWorld(t)
	pad, pt := w.structType("lib", "Pad", types.U8, types.I64, types.U8)
	pu8 := w.tab.RawPtr(types.U8)
	ofMut := w.raw("OfMut", pu8, w.tab.Mut(pt))
	cmp := w.raw("Compare", types.Int, pu8, pu8, types.Int)
	cast := w.raw("PtrCast", pu8, pu8)
	// K: a struct set field by field and a zeroed one, compared bytewise:
	// padding the program never wrote must read as zero.
	k := w.fn("lib", "K", types.Int)
	x := &seq{bb: w.body(k, types.Int)}
	s, z := x.local(pt), x.local(pt)
	ms, mz, ps, pz := x.local(w.tab.Mut(pt)), x.local(w.tab.Mut(pt)), x.local(pu8), x.local(pu8)
	x.assign(0, lp(z), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: pad, Ops: x.b.AddOperands(imm(types.U8, 0), imm(types.I64, 0), imm(types.U8, 0))})
	for i, ty := range []types.TypeID{types.U8, types.I64, types.U8} {
		x.assign(0, x.b.Place(s, mir.Projection{Kind: mir.ProjField, A: uint32(i), Type: ty}), use(imm(ty, 0)))
	}
	x.assign(0, lp(ms), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Exclusive, Place: lp(s)})
	x.assign(0, lp(mz), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Exclusive, Place: lp(z)})
	x.call(ofMut, []types.TypeID{pt}, lp(ps), cp(ms))
	x.call(ofMut, []types.TypeID{pt}, lp(pz), cp(mz))
	x.call(cast, []types.TypeID{pt, types.U8}, lp(ps), cp(ps))
	x.call(cast, []types.TypeID{pt, types.U8}, lp(pz), cp(pz))
	x.call(cmp, nil, lp(0), cp(ps), cp(pz), imm(types.Int, 24))
	x.ret(x.cur)
	w.bodies[k] = &Body{MIR: x.b, State: BodyReady}
	g := w.fn("lib", "Garbage", types.Int)
	y := w.body(g, types.Int)
	big := y.local(w.array(64, types.U64))
	y.assign(0, lp(big), mir.Rvalue{Kind: mir.RvRepeat, A: imm(types.U64, -1), Len: types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, 64)}})
	y.assign(0, lp(0), use(imm(types.Int, 0)))
	y.ret(0)
	y.done()
	e := w.evaluator(0)
	var first types.ConstID
	var wg sync.WaitGroup
	var mu sync.Mutex
	for g2 := 0; g2 < 8; g2++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 125; i++ {
				w.call(e, g, 0)
				r := w.call(e, k, 0) // one Evaluator: pooled slices are shared
				mu.Lock()
				if r.Err != nil || (first != 0 && r.Value != first) || w.intOf(r.Value) != 0 {
					t.Errorf("run %d: %+v value %d", i, r.Err, r.Value)
				}
				first = r.Value
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}
