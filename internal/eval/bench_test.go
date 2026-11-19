// Why: allocation and const serialization have costs of their own, beside the
// loop and call benchmarks in fuel_test.go.
// What: BenchmarkEvalAlloc (page allocations and frees through the overrides),
// BenchmarkConstOut (a 64 KiB []u8 result serialized into static data) and
// TestBenchBodies, which runs each benchmark body once under go test.
// How: hand-built MIR bodies in the test world, run through the Machine.

package eval

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// allocLoop declares AllocLoop(n: int): int, which allocates and frees a
// 64-byte page n times through the PageAlloc and PageFree overrides.
func (w *world) allocLoop() types.ItemID {
	pu8 := w.tab.RawPtr(types.U8)
	w.pageAlloc = w.fn("std.rt", "PageAlloc", pu8, types.Int)
	w.pageFree = w.fn("std.rt", "PageFree", types.Void, pu8, types.Int)
	f := w.fn("lib", "AllocLoop", types.Int, types.Int)
	x := &seq{bb: w.body(f, types.Int, types.Int)}
	i, cnd, p, unit := x.local(types.Int), x.local(types.Bool), x.local(pu8), x.local(types.Void)
	head, body, exit := x.block(), x.block(), x.block()
	x.assign(0, lp(0), use(imm(types.Int, 0)))
	x.assign(0, lp(i), use(imm(types.Int, 0)))
	x.gotoB(0, head)
	x.assign(head, lp(cnd), bin(mir.Lt, cp(i), cp(1)))
	x.branch(head, cp(cnd), body, exit)
	x.cur = body
	x.call(w.pageAlloc, nil, lp(p), imm(types.Int, 64))
	x.call(w.pageFree, nil, lp(unit), cp(p), imm(types.Int, 64))
	x.assign(x.cur, lp(i), bin(mir.Add, cp(i), imm(types.Int, 1)))
	x.gotoB(x.cur, head)
	x.ret(exit)
	w.bodies[f] = &Body{MIR: x.b, State: BodyReady}
	return f
}

func BenchmarkEvalAlloc(b *testing.B) {
	w := newWorld(b)
	f := w.allocLoop()
	e := w.evaluator(0)
	n := w.intVal(types.Int, 100_000)
	var steps uint64
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := w.call(e, f, 1<<40, n)
		if r.Err != nil {
			b.Fatal(r.Err)
		}
		steps += r.Steps
	}
	b.ReportMetric(float64(steps)/b.Elapsed().Seconds(), "steps/s")
}

// bigSlice declares Big(): []u8, a 64 KiB page returned as a slice.
func (w *world) bigSlice() types.ItemID {
	pu8 := w.tab.RawPtr(types.U8)
	su8 := w.tab.Slice(types.U8)
	w.pageAlloc = w.fn("std.rt", "PageAlloc", pu8, types.Int)
	sl := w.raw("Slice", su8, pu8, types.Int)
	f := w.fn("lib", "Big", su8)
	x := &seq{bb: w.body(f, su8)}
	p := x.local(pu8)
	x.call(w.pageAlloc, nil, lp(p), imm(types.Int, 65536))
	x.call(sl, []types.TypeID{types.U8}, lp(0), cp(p), imm(types.Int, 65536))
	x.ret(x.cur)
	w.bodies[f] = &Body{MIR: x.b, State: BodyReady}
	return f
}

func BenchmarkConstOut(b *testing.B) {
	w := newWorld(b)
	f := w.bigSlice()
	e := w.evaluator(0)
	b.SetBytes(65536)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r := w.call(e, f, 1<<40)
		if r.Err != nil || r.Value == types.NoConst {
			b.Fatal(r.Err)
		}
	}
}

// TestBenchBodies runs the benchmark bodies once, so a broken benchmark
// fails go test.
func TestBenchBodies(t *testing.T) {
	w := newWorld(t)
	f := w.allocLoop()
	if r := w.call(w.evaluator(0), f, 0, w.intVal(types.Int, 10)); r.Err != nil {
		t.Fatalf("AllocLoop: %+v", r.Err)
	}
	w2 := newWorld(t)
	g := w2.bigSlice()
	r := w2.call(w2.evaluator(0), g, 0)
	if r.Err != nil {
		t.Fatalf("Big: %+v", r.Err)
	}
	v := w2.consts.Get(r.Value)
	if len(v.Relocs) != 1 || types.LoadUint(v.Bytes[8:], 8) != 65536 {
		t.Fatalf("Big = %+v", v)
	}
	if p := w2.consts.Get(v.Relocs[0].Data); len(p.Bytes) != 65536 {
		t.Errorf("pointee of %d bytes", len(p.Bytes))
	}
}
