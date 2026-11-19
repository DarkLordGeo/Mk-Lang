// Why: regression tests for faults once found in the evaluator, each pinned
// so it cannot return.
// What: per-segment read records and arenas, fuel bounding time and memory,
// frames over the stack limit, offsets that cannot wrap, and the arena made
// on first use by a host.
// How: each test builds the shape that once failed (huge repeats, shared
// pointees, giant frames, wrapping products) and checks result, steps or time.

package eval

import (
	"runtime"
	"slices"
	"testing"
	"time"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// within fails the test when fn does not return within d: an evaluation
// whose time fuel does not bound would hang the compiler.
func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s still running after %v", what, d)
	}
}

func hasRead(rs []types.Read, kind types.ReadKind, key types.Key) bool {
	for _, r := range rs {
		if r.Kind == kind && r.Key == key {
			return true
		}
	}
	return false
}

// TestNestedSegmentReadsIndependentOfOrder: lib.F has a local of lib.S,
// lib.H calls F, N = H(), M = N + H() with a local of lib.S. The reads of N
// and of M are the same whether M or N is requested first, and both hold
// ReadLayout(lib.S) and ReadBody(lib.F): a nested segment records what it
// reads even when its reader touched the type, the body or the call site
// first.
func TestNestedSegmentReadsIndependentOfOrder(t *testing.T) {
	w := newWorld(t)
	sItem, st := w.structType("lib", "S", types.Int)
	agg := func(x *bb, l mir.LocalID) {
		x.assign(0, lp(l), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: sItem, Ops: x.b.AddOperands(imm(types.Int, 1))})
	}
	f := w.fn("lib", "F", types.Int)
	x := w.body(f, types.Int)
	agg(x, x.local(st))
	x.assign(0, lp(0), use(imm(types.Int, 1)))
	x.ret(0)
	x.done()
	h := w.fn("lib", "H", types.Int)
	y := w.body(h, types.Int)
	b1 := y.block()
	y.call(0, f, lp(0), b1)
	y.ret(b1)
	y.done()
	n := w.constItem("main", "N", types.Int, 0)
	z := w.body(n, types.Int)
	z1 := z.block()
	z.call(0, h, lp(0), z1)
	z.ret(z1)
	z.done()
	mk := w.constItem("main", "M", types.Int, 0)
	u := w.body(mk, types.Int)
	tn, th := u.local(types.Int), u.local(types.Int)
	agg(u, u.local(st))
	u.assign(0, lp(tn), mir.Rvalue{Kind: mir.RvConstRef, Item: n})
	u1 := u.block()
	u.call(0, h, lp(th), u1)
	u.assign(u1, lp(0), bin(mir.Add, cp(tn), cp(th)))
	u.ret(u1)
	u.done()
	sKey, fKey := w.items.Get(sItem).Key, w.items.Get(f).Key
	for _, stage := range []types.Stage{types.StageSignatures, types.StageBodies} {
		reads := func(first, then types.ItemID) []types.Read {
			e := w.evaluator(0)
			if r := e.Eval(constReq(w, first, stage)); r.Err != nil {
				t.Fatalf("%s: %+v", w.items.Get(first).Key, r.Err)
			}
			r := e.Eval(constReq(w, then, stage))
			if r.Err != nil || r.Value == types.NoConst {
				t.Fatalf("%s: %+v", w.items.Get(then).Key, r)
			}
			return r.Reads.Sorted()
		}
		nAfterM, nAlone := reads(mk, n), reads(n, n)
		mFirst, mAfterN := reads(mk, mk), reads(n, mk)
		if !slices.Equal(nAfterM, nAlone) {
			t.Errorf("stage %d: N's reads depend on the request order:\nafter M %v\nalone   %v", stage, nAfterM, nAlone)
		}
		if !slices.Equal(mFirst, mAfterN) {
			t.Errorf("stage %d: M's reads depend on the request order:\nfirst   %v\nafter N %v", stage, mFirst, mAfterN)
		}
		for name, rs := range map[string][]types.Read{"N": nAlone, "M": mAfterN} {
			if !hasRead(rs, types.ReadLayout, sKey) || !hasRead(rs, types.ReadBody, fKey) {
				t.Errorf("stage %d: %s's reads %v lack ReadLayout(lib.S) or ReadBody(lib.F)", stage, name, rs)
			}
		}
	}
}

