// Why: gen's host functions (internal/gen/host.go, reflect.go) need two
// Machine methods beyond reads and writes.
// What: TestHost_CopyKeepsPointerTag and TestHost_SetUint.
// How: Copy hands out the run's arena with its pointer tag intact and is
// charged; SetUint writes TypeKind and Receiver results truncated to width.

package eval

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// TestHost_CopyKeepsPointerTag: F(): bool { a := EvalArena(); b :=
// gen.GetArena(); return gen.Same(a, b) }, where GetArena copies
// Machine.Arena() into its result with Copy: the copied pointer is the
// arena's (its tag survives), and the copy is charged.
func TestHost_CopyKeepsPointerTag(t *testing.T) {
	w := newWorld(t)
	rec, _ := w.arenaWorld()
	ref := w.tab.Ref(rec)
	get := w.hostFn("GetArena", ref)
	same := w.hostFn("Same", types.Bool, ref, ref)
	f := w.fn("lib", "F", types.Bool)
	x := &seq{bb: w.body(f, types.Bool)}
	a, b := x.local(ref), x.local(ref)
	next := x.block()
	x.term(x.cur, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallBuiltin, Builtin: mir.BuiltinEvalArena}, Dest: lp(a), Target: next, Spawn: mir.NoLocal})
	x.cur = next
	x.call(get, nil, lp(b))
	x.call(same, nil, lp(0), cp(a), cp(b))
	x.ret(x.cur)
	w.bodies[f] = &Body{MIR: x.b, State: BodyReady}
	var before, after uint64
	h := mapHost{
		"gen.GetArena": func(m *Machine, c *Call) *types.EvalError {
			v, err := m.Arena()
			if err != nil {
				return err
			}
			before = m.seg().steps
			err = m.Copy(c.Result, v)
			after = m.seg().steps
			return err
		},
		"gen.Same": func(m *Machine, c *Call) *types.EvalError {
			m.SetBool(c.Result, m.Uint(c.Args[0]) == m.Uint(c.Args[1]))
			return nil
		},
	}
	r := w.runHost(w.evaluator(0), f, h)
	if r.Err != nil || w.intOf(r.Value) != 1 {
		t.Fatalf("%+v %+v: want true", r, r.Err)
	}
	if after-before != 1 {
		t.Fatalf("Copy of 8 bytes charged %d steps, want 1", after-before)
	}
}

// TestHost_SetUint: G(): u8 { return gen.Seven() }, where the host writes
// 0x107 with SetUint: the value is truncated to the type's width.
func TestHost_SetUint(t *testing.T) {
	w := newWorld(t)
	u8 := w.tab.IntType(8, false)
	seven := w.hostFn("Seven", u8)
	g := w.fn("lib", "G", u8)
	x := &seq{bb: w.body(g, u8)}
	x.call(seven, nil, lp(0))
	x.ret(x.cur)
	w.bodies[g] = &Body{MIR: x.b, State: BodyReady}
	h := mapHost{"gen.Seven": func(m *Machine, c *Call) *types.EvalError {
		m.SetUint(c.Result, 0x107)
		return nil
	}}
	r := w.runHost(w.evaluator(0), g, h)
	if r.Err != nil || w.intOf(r.Value) != 7 {
		t.Fatalf("%+v %+v: want 7", r, r.Err)
	}
}
