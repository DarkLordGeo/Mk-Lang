// Why: a const argument that is a capturing fn literal turns a call into a
// call of mono's instance with a hidden environment, and the evaluator must
// pick that instance; std.dyn.erased.Key must also agree with codegen.
// What: tests of exec.go's envCallee, of the forwarding of a const parameter
// (forwardsConst, constCallee) and of typekey.go.
// How: a hand-built world with a lowered body and its instance, called with
// and without the environment argument; type keys compared to fixed values.

package eval

import (
	"testing"

	"mk/internal/mir"
	"mk/internal/types"
)

// envCallWorld declares lib.G(a: i64, const k: i64): i64 with its lowered
// body (two parameters: a + k) and mono's instance at k = 7 (three: a, k and
// the hidden environment env: &(x: &i64), giving a*1000 + 7 + *((*env).x)),
// and main.Call(): i64, which calls G(5, const 7, &(x: &v)) with v = 30, as
// lower writes a call whose const argument is a capturing literal.
func envCallWorld(t *testing.T, w *world, kOp func(x *bb) mir.Operand) (call, g types.ItemID) {
	t.Helper()
	g = w.fn("lib", "G", types.I64, types.I64, types.I64)
	w.items.Get(g).Decl.(*types.FuncDecl).Sig.Params[1].Const = true
	refI := w.tab.Ref(types.I64)
	rec := w.tab.Record([]types.RecordField{{Name: w.in.Intern("x"), Type: refI}})
	env := w.tab.Ref(rec)

	gb := w.body(g, types.I64, types.I64, types.I64)
	gb.assign(0, lp(0), bin(mir.Add, cp(1), cp(2)))
	gb.ret(0)
	gb.done()

	seven := types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.I64, 7)}
	ib := w.body(g, types.I64, types.I64, types.I64, env)
	thousand := ib.local(types.I64)
	ib.assign(0, lp(thousand), bin(mir.Mul, cp(1), imm(types.I64, 1000)))
	ib.assign(0, lp(thousand), bin(mir.Add, cp(thousand), imm(types.I64, 7)))
	capture := ib.b.Place(3, mir.Projection{Kind: mir.ProjDeref, Type: rec},
		mir.Projection{Kind: mir.ProjField, A: 0, Type: refI}, mir.Projection{Kind: mir.ProjDeref, Type: types.I64})
	ib.assign(0, lp(0), bin(mir.Add, cp(thousand), mir.Copy(capture)))
	ib.ret(0)
	w.setInstance(g, []types.GenericArg{types.ConstGenericArg(seven)}, ib.b)

	call = w.fn("main", "Call", types.I64)
	x := w.body(call, types.I64)
	v, r, e, er := x.local(types.I64), x.local(refI), x.local(rec), x.local(env)
	x.assign(0, lp(v), use(imm(types.I64, 30)))
	x.assign(0, lp(r), mir.Rvalue{Kind: mir.RvRef, Place: lp(v)})
	x.assign(0, lp(e), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggRecord, Type: rec, Ops: x.b.AddOperands(cp(r))})
	x.assign(0, lp(er), mir.Rvalue{Kind: mir.RvRef, Place: lp(e)})
	next := x.block()
	x.call(0, g, lp(0), next, imm(types.I64, 5), kOp(x), cp(er))
	x.ret(next)
	x.done()
	return call, g
}

