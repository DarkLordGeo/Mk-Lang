// Why: `become f(args)` lowers to TermTailCall, a call that replaces the
// frame; the validator must check it as strictly as TermCall and also
// check what the tail call adds.
//
// What: callArgs, directCallee, genericCount, argTypes and reborrows (the
// checks a call and a tail call share, invariants M5 and M10), and
// tailCall.
//
// How: a tail call has no destination, target, error edge or spawn; with
// Env.Sig its callee's result and error set must equal the body's own,
// since the callee's return is the body's.

package mir

import "mk/internal/types"

// callArgs checks a call's argument range, operands and modes (M5, M10) and
// returns the arguments: a reborrow is a copy of a mut T or mut []T, and with
// Env.Facts a plain value is copied and any other moved.
func (v *validator) callArgs(t *Terminator) []Arg {
	b, tab := v.b, v.tab
	v.rng(t.Args, len(b.Args), "argument")
	args := b.Args[t.Args.Start:t.Args.End()]
	for _, a := range args {
		ty := v.argOperand(a.Operand)
		switch a.Mode {
		case ArgReborrow:
			k := tab.Kind(ty)
			if a.Operand.Kind != OpCopy || (k != types.KMut && k != types.KMutSlice) {
				v.fail("M10", "reborrow of %s", v.ts(ty))
			}
		case ArgCopy, ArgMove:
			if f := v.env.Facts; f != nil {
				if facts, ok := f(ty); ok && (facts.Kind == types.Plain) != (a.Mode == ArgCopy) {
					v.fail("M10", "%s passed by %v", v.ts(ty), a.Mode)
				}
			}
		default:
			v.fail("M2", "argument mode %d", a.Mode)
		}
	}
	return args
}

// directCallee checks what a direct call and a direct tail call share (M5):
// the callee names an item, its generic arguments are in range and well
// formed, and, with Env.Callee, there are at least as many arguments as
// parameters, and those past the parameters are environment arguments unless
// the callee is variadic (its extra arguments are its variadic ones). It
// returns the callee's signature, nil when Env cannot give one. what names
// the terminator in messages ("call", "tail call").
func (v *validator) directCallee(t *Terminator, args []Arg, what string) *types.Signature {
	if t.Callee.Fn.Item == types.NoItem {
		// A direct call names its function; a call through a value
		// is CallValue.
		v.fail("M5", "direct %s of no item", what)
	}
	v.rng(t.Callee.Fn.Args, len(v.b.GenericArgs), "generic argument")
	v.genericArgs(t.Callee.Fn.Args)
	if v.env.Callee == nil {
		return nil
	}
	sig := v.env.Callee(t.Callee.Fn.Item)
	if sig == nil {
		return nil
	}
	if len(args) < len(sig.Params) {
		v.fail("M5", "%d arguments, callee has %d parameters (variadic %v)", len(args), len(sig.Params), sig.Variadic)
	}
	if !sig.Variadic {
		v.envArgs(t, args[len(sig.Params):], len(sig.Params))
	}
	return sig
}

// genericCount checks that a direct call of a generic callee gives all its
// generic arguments (M5) and reports whether nothing about the callee is
// generic: no generic arguments and no generics, where the argument types
// can be compared with the parameters'.
func (v *validator) genericCount(t *Terminator, sig *types.Signature) bool {
	if len(sig.Generics) > 0 && int(t.Callee.Fn.Args.Len) != len(sig.Generics) {
		v.fail("M5", "%d generic arguments, callee has %d generics", t.Callee.Fn.Args.Len, len(sig.Generics))
	}
	return t.Callee.Fn.Args.Len == 0 && len(sig.Generics) == 0
}

// argTypes checks argument types for a callee that is not generic (M5):
// argument types equal the parameters' (a reborrow passes a mut T or mut []T
// of the parameter's own type); a fn argument may carry fewer flows than its
// parameter.
func (v *validator) argTypes(args []Arg, params []types.Param) {
	for i, a := range args {
		if at := v.argOperand(a.Operand); i < len(params) && !v.fits(at, params[i].Type) {
			v.fail("M5", "argument %d is %s, parameter is %s", i, v.ts(at), v.ts(params[i].Type))
		}
	}
}

// reborrows checks that a reborrow argument goes into a mut T or mut []T
// parameter (M10); param gives the type of parameter i < n.
func (v *validator) reborrows(args []Arg, n int, param func(i int) types.TypeID) {
	for i, a := range args {
		if i < n && a.Mode == ArgReborrow {
			if k := v.tab.Kind(param(i)); k != types.KMut && k != types.KMutSlice {
				v.fail("M10", "reborrow into a parameter of %s", v.ts(param(i)))
			}
		}
	}
}

// tailCall checks a TermTailCall (M5): no destination, target, error edge or
// spawn; a direct callee that names its function, or a call through a fn
// value; the arguments as a call's (callArgs, directCallee, genericCount,
// argTypes, reborrows). With Env.Sig, the callee's result and error set
// equal the body's own, since the callee's return is the body's: for a fn
// value always, its type being written in the body, and for a direct callee
// with Env.Callee only when nothing is generic, neither the callee, its
// reference nor the body. The generic case is check's E0312, which compares
// the declared signatures; an instance keeps generic callees'
// references (Fn.Args), so mono's validation does not compare them either.
func (v *validator) tailCall(t *Terminator) {
	b, tab := v.b, v.tab
	if t.HasErr || t.Spawn != NoLocal || t.Target != 0 || t.Dest != (Place{}) {
		v.fail("M5", "a tail call has no destination, target, error edge or spawn")
	}
	args := v.callArgs(t)
	own := v.env.Sig
	same := func(result types.TypeID, errs types.ErrorSetID, what string) {
		if own != nil && (result != own.Result || errs != own.Errors) {
			v.fail("M5", "tail call to %s returning %s, the body returns %s",
				what, v.ts(tab.ReturnType(result, errs)), v.ts(own.ReturnType(tab)))
		}
	}
	switch t.Callee.Kind {
	case CallDirect:
		sig := v.directCallee(t, args, "tail call")
		if sig == nil {
			return
		}
		if !v.genericCount(t, sig) {
			return
		}
		v.argTypes(args, sig.Params)
		v.reborrows(args, len(sig.Params), func(i int) types.TypeID { return sig.Params[i].Type })
		if b.GenericCount == 0 {
			same(sig.Result, sig.Errors, "a function")
		}
	case CallValue:
		ft := v.operand(t.Callee.Value)
		if tab.Kind(ft) != types.KFn {
			v.fail("M5", "tail call through %s", v.ts(ft))
		}
		fn := tab.Get(ft).Fn
		if len(args) < len(fn.Params) {
			v.fail("M5", "%d arguments, fn value has %d parameters", len(args), len(fn.Params))
		}
		v.envArgs(t, args[len(fn.Params):], len(fn.Params))
		v.reborrows(args, len(fn.Params), func(i int) types.TypeID { return fn.Params[i].Type })
		same(fn.Result, fn.Errors, "a fn value")
	default:
		v.fail("M5", "tail call of callee kind %d: only a direct call or a fn value", t.Callee.Kind)
	}
}
