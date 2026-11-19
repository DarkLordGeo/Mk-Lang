// Why: the range analysis turns a - or * it proves in range into SubNoWrap or
// MulNoWrap, and compile-time evaluation must run those forms too.
// What: TestNoWrapRunsAsWrap.
// How: the same loop in the wrapping and the no-wrap forms, on the generic and
// the int64 fast path, must give the same results and the same steps.

package eval

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// TestNoWrapRunsAsWrap: SubNoWrap and MulNoWrap are a - and * their
// producer proved in range, so the evaluator runs them as SubWrap and
// MulWrap, on the generic path (binaryOp over u8, i32 and i64) and on the
// int64 fast path (a loop over int locals, as TestAddNoWrapRunsAsAddWrap
// for AddNoWrap), with the same results and steps. Without a case for these
// forms the evaluator would raise "eval: integer binary".
func TestNoWrapRunsAsWrap(t *testing.T) {
	ti := func(w int, s bool) *tinfo {
		return &tinfo{class: cInt, width: w, signed: s, ty: &types.Type{Kind: types.KInt}}
	}
	for _, c := range []struct {
		name  string
		ti    *tinfo
		a, b  uint64
		nw, w mir.BinOp
	}{
		{"u8 9-4", ti(1, false), 9, 4, mir.SubNoWrap, mir.SubWrap},
		{"u8 0-1", ti(1, false), 0, 1, mir.SubNoWrap, mir.SubWrap},
		{"i32 -7-2", ti(4, true), 0xfffffff9, 2, mir.SubNoWrap, mir.SubWrap},
		{"i64 min-1", ti(8, true), 1 << 63, 1, mir.SubNoWrap, mir.SubWrap},
		{"u8 15*17", ti(1, false), 15, 17, mir.MulNoWrap, mir.MulWrap},
		{"u8 16*16", ti(1, false), 16, 16, mir.MulNoWrap, mir.MulWrap},
		{"i32 -7*3", ti(4, true), 0xfffffff9, 3, mir.MulNoWrap, mir.MulWrap},
		{"i64 max*2", ti(8, true), 1<<63 - 1, 2, mir.MulNoWrap, mir.MulWrap},
	} {
		want, wpc := binaryOp(c.w, c.ti, c.ti, c.a, c.b)
		got, pc := binaryOp(c.nw, c.ti, c.ti, c.a, c.b)
		if got != want || pc != wpc || pc != types.PanicNone {
			t.Errorf("%s: %v %#x %v, %v %#x %v", c.name, c.nw, got, pc, c.w, want, wpc)
		}
	}
	w := newWorld(t)
	// F(n) runs i from 0 to n - 1 with s = s * 1 + i - 0 in op's forms:
	// the sum of 0..n-1, through one - and one * per iteration.
	run := func(name string, sub, mul mir.BinOp) types.EvalResult {
		f := w.fn("lib", name, types.Int, types.Int)
		x := w.body(f, types.Int, types.Int)
		i, cnd, tmp := x.local(types.Int), x.local(types.Bool), x.local(types.Int)
		head, body, exit := x.block(), x.block(), x.block()
		x.assign(0, lp(0), use(imm(types.Int, 0)))
		x.assign(0, lp(i), use(imm(types.Int, 0)))
		x.gotoB(0, head)
		x.assign(head, lp(cnd), bin(mir.Lt, cp(i), cp(1)))
		x.branch(head, cp(cnd), body, exit)
		x.assign(body, lp(tmp), bin(mul, cp(0), imm(types.Int, 1)))
		x.assign(body, lp(tmp), bin(sub, cp(tmp), imm(types.Int, 0)))
		x.assign(body, lp(0), bin(mir.AddWrap, cp(tmp), cp(i)))
		x.assign(body, lp(i), bin(mir.AddWrap, cp(i), imm(types.Int, 1)))
		x.gotoB(body, head)
		x.ret(exit)
		x.done()
		return w.call(w.evaluator(0), f, 1<<20, w.intVal(types.Int, 100))
	}
	a, b := run("Wrap", mir.SubWrap, mir.MulWrap), run("NoWrap", mir.SubNoWrap, mir.MulNoWrap)
	if a.Err != nil || b.Err != nil {
		t.Fatalf("errors: %v, %v", a.Err, b.Err)
	}
	if w.intOf(b.Value) != 4950 || w.intOf(a.Value) != 4950 || a.Steps != b.Steps {
		t.Errorf("NoWrap loop %d in %d steps, Wrap %d in %d steps, want 4950 alike",
			w.intOf(b.Value), b.Steps, w.intOf(a.Value), a.Steps)
	}
}