// TestArenaPerSegment: T reads S1 = BuildStr(EvalArena(), 3), then
// calls EvalArena() and BuildStr itself. T's steps are the same whether S1
// was already in its slot or T's tree built it, and fuel = Steps suffices in
// both orders (a segment has its own steps).
func TestArenaPerSegment(t *testing.T) {
	w := newWorld(t)
	rec, _ := w.arenaWorld()
	build := w.buildStr(rec)
	s1 := w.strFromArena("S1", build, rec, 3)
	k := w.constItem("main", "T", types.Str, 0)
	x := &seq{bb: w.body(k, types.Str)}
	a, old := x.local(w.tab.Ref(rec)), x.local(types.Str)
	x.assign(x.cur, lp(old), mir.Rvalue{Kind: mir.RvConstRef, Item: s1})
	next := x.block()
	x.term(x.cur, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallBuiltin, Builtin: mir.BuiltinEvalArena}, Dest: lp(a), Target: next, Spawn: mir.NoLocal})
	x.cur = next
	x.call(build, nil, lp(0), cp(a), imm(types.Int, 3))
	x.ret(x.cur)
	x.done()
	run := func(s1First bool) types.EvalResult {
		e := w.evaluator(0)
		if s1First {
			if r := e.Eval(constReq(w, s1, types.StageBodies)); r.Err != nil {
				t.Fatalf("S1: %+v", r.Err)
			}
		}
		return e.Eval(constReq(w, k, types.StageBodies))
	}
	tFirst, s1First := run(false), run(true)
	if tFirst.Err != nil || s1First.Err != nil || tFirst.Value != s1First.Value {
		t.Fatalf("T first %+v, S1 first %+v", tFirst, s1First)
	}
	if tFirst.Steps != s1First.Steps {
		t.Errorf("T's steps: %d when T is requested first, %d after S1", tFirst.Steps, s1First.Steps)
	}
	w.items.Get(k).Decl.(*types.ConstDecl).Fuel = tFirst.Steps
	for _, first := range []bool{false, true} {
		if r := run(first); r.Err != nil || r.Steps != tFirst.Steps {
			t.Errorf("fuel = Steps, S1 first %v: %+v steps %d", first, r.Err, r.Steps)
		}
	}
}

// TestRepeatZeroSizeElement: [e; 1<<40] with e of an empty struct, kept
// in a local and as a const's value, finishes at once: the repeat and the
// serialization skip zero-size elements.
func TestRepeatZeroSizeElement(t *testing.T) {
	w := newWorld(t)
	eItem, et := w.structType("lib", "E")
	n := w.intVal(types.Int, 1<<40)
	big := w.tab.Array(types.ConstArg{Kind: types.CAValue, Value: n}, et)
	fill := func(x *bb, dst mir.Place) {
		e := x.local(et)
		x.assign(0, lp(e), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: eItem})
		x.assign(0, dst, mir.Rvalue{Kind: mir.RvRepeat, A: cp(e), Len: types.ConstArg{Kind: types.CAValue, Value: n}})
	}
	k := w.constItem("main", "K", types.Int, 0)
	x := w.body(k, types.Int)
	fill(x, lp(x.local(big)))
	x.assign(0, lp(0), use(imm(types.Int, 1)))
	x.ret(0)
	x.done()
	k2 := w.constItem("main", "K2", big, 0)
	y := w.body(k2, big)
	fill(y, lp(0))
	y.ret(0)
	y.done()
	e := w.evaluator(0)
	within(t, 10*time.Second, "[E{}; 1<<40] in a local", func() {
		if r := e.Eval(constReq(w, k, types.StageBodies)); r.Err != nil || w.intOf(r.Value) != 1 {
			t.Errorf("K: %+v", r)
		}
	})
	within(t, 10*time.Second, "[E{}; 1<<40] as a value", func() {
		r := e.Eval(constReq(w, k2, types.StageBodies))
		if r.Err != nil || r.Value == types.NoConst || len(w.consts.Get(r.Value).Bytes) != 0 {
			t.Errorf("K2: %+v", r)
		}
	})
}