// TestEnvCallRunsInstance: a direct call with a trailing environment
// argument runs mono's instance keyed by the const argument (5*1000 + 7 +
// 30), never the lowered body with too few parameters (which fails with "3
// arguments to lib.G, which has 2 parameters"), with the const operand as an
// immediate or as an interned constant.
func TestEnvCallRunsInstance(t *testing.T) {
	for _, c := range []struct {
		name string
		op   func(w *world) func(x *bb) mir.Operand
	}{
		{"imm", func(*world) func(x *bb) mir.Operand {
			return func(*bb) mir.Operand { return imm(types.I64, 7) }
		}},
		{"const", func(w *world) func(x *bb) mir.Operand {
			return func(*bb) mir.Operand {
				return mir.Operand{Kind: mir.OpConst, Const: w.intVal(types.I64, 7), Type: types.I64}
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld(t)
			call, _ := envCallWorld(t, w, c.op(w))
			res := w.call(w.evaluator(0), call, 1000)
			if res.Err != nil {
				t.Fatalf("Call: %+v", res.Err)
			}
			if got := w.intOf(res.Value); got != 5037 {
				t.Errorf("Call() = %d, want 5037 (the instance)", got)
			}
		})
	}
}

// TestPlainConstCallKeepsGenericBody: the same callee called without an
// environment argument (a const argument that captures nothing) still runs
// the lowered body by its FnRef alone (5 + 7).
func TestPlainConstCallKeepsGenericBody(t *testing.T) {
	w := newWorld(t)
	_, g := envCallWorld(t, w, func(*bb) mir.Operand { return imm(types.I64, 7) })
	plain := w.fn("main", "Plain", types.I64)
	x := w.body(plain, types.I64)
	next := x.block()
	x.call(0, g, lp(0), next, imm(types.I64, 5), imm(types.I64, 7))
	x.ret(next)
	x.done()
	res := w.call(w.evaluator(0), plain, 1000)
	if res.Err != nil {
		t.Fatalf("Plain: %+v", res.Err)
	}
	if got := w.intOf(res.Value); got != 12 {
		t.Errorf("Plain() = %d, want 12 (the lowered body)", got)
	}
}

// TestTypeKey_MatchesCodegen pins the values codegen's
// TestTypeKeyDistinctAndStable pins (encoding mk.typekey.v1), so eval's copy
// of the encoder and codegen's agree.
func TestTypeKey_MatchesCodegen(t *testing.T) {
	w := newWorld(t)
	_, point := w.structType("geo", "Point", types.I32, types.I32)
	for _, c := range []struct {
		ty   types.TypeID
		want uint64
	}{{types.I32, 0x4d1ab0b45079db60}, {types.Str, 0x2fbdb948dbbba685}, {point, 0xcdb4c64cadb22ddd}} {
		if got := typeKey(w.tab, c.ty); got != c.want {
			t.Errorf("typeKey(%s) = %#x, want %#x", w.tab.TypeString(c.ty, nil), got, c.want)
		}
	}
}

// TestTypeKey_Intrinsic: Key[T]() (std.dyn.erased.Key, bodiless intrinsic)
// evaluates to T's type key, distinct for two T.
func TestTypeKey_Intrinsic(t *testing.T) {
	w := newWorld(t)
	_, point := w.structType("geo", "Point", types.I32, types.I32)
	key := w.genericFn("std.dyn.erased", "Key", 1, types.U64)
	fd := w.items.Get(key).Decl.(*types.FuncDecl)
	fd.Bodiless, fd.Intrinsic = true, true
	for _, ty := range []types.TypeID{point, types.Str} {
		k := w.fn("main", "K"+w.tab.TypeString(ty, nil), types.U64)
		x := w.body(k, types.U64)
		next := x.block()
		x.term(0, mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect,
			Fn: mir.FnRef{Item: key, Args: x.b.AddGenericArgs(types.TypeArg(ty))}}, Dest: lp(0), Target: next, Spawn: mir.NoLocal})
		x.ret(next)
		x.done()
		res := w.call(w.evaluator(0), k, 100)
		if res.Err != nil {
			t.Fatalf("Key[%s]: %+v", w.tab.TypeString(ty, nil), res.Err)
		}
		v := w.consts.Get(res.Value)
		if got, want := types.LoadUint(v.Bytes, len(v.Bytes)), typeKey(w.tab, ty); got != want {
			t.Errorf("Key[%s]() = %#x, want %#x", w.tab.TypeString(ty, nil), got, want)
		}
	}
}

