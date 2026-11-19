// Why: regression tests for faults once found in the evaluator, each pinned
// so it cannot return.
// What: serialization off the Go stack, bodiless request items, generator
// success told apart from poison, the layout reads of error payloads, and
// stage stops and signature reuse through long const chains in linear time.
// How: hand-built worlds at sizes (deep lists, chains of 20,000 consts) where
// the old behavior would overflow the stack or take quadratic time.

package eval

import (
	"runtime/debug"
	"testing"
	"time"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// TestSerializeDeepChainNoGoStack: K: *Node is the head of an n-node
// linked list (struct Node { next: *Node }) built in own allocations
// through the PageAlloc override. Serializing it gives a chain of n RelocData
// consts, under a Go stack limit far below what one host frame per node
// would need, instead of a fatal stack overflow no Guard recovers.
func TestSerializeDeepChainNoGoStack(t *testing.T) {
	n := 300_000
	if testing.Short() {
		n = 50_000
	}
	old := debug.SetMaxStack(16 << 20)
	defer debug.SetMaxStack(old)
	w := newWorld(t)
	w.arenaWorld()
	pu8 := w.tab.RawPtr(types.U8)
	node := w.item("lib", "Node", types.KindStruct, &types.StructDecl{})
	nt := w.tab.Named(node, nil)
	pn := w.tab.RawPtr(nt)
	w.items.Get(node).Decl.(*types.StructDecl).Fields = []types.FieldDecl{{Name: w.in.Intern("next"), Type: pn}}
	cast := w.raw("Cast", pu8, pu8)
	write := w.raw("Write", types.Void, pn, nt)
	k := w.constItem("main", "K", pn, 0)
	x := &seq{bb: w.body(k, pn)}
	prev, i, cnd, p8, p, v := x.local(pn), x.local(types.Int), x.local(types.Bool), x.local(pu8), x.local(pn), x.local(nt)
	head, body, exit := x.block(), x.block(), x.block()
	x.assign(0, lp(i), use(imm(types.Int, 0)))
	x.gotoB(0, head)
	x.assign(head, lp(cnd), bin(mir.Lt, cp(i), imm(types.Int, int64(n))))
	x.branch(head, cp(cnd), body, exit)
	x.cur = body
	x.call(w.pageAlloc, nil, lp(p8), imm(types.Int, 8))
	x.call(cast, []types.TypeID{types.U8, nt}, lp(p), cp(p8))
	x.assign(x.cur, lp(v), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: node, Ops: x.b.AddOperands(cp(prev))})
	x.call(write, []types.TypeID{nt}, mir.LocalPlace(x.local(types.Void)), cp(p), cp(v))
	x.assign(x.cur, lp(prev), use(cp(p)))
	x.assign(x.cur, lp(i), bin(mir.Add, cp(i), imm(types.Int, 1)))
	x.gotoB(x.cur, head)
	x.assign(exit, lp(0), use(cp(prev)))
	x.ret(exit)
	w.bodies[k] = &Body{MIR: x.b, State: BodyReady}
	var r types.EvalResult
	within(t, 120*time.Second, "serializing a long chain", func() {
		r = w.evaluator(0).Eval(&types.EvalRequest{What: types.EvalConst, Item: k, Fuel: 1 << 40, Stage: types.StageBodies,
			Module: w.module("main"), Site: source.Span{File: 1, Start: 1, End: 2}, Target: types.X86_64Windows})
	})
	if r.Err != nil || r.Value == types.NoConst {
		t.Fatalf("K: %+v", r)
	}
	c := w.consts.Get(r.Value)
	depth := 0
	for len(c.Relocs) == 1 {
		depth++
		c = w.consts.Get(c.Relocs[0].Data)
	}
	if depth != n || len(c.Relocs) != 0 {
		t.Fatalf("chain of %d nodes (last has %d relocs), want %d", depth, len(c.Relocs), n)
	}
}