// TestRepeatCopiesEveryElement: the doubling repeat writes every
// element, pointers with their tags, for a count that is not a power of two.
func TestRepeatCopiesEveryElement(t *testing.T) {
	w := newWorld(t)
	ref := w.tab.Ref(types.Int)
	pItem, pt := w.structType("lib", "P", types.Int, ref)
	arr := w.array(5, pt)
	f := w.fn("lib", "Rep", types.Int)
	x := w.body(f, types.Int)
	v, r, p, a, i, j, s, d := x.local(types.Int), x.local(ref), x.local(pt), x.local(arr), x.local(types.Int), x.local(types.Int), x.local(types.Int), x.local(types.Int)
	x.assign(0, lp(v), use(imm(types.Int, 5)))
	x.assign(0, lp(r), mir.Rvalue{Kind: mir.RvRef, Place: lp(v)})
	x.assign(0, lp(p), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: pItem, Ops: x.b.AddOperands(imm(types.Int, 7), cp(r))})
	x.assign(0, lp(a), mir.Rvalue{Kind: mir.RvRepeat, A: cp(p), Len: types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, 5)}})
	x.assign(0, lp(i), use(imm(types.Int, 3)))
	x.assign(0, lp(j), use(imm(types.Int, 4)))
	x.assign(0, lp(s), use(mir.Copy(x.b.Place(a, mir.Projection{Kind: mir.ProjIndex, A: uint32(i), Type: pt}, mir.Projection{Kind: mir.ProjField, A: 0, Type: types.Int}))))
	x.assign(0, lp(d), use(mir.Copy(x.b.Place(a, mir.Projection{Kind: mir.ProjIndex, A: uint32(j), Type: pt}, mir.Projection{Kind: mir.ProjField, A: 1, Type: ref}, mir.Projection{Kind: mir.ProjDeref, Type: types.Int}))))
	x.assign(0, lp(0), bin(mir.Add, cp(s), cp(d)))
	x.ret(0)
	x.done()
	if r := w.call(w.evaluator(0), f, 0); r.Err != nil || w.intOf(r.Value) != 12 {
		t.Errorf("a[3].f0 + *a[4].f1 = %+v, want 12", r)
	}
}