// forwardWorld declares lib.G(a: i64, const k: i64): i64 (a + k) and
// lib.F(a: i64, const k: i64): i64, whose lowered body passes its k on to
// G's const k (a const parameter forwarded as a const argument), as the
// parameter's own local or, with temp, through a once-assigned temporary
// copy (the slot lower makes for an argument); mono's
// instance of F at k = 7 gives a*1000 + 7. main.Call() calls F(5, const 7).
func forwardWorld(t *testing.T, w *world, temp bool) types.ItemID {
	t.Helper()
	g := w.fn("lib", "G", types.I64, types.I64, types.I64)
	w.items.Get(g).Decl.(*types.FuncDecl).Sig.Params[1].Const = true
	gb := w.body(g, types.I64, types.I64, types.I64)
	gb.assign(0, lp(0), bin(mir.Add, cp(1), cp(2)))
	gb.ret(0)
	gb.done()

	f := w.fn("lib", "F", types.I64, types.I64, types.I64)
	w.items.Get(f).Decl.(*types.FuncDecl).Sig.Params[1].Const = true
	fb := w.body(f, types.I64, types.I64, types.I64)
	k := cp(2)
	if temp {
		tmp := fb.local(types.I64)
		fb.assign(0, lp(tmp), use(cp(2)))
		k = cp(tmp)
	}
	next := fb.block()
	fb.call(0, g, lp(0), next, cp(1), k)
	fb.ret(next)
	fb.done()

	seven := types.ConstArg{Kind: types.CAValue, Value: w.intVal(types.I64, 7)}
	ib := w.body(f, types.I64, types.I64, types.I64)
	ib.assign(0, lp(0), bin(mir.Mul, cp(1), imm(types.I64, 1000)))
	ib.assign(0, lp(0), bin(mir.Add, cp(0), imm(types.I64, 7)))
	ib.ret(0)
	w.setInstance(f, []types.GenericArg{types.ConstGenericArg(seven)}, ib.b)

	call := w.fn("main", "Call", types.I64)
	x := w.body(call, types.I64)
	n := x.block()
	x.call(0, f, lp(0), n, imm(types.I64, 5), imm(types.I64, 7))
	x.ret(n)
	x.done()
	return call
}

// TestForwardingCallRunsInstance: a callee that passes a const parameter on
// as a const argument runs mono's instance keyed by the call's const value
// (5*1000 + 7), as codegen does, where a forwarded Print format is
// expanded, not the lowered body (5 + 7), whose forwarded Print the
// evaluator cannot run. Both the direct and the slotted forms.
func TestForwardingCallRunsInstance(t *testing.T) {
	for _, temp := range []bool{false, true} {
		w := newWorld(t)
		call := forwardWorld(t, w, temp)
		res := w.call(w.evaluator(0), call, 1000)
		if res.Err != nil {
			t.Fatalf("temp %v: Call: %+v", temp, res.Err)
		}
		if got := w.intOf(res.Value); got != 5007 {
			t.Errorf("temp %v: Call() = %d, want 5007 (the instance)", temp, got)
		}
	}
}

// TestForwardingConstInATemporary: a call that passes the const argument
// as a temporary (lower's form for a named const, an aggregate or a field)
// is keyed by the temporary's value, so it runs the instance as the literal
// form does, not the lowered body with its forwarded Print (the driver's
// ctprint_test.go shape S10).
func TestForwardingConstInATemporary(t *testing.T) {
	w := newWorld(t)
	forwardWorld(t, w, false)
	f, _ := w.items.ByKey(types.ItemKey("lib", "F"))
	call := w.fn("main", "Call2", types.I64)
	x := w.body(call, types.I64)
	k := x.local(types.I64)
	x.assign(0, lp(k), use(imm(types.I64, 7)))
	n := x.block()
	x.call(0, f, lp(0), n, imm(types.I64, 5), cp(k))
	x.ret(n)
	x.done()
	res := w.call(w.evaluator(0), call, 1000)
	if res.Err != nil || w.intOf(res.Value) != 5007 {
		t.Fatalf("Call2: %+v, want 5007 (the instance)", res)
	}
}

// TestForwardingCalleeNotReady: a forwarding callee whose body is not
// ready (poisoned by an earlier diagnostic) ends the evaluation in poison
// before any body runs, so the lowered body's forwarded Print is never
// reached: forwardsConst answers false and resolve stops on the state, as
// for any callee.
func TestForwardingCalleeNotReady(t *testing.T) {
	w := newWorld(t)
	call := forwardWorld(t, w, false)
	f, _ := w.items.ByKey(types.ItemKey("lib", "F"))
	w.setState(f, BodyPoisoned)
	wantPoison(t, w.call(w.evaluator(0), call, 1000))
}