// TestCyclicValueStillRefused: a node whose next points at itself is
// refused as a cyclic value, with the walk off the Go stack.
func TestCyclicValueStillRefused(t *testing.T) {
	w := newWorld(t)
	w.arenaWorld()
	pu8 := w.tab.RawPtr(types.U8)
	node := w.item("lib", "Node", types.KindStruct, &types.StructDecl{})
	nt := w.tab.Named(node, nil)
	pn := w.tab.RawPtr(nt)
	w.items.Get(node).Decl.(*types.StructDecl).Fields = []types.FieldDecl{{Name: w.in.Intern("next"), Type: pn}}
	cast := w.raw("Cast", pu8, pu8)
	write := w.raw("Write", types.Void, pn, nt)
	k := w.constItem("main", "K", pn, 0)
	x := &seq{bb: w.body(k, pn)}
	p8, p, v := x.local(pu8), x.local(pn), x.local(nt)
	x.call(w.pageAlloc, nil, lp(p8), imm(types.Int, 8))
	x.call(cast, []types.TypeID{types.U8, nt}, lp(p), cp(p8))
	x.assign(x.cur, lp(v), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: node, Ops: x.b.AddOperands(cp(p))})
	x.call(write, []types.TypeID{nt}, mir.LocalPlace(x.local(types.Void)), cp(p), cp(v))
	x.assign(x.cur, lp(0), use(cp(p)))
	x.ret(x.cur)
	w.bodies[k] = &Body{MIR: x.b, State: BodyReady}
	r := w.evaluator(0).Eval(constReq(w, k, types.StageBodies))
	if r.Err == nil || r.Err.Kind != types.EvalRefused || r.Err.Refusal != types.RefuseRawAccess {
		t.Fatalf("K: %+v, want a raw-access refusal", r)
	}
}

// TestE0901_EvalCallOfBodiless: an EvalCall whose item is bodiless (check
// folds `[raw.Popcount64(255)]u8` and `[lib.CFunc(3)]u8` into one) runs
// eval's intrinsic or refuses, never the ICE "request to run
// bodiless".
func TestE0901_EvalCallOfBodiless(t *testing.T) {
	w := newWorld(t)
	pop := w.raw("Popcount64", types.Int, types.U64)
	ext := w.fn("lib", "CFunc", types.Int, types.Int)
	fd := w.items.Get(ext).Decl.(*types.FuncDecl)
	fd.Bodiless, fd.Sig.Extern = true, true
	sp := w.fn("std.rt", "StackPointer", types.Uint)
	sd := w.items.Get(sp).Decl.(*types.FuncDecl)
	sd.Bodiless, sd.Intrinsic = true, true
	site := source.Span{File: 1, Start: 1, End: 2}
	for _, stage := range []types.Stage{types.StageSignatures, types.StageBodies} {
		e := w.evaluator(0)
		req := func(fn types.ItemID, args ...types.ConstID) types.EvalResult {
			var r types.EvalResult
			if msg := iceOf(func() {
				r = e.Eval(&types.EvalRequest{What: types.EvalCall, Item: fn, Args: args, Stage: stage,
					Module: w.module("main"), Site: site, Target: types.X86_64Windows})
			}); msg != "" {
				t.Fatalf("stage %d, %s: ICE %s", stage, w.items.Get(fn).Key, msg)
			}
			return r
		}
		if r := req(pop, w.intVal(types.U64, 255)); r.Err != nil || w.intOf(r.Value) != 8 {
			t.Errorf("stage %d: Popcount64(255) = %+v %d, want 8", stage, r.Err, r.Value)
		}
		for _, c := range []struct {
			fn   types.ItemID
			args []types.ConstID
			want types.Refusal
		}{{ext, []types.ConstID{w.intVal(types.Int, 3)}, types.RefuseExtern}, {sp, nil, types.RefusePointerBytes}} {
			r := req(c.fn, c.args...)
			if r.Err == nil || r.Err.Kind != types.EvalRefused || r.Err.Refusal != c.want {
				t.Errorf("stage %d, %s: %+v, want refusal %d", stage, w.items.Get(c.fn).Key, r.Err, c.want)
				continue
			}
			if len(r.Err.Items) != 1 || r.Err.Items[0] != c.fn || r.Err.At != site || r.Err.Trace[0] != site {
				t.Errorf("stage %d, %s: trace %v %v at %v, want the callee at the site", stage, w.items.Get(c.fn).Key, r.Err.Items, r.Err.Trace, r.Err.At)
			}
		}
	}
}