// TestSerializeSharedPointeesOnce: const K: *Node, where each of 40
// page-allocated nodes has a and b pointing at the previous one. Serializing
// it walks each node once, not each of its 2^40 paths, and the shared
// pointee interns once.
func TestSerializeSharedPointeesOnce(t *testing.T) {
	const n = 40
	w := newWorld(t)
	w.arenaWorld()
	pu8 := w.tab.RawPtr(types.U8)
	node := w.item("lib", "Node", types.KindStruct, &types.StructDecl{})
	nt := w.tab.Named(node, nil)
	pn := w.tab.RawPtr(nt)
	w.items.Get(node).Decl.(*types.StructDecl).Fields = []types.FieldDecl{{Name: w.in.Intern("a"), Type: pn}, {Name: w.in.Intern("b"), Type: pn}}
	cast := w.raw("Cast", pu8, pu8)
	write := w.raw("Write", types.Void, pn, nt)
	k := w.constItem("main", "K", pn, 0)
	x := &seq{bb: w.body(k, pn)}
	prev, i, cnd, p8, p, v := x.local(pn), x.local(types.Int), x.local(types.Bool), x.local(pu8), x.local(pn), x.local(nt)
	head, body, exit := x.block(), x.block(), x.block()
	x.assign(0, lp(i), use(imm(types.Int, 0)))
	x.gotoB(0, head)
	x.assign(head, lp(cnd), bin(mir.Lt, cp(i), imm(types.Int, n)))
	x.branch(head, cp(cnd), body, exit)
	x.cur = body
	x.call(w.pageAlloc, nil, lp(p8), imm(types.Int, 16))
	x.call(cast, []types.TypeID{types.U8, nt}, lp(p), cp(p8))
	x.assign(x.cur, lp(v), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: node, Ops: x.b.AddOperands(cp(prev), cp(prev))})
	x.call(write, []types.TypeID{nt}, mir.LocalPlace(x.local(types.Void)), cp(p), cp(v))
	x.assign(x.cur, lp(prev), use(cp(p)))
	x.assign(x.cur, lp(i), bin(mir.Add, cp(i), imm(types.Int, 1)))
	x.gotoB(x.cur, head)
	x.assign(exit, lp(0), use(cp(prev)))
	x.ret(exit)
	w.bodies[k] = &Body{MIR: x.b, State: BodyReady}
	before := w.consts.Len()
	var r types.EvalResult
	within(t, 10*time.Second, "serializing 40 nodes with shared pointees", func() {
		r = w.evaluator(0).Eval(constReq(w, k, types.StageBodies))
	})
	if r.Err != nil || r.Value == types.NoConst {
		t.Fatalf("K: %+v", r)
	}
	// n nodes and K itself; the loop's int consts were interned before.
	if grew := w.consts.Len() - before; grew != n+1 {
		t.Errorf("Consts grew by %d, want %d", grew, n+1)
	}
	c := w.consts.Get(r.Value)
	for depth := 0; ; depth++ {
		if len(c.Relocs) == 0 {
			if depth != n {
				t.Errorf("chain of %d nodes, want %d", depth, n)
			}
			break
		}
		if depth > 0 && (len(c.Relocs) != 2 || c.Relocs[0].Data != c.Relocs[1].Data) {
			t.Fatalf("node %d: relocs %+v, want a and b at one pointee", depth, c.Relocs)
		}
		c = w.consts.Get(c.Relocs[0].Data)
	}
}

// TestHugeFrameIsStackLimit: F holds a local of [1<<33]u8. Calling it
// is PanicStackLimit (E0901), a user program's diagnostic, not an ICE, and
// again on a second evaluation in the same Evaluator and through another
// caller.
func TestHugeFrameIsStackLimit(t *testing.T) {
	w := newWorld(t)
	big := w.tab.Array(types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, 1<<33)}, types.U8)
	f := w.fn("lib", "Huge", types.Int)
	x := w.body(f, types.Int)
	x.local(big)
	x.assign(0, lp(0), use(imm(types.Int, 1)))
	x.ret(0)
	x.done()
	k := w.constCalling0("main", "K", f, 0)
	e := w.evaluator(0)
	want := func(what string, r types.EvalResult) {
		t.Helper()
		if r.Err == nil || r.Err.Kind != types.EvalPanic || r.Err.Panic != types.PanicStackLimit {
			t.Errorf("%s: %+v, want PanicStackLimit", what, r.Err)
		}
	}
	for i := 0; i < 2; i++ {
		var r types.EvalResult
		if msg := iceOf(func() { r = w.call(e, f, 0) }); msg != "" {
			t.Fatalf("call %d: ICE %s", i, msg)
		}
		want("call", r)
	}
	var r types.EvalResult
	if msg := iceOf(func() { r = e.Eval(constReq(w, k, types.StageBodies)) }); msg != "" {
		t.Fatalf("const: ICE %s", msg)
	}
	want("const K = Huge()", r)
}

