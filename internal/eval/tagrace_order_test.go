// Why: the tagged mode's race detector must honor atomic memory orders: only
// an ordering edge makes publishing data through a flag race-free.
// What: TestRelaxedPublishRaces and TestCasOrders, over
// the orders of tagrace_order.go.
// How: two tasks publish a Cell through an atomic flag over 32 seeds; Relaxed
// alone, or a fence on one side only, must give VRace, while Release/Acquire,
// SeqCst, or Relaxed between a release and an acquire fence runs clean.

package eval

import (
	"fmt"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// TestRelaxedPublishRaces: task A writes a Cell and then stores 1
// to an atomic flag; task B loads the flag and, when it reads 1, reads the
// Cell. Over 32 seeds, a case that does not order the publication must
// give VRace whenever B read 1 (and in at least one seed), and a case that
// orders it must run clean in every seed.
func TestRelaxedPublishRaces(t *testing.T) {
	cases := []struct {
		name           string
		store, load    uint8
		wfence, rfence bool
		race           bool
	}{
		{"relaxed", ordRelaxed, ordRelaxed, false, false, true},
		{"release acquire", ordRelease, ordAcquire, false, false, false},
		{"seqcst", ordSeqCst, ordSeqCst, false, false, false},
		{"relaxed with both fences", ordRelaxed, ordRelaxed, true, true, false},
		{"release store, relaxed load", ordRelease, ordRelaxed, false, false, true},
		{"relaxed store, acquire load", ordRelaxed, ordAcquire, false, false, true},
		{"release fence, acquire load", ordRelaxed, ordAcquire, true, false, false},
		{"release store, acquire fence", ordRelease, ordRelaxed, false, true, false},
		{"release fence only", ordRelaxed, ordRelaxed, true, false, true},
		{"acquire fence only", ordRelaxed, ordRelaxed, false, true, true},
		// Unordered makes no edge, even between two fences (a fence
		// synchronizes only through atomics of at least relaxed order).
		{"unordered with both fences", ordUnordered, ordUnordered, true, true, true},
		{"unordered store, both fences, relaxed load", ordUnordered, ordRelaxed, true, true, true},
		{"relaxed store, both fences, unordered load", ordRelaxed, ordUnordered, true, true, true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRaceWorld(t)
			o := r.orderedOps()
			f := r.publish(o, fmt.Sprintf("Pub%d", i), tc.store, tc.load, tc.wfence, tc.rfence)
			e := r.evaluator(0)
			races := 0
			for seed := uint64(1); seed <= 32; seed++ {
				res := r.tcall(e, f, &TagOptions{Seed: seed})
				if !tc.race {
					wantClean(t, res)
					continue
				}
				if len(res.Violations) > 0 {
					v := wantViolation(t, res, VRace)
					if v.Task == 0 || v.Other == 0 || v.Task == v.Other {
						t.Fatalf("seed %d: VRace between tasks %d and %d", seed, v.Task, v.Other)
					}
					races++
					continue
				}
				wantClean(t, res)
				if r.intOf(res.Value) == 1 {
					t.Fatalf("seed %d: B read the flag as 1 and then the Cell with no VRace", seed)
				}
			}
			if tc.race && races == 0 {
				t.Fatal("no seed ran B after A's store: the case checks nothing")
			}
		})
	}
}

// orderedIntrinsics are std.raw's LoadOrd, StoreOrd and FenceOrd in the
// race world.
type orderedIntrinsics struct{ load, store, fence types.ItemID }

func (r *raceWorld) orderedOps() orderedIntrinsics {
	w := r.world
	ap := w.tab.Ref(w.tab.Named(r.atomic, targs(w.tab.Param(0))))
	o := orderedIntrinsics{
		load:  w.method("std.raw", "Atomic", "LoadOrd", 1, w.tab.Param(0), ap, types.U8),
		store: w.method("std.raw", "Atomic", "StoreOrd", 1, types.Void, ap, w.tab.Param(0), types.U8),
		fence: w.fn("std.raw", "FenceOrd", types.Void, types.U8),
	}
	for _, f := range []types.ItemID{o.load, o.store, o.fence} {
		fd := w.items.Get(f).Decl.(*types.FuncDecl)
		fd.Bodiless, fd.Intrinsic = true, true
	}
	return o
}

