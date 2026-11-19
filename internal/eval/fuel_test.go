// Why: fuel bounds every compile-time evaluation, and its step counts are
// part of the compiler's observable behavior, so they must be exact.
// What: the step table with frame charges, exhaustion at the exact statement,
// frames charged and freed, the testdata/steps.golden cases and benchmarks.
// How: one step per MIR statement or terminator plus one per 16 bytes moved
// or allocated; the tests count the charges by hand and compare.

package eval

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// loopFn declares Loop(n: int): int { s := 0; for i := 0; i < n; i++ { s +%= i }; return s }.
func (w *world) loopFn(mod, name string) types.ItemID {
	f := w.fn(mod, name, types.Int, types.Int)
	x := w.body(f, types.Int, types.Int)
	i, cnd := x.local(types.Int), x.local(types.Bool)
	head, body, exit := x.block(), x.block(), x.block()
	x.assign(0, lp(0), use(imm(types.Int, 0)))
	x.assign(0, lp(i), use(imm(types.Int, 0)))
	x.gotoB(0, head)
	x.assign(head, lp(cnd), bin(mir.Lt, cp(i), cp(1)))
	x.branch(head, cp(cnd), body, exit)
	x.assign(body, lp(0), bin(mir.AddWrap, cp(0), cp(i)))
	x.assign(body, lp(i), bin(mir.Add, cp(i), imm(types.Int, 1)))
	x.gotoB(body, head)
	x.ret(exit)
	x.done()
	return f
}

// spinFn declares Spin(): int { for { } }: one goto forever.
func (w *world) spinFn(name string) (types.ItemID, *bb) {
	f := w.fn("lib", name, types.Int)
	x := w.body(f, types.Int)
	b1 := x.block()
	x.gotoB(0, b1)
	x.assign(b1, lp(0), use(imm(types.Int, 1)))
	x.gotoB(b1, b1)
	x.done()
	return f, x
}

func TestDefaultFuel(t *testing.T) {
	if types.DefaultFuel != 4_000_000 {
		t.Fatalf("DefaultFuel = %d", types.DefaultFuel)
	}
	w := newWorld(t)
	f, _ := w.spinFn("Spin")
	r := w.call(w.evaluator(0), f, 0)
	if r.Err == nil || r.Err.Kind != types.EvalFuel {
		t.Fatalf("got %+v, want fuel exhaustion", r.Err)
	}
	// Frame 32 bytes (2 steps), then 3 steps per round: the next charge (2
	// or 1) no longer fits.
	if r.Err.Steps > types.DefaultFuel || r.Err.Steps+3 <= types.DefaultFuel || r.Steps != r.Err.Steps {
		t.Errorf("steps %d (result %d) with the default fuel", r.Err.Steps, r.Steps)
	}
}

func TestE0902_FuelExhaustedAtSite(t *testing.T) {
	w := newWorld(t)
	f, x := w.spinFn("Spin")
	r := w.call(w.evaluator(0), f, 1001)
	if r.Err == nil || r.Err.Kind != types.EvalFuel {
		t.Fatalf("got %+v", r.Err)
	}
	// 2 for the frame, 1 for the entry goto, then rounds of 3 (assign 2,
	// goto 1): after 333 assignments 1001 steps are used and the goto's 1
	// does not fit.
	if r.Err.Steps != 1001 || r.Err.At != x.b.Span(x.b.Blocks[1].Term.Span) {
		t.Errorf("steps %d at %v", r.Err.Steps, r.Err.At)
	}
	if len(r.Err.Items) != 1 || r.Err.Items[0] != f {
		t.Errorf("trace %v", r.Err.Items)
	}
}

func TestE0902_ExactStep(t *testing.T) {
	w := newWorld(t)
	f := w.fn("lib", "Two", types.Int)
	x := w.body(f, types.Int)
	t1 := x.local(types.Int)
	x.assign(0, lp(t1), use(imm(types.Int, 1)))      // statement 1: 1 + c(8) = 2
	s2 := x.assign(0, lp(0), use(imm(types.Int, 2))) // statement 2: 2
	x.ret(0)
	x.done()
	// Frame: two ints, 16 bytes, rounded to 16, plus 16: 32 bytes, 2 steps.
	r := w.call(w.evaluator(0), f, 2+2+1)
	if r.Err == nil || r.Err.Kind != types.EvalFuel || r.Err.Steps != 4 || r.Err.At != x.b.Span(s2) {
		t.Fatalf("got %+v, want fuel at statement 2 after 4 steps", r.Err)
	}
	if next := uint64(2); next <= 5-r.Err.Steps {
		t.Errorf("the next charge %d fits the remaining %d", next, 5-r.Err.Steps)
	}
	// With exactly enough: 2 + 2 + 2 + return 1 + c(8) = 8.
	if r := w.call(w.evaluator(0), f, 8); r.Err != nil || r.Steps != 8 || w.intOf(r.Value) != 2 {
		t.Errorf("exact fuel: %+v steps %d", r.Err, r.Steps)
	}
	if r := w.call(w.evaluator(0), f, 7); r.Err == nil || r.Err.Steps != 6 {
		t.Errorf("one short: %+v", r.Err)
	}
}