// TestIndexOffsetWraps: p = PageAlloc(8); s = raw.Slice(p, 1<<33):
// s[(1<<32)+3] is refused, never a wrapped offset inside the allocation; so
// are s[(1<<32)+3..], raw.Offset[u64](p, ±1<<61) and raw.Copy[u64] of
// 1<<61+1 elements, whose products wrap.
func TestIndexOffsetWraps(t *testing.T) {
	w := newWorld(t)
	w.arenaWorld()
	pu8, pu64 := w.tab.RawPtr(types.U8), w.tab.RawPtr(types.U64)
	su8 := w.tab.Slice(types.U8)
	mkSlice := w.raw("Slice", su8, pu8, types.Int)
	offset := w.raw("Offset", pu64, pu64, types.Int)
	cast := w.raw("Cast", pu8, pu8)
	same := w.raw("Same", types.Bool, pu64, pu64)
	cpy := w.raw("Copy", types.Void, pu64, pu64, types.Int)
	var n int
	index := func(i int64) types.ItemID {
		n++
		f := w.fn("lib", "Index"+itoa(n), types.U8)
		x := &seq{bb: w.body(f, types.U8)}
		p, s, il := x.local(pu8), x.local(su8), x.local(types.Int)
		x.call(w.pageAlloc, nil, lp(p), imm(types.Int, 8))
		x.call(mkSlice, []types.TypeID{types.U8}, lp(s), cp(p), imm(types.Int, 1<<33))
		x.assign(x.cur, lp(il), use(imm(types.Int, i)))
		x.assign(x.cur, lp(0), use(mir.Copy(x.b.Place(s, mir.Projection{Kind: mir.ProjIndex, A: uint32(il), Type: types.U8}))))
		x.ret(x.cur)
		w.bodies[f] = &Body{MIR: x.b, State: BodyReady}
		return f
	}
	if r := w.call(w.evaluator(0), index(3), 0); r.Err != nil || w.intOf(r.Value) != 0 {
		t.Errorf("s[3]: %+v", r)
	}
	wantRefusal(t, w.call(w.evaluator(0), index(8), 0), types.RefuseRawAccess)
	wantRefusal(t, w.call(w.evaluator(0), index(1<<32+3), 0), types.RefuseRawAccess)
	// s[(1<<32)+3..]
	g := w.fn("lib", "Sub", types.Int)
	y := &seq{bb: w.body(g, types.Int)}
	p, s, s2 := y.local(pu8), y.local(su8), y.local(su8)
	y.call(w.pageAlloc, nil, lp(p), imm(types.Int, 8))
	y.call(mkSlice, []types.TypeID{types.U8}, lp(s), cp(p), imm(types.Int, 1<<33))
	y.assign(y.cur, lp(s2), mir.Rvalue{Kind: mir.RvSlice, Place: lp(s), HasLo: true, A: imm(types.Int, 1<<32+3)})
	y.assign(y.cur, lp(0), mir.Rvalue{Kind: mir.RvLen, Place: lp(s2)})
	y.ret(y.cur)
	w.bodies[g] = &Body{MIR: y.b, State: BodyReady}
	wantRefusal(t, w.call(w.evaluator(0), g, 0), types.RefuseRawAccess)
	// raw.Offset[u64](p, k), then Same(q, p): ±1<<61 wraps to a zero offset.
	off := func(k int64) types.ItemID {
		n++
		f := w.fn("lib", "Off"+itoa(n), types.Bool)
		x := &seq{bb: w.body(f, types.Bool)}
		p8, p, q := x.local(pu8), x.local(pu64), x.local(pu64)
		x.call(w.pageAlloc, nil, lp(p8), imm(types.Int, 64))
		x.call(cast, []types.TypeID{types.U8, types.U64}, lp(p), cp(p8))
		x.call(offset, []types.TypeID{types.U64}, lp(q), cp(p), imm(types.Int, k))
		x.call(offset, []types.TypeID{types.U64}, lp(q), cp(q), imm(types.Int, -k))
		x.call(same, []types.TypeID{types.U64}, lp(0), cp(q), cp(p))
		x.ret(x.cur)
		w.bodies[f] = &Body{MIR: x.b, State: BodyReady}
		return f
	}
	if r := w.call(w.evaluator(0), off(3), 0); r.Err != nil || w.intOf(r.Value) != 1 {
		t.Errorf("Offset(Offset(p, 3), -3) == p: %+v", r)
	}
	wantRefusal(t, w.call(w.evaluator(0), off(1<<61), 0), types.RefuseRawAccess)
	wantRefusal(t, w.call(w.evaluator(0), off(-1<<61), 0), types.RefuseRawAccess)
	// raw.Copy[u64](d, s, 1<<61 + 1): 8·count wraps to 8 bytes.
	h := w.fn("lib", "Cp", types.Int)
	z := &seq{bb: w.body(h, types.Int)}
	a8, b8, pa, pb := z.local(pu8), z.local(pu8), z.local(pu64), z.local(pu64)
	z.call(w.pageAlloc, nil, lp(a8), imm(types.Int, 64))
	z.call(w.pageAlloc, nil, lp(b8), imm(types.Int, 64))
	z.call(cast, []types.TypeID{types.U8, types.U64}, lp(pa), cp(a8))
	z.call(cast, []types.TypeID{types.U8, types.U64}, lp(pb), cp(b8))
	z.call(cpy, []types.TypeID{types.U64}, mir.LocalPlace(z.local(types.Void)), cp(pa), cp(pb), imm(types.Int, 1<<61+1))
	z.assign(z.cur, lp(0), use(imm(types.Int, 1)))
	z.ret(z.cur)
	w.bodies[h] = &Body{MIR: z.b, State: BodyReady}
	if r := w.call(w.evaluator(0), h, 0); r.Err == nil || (r.Err.Kind != types.EvalFuel && (r.Err.Kind != types.EvalRefused || r.Err.Refusal != types.RefuseRawAccess)) {
		t.Errorf("Copy of 1<<61+1 u64: %+v, want refused or out of fuel", r)
	}
}

