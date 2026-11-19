// Why: lower emits forms the evaluator must run at no extra cost: AddNoWrap
// from the range analysis and calls of std.rt's trace hooks.
// What: TestAddNoWrapRunsAsAddWrap and TestTraceHooksAreFree.
// How: each form is compared with the plain form in results, steps and the
// reads recorded.

package eval

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// TestAddNoWrapRunsAsAddWrap: AddNoWrap is an integer add its
// producer proved in range, so the evaluator runs it as AddWrap, on the
// generic path (binaryOp, u8 and i32) and on the int64 fast path (a loop
// over int locals, as loopFn with AddNoWrap for the sum), with the same
// result and the same steps as AddWrap.
func TestAddNoWrapRunsAsAddWrap(t *testing.T) {
	ti := func(w int, s bool) *tinfo {
		return &tinfo{class: cInt, width: w, signed: s, ty: &types.Type{Kind: types.KInt}}
	}
	for _, c := range []struct {
		name string
		ti   *tinfo
		a, b uint64
	}{
		{"u8 3+4", ti(1, false), 3, 4},
		{"u8 255+1", ti(1, false), 255, 1},
		{"i32 -7+2", ti(4, true), 0xfffffff9, 2},
		{"i64 max+1", ti(8, true), 1<<63 - 1, 1},
	} {
		want, wpc := binaryOp(mir.AddWrap, c.ti, c.ti, c.a, c.b)
		got, pc := binaryOp(mir.AddNoWrap, c.ti, c.ti, c.a, c.b)
		if got != want || pc != wpc || pc != types.PanicNone {
			t.Errorf("%s: AddNoWrap %#x %v, AddWrap %#x %v", c.name, got, pc, want, wpc)
		}
	}
	w := newWorld(t)
	run := func(name string, op mir.BinOp) types.EvalResult {
		f := w.fn("lib", name, types.Int, types.Int)
		x := w.body(f, types.Int, types.Int)
		i, cnd := x.local(types.Int), x.local(types.Bool)
		head, body, exit := x.block(), x.block(), x.block()
		x.assign(0, lp(0), use(imm(types.Int, 0)))
		x.assign(0, lp(i), use(imm(types.Int, 0)))
		x.gotoB(0, head)
		x.assign(head, lp(cnd), bin(mir.Lt, cp(i), cp(1)))
		x.branch(head, cp(cnd), body, exit)
		x.assign(body, lp(0), bin(op, cp(0), cp(i)))
		x.assign(body, lp(i), bin(op, cp(i), imm(types.Int, 1)))
		x.gotoB(body, head)
		x.ret(exit)
		x.done()
		return w.call(w.evaluator(0), f, 1<<20, w.intVal(types.Int, 100))
	}
	a, b := run("Wrap", mir.AddWrap), run("NoWrap", mir.AddNoWrap)
	if a.Err != nil || b.Err != nil {
		t.Fatalf("errors: %v, %v", a.Err, b.Err)
	}
	if w.intOf(b.Value) != 4950 || w.intOf(a.Value) != 4950 || a.Steps != b.Steps {
		t.Errorf("AddNoWrap loop %d in %d steps, AddWrap %d in %d steps, want 4950 alike",
			w.intOf(b.Value), b.Steps, w.intOf(a.Value), a.Steps)
	}
}

// TestTraceHooksAreFree: lower calls std.rt.TraceFail or TraceTry before
// every error return. The evaluator skips those calls at no cost: F, which
// calls both (they have no body, so running one would be an ICE), returns 7 in
// the steps of G, the same body without the calls, and records no read of
// either hook.
func TestTraceHooksAreFree(t *testing.T) {
	w := newWorld(t)
	site := w.tab.Ref(types.U8)
	tf := w.fn("std.rt", "TraceFail", types.Void, site)
	tt := w.fn("std.rt", "TraceTry", types.Void, site)
	mk := func(name string, trace bool) types.ItemID {
		f := w.fn("lib", name, types.Int)
		x := w.body(f, types.Int)
		v := x.local(types.Void)
		cur := mir.BlockID(0)
		x.assign(cur, lp(0), use(imm(types.Int, 7)))
		if trace {
			for _, h := range []types.ItemID{tf, tt} {
				next := x.block()
				x.call(cur, h, lp(v), next, mir.Imm(site, 0))
				cur = next
			}
		}
		x.ret(cur)
		x.done()
		return f
	}
	f, g := mk("F", true), mk("G", false)
	rf, rg := w.call(w.evaluator(0), f, 1<<20), w.call(w.evaluator(0), g, 1<<20)
	if rf.Err != nil || rg.Err != nil {
		t.Fatalf("errors: %v, %v", rf.Err, rg.Err)
	}
	if w.intOf(rf.Value) != 7 || rf.Steps != rg.Steps {
		t.Errorf("F %d in %d steps, G in %d steps", w.intOf(rf.Value), rf.Steps, rg.Steps)
	}
	for _, rd := range rf.Reads.Sorted() {
		if k := rd.Key.String(); k == "std.rt.TraceFail" || k == "std.rt.TraceTry" {
			t.Errorf("read of a trace hook: %v", rd)
		}
	}
}
