// Why: the tagged mode checks the memory model while tasks run, so it needs a
// host that schedules spawns, joins and Io entries deterministically by seed.
// What: TestSBHost_ScopeJoin, _Schedules, _IoRefused, _Prelude, _Globals and
// _Cancel, FuzzTagged and BenchmarkTaggedRun.
// How: MIR built by hand in eval's test world with a small std.core (Io,
// Scope, Io.Scope, InertIoValue, IoSleep, an Inert table entry).

package eval

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// hostWorld is a world with std.core's host items.
type hostWorld struct {
	*world
	ioT, scopeT              types.TypeID
	ioScope, sleep, inertNow types.ItemID
	bump                     types.ItemID
}

func newHostWorld(t *testing.T) *hostWorld {
	w := newWorld(t)
	h := &hostWorld{world: w}
	core := w.module("std.core")
	w.items.Module(core).Std = true
	var ioItem types.ItemID
	ioItem, h.ioT = w.structType("std.core", "Io", types.U64)
	w.known.Io = ioItem
	_, h.scopeT = w.structType("std.core", "Scope", w.array(16, types.U64))
	h.ioScope = w.fn("std.core", "Io.Scope", h.scopeT, h.ioT)
	sc := w.items.Get(h.ioScope)
	sc.Name, sc.Owner = w.in.Intern("Scope"), ioItem
	sc.Decl.(*types.FuncDecl).Bodiless = true
	// InertIoValue(): Io { return Io(0) }
	iv := w.fn("std.core", "InertIoValue", h.ioT)
	x := w.body(iv, h.ioT)
	x.assign(0, lp(0), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: ioItem, Ops: x.b.AddOperands(imm(types.U64, 0))})
	x.ret(0)
	x.done()
	// IoSleep(io: Io, nanos: i64): i64 (its result zeroed by the host).
	h.sleep = w.fn("std.core", "IoSleep", types.I64, h.ioT, types.I64)
	y := w.body(h.sleep, types.I64, h.ioT, types.I64)
	y.assign(0, lp(0), use(imm(types.I64, 99))) // never runs: the host serves it
	y.ret(0)
	y.done()
	// InertNow(io: Io): i64, an inert table entry.
	h.inertNow = w.fn("std.core", "InertNow", types.I64, h.ioT)
	z := w.body(h.inertNow, types.I64, h.ioT)
	z.assign(0, lp(0), use(imm(types.I64, 7)))
	z.ret(0)
	z.done()
	// Bump(out: mut i64) { *out = *out +% 1 }
	mi := w.tab.Mut(types.I64)
	h.bump = w.fn("lib", "Bump", types.Void, mi)
	b := w.body(h.bump, types.Void, mi)
	b.assign(0, b.deref(1, types.I64), bin(mir.AddWrap, mir.Copy(b.deref(1, types.I64)), imm(types.I64, 1)))
	b.ret(0)
	b.done()
	return h
}

// spawn ends blk with s.go fn(args) and goes on at next.
func (x *bb) spawn(blk mir.BlockID, s mir.LocalID, fn types.ItemID, next mir.BlockID, args ...mir.Operand) {
	x.call(blk, fn, lp(x.local(types.Void)), next, args...)
	x.b.Blocks[blk].Term.Spawn = s
}

// join appends StJoin s to blk.
func (x *bb) join(blk mir.BlockID, s mir.LocalID) {
	x.b.Blocks[blk].Stmts = append(x.b.Blocks[blk].Stmts, mir.Statement{Kind: mir.StJoin, Local: s, Span: x.sp()})
}

// scopeRun builds Run(io: Io): i64 { a, b := 0, 0; scope s := io.Scope() {
// s.go Bump(mut a); s.go Bump(mut b); [read a] }; return a + b }.
func (h *hostWorld) scopeRun(name string, readA bool) types.ItemID {
	w := h.world
	f := w.fn("main", name, types.I64, h.ioT)
	x := w.body(f, types.I64, h.ioT)
	a, b := x.local(types.I64), x.local(types.I64)
	s := x.b.AddLocal(mir.Local{Type: h.scopeT, Kind: mir.LocalScope, Loop: mir.NoLoop, Span: x.sp()})
	ta, tb, tmp := x.local(w.tab.Mut(types.I64)), x.local(w.tab.Mut(types.I64)), x.local(types.I64)
	b1, b2, b3 := x.block(), x.block(), x.block()
	x.assign(0, lp(a), use(imm(types.I64, 0)))
	x.assign(0, lp(b), use(imm(types.I64, 0)))
	x.call(0, h.ioScope, lp(s), b1, cp(1))
	x.assign(b1, lp(ta), mutOf(lp(a)))
	x.spawn(b1, s, h.bump, b2, cp(ta))
	x.assign(b2, lp(tb), mutOf(lp(b)))
	x.spawn(b2, s, h.bump, b3, cp(tb))
	if readA {
		x.assign(b3, lp(tmp), use(cp(a)))
	}
	x.join(b3, s)
	x.assign(b3, lp(0), bin(mir.AddWrap, cp(a), cp(b)))
	x.ret(b3)
	x.done()
	return f
}