// TestDeadFrameRecordsSmall: a loop of empty calls to the default fuel
// leaves one dead record per call, a few bytes each, not a whole allocation
// record. Measured as the bytes the Go heap allocated during the evaluation
// (runtime.MemStats.TotalAlloc).
func TestDeadFrameRecordsSmall(t *testing.T) {
	w := newWorld(t)
	g := w.fn("lib", "Empty", types.Void)
	x := w.body(g, types.Void)
	x.ret(0)
	x.done()
	c := w.constItem("main", "C", types.Int, 0)
	y := w.body(c, types.Int)
	loop, after := y.block(), y.block()
	unit := y.local(types.Void)
	y.gotoB(0, loop)
	y.call(loop, g, lp(unit), after)
	y.gotoB(after, loop)
	y.done()
	e := w.evaluator(0)
	var before, after2 runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	r := e.Eval(constReq(w, c, types.StageBodies))
	runtime.ReadMemStats(&after2)
	if r.Err == nil || r.Err.Kind != types.EvalFuel {
		t.Fatalf("got %+v, want fuel exhaustion", r.Err)
	}
	// A round: the call 1, the frame (16 bytes) 1, the return 1, the goto 1.
	calls := r.Err.Steps / 4
	grew := after2.TotalAlloc - before.TotalAlloc
	// Frame bytes recycle through the evaluation's spare slices, so what
	// grows is the dead records: 8 bytes each, about 5x that with Go's
	// slice growth; 64 bytes per call leaves margin and fails a record of
	// 16 bytes or more.
	if limit := 64 * calls; grew > limit {
		t.Errorf("%d calls allocated %d bytes, want at most %d", calls, grew, limit)
	}
	t.Logf("%d calls, %d steps: %d bytes allocated, %.1f per call", calls, r.Err.Steps, grew, float64(grew)/float64(calls))
}

// sitesWorld declares Callee and Caller (calling it) and, when extra > 0,
// compiles extra other bodies first in e, as a large session does.
func sitesWorld(tb testing.TB, extra int) (*world, *Evaluator, types.ItemID) {
	w := newWorld(tb)
	e := w.evaluator(0)
	for i := 0; i < extra; i++ {
		b := mir.NewBody(types.NoItem, source.Span{}, types.Int)
		b.AddBlock(mir.OriginBody, mir.NoDefer)
		e.compiledOf(b)
	}
	callee := w.retConst("lib", "Callee", 1)
	caller := w.fn("lib", "Caller", types.Int)
	x := w.body(caller, types.Int)
	b1 := x.block()
	x.call(0, callee, lp(0), b1)
	x.ret(b1)
	x.done()
	return w, e, caller
}