func TestStepsExactOnMidBlockPanic(t *testing.T) {
	w := newWorld(t)
	arr := w.array(2, types.Int)
	f := w.fn("lib", "Mid", types.Int)
	x := w.body(f, types.Int)
	a, i := x.local(arr), x.local(types.Int)
	x.assign(0, lp(i), use(imm(types.Int, 5)))                                                                          // 2
	x.assign(0, lp(0), use(mir.Copy(x.b.Place(a, mir.Projection{Kind: mir.ProjIndex, A: uint32(i), Type: types.Int})))) // 2, panics
	for k := 0; k < 8; k++ {
		x.assign(0, lp(i), use(imm(types.Int, int64(k))))
	}
	x.ret(0)
	x.done()
	// Frame: 8 + 16 + 8 = 32 bytes, rounded 32, plus 16: 48, 3 steps.
	want := uint64(3 + 2 + 2)
	for _, fuel := range []uint64{1 << 30, want} {
		e := w.evaluator(0)
		r := w.call(e, f, fuel)
		if r.Err == nil || r.Err.Panic != types.PanicBounds || r.Err.Steps != want || r.Steps != want {
			t.Errorf("fuel %d: %+v steps %d, want bounds after %d", fuel, r.Err, r.Steps, want)
		}
		// Ample fuel charges the block once and gives back the rest; exact
		// fuel cannot, and runs statement by statement.
		if pre := e.preEntries.Load(); (fuel == want) != (pre == 0) {
			t.Errorf("fuel %d: %d blocks charged once", fuel, pre)
		}
	}
}

func TestFramesChargedAndFreed(t *testing.T) {
	w := newWorld(t)
	big := w.array(65536, types.U64)
	f := w.fn("lib", "F", types.Void, types.Int)
	x := w.body(f, types.Void, types.Int)
	a, cnd := x.local(big), x.local(types.Bool)
	yes, done := x.block(), x.block()
	x.assign(0, lp(cnd), bin(mir.Eq, cp(1), imm(types.Int, 0)))
	x.branch(0, cp(cnd), yes, done)
	x.assign(yes, lp(a), mir.Rvalue{Kind: mir.RvRepeat, A: imm(types.U64, 0), Len: types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, 65536)}})
	x.gotoB(yes, done)
	x.ret(done)
	x.done()
	c := w.constItem("main", "C", types.Int, 0)
	y := w.body(c, types.Int)
	loop, after := y.block(), y.block()
	unit := y.local(types.Void)
	y.gotoB(0, loop)
	y.call(loop, f, lp(unit), after, imm(types.Int, 1))
	y.gotoB(after, loop)
	y.done()
	var before runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	e := w.evaluator(0)
	req := constReq(w, c, types.StageBodies)
	r := e.Eval(req)
	var after2 runtime.MemStats
	runtime.ReadMemStats(&after2)
	if r.Err == nil || r.Err.Kind != types.EvalFuel {
		t.Fatalf("got %+v", r.Err)
	}
	// F's frame: 8 + 524,288 + 1 bytes, rounded to 524,304, plus 16:
	// 524,320 bytes, 32,770 steps. A round: the call 1 + c(8) = 2, the
	// frame, F's assignment 2, switch 1 and return 1, the goto back 1. Before
	// the rounds: C's frame (32 bytes) 2 and the entry goto 1.
	perRound := uint64(2 + 32770 + 2 + 1 + 1 + 1)
	if rounds := (r.Err.Steps - 3) / perRound; rounds != 122 || (r.Err.Steps-3)%perRound != 0 {
		t.Errorf("%d rounds in %d steps (%d per round), want 122", rounds, r.Err.Steps, perRound)
	}
	if grow := int64(after2.HeapInuse) - int64(before.HeapInuse); grow > 128<<20 {
		t.Errorf("heap in use grew %d bytes", grow)
	}
}

