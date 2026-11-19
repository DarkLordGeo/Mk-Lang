// Why: a const may not depend on a same-module body or layout before that
// stage exists, and the evaluator enforces this with E0908 and poison.
// What: tests of the stage rule, const slots, cycles and poison: E0908
// through generics and fn values, nested stops, one diagnostic per cause,
// poisoned and cached bodies, E0909 cycles and concurrent requests.
// How: hand-built modules requested at StageSignatures and StageBodies.

package eval

import (
	"slices"
	"sync"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// retConst declares a function of mod returning v.
func (w *world) retConst(mod, name string, v int64) types.ItemID {
	f := w.fn(mod, name, types.Int)
	x := w.body(f, types.Int)
	x.assign(0, lp(0), use(imm(types.Int, v)))
	x.ret(0)
	x.done()
	return f
}

// constOf declares const name = <read of c> + d.
func (w *world) constPlus(mod, name string, c types.ItemID, d int64) types.ItemID {
	k := w.constItem(mod, name, types.Int, 0)
	x := w.body(k, types.Int)
	t0 := x.local(types.Int)
	x.assign(0, lp(t0), mir.Rvalue{Kind: mir.RvConstRef, Item: c})
	x.assign(0, lp(0), bin(mir.Add, cp(t0), imm(types.Int, d)))
	x.ret(0)
	x.done()
	return k
}

func wantStage(t *testing.T, r types.EvalResult, items ...types.ItemID) {
	t.Helper()
	if r.Err == nil || r.Err.Kind != types.EvalStage {
		t.Fatalf("got %+v (value %d), want EvalStage", r.Err, r.Value)
	}
	if len(r.Err.Items) != len(items) {
		t.Fatalf("items %v, want %v", r.Err.Items, items)
	}
	for i := range items {
		if r.Err.Items[i] != items[i] {
			t.Fatalf("items %v, want %v", r.Err.Items, items)
		}
	}
	if len(r.Err.Trace) != len(items) || r.Err.Trace[len(items)-1] != r.Err.At {
		t.Errorf("trace %v at %v", r.Err.Trace, r.Err.At)
	}
}

func wantPoison(t *testing.T, r types.EvalResult) {
	t.Helper()
	if r.Err != nil || r.Value != types.NoConst {
		t.Fatalf("got %+v value %d, want poison", r.Err, r.Value)
	}
}

func TestE0908_SameModuleBody(t *testing.T) {
	w := newWorld(t)
	h := w.retConst("main", "Helper", 3)
	x := w.constCalling("main", "X", h, 0, 0)
	w.items.Get(h).Decl.(*types.FuncDecl).Sig.Params = nil
	w.bodies[x].MIR.Args[0].Operand = mir.Operand{} // no argument
	x2 := w.constItem("main", "X2", types.Int, 0)
	y := w.body(x2, types.Int)
	b1 := y.block()
	callAt := y.call(0, h, lp(0), b1)
	y.ret(b1)
	y.done()
	e := w.evaluator(0)
	r := e.Eval(constReq(w, x2, types.StageSignatures))
	wantStage(t, r, x2, h)
	if r.Err.At != y.b.Span(callAt) {
		t.Errorf("at %v, want the call %v", r.Err.At, y.b.Span(callAt))
	}
	// A call request of the same-module function itself.
	r = e.Eval(&types.EvalRequest{What: types.EvalCall, Item: h, Stage: types.StageSignatures, Module: w.module("main"), Site: w.span()})
	wantStage(t, r, h)
	// At StageBodies it runs.
	if r := w.evaluator(0).Eval(constReq(w, x2, types.StageBodies)); r.Err != nil || w.intOf(r.Value) != 3 {
		t.Errorf("at StageBodies: %+v", r)
	}
}

func TestE0908_SameModuleLayout(t *testing.T) {
	w := newWorld(t)
	loc, lt := w.structType("main", "Local", types.Int)
	k := w.constItem("main", "K", types.Int, 0)
	x := w.body(k, types.Int)
	v := x.local(lt)
	x.assign(0, lp(v), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: loc, Ops: x.b.AddOperands(imm(types.Int, 1))})
	x.assign(0, lp(0), use(mir.Copy(x.b.Place(v, mir.Projection{Kind: mir.ProjField, A: 0, Type: types.Int}))))
	x.ret(0)
	x.done()
	r := w.evaluator(0).Eval(constReq(w, k, types.StageSignatures))
	wantStage(t, r, k, loc)
	// SizeOf of a same-module type inside a lower function reached with it
	// as a generic argument is the same stop; a reference to it is not.
	ref := w.tab.Ref(lt)
	k2 := w.constItem("main", "K2", types.Int, 0)
	y := w.body(k2, types.Int)
	y.assign(0, lp(0), mir.Rvalue{Kind: mir.RvSizeOf, Type: ref})
	y.ret(0)
	y.done()
	if r := w.evaluator(0).Eval(constReq(w, k2, types.StageSignatures)); r.Err != nil || w.intOf(r.Value) != 8 {
		t.Errorf("SizeOf[&Local] at stage 5: %+v, want 8", r)
	}
}

