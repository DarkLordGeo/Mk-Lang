// Why: gen's generator API runs as host functions inside the evaluator, and
// they need the Machine's reads, writes, fuel and arena.
// What: tests of the Host interface and the Machine methods it uses: reads
// and writes, Charge, errors, and Arena returning the arena EvalArena() made.
// How: a mapHost serves bodiless intrinsics of module "gen" to hand-built
// calls.

package eval

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

type mapHost map[string]HostFunc

func (h mapHost) Intrinsic(k types.Key) (HostFunc, bool) {
	f, ok := h[k.String()]
	return f, ok
}

// hostFn declares a bodiless intrinsic of module "gen" for a host.
func (w *world) hostFn(name string, result types.TypeID, params ...types.TypeID) types.ItemID {
	f := w.fn("gen", name, result, params...)
	fd := w.items.Get(f).Decl.(*types.FuncDecl)
	fd.Bodiless, fd.Intrinsic = true, true
	return f
}

func (w *world) runHost(e *Evaluator, fn types.ItemID, h Host) types.EvalResult {
	return e.Run(&types.EvalRequest{What: types.EvalCall, Item: fn, Fuel: 1 << 20, Stage: types.StageBodies,
		Module: w.module("main"), Site: source.Span{File: 1, Start: 1, End: 2}, Target: types.X86_64Windows}, h)
}

// TestHost_ArenaIsEvalArena: F(): bool { a := EvalArena(); return
// gen.SameArena(a) }, where the host compares its argument with
// Machine.Arena(): one run has one arena.
func TestHost_ArenaIsEvalArena(t *testing.T) {
	w := newWorld(t)
	rec, _ := w.arenaWorld()
	ref := w.tab.Ref(rec)
	same := w.hostFn("SameArena", types.Bool, ref)
	f := w.fn("lib", "F", types.Bool)
	x := &seq{bb: w.body(f, types.Bool)}
	a := x.local(ref)
	next := x.block()
	x.term(x.cur, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallBuiltin, Builtin: mir.BuiltinEvalArena}, Dest: lp(a), Target: next, Spawn: mir.NoLocal})
	x.cur = next
	x.call(same, nil, lp(0), cp(a))
	x.ret(x.cur)
	w.bodies[f] = &Body{MIR: x.b, State: BodyReady}
	calls := 0
	h := mapHost{"gen.SameArena": func(m *Machine, c *Call) *types.EvalError {
		calls++
		v, err := m.Arena()
		if err != nil {
			return err
		}
		v2, _ := m.Arena()
		if v2 != v || v.Type != ref {
			t.Errorf("Arena gave %+v then %+v, type %d want %d", v, v2, v.Type, ref)
		}
		if err := m.Charge(3); err != nil {
			return err
		}
		m.SetBool(c.Result, m.Uint(c.Args[0]) == m.Uint(v))
		return nil
	}}
	r := w.runHost(w.evaluator(0), f, h)
	if r.Err != nil || calls != 1 || w.intOf(r.Value) != 1 {
		t.Fatalf("calls %d, %+v %+v: want true", calls, r, r.Err)
	}
}

// TestHost_ChargeAndErrors: a host's Charge counts toward Steps and stops
// with E0902 when it does not fit; a host error ends the evaluation with the
// call's trace.
func TestHost_ChargeAndErrors(t *testing.T) {
	w := newWorld(t)
	g := w.hostFn("Work", types.Int, types.Int)
	f := w.fn("lib", "F", types.Int)
	x := &seq{bb: w.body(f, types.Int)}
	x.call(g, nil, lp(0), imm(types.Int, 7))
	x.ret(x.cur)
	w.bodies[f] = &Body{MIR: x.b, State: BodyReady}
	h := mapHost{"gen.Work": func(m *Machine, c *Call) *types.EvalError {
		n := m.Int(c.Args[0])
		if err := m.Charge(uint64(n) * 100); err != nil {
			return err
		}
		if n == 7 {
			return &types.EvalError{Kind: types.EvalPanic, Panic: types.PanicExplicit, Message: "seven"}
		}
		return nil
	}}
	r := w.runHost(w.evaluator(0), f, h)
	if r.Err == nil || r.Err.Kind != types.EvalPanic || r.Err.Message != "seven" || len(r.Err.Items) != 1 || r.Err.Items[0] != f {
		t.Fatalf("%+v: want the host's panic in F", r.Err)
	}
	// Frame of 8+16 bytes rounded to 16, plus 16: 2 steps; the call 1+1;
	// the host's own step; then 700.
	if r.Steps != 2+2+1+700 {
		t.Errorf("steps %d", r.Steps)
	}
	small := w.evaluator(0)
	rs := small.Run(&types.EvalRequest{What: types.EvalCall, Item: f, Fuel: 100, Stage: types.StageBodies,
		Module: w.module("main"), Site: source.Span{File: 1, Start: 1, End: 2}, Target: types.X86_64Windows}, h)
	if rs.Err == nil || rs.Err.Kind != types.EvalFuel {
		t.Fatalf("%+v: want fuel exhaustion in the host's charge", rs.Err)
	}
}