// publish builds Run(io: Io): i64 { c := Cell(0); l := Atomic[u32](0);
// seen := 0; scope s { s.go W(&c, &l); s.go R(&c, &l, &seen) }; return seen }
// with W: c.v = 7; [FenceOrd(Release)]; l.StoreOrd(1, so), and R: if
// l.LoadOrd(lo) == 1 { [FenceOrd(Acquire)]; _ = c.v }. Run returns 1 when
// R read the flag as 1 (R reports it through a second Cell).
func (r *raceWorld) publish(o orderedIntrinsics, name string, so, lo uint8, wfence, rfence bool) types.ItemID {
	w := r.world
	rc, ra := w.tab.Ref(r.cellT), w.tab.Ref(r.at32)
	u32 := []types.TypeID{types.U32}

	wr := w.fn("main", name+"W", types.Void, rc, ra)
	x := w.body(wr, types.Void, rc, ra)
	b1, b2 := x.block(), x.block()
	x.assign(0, r.field(x, 1), use(imm(types.I64, 7)))
	if wfence {
		x.call(0, o.fence, lp(x.local(types.Void)), b1, imm(types.U8, ordRelease))
	} else {
		x.gotoB(0, b1)
	}
	x.callG(b1, o.store, u32, lp(x.local(types.Void)), b2, cp(2), imm(types.U32, 1), imm(types.U8, int64(so)))
	x.ret(b2)
	x.done()

	rd := w.fn("main", name+"R", types.Void, rc, ra, rc)
	y := w.body(rd, types.Void, rc, ra, rc)
	v, eq, got := y.local(types.U32), y.local(types.Bool), y.local(types.I64)
	c1, c2, c3, c4 := y.block(), y.block(), y.block(), y.block()
	y.callG(0, o.load, u32, lp(v), c1, cp(2), imm(types.U8, int64(lo)))
	y.assign(c1, lp(eq), bin(mir.Eq, cp(v), imm(types.U32, 1)))
	y.branch(c1, cp(eq), c2, c4)
	if rfence {
		y.call(c2, o.fence, lp(y.local(types.Void)), c3, imm(types.U8, ordAcquire))
	} else {
		y.gotoB(c2, c3)
	}
	y.assign(c3, lp(got), use(mir.Copy(r.field(y, 1)))) // the read the flag publishes
	y.assign(c3, r.field(y, 3), use(imm(types.I64, 1))) // R's own Cell: it saw 1
	y.gotoB(c3, c4)
	y.ret(c4)
	y.done()

	f := w.fn("main", name, types.I64, r.ioT)
	z := w.body(f, types.I64, r.ioT)
	c, l, seen := z.local(r.cellT), z.local(r.at32), z.local(r.cellT)
	pc, pl, ps := z.local(rc), z.local(ra), z.local(rc)
	s := z.b.AddLocal(mir.Local{Type: r.scopeT, Kind: mir.LocalScope, Loop: mir.NoLoop, Span: z.sp()})
	d1, d2, d3 := z.block(), z.block(), z.block()
	z.assign(0, lp(c), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: r.cell, Ops: z.b.AddOperands(imm(types.I64, 0))})
	z.assign(0, lp(seen), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: r.cell, Ops: z.b.AddOperands(imm(types.I64, 0))})
	z.assign(0, lp(l), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: r.atomic, Type: r.at32, Ops: z.b.AddOperands(imm(types.U32, 0))})
	z.assign(0, lp(pc), refOf(lp(c)))
	z.assign(0, lp(pl), refOf(lp(l)))
	z.assign(0, lp(ps), refOf(lp(seen)))
	z.call(0, r.ioScope, lp(s), d1, cp(1))
	z.spawn(d1, s, wr, d2, cp(pc), cp(pl))
	z.spawn(d2, s, rd, d3, cp(pc), cp(pl), cp(ps))
	z.join(d3, s)
	z.assign(d3, lp(0), use(mir.Copy(r.field(z, ps))))
	z.ret(d3)
	z.done()
	return f
}

// TestCasOrders: a CAS is a read-modify-write of its success
// order when it writes and a load of its failure order when it does not
// (tagrace_order.go, casOK). A writes the Cell and publishes the flag with
// a store or a CAS that succeeds; B reads the flag with a load or a CAS
// that always fails (5 to 6), then the Cell when it read 1.
func TestCasOrders(t *testing.T) {
	cases := []struct {
		name   string
		wcas   bool
		ws, wf uint8
		rcas   bool
		rs, rf uint8
		race   bool
	}{
		{"release cas, acquire load", true, ordRelease, ordRelaxed, false, ordAcquire, 0, false},
		{"relaxed cas, acquire load", true, ordRelaxed, ordRelaxed, false, ordAcquire, 0, true},
		{"acqrel cas, acquire load", true, ordAcqRel, ordAcquire, false, ordAcquire, 0, false},
		{"release store, failing cas acquire failure", false, ordRelease, 0, true, ordSeqCst, ordAcquire, false},
		{"release store, failing cas relaxed failure", false, ordRelease, 0, true, ordSeqCst, ordRelaxed, true},
		{"release store, failing seqcst cas relaxed failure", false, ordRelease, 0, true, ordAcqRel, ordRelaxed, true},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRaceWorld(t)
			o := r.orderedOps()
			f := r.casPublish(o, fmt.Sprintf("Cas%d", i), tc.wcas, tc.ws, tc.wf, tc.rcas, tc.rs, tc.rf)
			e := r.evaluator(0)
			races := 0
			for seed := uint64(1); seed <= 32; seed++ {
				res := r.tcall(e, f, &TagOptions{Seed: seed})
				if !tc.race {
					wantClean(t, res)
					continue
				}
				if len(res.Violations) > 0 {
					wantViolation(t, res, VRace)
					races++
					continue
				}
				wantClean(t, res)
				if r.intOf(res.Value) == 1 {
					t.Fatalf("seed %d: B read the flag as 1 and then the Cell with no VRace", seed)
				}
			}
			if tc.race && races == 0 {
				t.Fatal("no seed ran B after A's publication: the case checks nothing")
			}
		})
	}
}