// wrapInstance declares lib.Wrap[T](): int and serves its instance body for
// T = arg, whose local has type T (mono's substitution).
func (w *world) wrapInstance(arg types.TypeID) (types.ItemID, []types.GenericArg) {
	wrap, ok := w.items.ByKey(types.ItemKey("lib", "Wrap"))
	if !ok {
		wrap = w.fn("lib", "Wrap", types.Int)
		w.items.Get(wrap).Decl.(*types.FuncDecl).Sig.Generics = []types.GenericParam{{Name: w.in.Intern("T")}}
	}
	args := []types.GenericArg{types.TypeArg(arg)}
	x := &bb{w: w, b: mir.NewBody(wrap, w.span(), types.Int)}
	x.b.AddBlock(mir.OriginBody, mir.NoDefer)
	x.local(arg)
	x.assign(0, lp(0), mir.Rvalue{Kind: mir.RvSizeOf, Type: arg})
	x.ret(0)
	w.setInstance(wrap, args, x.b)
	return wrap, args
}

// constCallingInst declares const name = f[args]().
func (w *world) constCallingInst(mod, name string, f types.ItemID, args []types.GenericArg) types.ItemID {
	k := w.constItem(mod, name, types.Int, 0)
	x := w.body(k, types.Int)
	b1 := x.block()
	x.term(0, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: f, Args: x.b.AddGenericArgs(args...)}},
		Dest: lp(0), Target: b1, Spawn: mir.NoLocal})
	x.ret(b1)
	x.done()
	return k
}

func TestE0908_ViaImportedGeneric(t *testing.T) {
	w := newWorld(t)
	loc, lt := w.structType("main", "Local", types.Int, types.Int)
	wrap, args := w.wrapInstance(lt)
	k := w.constCallingInst("main", "K", wrap, args)
	r := w.evaluator(0).Eval(constReq(w, k, types.StageSignatures))
	wantStage(t, r, k, wrap, loc)
	if r := w.evaluator(0).Eval(constReq(w, k, types.StageBodies)); r.Err != nil || w.intOf(r.Value) != 16 {
		t.Errorf("at StageBodies: %+v", r)
	}
}

func TestCachedBodyStillStops(t *testing.T) {
	w := newWorld(t)
	loc, lt := w.structType("m", "Local", types.Int)
	wrap, args := w.wrapInstance(lt)
	k1 := w.constCallingInst("m", "K1", wrap, args)
	k2 := w.constCallingInst("m", "K2", wrap, args)
	kn := w.constCallingInst("n", "KN", wrap, args)
	e := w.evaluator(0)
	wantStage(t, e.Eval(constReq(w, k1, types.StageSignatures)), k1, wrap, loc)
	wantStage(t, e.Eval(constReq(w, k2, types.StageSignatures)), k2, wrap, loc)
	r := e.Eval(constReq(w, kn, types.StageSignatures))
	if r.Err != nil || w.intOf(r.Value) != 8 {
		t.Fatalf("from module n: %+v", r)
	}
	var layouts int
	for _, rd := range r.Reads.Sorted() {
		if rd.Kind == types.ReadLayout && rd.Key == w.items.Get(loc).Key {
			layouts++
		}
	}
	if layouts != 1 {
		t.Errorf("ReadLayout of m.Local recorded %d times from a cached body; reads %v", layouts, r.Reads.Sorted())
	}
}