// TestSBHost_ScopeJoin: two spawns writing disjoint mut arguments, every
// schedule of seeds 1 to 32, run clean with the same value; a parent read
// of a mut argument before the join is VRemovedTag or VProtected under some
// seed.
func TestSBHost_ScopeJoin(t *testing.T) {
	h := newHostWorld(t)
	good, bad := h.scopeRun("Good", false), h.scopeRun("Bad", true)
	e := h.evaluator(0)
	found := 0
	for seed := uint64(1); seed <= 32; seed++ {
		r := h.tcall(e, good, &TagOptions{Seed: seed})
		wantClean(t, r)
		if v := h.intOf(r.Value); v != 2 {
			t.Fatalf("seed %d: Run = %d, want 2", seed, v)
		}
		if len(r.Starts) != 2 {
			t.Fatalf("seed %d: %d starts, want 2", seed, len(r.Starts))
		}
		rb := h.tcall(e, bad, &TagOptions{Seed: seed})
		if len(rb.Violations) > 0 {
			if k := rb.Violations[0].Kind; k != VRemovedTag && k != VProtected {
				t.Fatalf("seed %d: violation %s, want VRemovedTag or VProtected", seed, k)
			}
			found++
		}
	}
	if found == 0 {
		t.Fatal("a parent read of a task's mut argument before the join is clean under every seed of 1 to 32")
	}
	t.Logf("the parent read is a violation under %d of 32 seeds", found)
}

// TestSBHost_Schedules: over 1,000 seeds a spawn's start covers now, a
// later yield point and the join (the host's three choices).
func TestSBHost_Schedules(t *testing.T) {
	h := newHostWorld(t)
	f := h.scopeRun("Two", false)
	e := h.evaluator(0)
	seen := map[[2]uint32]bool{}
	for seed := uint64(0); seed < 1000; seed++ {
		r := h.tcall(e, f, &TagOptions{Seed: seed})
		wantClean(t, r)
		seen[[2]uint32{r.Starts[0], r.Starts[1]}] = true
	}
	// Yield points of the parent: 1 and 2 are the spawns, 3 the join.
	if len(seen) < 3 {
		t.Fatalf("only %d distinct schedules in 1,000 seeds: %v", len(seen), seen)
	}
	for _, want := range [][2]uint32{{1, 2}, {3, 3}, {1, 3}} {
		if !seen[want] {
			t.Errorf("schedule %v never taken: %v", want, seen)
		}
	}
}

// TestSBHost_IoRefused: an Io entry the host does not serve is Skipped "io
// <entry>"; IoSleep is served as a yield point.
func TestSBHost_IoRefused(t *testing.T) {
	h := newHostWorld(t)
	w := h.world
	f := w.fn("main", "Now", types.I64, h.ioT)
	x := w.body(f, types.I64, h.ioT)
	b1 := x.block()
	x.call(0, h.inertNow, lp(0), b1, cp(1))
	x.ret(b1)
	x.done()
	r := h.tcall(h.evaluator(0), f, nil)
	if r.Skipped != "io now" {
		t.Fatalf("Skipped %q (err %+v), want \"io now\"", r.Skipped, r.Err)
	}
	g := w.fn("main", "Sleep", types.I64, h.ioT)
	y := w.body(g, types.I64, h.ioT)
	c1 := y.block()
	y.call(0, h.sleep, lp(0), c1, cp(1), imm(types.I64, 0))
	y.ret(c1)
	y.done()
	rs := h.tcall(h.evaluator(0), g, nil)
	wantClean(t, rs)
	if v := h.intOf(rs.Value); v != 0 {
		t.Fatalf("IoSleep served as %d, want the host's zero result", v)
	}
}