// TestEvalGen_SuccessDistinctFromPoison: a generator run that completes
// gives a value (the void const), one that enters a poisoned body, at once
// or after work, gives poison, Value NoConst with no error, so gen never
// splices a partial run's output.
func TestEvalGen_SuccessDistinctFromPoison(t *testing.T) {
	w := newWorld(t)
	gen := func(name string) types.ItemID { return w.fn("lib", name, types.Void) }
	ok := gen("Ok")
	x := w.body(ok, types.Void)
	x.ret(0)
	x.done()
	bad := gen("Bad")
	w.setState(bad, BodyPoisoned)
	sick := w.retConst("lib", "Sick", 1)
	w.setState(sick, BodyPoisoned)
	late := gen("Late")
	y := w.body(late, types.Void)
	l := y.local(types.Int)
	y.assign(0, lp(l), use(imm(types.Int, 7)))
	b1 := y.block()
	y.call(0, sick, lp(l), b1)
	y.ret(b1)
	y.done()
	e := w.evaluator(0)
	run := func(fn types.ItemID) types.EvalResult {
		return e.Run(&types.EvalRequest{What: types.EvalGen, Item: fn, Stage: types.StageSignatures,
			Module: w.module("main"), Site: source.Span{File: 1, Start: 1, End: 2}, Target: types.X86_64Windows}, nil)
	}
	r := run(ok)
	if r.Err != nil || r.Value == types.NoConst {
		t.Fatalf("completed run: %+v, want a value", r)
	}
	if v := w.consts.Get(r.Value); v.Type != types.Void || len(v.Bytes) != 0 {
		t.Errorf("completed run's value %+v, want the void const", v)
	}
	wantPoison(t, run(bad))
	wantPoison(t, run(late))
}

// errWorld declares lib.E with the given payload and const main.K: int whose
// initializer holds a local of lib.E!int, and returns K's reads.
func errWorld(t *testing.T, payload ...types.TypeID) (*world, []types.Read) {
	w := newWorld(t)
	e := w.errorItem("lib", "E", payload...)
	eu := w.tab.ErrorUnion(w.tab.ErrorSet([]types.ErrorMember{{Item: e}}), types.Int)
	k := w.constItem("main", "K", types.Int, 0)
	x := w.body(k, types.Int)
	x.local(eu)
	x.assign(0, lp(0), use(imm(types.Int, 1)))
	x.ret(0)
	x.done()
	r := w.evaluator(0).Eval(constReq(w, k, types.StageBodies))
	if r.Err != nil || w.intOf(r.Value) != 1 {
		t.Fatalf("K: %+v", r)
	}
	return w, r.Reads.Sorted()
}

// TestReads_ErrorPayloadLayout: an evaluation holding memory of lib.E!int
// records ReadLayout of lib.E with Name "" (iface's key for an error), and
// its hash changes when lib.E gains a payload field, so a cached value is
// not replayed over a changed union layout.
func TestReads_ErrorPayloadLayout(t *testing.T) {
	read := func(w *world, rs []types.Read) types.Read {
		t.Helper()
		key := types.ItemKey("lib", "E")
		for _, r := range rs {
			if r.Kind == types.ReadLayout && r.Key == key {
				if r.Name != "" {
					t.Errorf("ReadLayout(lib.E) Name %q, want empty", r.Name)
				}
				return r
			}
		}
		t.Fatalf("no ReadLayout(lib.E) among %v", rs)
		return types.Read{}
	}
	w1, rs1 := errWorld(t, types.Int, types.Int)
	w2, rs2 := errWorld(t, types.Int, types.Int)
	w3, rs3 := errWorld(t, types.Int, types.Int, types.I64)
	a, b, c := read(w1, rs1), read(w2, rs2), read(w3, rs3)
	if a.Hash != b.Hash {
		t.Errorf("equal payloads give different hashes")
	}
	if a.Hash == c.Hash {
		t.Errorf("a payload field added leaves the hash unchanged")
	}
}

// chainWorld declares consts A0..An, Ai = A(i-1) + 1, A0 = root's body.
func chainWorld(t *testing.T, n int, root func(w *world, x *bb)) (*world, []types.ItemID) {
	w := newWorld(t)
	cs := make([]types.ItemID, n+1)
	for i := 0; i <= n; i++ {
		cs[i] = w.constItem("main", "A"+itoa(i), types.Int, 0)
	}
	for i := 0; i <= n; i++ {
		x := w.body(cs[i], types.Int)
		if i == 0 {
			root(w, x)
		} else {
			t0 := x.local(types.Int)
			x.assign(0, lp(t0), mir.Rvalue{Kind: mir.RvConstRef, Item: cs[i-1]})
			x.assign(0, lp(0), bin(mir.Add, cp(t0), imm(types.Int, 1)))
			x.ret(0)
		}
		if i < 2 || i == n {
			x.done()
		} else {
			w.bodies[cs[i]] = &Body{MIR: x.b, State: BodyReady}
		}
	}
	return w, cs
}