// TestSitesScaleWithEntered: the call-site records of an evaluation
// scale with the bodies it enters, not with the bodies the session compiled
// One call after 20,000 compiled bodies allocates about what it
// allocates after none.
func TestSitesScaleWithEntered(t *testing.T) {
	perEval := func(extra int) uint64 {
		w, e, caller := sitesWorld(t, extra)
		w.call(e, caller, 0) // compile both bodies
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		const runs = 200
		for i := 0; i < runs; i++ {
			if r := w.call(e, caller, 0); r.Err != nil {
				t.Fatal(r.Err)
			}
		}
		runtime.ReadMemStats(&after)
		return (after.TotalAlloc - before.TotalAlloc) / runs
	}
	small, large := perEval(0), perEval(20000)
	if large > 2*small+1024 {
		t.Errorf("one call allocates %d bytes after 20,000 compiled bodies, %d after none", large, small)
	}
}

// BenchmarkCallAfterManyBodies: one evaluation calling one body in an
// Evaluator that compiled 100,000 others; B/op must not grow with them.
func BenchmarkCallAfterManyBodies(b *testing.B) {
	w, e, caller := sitesWorld(b, 100000)
	w.call(e, caller, 0)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		w.call(e, caller, 0)
	}
}

// TestStrCompareCharged: a str compare is a compare of the bytes the
// shorter operand has, charged c(n), so its steps grow with the
// strs and fuel bounds its time.
func TestStrCompareCharged(t *testing.T) {
	w := newWorld(t)
	long := make([]byte, 1<<20)
	for i := range long {
		long[i] = 'a' + byte(i%26)
	}
	steps := func(name string, a, b types.ConstID) uint64 {
		f := w.fn("lib", name, types.Bool)
		x := w.body(f, types.Bool)
		x.assign(0, lp(0), bin(mir.Eq, mir.Const(types.Str, a), mir.Const(types.Str, b)))
		x.ret(0)
		x.done()
		r := w.call(w.evaluator(0), f, 0)
		if r.Err != nil || w.intOf(r.Value) != 1 {
			t.Fatalf("%s: %+v", name, r)
		}
		return r.Steps
	}
	short := steps("Short", w.strConst("abc"), w.strConst("abc"))
	lng := steps("Long", w.strConst(string(long)), w.strConst(string(long)))
	if d := lng - short; d != c(1<<20)-c(3) {
		t.Errorf("1 MiB compare costs %d steps more than a 3-byte one, want %d", d, c(1<<20)-c(3))
	}
}

// TestStrSliceReadsBoundariesOnly: slicing a str reads its two
// boundary bytes and no others, so its time does not grow with the str: a
// pointer word in the middle of the str's allocation is not read, and one
// under a boundary is refused as pointer bytes.
func TestStrSliceReadsBoundariesOnly(t *testing.T) {
	w := newWorld(t)
	w.arenaWorld()
	pu8 := w.tab.RawPtr(types.U8)
	ppu8 := w.tab.RawPtr(pu8)
	mkStr := w.raw("Str", types.Str, pu8, types.Int)
	cast := w.raw("Cast", pu8, pu8)
	offset := w.raw("Offset", pu8, pu8, types.Int)
	write := w.raw("Write", types.Void, ppu8, pu8)
	sub := func(name string, lo, hi int64) types.ItemID {
		f := w.fn("lib", name, types.Int)
		x := &seq{bb: w.body(f, types.Int)}
		p, q, pq, s, s2 := x.local(pu8), x.local(pu8), x.local(ppu8), x.local(types.Str), x.local(types.Str)
		x.call(w.pageAlloc, nil, lp(p), imm(types.Int, 1<<20))
		x.call(offset, []types.TypeID{types.U8}, lp(q), cp(p), imm(types.Int, 512))
		x.call(cast, []types.TypeID{types.U8, pu8}, lp(pq), cp(q))
		x.call(write, []types.TypeID{pu8}, mir.LocalPlace(x.local(types.Void)), cp(pq), cp(p)) // a pointer word at 512
		x.call(mkStr, nil, lp(s), cp(p), imm(types.Int, 1<<20))
		x.assign(x.cur, lp(s2), mir.Rvalue{Kind: mir.RvSlice, Place: lp(s), HasLo: true, A: imm(types.Int, lo), HasHi: true, B: imm(types.Int, hi)})
		x.assign(x.cur, lp(0), mir.Rvalue{Kind: mir.RvLen, Place: lp(s2)})
		x.ret(x.cur)
		w.bodies[f] = &Body{MIR: x.b, State: BodyReady}
		return f
	}
	if r := w.call(w.evaluator(0), sub("Around", 0, 1<<19), 0); r.Err != nil || w.intOf(r.Value) != 1<<19 {
		t.Errorf("s[0..1<<19] over a pointer word it does not read: %+v", r)
	}
	wantRefusal(t, w.call(w.evaluator(0), sub("Under", 514, 600), 0), types.RefusePointerBytes)
}