// TestSBHost_Prelude: Cond() forced by TagOptions.Cond and drawn from the
// seed otherwise.
func TestSBHost_Prelude(t *testing.T) {
	w := newWorld(t)
	cond := w.fn("main", "Cond", types.Bool)
	w.items.Get(cond).Decl.(*types.FuncDecl).Bodiless = true
	f := w.fn("main", "C", types.Bool)
	x := w.body(f, types.Bool)
	b1 := x.block()
	x.call(0, cond, lp(0), b1)
	x.ret(b1)
	x.done()
	e := w.evaluator(0)
	for _, c := range []struct {
		cond int8
		want int64
	}{{1, 1}, {-1, 0}} {
		r := w.tcall(e, f, &TagOptions{Cond: c.cond})
		wantClean(t, r)
		if v := w.intOf(r.Value); v != c.want {
			t.Fatalf("Cond forced %d gives %d, want %d", c.cond, v, c.want)
		}
	}
	ones := 0
	for seed := uint64(0); seed < 64; seed++ {
		r := w.tcall(e, f, &TagOptions{Seed: seed})
		wantClean(t, r)
		ones += int(w.intOf(r.Value))
	}
	if ones == 0 || ones == 64 {
		t.Fatalf("Cond drawn from 64 seeds is true %d times", ones)
	}
}

// FuzzTagged runs FuzzEval's generated bodies in the tagged mode: no ICE
// and no Go panic, and a body the plain mode runs to a value either
// completes with that value, reports a violation, or is Skipped.
func FuzzTagged(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10})
	f.Add([]byte{0, 0, 0, 0, 0, 2, 0, 0, 2, 4, 4})
	f.Add([]byte{3, 1, 4, 1, 5, 9, 2, 6, 5, 3, 5, 8, 9, 7, 9, 3, 2, 3, 8, 4})
	f.Fuzz(func(t *testing.T, data []byte) {
		w := newWorld(t)
		fn, ok := fuzzBody(w, &fuzzSrc{b: data})
		if !ok {
			return
		}
		arg := w.intVal(types.Int, int64(len(data)))
		plain := w.call(w.evaluator(4096), fn, 20000, arg)
		var r TaggedResult
		if ie := source.Guard("FuzzTagged", source.Span{}, func() {
			r = w.evaluator(4096).RunTagged(&types.EvalRequest{What: types.EvalCall, Item: fn, Args: []types.ConstID{arg}, Fuel: 20000,
				Stage: types.StageBodies, Module: w.module("main"), Site: source.Span{File: 1, Start: 1, End: 2}, Target: types.X86_64Windows}, &TagOptions{Seed: uint64(len(data))})
		}); ie != nil {
			t.Fatalf("Go panic or ICE: %s", ie.What)
		}
		if plain.Err != nil || len(r.Violations) > 0 || r.Skipped != "" {
			return
		}
		if r.Err != nil {
			t.Fatalf("plain mode gives %v, tagged mode the error %+v", w.intOf(plain.Value), r.Err)
		}
		if w.intOf(r.Value) != w.intOf(plain.Value) || r.Steps != plain.Steps {
			t.Fatalf("tagged %d in %d steps, plain %d in %d", w.intOf(r.Value), r.Steps, w.intOf(plain.Value), plain.Steps)
		}
	})
}

// BenchmarkTaggedRun is the tagged-run benchmark: the fuel test's
// loop body (BenchmarkEvalLoop's) run through RunTagged, beside the plain
// run, so the ratio of the two is the tagged mode's cost (target: at most
// 10 times). A number from it is a Go benchmark that sizes the runs, never
// a tools/bench row.
func BenchmarkTaggedRun(b *testing.B) {
	w := newWorld(b)
	f := w.loopFn("lib", "Loop")
	n := w.intVal(types.Int, 100_000)
	for _, tagged := range []bool{false, true} {
		name := "plain"
		if tagged {
			name = "tagged"
		}
		b.Run(name, func(b *testing.B) {
			e := w.evaluator(0)
			var steps uint64
			for range b.N {
				if tagged {
					r := w.tcall(e, f, nil, n)
					if r.Err != nil || len(r.Violations) > 0 {
						b.Fatalf("%+v %+v", r.Err, r.Violations)
					}
					steps += r.Steps
				} else {
					r := w.call(e, f, 1<<40, n)
					if r.Err != nil {
						b.Fatal(r.Err)
					}
					steps += r.Steps
				}
			}
			b.ReportMetric(float64(steps)/b.Elapsed().Seconds(), "steps/s")
		})
	}
}