// TestLongChainStageStopLinear: A0 calls same-module Helper, so a
// StageSignatures request for An stops with EvalStage through every
// segment. Each segment's chain is its suffix of one trace, capped at 65
// entries, built in time linear in the chain.
func TestLongChainStageStopLinear(t *testing.T) {
	n := 200_000
	if testing.Short() {
		n = 20_000
	}
	old := debug.SetMaxStack(64 << 20)
	defer debug.SetMaxStack(old)
	var helper types.ItemID
	w, cs := chainWorld(t, n, func(w *world, x *bb) {
		helper = w.retConst("main", "Helper", 3)
		b1 := x.block()
		x.call(0, helper, lp(0), b1)
		x.ret(b1)
	})
	e := w.evaluator(0)
	var r types.EvalResult
	within(t, 30*time.Second, "a stage stop through a long chain", func() {
		r = e.Eval(constReq(w, cs[n], types.StageSignatures))
	})
	if r.Err == nil || r.Err.Kind != types.EvalStage {
		t.Fatalf("A%d: %+v, want EvalStage", n, r)
	}
	it := r.Err.Items
	if len(it) != 65 || it[0] != cs[n] || it[32] != types.NoItem || it[63] != cs[0] || it[64] != helper {
		t.Fatalf("A%d: items %v", n, it)
	}
	if r.Err.Trace[64] != r.Err.At {
		t.Errorf("A%d: At %v is not the trace's last span %v", n, r.Err.At, r.Err.Trace[64])
	}
	// Every segment stored its own suffix chain.
	wantStage(t, e.Eval(constReq(w, cs[0], types.StageSignatures)), cs[0], helper)
	wantStage(t, e.Eval(constReq(w, cs[1], types.StageSignatures)), cs[1], cs[0], helper)
	if r := e.Eval(constReq(w, cs[n-1], types.StageSignatures)); r.Err == nil || len(r.Err.Items) != 65 || r.Err.Items[0] != cs[n-1] {
		t.Errorf("A%d: %+v", n-1, r.Err)
	}
}

// TestSigReuseLinear: after a StageSignatures request for An reached the
// whole chain, the declaration evaluations of A0..An on the same Evaluator
// reuse the stage-5 outcomes with at most about 3n Program.Body calls in all,
// in item order and in reverse order, rather than walking the whole chain
// again for each, which is (n+1)(n+2)/2 calls. With A(n/2)'s initializer
// poisoned after stage 5, A(n/2) and every const above it are poison and every
// const below keeps its value, in both orders.
func TestSigReuseLinear(t *testing.T) {
	n := 100_000
	if testing.Short() {
		n = 10_000
	}
	old := debug.SetMaxStack(64 << 20)
	defer debug.SetMaxStack(old)
	for _, c := range []struct {
		name     string
		reverse  bool
		poisonAt int
	}{{"item order", false, -1}, {"reverse order", true, -1}, {"item order, poisoned", false, n / 2}, {"reverse order, poisoned", true, n / 2}} {
		t.Run(c.name, func(t *testing.T) {
			w, cs := chainWorld(t, n, func(w *world, x *bb) {
				x.assign(0, lp(0), use(imm(types.Int, 0)))
				x.ret(0)
			})
			e := w.evaluator(0)
			if r := e.Eval(constReq(w, cs[n], types.StageSignatures)); r.Err != nil || w.intOf(r.Value) != int64(n) {
				t.Fatalf("A%d at stage 5: %+v", n, r)
			}
			if c.poisonAt >= 0 {
				w.setState(cs[c.poisonAt], BodyPoisoned)
			}
			w.mu.Lock()
			w.bodyCalls = nil
			w.mu.Unlock()
			for k := 0; k <= n; k++ {
				i := k
				if c.reverse {
					i = n - k
				}
				r := e.Eval(constReq(w, cs[i], types.StageBodies))
				if c.poisonAt >= 0 && i >= c.poisonAt {
					if r.Err != nil || r.Value != types.NoConst {
						t.Fatalf("A%d: %+v, want poison", i, r)
					}
					continue
				}
				if r.Err != nil || w.intOf(r.Value) != int64(i) {
					t.Fatalf("A%d: %+v, want %d", i, r, i)
				}
			}
			w.mu.Lock()
			calls := len(w.bodyCalls)
			w.mu.Unlock()
			if calls > 3*n {
				t.Errorf("%d Program.Body calls for %d declaration evaluations, want at most %d", calls, n+1, 3*n)
			}
		})
	}
}