// TestHostArenaFirstUse: a host's Arena() before any EvalArena() makes
// the arena (it is made on first use), and the EvalArena() that follows
// gives the same one (one run has one arena). A failure while making
// it ends the evaluation with the arena's frames in the trace.
func TestHostArenaFirstUse(t *testing.T) {
	w := newWorld(t)
	rec, mk := w.arenaWorld()
	ref := w.tab.Ref(rec)
	touch := w.hostFn("Touch", types.Bool)
	same := w.hostFn("SameArena", types.Bool, ref)
	f := w.fn("lib", "F", types.Bool)
	x := &seq{bb: w.body(f, types.Bool)}
	a, ok := x.local(ref), x.local(types.Bool)
	x.call(touch, nil, lp(ok))
	next := x.block()
	x.term(x.cur, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallBuiltin, Builtin: mir.BuiltinEvalArena}, Dest: lp(a), Target: next, Spawn: mir.NoLocal})
	x.cur = next
	x.call(same, nil, lp(0), cp(a))
	x.assign(x.cur, lp(0), bin(mir.BitAnd, cp(0), cp(ok)))
	x.ret(x.cur)
	w.bodies[f] = &Body{MIR: x.b, State: BodyReady}
	var first Value
	h := mapHost{
		"gen.Touch": func(m *Machine, c *Call) *types.EvalError {
			v, err := m.Arena()
			if err != nil {
				return err
			}
			first = v
			m.SetBool(c.Result, m.Uint(v) != 0)
			return nil
		},
		"gen.SameArena": func(m *Machine, c *Call) *types.EvalError {
			v, err := m.Arena()
			if err != nil {
				return err
			}
			m.SetBool(c.Result, v == first && m.Uint(c.Args[0]) == m.Uint(v))
			return nil
		},
	}
	var r types.EvalResult
	if msg := iceOf(func() { r = w.runHost(w.evaluator(0), f, h) }); msg != "" {
		t.Fatalf("ICE %s", msg)
	}
	if r.Err != nil || w.intOf(r.Value) != 1 {
		t.Fatalf("%+v %+v: want the host's arena to be EvalArena()'s", r, r.Err)
	}
	// Fuel that runs out inside MakeEvalArena: E0902 with F and the arena
	// function in the trace.
	for fuel := r.Steps - 1; fuel > 0; fuel-- {
		rs := w.evaluator(0).Run(&types.EvalRequest{What: types.EvalCall, Item: f, Fuel: fuel, Stage: types.StageBodies,
			Module: w.module("main"), Site: source.Span{File: 1, Start: 1, End: 2}, Target: types.X86_64Windows}, h)
		if rs.Err == nil || rs.Err.Kind != types.EvalFuel {
			t.Fatalf("fuel %d: %+v, want EvalFuel", fuel, rs.Err)
		}
		if len(rs.Err.Items) == 2 && rs.Err.Items[0] == f && rs.Err.Items[1] == mk {
			return
		}
	}
	t.Errorf("no fuel limit stopped inside MakeEvalArena")
}