func TestE0908_ViaFnValue(t *testing.T) {
	w := newWorld(t)
	h := w.retConst("main", "Helper", 3)
	ft := w.tab.Fn(types.FnType{Result: types.Int})
	apply := w.fn("lib", "Apply", types.Int, ft)
	x := w.body(apply, types.Int, ft)
	b1 := x.block()
	x.term(0, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallValue, Value: cp(1)}, Dest: lp(0), Target: b1, Spawn: mir.NoLocal})
	x.ret(b1)
	x.done()
	k := w.constItem("main", "K", types.Int, 0)
	y := w.body(k, types.Int)
	fv := y.local(ft)
	b2 := y.block()
	y.assign(0, lp(fv), mir.Rvalue{Kind: mir.RvFnRef, Fn: mir.FnRef{Item: h}})
	y.call(0, apply, lp(0), b2, cp(fv))
	y.ret(b2)
	y.done()
	wantStage(t, w.evaluator(0).Eval(constReq(w, k, types.StageSignatures)), k, apply, h)
	if r := w.evaluator(0).Eval(constReq(w, k, types.StageBodies)); r.Err != nil || w.intOf(r.Value) != 3 {
		t.Errorf("at StageBodies: %+v", r)
	}
}

func TestE0908_SameModuleConstNoBodyAllowed(t *testing.T) {
	w := newWorld(t)
	h := w.retConst("main", "Helper", 1)
	w.forbidBody[h] = true
	slots := w.constItem("main", "Slots", types.Int, 0)
	x := w.body(slots, types.Int)
	x.assign(0, lp(0), use(imm(types.Int, 254)))
	x.ret(0)
	x.done()
	r := w.evaluator(0).Eval(constReq(w, slots, types.StageSignatures))
	if r.Err != nil || w.intOf(r.Value) != 254 {
		t.Fatalf("Slots at stage 5: %+v", r)
	}
	w.setState(slots, BodyNotLowered)
	if msg := iceOf(func() { w.evaluator(0).Eval(constReq(w, slots, types.StageSignatures)) }); msg == "" {
		t.Errorf("BodyNotLowered for the initializer is not an ICE")
	}
}

func TestNestedConstStageStop(t *testing.T) {
	w := newWorld(t)
	h := w.retConst("main", "Helper", 3)
	n := w.constItem("main", "N", types.Int, 0)
	x := w.body(n, types.Int)
	b1 := x.block()
	x.call(0, h, lp(0), b1)
	x.ret(b1)
	x.done()
	m := w.constPlus("main", "M", n, 1)
	e := w.evaluator(0)
	wantStage(t, e.Eval(constReq(w, m, types.StageSignatures)), m, n, h)
	// The stored stop gives N's own chain to a later use request.
	wantStage(t, e.Eval(constReq(w, n, types.StageSignatures)), n, h)
	for item, want := range map[types.ItemID]int64{n: 3, m: 4} {
		if r := e.Eval(constReq(w, item, types.StageBodies)); r.Err != nil || w.intOf(r.Value) != want {
			t.Errorf("declaration evaluation of %s: %+v", w.items.Get(item).Key, r)
		}
	}
}

func TestOneDiagnosticForConstFailure(t *testing.T) {
	w := newWorld(t)
	boom := w.panicking("Boom", func(x *bb) { x.term(0, mir.Terminator{Kind: mir.TermPanic, Panic: types.PanicAssert}) })
	spin, _ := w.spinFn("Spin")
	ext := w.fn("lib", "CExt", types.Int)
	fd := w.items.Get(ext).Decl.(*types.FuncDecl)
	fd.Bodiless, fd.Sig.Extern = true, true
	for _, c := range []struct {
		name string
		fn   types.ItemID
		kind types.EvalErrorKind
	}{{"panic", boom, types.EvalPanic}, {"fuel", spin, types.EvalFuel}, {"refusal", ext, types.EvalRefused}} {
		t.Run(c.name, func(t *testing.T) {
			k := w.constItem("main", "X"+c.name, types.Int, 1000)
			x := w.body(k, types.Int)
			b1 := x.block()
			x.call(0, c.fn, lp(0), b1)
			x.ret(b1)
			x.done()
			e := w.evaluator(0)
			wantPoison(t, e.Eval(constReq(w, k, types.StageSignatures)))
			r := e.Eval(constReq(w, k, types.StageBodies))
			if r.Err == nil || r.Err.Kind != c.kind {
				t.Fatalf("declaration evaluation: %+v, want kind %d", r.Err, c.kind)
			}
			wantPoison(t, e.Eval(constReq(w, k, types.StageSignatures)))
			// A reader of X gets poison too, never a second error.
			rd := w.constPlus("main", "R"+c.name, k, 1)
			wantPoison(t, e.Eval(constReq(w, rd, types.StageBodies)))
		})
	}
}

