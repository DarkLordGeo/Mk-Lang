// Why: a non-generic body served as lowered still calls std.raw.Release and
// std.raw.HasFree, and the evaluator must find the Free impl behind them.
// What: tests of release.go: Release calling an impl's Free or forgetting,
// the reads recorded for the cache, and an ICE when FreeImpls is missing.
// How: a test Program with FreeImpls over a fixed impl table.

package eval

import (
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// freeProgram is the test world with FreeImpls over a fixed impl table.
type freeProgram struct {
	*world
	impls map[types.TypeID]types.FnRef
}

func (p *freeProgram) FreeImpl(t types.TypeID) (types.FnRef, bool, []types.Read) {
	fn, ok := p.impls[t]
	r := types.Read{Kind: types.ReadNoImpl, Key: types.ItemKey("std.core", "Free"), Name: p.tab.TypeString(t, nil)}
	if ok {
		r.Kind = types.ReadImpl
	}
	return fn, ok, []types.Read{r}
}

func (p *freeProgram) evaluator() *Evaluator {
	w := p.world
	return New(Config{Types: w.tab, Items: w.items, Consts: w.consts, Interner: w.in, Program: p,
		Known: w.known, Target: types.X86_64Windows, TargetItem: w.target})
}

// r1Bodies declares IntFree(self: int, a: mut int) { *a = self }, and
// MainRelease(): int { x := 7; y := 0; raw.Release[int, int](x, &y); y }
// and MainHas(): bool { raw.HasFree[int]() }, both non-generic bodies
// served as lowered.
func r1Bodies(w *world) (intFree, mainRelease, mainHas types.ItemID) {
	mutInt := w.tab.Mut(types.Int)
	p0, p1 := w.tab.Param(0), w.tab.Param(1)
	release := w.raw("Release", types.Void, p0, w.tab.Ref(p1))
	hasFree := w.raw("HasFree", types.Bool)

	intFree = w.fn("main", "IntFree", types.Void, types.Int, mutInt)
	fb := w.body(intFree, types.Void, types.Int, mutInt)
	fb.assign(0, fb.b.Place(2, mir.Projection{Kind: mir.ProjDeref, Type: types.Int}), use(cp(1)))
	fb.ret(0)
	fb.done()

	mainRelease = w.fn("main", "MainRelease", types.Int)
	x := &seq{bb: w.body(mainRelease, types.Int)}
	xl, y, r, v := x.local(types.Int), x.local(types.Int), x.local(mutInt), x.local(types.Void)
	x.assign(0, lp(xl), use(imm(types.Int, 7)))
	x.assign(0, lp(y), use(imm(types.Int, 0)))
	x.assign(0, lp(r), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Exclusive, Place: lp(y)})
	x.call(release, []types.TypeID{types.Int, types.Int}, lp(v), cp(xl), cp(r))
	x.assign(x.cur, lp(0), use(cp(y)))
	x.ret(x.cur)
	x.done()

	mainHas = w.fn("main", "MainHas", types.Bool)
	h := &seq{bb: w.body(mainHas, types.Bool)}
	h.call(hasFree, []types.TypeID{types.Int}, lp(0))
	h.ret(h.cur)
	h.done()
	return intFree, mainRelease, mainHas
}

// TestEvalReleaseCallsImplFree: with an impl of Free for int, Release
// runs it with both arguments (y becomes 7), HasFree is true, and each
// lookup's ReadImpl is in the read set.
func TestEvalReleaseCallsImplFree(t *testing.T) {
	w := newWorld(t)
	intFree, mainRelease, mainHas := r1Bodies(w)
	p := &freeProgram{world: w, impls: map[types.TypeID]types.FnRef{types.Int: {Item: intFree}}}
	e := p.evaluator()
	res := w.call(e, mainRelease, 1000)
	if res.Err != nil || w.intOf(res.Value) != 7 {
		t.Fatalf("MainRelease: %+v %v, want 7 (IntFree ran)", res.Err, res.Value)
	}
	want := types.Read{Kind: types.ReadImpl, Key: types.ItemKey("std.core", "Free"), Name: "int"}
	if !hasExactRead(res.Reads, want) {
		t.Errorf("reads %v lack %v", res.Reads.Sorted(), want)
	}
	res = w.call(e, mainHas, 1000)
	if res.Err != nil || w.intOf(res.Value) != 1 {
		t.Fatalf("MainHas: %+v, want true", res.Err)
	}
	if !hasExactRead(res.Reads, want) {
		t.Errorf("HasFree reads %v lack %v", res.Reads.Sorted(), want)
	}
}

