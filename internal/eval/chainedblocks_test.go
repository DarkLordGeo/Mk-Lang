// Why: runBlock chains a pre-charged block's flat goto or switch successor
// itself, and that shortcut must not change where fuel runs out.
// What: TestPerfMid_ChainedBlocksStopExactly and
// TestPerfMid_ChainedBlockPanicSteps.
// How: runs a loop at every fuel from 1 to its total plus 2: with fuel F a
// stop is exactly at the longest prefix of statement costs that fits in F.

package eval

import (
	"math"
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// TestPerfMid_ChainedBlocksStopExactly runs the budget loop (loopFn, n = 6)
// with every fuel from 1 (0 asks for the default) to its total plus 2.
// Steps(F), the steps of a fuel stop or of the finished run, never exceed F,
// never decrease as F grows, and change only to F itself (the next statement's
// cost then fits for the first time): so each stop is at the longest fitting
// prefix. Every F with the same Steps stops at the same statement. With ample
// fuel the blocks were charged once, chained (preEntries > the 2 entries of
// runLoop alone).
func TestPerfMid_ChainedBlocksStopExactly(t *testing.T) {
	w := newWorld(t)
	f := w.loopFn("lib", "Loop")
	n := w.intVal(types.Int, 6)
	e := w.evaluator(0)
	full := w.call(e, f, 1<<30, n)
	if full.Err != nil || w.intOf(full.Value) != 15 {
		t.Fatalf("ample fuel: %+v value %v", full.Err, full.Value)
	}
	if pre := e.preEntries.Load(); pre < 10 {
		t.Errorf("ample fuel: %d blocks charged once, want every block of the loop", pre)
	}
	total := full.Steps
	var prev uint64
	at := map[uint64]string{}
	for fuel := uint64(1); fuel <= total+2; fuel++ {
		r := w.call(w.evaluator(0), f, fuel, n)
		steps, where := r.Steps, "done"
		if r.Err != nil {
			if r.Err.Kind != types.EvalFuel {
				t.Fatalf("fuel %d: %+v", fuel, r.Err)
			}
			steps, where = r.Err.Steps, r.Err.At.String()
		}
		switch {
		case fuel >= total && (r.Err != nil || steps != total):
			t.Fatalf("fuel %d >= total %d: %+v steps %d", fuel, total, r.Err, steps)
		case fuel < total && r.Err == nil:
			t.Fatalf("fuel %d < total %d finished", fuel, total)
		case steps > fuel:
			t.Fatalf("fuel %d: %d steps charged", fuel, steps)
		case steps < prev:
			t.Fatalf("fuel %d: steps went down from %d to %d", fuel, prev, steps)
		case steps != prev && steps != fuel:
			t.Fatalf("fuel %d: steps %d, from %d: a statement that fit did not run", fuel, steps, prev)
		}
		if w0, ok := at[steps]; ok && w0 != where {
			t.Fatalf("fuel %d: %d steps stop at %s, earlier at %s", fuel, steps, where, w0)
		}
		at[steps] = where
		prev = steps
	}
}

// TestPerfMid_ChainedBlockPanicSteps: a trap inside a chained block, in two
// shapes. "self": one block `s = s +% i; i = i + 1; goto self`. "two": a
// head `c = i <= n; switch c` and a body `s = s +% i; i = i + 1; s = s +% 1;
// goto head`, so the trapping block is not the block runLoop entered, and
// the give-back must use the block running (m.preBlock). From i = MaxInt-5
// the sixth `i + 1` traps. Its steps are the same with ample fuel
// (pre-charged, chained, the rest given back) and with exactly that fuel,
// and one step less runs out of fuel instead.
func TestPerfMid_ChainedBlockPanicSteps(t *testing.T) {
	for _, shape := range []string{"self", "two"} {
		t.Run(shape, func(t *testing.T) {
			w := newWorld(t)
			f := w.fn("lib", "Ovf", types.Int, types.Int)
			x := w.body(f, types.Int, types.Int)
			i, c := x.local(types.Int), x.local(types.Bool)
			x.assign(0, lp(0), use(imm(types.Int, 0)))
			x.assign(0, lp(i), use(cp(1)))
			if shape == "self" {
				loop := x.block()
				x.gotoB(0, loop)
				x.assign(loop, lp(0), bin(mir.AddWrap, cp(0), cp(i)))
				x.assign(loop, lp(i), bin(mir.Add, cp(i), imm(types.Int, 1)))
				x.gotoB(loop, loop)
			} else {
				head, body, exit := x.block(), x.block(), x.block()
				x.gotoB(0, head)
				x.assign(head, lp(c), bin(mir.Le, cp(i), imm(types.Int, math.MaxInt64)))
				x.branch(head, cp(c), body, exit)
				x.assign(body, lp(0), bin(mir.AddWrap, cp(0), cp(i)))
				x.assign(body, lp(i), bin(mir.Add, cp(i), imm(types.Int, 1)))
				x.assign(body, lp(0), bin(mir.AddWrap, cp(0), imm(types.Int, 1)))
				x.gotoB(body, head)
				x.ret(exit)
			}
			x.done()
			n := w.intVal(types.Int, math.MaxInt64-5)
			e := w.evaluator(0)
			r := w.call(e, f, 1<<30, n)
			if r.Err == nil || r.Err.Kind != types.EvalPanic || r.Err.Panic != types.PanicOverflow {
				t.Fatalf("ample fuel: %+v, want an overflow panic", r.Err)
			}
			if pre := e.preEntries.Load(); pre < 6 {
				t.Errorf("ample fuel: %d blocks charged once, want the loop's blocks chained", pre)
			}
			want := r.Err.Steps
			if r2 := w.call(w.evaluator(0), f, want, n); r2.Err == nil || r2.Err.Panic != types.PanicOverflow ||
				r2.Err.Steps != want || r2.Err.At != r.Err.At {
				t.Errorf("exact fuel %d: %+v, want the same panic at %s", want, r2.Err, r.Err.At)
			}
			if r3 := w.call(w.evaluator(0), f, want-1, n); r3.Err == nil || r3.Err.Kind != types.EvalFuel || r3.Err.Steps >= want {
				t.Errorf("fuel %d: %+v, want out of fuel before the panic", want-1, r3.Err)
			}
		})
	}
}