func TestDeclarationEvalAfterBorrow(t *testing.T) {
	w := newWorld(t)
	bad := w.retConst("main", "Bad", 1)
	w.setState(bad, BodyPoisoned)
	c := w.constItem("main", "C", types.Int, 0)
	x := w.body(c, types.Int)
	b1 := x.block()
	x.call(0, bad, lp(0), b1)
	x.ret(b1)
	x.done()
	wantPoison(t, w.evaluator(0).Eval(constReq(w, c, types.StageBodies)))
	// A stage-5 outcome is not reused once an initializer it entered has a
	// diagnostic.
	ok := w.constItem("main", "OK", types.Int, 0)
	y := w.body(ok, types.Int)
	y.assign(0, lp(0), use(imm(types.Int, 5)))
	y.ret(0)
	y.done()
	e := w.evaluator(0)
	if r := e.Eval(constReq(w, ok, types.StageSignatures)); w.intOf(r.Value) != 5 {
		t.Fatalf("OK at stage 5: %+v", r)
	}
	w.setState(ok, BodyPoisoned)
	wantPoison(t, e.Eval(constReq(w, ok, types.StageBodies)))
	// Many use requests in parallel on one module: no deadlock, no
	// forbidden Program call.
	h := w.retConst("main", "H", 2)
	w.forbidBody[h] = true
	var ks []types.ItemID
	prev := w.constItem("main", "P0", types.Int, 0)
	z := w.body(prev, types.Int)
	z.assign(0, lp(0), use(imm(types.Int, 0)))
	z.ret(0)
	z.done()
	ks = append(ks, prev)
	for i := 1; i < 16; i++ {
		prev = w.constPlus("main", "P"+itoa(i), prev, 1)
		ks = append(ks, prev)
	}
	e2 := w.evaluator(0)
	var wg sync.WaitGroup
	results := make([]types.EvalResult, 64)
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[g] = e2.Eval(constReq(w, ks[(g*7)%len(ks)], types.StageSignatures))
		}()
	}
	wg.Wait()
	for g, r := range results {
		if want := int64((g * 7) % len(ks)); r.Err != nil || w.intOf(r.Value) != want {
			t.Errorf("goroutine %d: %+v, want %d", g, r, want)
		}
	}
}

func TestE0909_SelfCycle(t *testing.T) {
	w := newWorld(t)
	x := w.constItem("main", "X", types.Int, 0)
	y := w.body(x, types.Int)
	t0 := y.local(types.Int)
	at := y.assign(0, lp(t0), mir.Rvalue{Kind: mir.RvConstRef, Item: x})
	y.assign(0, lp(0), bin(mir.Add, cp(t0), imm(types.Int, 1)))
	y.ret(0)
	y.done()
	r := w.evaluator(0).Eval(constReq(w, x, types.StageBodies))
	if r.Err == nil || r.Err.Kind != types.EvalCycle || len(r.Err.Items) != 1 || r.Err.Items[0] != x || r.Err.Trace[0] != y.b.Span(at) {
		t.Errorf("got %+v, want EvalCycle [X] at the read", r.Err)
	}
}

// cycleAB declares A = B + 1 and B = A + 1 (A's key is the least).
func (w *world) cycleAB() (types.ItemID, types.ItemID) {
	a := w.constItem("main", "A", types.Int, 0)
	b := w.constItem("main", "B", types.Int, 0)
	for _, p := range [][2]types.ItemID{{a, b}, {b, a}} {
		x := w.body(p[0], types.Int)
		t0 := x.local(types.Int)
		x.assign(0, lp(t0), mir.Rvalue{Kind: mir.RvConstRef, Item: p[1]})
		x.assign(0, lp(0), bin(mir.Add, cp(t0), imm(types.Int, 1)))
		x.ret(0)
		x.done()
	}
	return a, b
}

func TestE0909_TwoConsts(t *testing.T) {
	w := newWorld(t)
	a, b := w.cycleAB()
	e := w.evaluator(0)
	r := e.Eval(constReq(w, a, types.StageBodies))
	if r.Err == nil || r.Err.Kind != types.EvalCycle || len(r.Err.Items) != 2 || r.Err.Items[0] != a || r.Err.Items[1] != b {
		t.Fatalf("A: %+v, want EvalCycle [A, B]", r.Err)
	}
	wantPoison(t, e.Eval(constReq(w, b, types.StageBodies)))
}