// goldenCases are the fixed evaluations of testdata/steps.golden: each
// builds its functions and returns the one to call and its arguments.
var goldenCases = []struct {
	name string
	mk   func(w *world) (types.ItemID, []types.ConstID)
}{
	{"loop 100", func(w *world) (types.ItemID, []types.ConstID) {
		return w.loopFn("lib", "Loop"), []types.ConstID{w.intVal(types.Int, 100)}
	}},
	{"calls fib 10", func(w *world) (types.ItemID, []types.ConstID) {
		return w.fibFn(), []types.ConstID{w.intVal(types.Int, 10)}
	}},
	{"aggregate struct", func(w *world) (types.ItemID, []types.ConstID) {
		p, pt := w.structType("lib", "P", types.I32, types.Int, types.U8)
		f := w.fn("lib", "Mk", pt)
		x := w.body(f, pt)
		x.assign(0, lp(0), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: p, Ops: x.b.AddOperands(imm(types.I32, 1), imm(types.Int, 2), imm(types.U8, 3))})
		x.ret(0)
		x.done()
		return f, nil
	}},
	{"slice len", func(w *world) (types.ItemID, []types.ConstID) {
		arr := w.array(10, types.Int)
		f := w.fn("lib", "SL", types.Int)
		x := w.body(f, types.Int)
		a, s := x.local(arr), x.local(w.tab.Slice(types.Int))
		x.assign(0, lp(a), mir.Rvalue{Kind: mir.RvRepeat, A: imm(types.Int, 3), Len: types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, 10)}})
		x.assign(0, lp(s), mir.Rvalue{Kind: mir.RvSlice, Place: lp(a), HasLo: true, A: imm(types.Int, 2), HasHi: true, B: imm(types.Int, 7)})
		x.assign(0, lp(0), mir.Rvalue{Kind: mir.RvLen, Place: lp(s)})
		x.ret(0)
		x.done()
		return f, nil
	}},
	{"vector add u8x16", func(w *world) (types.ItemID, []types.ConstID) {
		_, vt := w.structType("std.simd", "Vu8x16", w.array(16, types.U8))
		add := w.fn("std.simd", "Vec:AddWrap", vt, vt, vt)
		fd := w.items.Get(add).Decl.(*types.FuncDecl)
		fd.Bodiless, fd.Intrinsic = true, true
		f := w.fn("lib", "VAdd", vt, vt)
		x := w.body(f, vt, vt)
		next := x.block()
		x.term(0, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: add,
			Args: x.b.AddGenericArgs(types.TypeArg(types.U8), types.ConstGenericArg(types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, 16)}))}},
			Args: x.b.AddArgs(mir.Arg{Operand: cp(1)}, mir.Arg{Operand: cp(1)}), Dest: lp(0), Target: next, Spawn: mir.NoLocal})
		x.ret(next)
		x.done()
		b := make([]byte, 16)
		for i := range b {
			b[i] = byte(i * 17)
		}
		return f, []types.ConstID{w.consts.Intern(types.ConstValue{Type: vt, Align: 1, Bytes: b})}
	}},
	{"fill 1000 bytes", func(w *world) (types.ItemID, []types.ConstID) {
		arr := w.array(1000, types.U8)
		f := w.fn("lib", "Fill", arr)
		x := w.body(f, arr)
		x.assign(0, lp(0), mir.Rvalue{Kind: mir.RvRepeat, A: imm(types.U8, 7), Len: types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.Int, 1000)}})
		x.ret(0)
		x.done()
		return f, nil
	}},
}

// fibFn declares Fib(n: int): int { if n < 2 { return n }; return Fib(n-1) + Fib(n-2) }.
func (w *world) fibFn() types.ItemID {
	f := w.fn("lib", "Fib", types.Int, types.Int)
	x := w.body(f, types.Int, types.Int)
	cnd, a, b, n1 := x.local(types.Bool), x.local(types.Int), x.local(types.Int), x.local(types.Int)
	base, rec, r2, sum := x.block(), x.block(), x.block(), x.block()
	x.assign(0, lp(cnd), bin(mir.Lt, cp(1), imm(types.Int, 2)))
	x.branch(0, cp(cnd), base, rec)
	x.assign(base, lp(0), use(cp(1)))
	x.ret(base)
	x.assign(rec, lp(n1), bin(mir.Sub, cp(1), imm(types.Int, 1)))
	x.call(rec, f, lp(a), r2, cp(n1))
	x.assign(r2, lp(n1), bin(mir.Sub, cp(1), imm(types.Int, 2)))
	x.call(r2, f, lp(b), sum, cp(n1))
	x.assign(sum, lp(0), bin(mir.Add, cp(a), cp(b)))
	x.ret(sum)
	x.done()
	return f
}

func TestStepGolden(t *testing.T) {
	var out strings.Builder
	out.WriteString("# step counts of the evaluator; go test ./internal/eval -run StepGolden -update\n")
	for _, c := range goldenCases {
		w := newWorld(t)
		f, args := c.mk(w)
		r := w.call(w.evaluator(0), f, 0, args...)
		if r.Err != nil {
			t.Fatalf("%s: %+v", c.name, r.Err)
		}
		fmt.Fprintf(&out, "%s: %d steps\n", c.name, r.Steps)
	}
	const path = "testdata/steps.golden"
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(out.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	if got := out.String(); got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("step counts changed (a cost table or lowering change updates the golden):\n got:\n%s\nwant:\n%s", got, want)
	}
}

// BenchmarkEvalLoop is the loop of the evaluator's speed budget (at least
// 200M steps per second), reported as steps per second.
func BenchmarkEvalLoop(b *testing.B) {
	w := newWorld(b)
	f := w.loopFn("lib", "Loop")
	e := w.evaluator(0)
	n := w.intVal(types.Int, 1_000_000)
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

// BenchmarkEvalCalls is recursive Fibonacci: frame push and pop.
func BenchmarkEvalCalls(b *testing.B) {
	w := newWorld(b)
	f := w.fibFn()
	e := w.evaluator(0)
	n := w.intVal(types.Int, 20)
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