// TestEvalReleaseForgets: with no impl, Release forgets x (y stays 0),
// HasFree is false, and ReadNoImpl is recorded, so adding the impl
// invalidates a cached evaluation.
func TestEvalReleaseForgets(t *testing.T) {
	w := newWorld(t)
	_, mainRelease, mainHas := r1Bodies(w)
	p := &freeProgram{world: w, impls: map[types.TypeID]types.FnRef{}}
	e := p.evaluator()
	res := w.call(e, mainRelease, 1000)
	if res.Err != nil || w.intOf(res.Value) != 0 {
		t.Fatalf("MainRelease: %+v, want 0 (x forgotten)", res.Err)
	}
	want := types.Read{Kind: types.ReadNoImpl, Key: types.ItemKey("std.core", "Free"), Name: "int"}
	if !hasExactRead(res.Reads, want) {
		t.Errorf("reads %v lack %v", res.Reads.Sorted(), want)
	}
	if res = w.call(e, mainHas, 1000); res.Err != nil || w.intOf(res.Value) != 0 {
		t.Fatalf("MainHas: %+v, want false", res.Err)
	}
}

// TestEvalWithoutFreeImplsICE: a Program without FreeImpls makes a
// Release or HasFree call an internal error naming the missing
// std.core.Free, never a silent drop.
func TestEvalWithoutFreeImplsICE(t *testing.T) {
	w := newWorld(t)
	_, mainRelease, mainHas := r1Bodies(w)
	e := w.evaluator(0)
	for _, fn := range []types.ItemID{mainRelease, mainHas} {
		ie := source.Guard("r1", source.Span{}, func() { w.call(e, fn, 1000) })
		if ie == nil || !strings.Contains(ie.What, "std.core.Free") {
			t.Errorf("%s: got %v, want an ICE naming the missing std.core.Free", w.items.Get(fn).Key, ie)
		}
	}
}

// readsProgram is the test world serving extra Body.Reads for some items,
// as the driver does for a mono instance whose Release or HasFree it
// resolved.
type readsProgram struct {
	*world
	extra map[types.ItemID][]types.Read
}

func (p *readsProgram) Body(fn types.ItemID, args []types.GenericArg) Body {
	b := p.world.Body(fn, args)
	b.Reads = p.extra[fn]
	return b
}

// TestIncEvalBodyReads: the evaluator's part of incremental read tracking.
// The reads a Program puts in Body.Reads join the evaluation's read set with
// ReadBody on every evaluation that enters the body, compiled code being
// shared, so a ReadNoImpl of std.core.Free behind a folded HasFree reaches the
// cache.
func TestIncEvalBodyReads(t *testing.T) {
	w := newWorld(t)
	callee := w.fn("main", "Callee", types.Int)
	c := &seq{bb: w.body(callee, types.Int)}
	c.assign(0, lp(0), use(imm(types.Int, 3)))
	c.ret(0)
	c.done()
	mainFn := w.fn("main", "MainCallsCallee", types.Int)
	x := &seq{bb: w.body(mainFn, types.Int)}
	x.call(callee, nil, lp(0))
	x.ret(x.cur)
	x.done()
	want := types.Read{Kind: types.ReadNoImpl, Key: types.ItemKey("std.core", "Free"), Name: "int"}
	p := &readsProgram{world: w, extra: map[types.ItemID][]types.Read{callee: {want}}}
	e := New(Config{Types: w.tab, Items: w.items, Consts: w.consts, Interner: w.in, Program: p,
		Known: w.known, Target: types.X86_64Windows, TargetItem: w.target})
	for i := 0; i < 2; i++ {
		res := w.call(e, mainFn, 1000)
		if res.Err != nil || w.intOf(res.Value) != 3 {
			t.Fatalf("run %d: %+v, want 3", i, res.Err)
		}
		if !hasExactRead(res.Reads, want) {
			t.Errorf("run %d: reads %v lack %v", i, res.Reads.Sorted(), want)
		}
	}
}