func TestE0909_CanonicalWhateverEntry(t *testing.T) {
	w := newWorld(t)
	a, b := w.cycleAB()
	var first *types.EvalError
	for _, order := range [][]types.ItemID{{a, b}, {b, a}} {
		e := w.evaluator(0)
		var errs []*types.EvalError
		for _, it := range order {
			if r := e.Eval(constReq(w, it, types.StageBodies)); r.Err != nil {
				errs = append(errs, r.Err)
			}
		}
		if len(errs) != 1 || errs[0].Items[0] != a || len(errs[0].Items) != 2 {
			t.Fatalf("order %v: errors %+v, want one cycle from A", order, errs)
		}
		if first == nil {
			first = errs[0]
		} else if errs[0].Trace[0] != first.Trace[0] || errs[0].Trace[1] != first.Trace[1] {
			t.Errorf("trace %v differs from %v", errs[0].Trace, first.Trace)
		}
	}
}

func TestReaderOfCycleIsPoison(t *testing.T) {
	w := newWorld(t)
	a, _ := w.cycleAB()
	c := w.constPlus("main", "C", a, 0)
	e := w.evaluator(0)
	wantPoison(t, e.Eval(constReq(w, c, types.StageBodies)))
	if r := e.Eval(constReq(w, a, types.StageBodies)); r.Err == nil || r.Err.Kind != types.EvalCycle {
		t.Errorf("A after its reader: %+v", r.Err)
	}
}

func TestPoisonedBody(t *testing.T) {
	w := newWorld(t)
	g := w.retConst("lib", "G", 1)
	w.setState(g, BodyPoisoned)
	k := w.constCalling("main", "K", g, 0, 0)
	w.items.Get(g).Decl.(*types.FuncDecl).Sig.Params = nil
	w.bodies[k].MIR.Args = nil
	w.bodies[k].MIR.Blocks[0].Term.Args = mir.Range{}
	wantPoison(t, w.evaluator(0).Eval(constReq(w, k, types.StageSignatures)))
	// A poisoned lower const is poison too.
	lc := w.constItem("lib", "LC", types.Int, 0)
	w.lowerVals[lc] = Const{Poisoned: true}
	wantPoison(t, w.evaluator(0).Eval(constReq(w, w.constPlus("main", "U", lc, 1), types.StageBodies)))
}

func TestEvalConcurrent(t *testing.T) {
	w := newWorld(t)
	f := w.loopFn("lib", "Loop")
	var ks []types.ItemID
	for i := 0; i < 8; i++ {
		ks = append(ks, w.constCalling("main", "K"+itoa(i), f, int64(100*i), 0))
	}
	sum := w.constItem("main", "Sum", types.Int, 0)
	x := w.body(sum, types.Int)
	acc := x.local(types.Int)
	x.assign(0, lp(0), use(imm(types.Int, 0)))
	for _, k := range ks {
		x.assign(0, lp(acc), mir.Rvalue{Kind: mir.RvConstRef, Item: k})
		x.assign(0, lp(0), bin(mir.Add, cp(0), cp(acc)))
	}
	x.ret(0)
	x.done()
	all := append(ks, sum)
	ref := map[types.ItemID]types.EvalResult{}
	for _, k := range all {
		ref[k] = w.evaluator(0).Eval(constReq(w, k, types.StageBodies))
	}
	e := w.evaluator(0)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for g := 0; g < 64; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			k := all[(g*5)%len(all)]
			r := e.Eval(constReq(w, k, types.StageSignatures))
			mu.Lock()
			defer mu.Unlock()
			if r.Err != nil || r.Value != ref[k].Value || r.Steps != ref[k].Steps {
				t.Errorf("goroutine %d, %s: value %d steps %d, want %d %d", g, w.items.Get(k).Key, r.Value, r.Steps, ref[k].Value, ref[k].Steps)
			}
			// The reads too: whichever goroutine's tree evaluated a const
			// first, its slot holds the same reads.
			rk := ref[k]
			if got, want := r.Reads.Sorted(), rk.Reads.Sorted(); !slices.Equal(got, want) {
				t.Errorf("goroutine %d, %s: reads %v, want %v", g, w.items.Get(k).Key, got, want)
			}
		}()
	}
	wg.Wait()
}