// TestSBHost_Globals: a var global of the unit's own module is an
// allocation of each run, written from its value at the start (two runs that
// add one both see the initial value); a std var global makes the run
// Skipped; plain mode still refuses both.
func TestSBHost_Globals(t *testing.T) {
	w := newWorld(t)
	g := w.item("main", "G", types.KindGlobal, &types.GlobalDecl{Type: types.I64, Value: w.intVal(types.I64, 5), HasValue: true})
	core := w.module("std.core")
	w.items.Module(core).Std = true
	sg := w.item("std.core", "Hidden", types.KindGlobal, &types.GlobalDecl{Type: types.I64, Value: w.intVal(types.I64, 1), HasValue: true})
	mi := w.tab.Ref(types.I64) // &G: tag 0, own memory
	bump := func(name string, glob types.ItemID) types.ItemID {
		f := w.fn("main", name, types.I64)
		x := w.body(f, types.I64)
		p := x.local(mi)
		x.assign(0, lp(p), mir.Rvalue{Kind: mir.RvGlobalRef, Item: glob})
		x.assign(0, x.deref(p, types.I64), bin(mir.AddWrap, mir.Copy(x.deref(p, types.I64)), imm(types.I64, 1)))
		x.assign(0, lp(0), use(mir.Copy(x.deref(p, types.I64))))
		x.ret(0)
		x.done()
		return f
	}
	own, std := bump("Own", g), bump("Std", sg)
	e := w.evaluator(0)
	for run := 0; run < 2; run++ {
		r := w.tcall(e, own, nil)
		wantClean(t, r)
		if v := w.intOf(r.Value); v != 6 {
			t.Fatalf("run %d: G + 1 = %d, want 6 (a fresh allocation per run)", run, v)
		}
	}
	if r := w.tcall(e, std, nil); r.Skipped != "std global std.core.Hidden" {
		t.Fatalf("a std var global: Skipped %q (err %+v), want \"std global std.core.Hidden\"", r.Skipped, r.Err)
	}
	if r := w.call(e, own, 0); r.Err == nil || r.Err.Kind != types.EvalRefused || r.Err.Refusal != types.RefuseGlobal {
		t.Fatalf("plain mode: %+v, want RefuseGlobal", r.Err)
	}
}

// TestSBHost_Cancel: after a join with cancel, a task that starts then gets
// SysErr.Canceled from a host Io entry; a task that started before gets
// SysErr.None (a task of a cancelled scope still starts).
func TestSBHost_Cancel(t *testing.T) {
	h := newHostWorld(t)
	w := h.world
	sysErr, sysErrT := w.enumType("std.core", "SysErr", nil, nil, nil)
	ed := w.items.Get(sysErr).Decl.(*types.EnumDecl)
	ed.Variants[0].Name, ed.Variants[2].Name = w.in.Intern("None"), w.in.Intern("Canceled")
	ed.Variants[2].Discr = 8
	_, resT := w.structType("std.core", "IoResult", types.I64, sysErrT, types.I64)
	yield := w.fn("std.core", "IoYield", resT, h.ioT)
	mi := w.tab.Mut(types.U64)
	// Probe(io: Io, out: mut u64) { *out = discriminant(IoYield(io).err) }
	probe := w.fn("lib", "Probe", types.Void, h.ioT, mi)
	x := w.body(probe, types.Void, h.ioT, mi)
	res := x.local(resT)
	b1 := x.block()
	x.call(0, yield, lp(res), b1, cp(1))
	x.assign(b1, x.deref(2, types.U64), mir.Rvalue{Kind: mir.RvDiscriminant, Place: x.b.Place(res, mir.Projection{Kind: mir.ProjField, A: 1, Type: sysErrT})})
	x.ret(b1)
	x.done()
	// Run(io: Io): u64 { out := 99; scope s := io.Scope() { s.go Probe(io,
	// mut out) } cancel; return out }
	f := w.fn("main", "Run", types.U64, h.ioT)
	y := w.body(f, types.U64, h.ioT)
	out, mo := y.local(types.U64), y.local(mi)
	s := y.b.AddLocal(mir.Local{Type: h.scopeT, Kind: mir.LocalScope, Loop: mir.NoLoop, Span: y.sp()})
	c1, c2 := y.block(), y.block()
	y.assign(0, lp(out), use(imm(types.U64, 99)))
	y.call(0, h.ioScope, lp(s), c1, cp(1))
	y.assign(c1, lp(mo), mutOf(lp(out)))
	y.spawn(c1, s, probe, c2, cp(1), cp(mo))
	y.b.Blocks[c2].Stmts = append(y.b.Blocks[c2].Stmts, mir.Statement{Kind: mir.StJoin, Local: s, Cancel: true, Span: y.sp()})
	y.assign(c2, lp(0), use(cp(out)))
	y.ret(c2)
	y.done()
	e := h.evaluator(0)
	seen := map[int64]bool{}
	for seed := uint64(0); seed < 64; seed++ {
		r := h.tcall(e, f, &TagOptions{Seed: seed})
		wantClean(t, r)
		v := h.intOf(r.Value)
		want := int64(0) // started at its spawn, before the cancel
		if r.Starts[0] >= 2 {
			want = 8 // started at the join, after the cancel
		}
		if v != want {
			t.Fatalf("seed %d: start %d gives %d, want %d", seed, r.Starts[0], v, want)
		}
		seen[v] = true
	}
	if !seen[0] || !seen[8] {
		t.Fatalf("64 seeds give only %v", seen)
	}
}
