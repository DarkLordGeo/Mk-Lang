// Why: two tasks writing the same interior memory (a Cell) with no ordering
// between them is a data race, and the tagged mode must report it.
// What: TestSBRace_InteriorUnordered and TestSBRace_AtomicOrders.
// How: MIR built by hand in taghost_test.go's host world, with a Cell and a
// raw.Atomic shaped like std's, run under every schedule of a seed range.

package eval

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// raceWorld is the host world with std.core.Cell (one i64) and
// std.raw.Atomic[T] with its intrinsic methods Swap and Store.
type raceWorld struct {
	*hostWorld
	cell, atomic      types.ItemID
	cellT, at32       types.TypeID
	swap, store       types.ItemID
	put, locked, plus types.ItemID
}

func newRaceWorld(t *testing.T) *raceWorld {
	h := newHostWorld(t)
	w := h.world
	r := &raceWorld{hostWorld: h}
	r.cell, r.cellT = w.structType("std.core", "Cell", types.I64)
	w.known.Cell = r.cell
	r.atomic = w.genericStruct("std.raw", "Atomic", 1, w.tab.Param(0))
	w.known.Atomic = r.atomic
	r.at32 = w.tab.Named(r.atomic, targs(types.U32))
	ap := w.tab.Ref(w.tab.Named(r.atomic, targs(w.tab.Param(0))))
	r.swap = w.method("std.raw", "Atomic", "Swap", 1, w.tab.Param(0), ap, w.tab.Param(0))
	r.store = w.method("std.raw", "Atomic", "Store", 1, types.Void, ap, w.tab.Param(0))
	for _, f := range []types.ItemID{r.swap, r.store} {
		fd := w.items.Get(f).Decl.(*types.FuncDecl)
		fd.Bodiless, fd.Intrinsic = true, true
	}
	rc, ra := w.tab.Ref(r.cellT), w.tab.Ref(r.at32)
	// Put(c: &Cell, v: i64) { c.v = v }: a write of interior bytes.
	r.put = w.fn("lib", "Put", types.Void, rc, types.I64)
	x := w.body(r.put, types.Void, rc, types.I64)
	x.assign(0, r.field(x, 1), use(cp(2)))
	x.ret(0)
	x.done()
	// Locked(c: &Cell, l: &Atomic[u32], v: i64) { l.Swap(1); c.v = v;
	// l.Store(0) }: the same write inside an atomic-based lock.
	r.locked = w.fn("lib", "Locked", types.Void, rc, ra, types.I64)
	y := w.body(r.locked, types.Void, rc, ra, types.I64)
	old := y.local(types.U32)
	b1, b2 := y.block(), y.block()
	y.callG(0, r.swap, []types.TypeID{types.U32}, lp(old), b1, cp(2), imm(types.U32, 1))
	y.assign(b1, r.field(y, 1), use(cp(3)))
	y.callG(b1, r.store, []types.TypeID{types.U32}, lp(y.local(types.Void)), b2, cp(2), imm(types.U32, 0))
	y.ret(b2)
	y.done()
	return r
}

// field is (*l).v for a local l of type &Cell.
func (r *raceWorld) field(x *bb, l mir.LocalID) mir.Place {
	return x.b.Place(l, mir.Projection{Kind: mir.ProjDeref, Type: r.cellT}, mir.Projection{Kind: mir.ProjField, A: 0, Type: types.I64})
}

// twoTasks builds Run(io: Io): i64 { c := Cell(0); l := Atomic[u32](0);
// scope s := io.Scope() { s.go F(&c[, &l], 1); s.go F(&c[, &l], 2) };
// return c.v }, with F Locked when lock is set and Put otherwise.
func (r *raceWorld) twoTasks(name string, lock bool) types.ItemID {
	w := r.world
	f := w.fn("main", name, types.I64, r.ioT)
	x := w.body(f, types.I64, r.ioT)
	c, l := x.local(r.cellT), x.local(r.at32)
	rc, rl := x.local(w.tab.Ref(r.cellT)), x.local(w.tab.Ref(r.at32))
	s := x.b.AddLocal(mir.Local{Type: r.scopeT, Kind: mir.LocalScope, Loop: mir.NoLoop, Span: x.sp()})
	b1, b2, b3 := x.block(), x.block(), x.block()
	x.assign(0, lp(c), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: r.cell, Ops: x.b.AddOperands(imm(types.I64, 0))})
	x.assign(0, lp(l), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: r.atomic, Type: r.at32, Ops: x.b.AddOperands(imm(types.U32, 0))})
	x.assign(0, lp(rc), refOf(lp(c)))
	x.assign(0, lp(rl), refOf(lp(l)))
	x.call(0, r.ioScope, lp(s), b1, cp(1))
	for i, blk := range []mir.BlockID{b1, b2} {
		next := []mir.BlockID{b2, b3}[i]
		v := imm(types.I64, int64(i+1))
		if lock {
			x.spawn(blk, s, r.locked, next, cp(rc), cp(rl), v)
		} else {
			x.spawn(blk, s, r.put, next, cp(rc), v)
		}
	}
	x.join(b3, s)
	x.assign(b3, lp(0), use(mir.Copy(r.field(x, rc))))
	x.ret(b3)
	x.done()
	return f
}

// TestSBRace_InteriorUnordered: two tasks writing one interior byte with no
// edge between them is VRace in every schedule, naming both tasks.
func TestSBRace_InteriorUnordered(t *testing.T) {
	r := newRaceWorld(t)
	f := r.twoTasks("Unordered", false)
	e := r.evaluator(0)
	for seed := uint64(1); seed <= 32; seed++ {
		res := r.tcall(e, f, &TagOptions{Seed: seed})
		v := wantViolation(t, res, VRace)
		if v.Task == 0 || v.Other == 0 || v.Task == v.Other {
			t.Fatalf("seed %d: VRace between tasks %d and %d, want the two spawned tasks", seed, v.Task, v.Other)
		}
		off := r.tcall(e, f, &TagOptions{Seed: seed, NoRaces: true})
		wantClean(t, off)
	}
}

// TestSBRace_AtomicOrders: the same writes inside an atomic-based lock run
// clean in every schedule, and the parent's read after the join is ordered
// after both.
func TestSBRace_AtomicOrders(t *testing.T) {
	r := newRaceWorld(t)
	f := r.twoTasks("Locked", true)
	e := r.evaluator(0)
	vals := map[int64]bool{}
	for seed := uint64(1); seed <= 32; seed++ {
		res := r.tcall(e, f, &TagOptions{Seed: seed})
		wantClean(t, res)
		vals[r.intOf(res.Value)] = true
	}
	for v := range vals {
		if v != 1 && v != 2 {
			t.Fatalf("Run = %d, want 1 or 2", v)
		}
	}
}
