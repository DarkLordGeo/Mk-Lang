// Why: a generic instance named as a value, `Same[i32]`, is a fn value of that
// instance, so mono must collect it like a named function.
// What: TestInstanceValueCollected.
// How: passes an RvFnRef of Same at i32 to a fn-typed parameter and requires
// the instance to exist, address-taken.

package mono

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// TestInstanceValueCollected: `Apply(Same[i32], 4)` lowers to an
// RvFnRef of Same at i32 passed to a fn-typed parameter; mono collects the
// instance and marks it address-taken, as it does a named fn value.
func TestInstanceValueCollected(t *testing.T) {
	w := newWorld()
	tab := w.tab
	p0 := tab.Param(0)
	same := w.fn("Same", 1, []types.TypeID{p0}, p0)
	sb := w.body(same)
	sb.assign(mir.ReturnLocal, mir.Rvalue{Kind: mir.RvUse, A: copyOf(sb.b, 1)})
	sb.ret()
	fnT := tab.Fn(types.FnType{Params: []types.FnParam{{Type: types.I32}}, Result: types.I32})
	apply := w.fn("Apply", 0, []types.TypeID{fnT, types.I32}, types.I32)
	ab := w.body(apply)
	ab.callTerm(mir.Callee{Kind: mir.CallValue, Value: copyOf(ab.b, 1)}, mir.ReturnLocal, mir.NoLocal, arg(copyOf(ab.b, 2)))
	ab.ret()
	four := w.fn("Four", 0, nil, types.I32)
	fb := w.body(four)
	tmp := fb.temp(fnT)
	fb.assign(tmp, mir.Rvalue{Kind: mir.RvFnRef, Fn: mir.FnRef{Item: same, Args: fb.b.AddGenericArgs(ty(types.I32))}, Type: fnT})
	fb.call(apply, nil, mir.ReturnLocal, arg(copyOf(fb.b, tmp)), arg(mir.Imm(types.I32, 4)))
	fb.ret()
	in, ds := w.collect(t, Options{Validate: true}, mainRoot(four))
	if len(ds) != 0 {
		t.Fatalf("diagnostics: %v", ds)
	}
	closedOver(t, w, in)
	inst := w.inst(t, in, "m.Same[i32]")
	if !inst.AddrTaken {
		t.Fatalf("m.Same[i32] is not address-taken:\n%s", summary(in))
	}
	if inst.Body == nil || inst.Body.GenericCount != 0 {
		t.Fatalf("m.Same[i32] has no instance body")
	}
}