// casPublish is publish with A's publication a StoreOrd(1, ws) or a
// CasOrd(0, 1, ws, wf), and B's read a LoadOrd(rs) or a CasOrd(5, 6, rs,
// rf) whose value it tests.
func (r *raceWorld) casPublish(o orderedIntrinsics, name string, wcas bool, ws, wf uint8, rcas bool, rs, rf uint8) types.ItemID {
	w := r.world
	rc, ra := w.tab.Ref(r.cellT), w.tab.Ref(r.at32)
	u32 := []types.TypeID{types.U32}
	ap := w.tab.Ref(w.tab.Named(r.atomic, targs(w.tab.Param(0))))
	resT := w.tab.Record([]types.RecordField{{Name: w.in.Intern("ok"), Type: types.Bool}, {Name: w.in.Intern("value"), Type: w.tab.Param(0)}})
	cas := w.method("std.raw", "Atomic", "CasOrd", 1, resT, ap, w.tab.Param(0), w.tab.Param(0), types.U8, types.U8, types.Bool)
	fd := w.items.Get(cas).Decl.(*types.FuncDecl)
	fd.Bodiless, fd.Intrinsic = true, true
	res32 := w.tab.Record([]types.RecordField{{Name: w.in.Intern("ok"), Type: types.Bool}, {Name: w.in.Intern("value"), Type: types.U32}})

	wr := w.fn("main", name+"W", types.Void, rc, ra)
	x := w.body(wr, types.Void, rc, ra)
	b1 := x.block()
	x.assign(0, r.field(x, 1), use(imm(types.I64, 7)))
	if wcas {
		x.callG(0, cas, u32, lp(x.local(res32)), b1, cp(2), imm(types.U32, 0), imm(types.U32, 1), imm(types.U8, int64(ws)), imm(types.U8, int64(wf)), imm(types.Bool, 0))
	} else {
		x.callG(0, o.store, u32, lp(x.local(types.Void)), b1, cp(2), imm(types.U32, 1), imm(types.U8, int64(ws)))
	}
	x.ret(b1)
	x.done()

	rd := w.fn("main", name+"R", types.Void, rc, ra, rc)
	y := w.body(rd, types.Void, rc, ra, rc)
	v, eq, got := y.local(types.U32), y.local(types.Bool), y.local(types.I64)
	c1, c2, c3 := y.block(), y.block(), y.block()
	if rcas {
		rr := y.local(res32)
		c0 := y.block()
		y.callG(0, cas, u32, lp(rr), c0, cp(2), imm(types.U32, 5), imm(types.U32, 6), imm(types.U8, int64(rs)), imm(types.U8, int64(rf)), imm(types.Bool, 0))
		y.assign(c0, lp(v), use(mir.Copy(y.b.Place(rr, mir.Projection{Kind: mir.ProjField, A: 1, Type: types.U32}))))
		y.gotoB(c0, c1)
	} else {
		y.callG(0, o.load, u32, lp(v), c1, cp(2), imm(types.U8, int64(rs)))
	}
	y.assign(c1, lp(eq), bin(mir.Eq, cp(v), imm(types.U32, 1)))
	y.branch(c1, cp(eq), c2, c3)
	y.assign(c2, lp(got), use(mir.Copy(r.field(y, 1))))
	y.assign(c2, r.field(y, 3), use(imm(types.I64, 1)))
	y.gotoB(c2, c3)
	y.ret(c3)
	y.done()

	f := w.fn("main", name, types.I64, r.ioT)
	z := w.body(f, types.I64, r.ioT)
	c, l, seen := z.local(r.cellT), z.local(r.at32), z.local(r.cellT)
	pc, pl, ps := z.local(rc), z.local(ra), z.local(rc)
	s := z.b.AddLocal(mir.Local{Type: r.scopeT, Kind: mir.LocalScope, Loop: mir.NoLoop, Span: z.sp()})
	d1, d2, d3 := z.block(), z.block(), z.block()
	z.assign(0, lp(c), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: r.cell, Ops: z.b.AddOperands(imm(types.I64, 0))})
	z.assign(0, lp(seen), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: r.cell, Ops: z.b.AddOperands(imm(types.I64, 0))})
	z.assign(0, lp(l), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: r.atomic, Type: r.at32, Ops: z.b.AddOperands(imm(types.U32, 0))})
	z.assign(0, lp(pc), refOf(lp(c)))
	z.assign(0, lp(pl), refOf(lp(l)))
	z.assign(0, lp(ps), refOf(lp(seen)))
	z.call(0, r.ioScope, lp(s), d1, cp(1))
	z.spawn(d1, s, wr, d2, cp(pc), cp(pl))
	z.spawn(d2, s, rd, d3, cp(pc), cp(pl), cp(ps))
	z.join(d3, s)
	z.assign(d3, lp(0), use(mir.Copy(r.field(z, ps))))
	z.ret(d3)
	z.done()
	return f
}
