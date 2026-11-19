// Why: any non-generic function can be passed as a plain fn value (T.Get
// in namespace form) or spawned, but a std intrinsic has no body of its
// own to point at.
//
// What: wrapper (an intrinsic's out-of-line body), wrapperFeatures,
// wrapperKey, inlineIntrinsic, and subst, substConst and substErrors (the
// intrinsic's signature with its generic arguments).
//
// How: an intrinsic used as a fn value (RvFnRef, a RelocFn in a const) or
// spawned by s.go gets a wrapper: a pack-local function with the
// intrinsic's signature whose body is a synthesized MIR body of one call of
// the intrinsic, emitted like any member, so the inline lowering is the
// only lowering.

package codegen

import (
	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// wrapperFn is one intrinsic wrapper of a pack.
type wrapperFn struct {
	fn   *llvm.Function
	sig  *lsig
	item types.ItemID
}

// inlineIntrinsic reports a std intrinsic lowered inline at each call,
// that is every intrinsic but the support-pack bodies, which are functions.
func (g *packGen) inlineIntrinsic(item types.ItemID) bool {
	it := g.items.Get(item)
	fd, ok := it.Decl.(*types.FuncDecl)
	return ok && fd.Intrinsic && !supportBodies[it.Key.String()]
}

// scopeMakers are the intrinsics whose result is a scope record that
// std.task.Enter links into its parent, so it must be written in place.
var scopeMakers = map[string]bool{"std.core.Io:Scope": true, "std.core.Io:ScopeOf": true, "std.core.Io:ScopeWith": true}

// wrapperFeatures is the target-features string a wrapper taken in
// function fi is compiled with: fi's own (Fma named as a value
// in a cpu "fma" function lowers to the fma instruction, never to an fmaf
// libcall Mk does not link), or none for a value taken outside any
// function (a RelocFn of a const), which check allows only where the
// baseline suffices (E1106).
func (g *packGen) wrapperFeatures(fi int) string {
	if fi < 0 {
		return ""
	}
	return g.featuresOf(fi)
}

// wrapperKey keys a wrapper by intrinsic, arguments and features.
func wrapperKey(item types.ItemID, args []types.GenericArg, features string) string {
	return argsKey(item, args) + "@" + features
}

// wrapper is the pack's wrapper of intrinsic item with generic arguments
// args (the FnRef's), taken in function fi (-1 for none), defined on
// first use per feature set: internal, named after the intrinsic's key
// with ".fn" (plus a pack ordinal for a generic instance or a feature set),
// with a FuncTable row of kind 2.
func (g *packGen) wrapper(item types.ItemID, args []types.GenericArg, fi int) wrapperFn {
	tf := g.wrapperFeatures(fi)
	k := wrapperKey(item, args, tf)
	if w, ok := g.wrappers[k]; ok {
		return w
	}
	it := g.items.Get(item)
	sig := g.declSig(item)
	if len(args) != len(sig.Generics) {
		source.ICEf(it.Span, "codegen: intrinsic %s used as a value with %d generic arguments for %d", it.Key, len(args), len(sig.Generics))
	}
	sub := func(t types.TypeID) types.TypeID { return g.subst(t, args) }
	b := mir.NewBody(item, it.Span, sub(sig.ReturnType(g.tt)))
	var params []types.TypeID
	var cargs []mir.Arg
	for _, p := range sig.Params {
		pt := sub(p.Type)
		params = append(params, pt)
		cargs = append(cargs, mir.Arg{Operand: mir.Copy(mir.LocalPlace(b.AddParam(pt, p.Name, 0)))})
	}
	result := b.Locals[mir.ReturnLocal].Type
	call := b.AddBlock(mir.OriginBody, mir.NoDefer)
	after := b.AddBlock(mir.OriginBody, mir.NoDefer)
	b.Blocks[call].Term = mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect,
		Fn: mir.FnRef{Item: item, Args: b.AddGenericArgs(args...)}}, Args: b.AddArgs(cargs...),
		Dest: mir.LocalPlace(mir.ReturnLocal), Target: after, Spawn: mir.NoLocal}
	if result == types.Never {
		b.Blocks[after].Term = mir.Terminator{Kind: mir.TermUnreachable}
	} else {
		b.Blocks[after].Term = mir.Terminator{Kind: mir.TermReturn}
	}

	if err := b.Validate(&mir.Env{Types: g.tt}); err != nil {
		source.ICEf(it.Span, "codegen: the wrapper body of %s: %v", it.Key, err)
	}
	name := "mk." + it.Key.String() + ".fn"
	if len(args) > 0 || tf != "" {
		g.nwrap++
		name += "." + itoa(g.nwrap)
	}
	ls := g.lowerSig(params, len(params), false, result)
	if scopeMakers[it.Key.String()] && !ls.sret {
		// Enter links the record at the destination into its parent; a
		// record returned in registers would move after that.
		source.ICEf(it.Span, "codegen: %s as a value returns its scope record in registers, which would move it", it.Key)
	}
	attrs := g.fnAttrs(ls.result)
	if tf != "" {
		attrs = withFeatures(attrs, tf)
	}
	fn := g.m.AddFunction(name, g.funcSig(ls, llvm.Internal, llvm.Default, false, attrs))
	w := wrapperFn{fn: fn, sig: ls, item: item}
	g.wrappers[k] = w
	g.extraFns = append(g.extraFns, fn)
	fg := &fnGen{g: g, f: &Func{Symbol: name, Key: name, Item: item, Body: b, Pack: g.p}, fi: -1, b: b, fn: fn, sig: ls}
	fg.subprogram(name, it.Span)
	fg.emitBlocks()
	return w
}

// subst is t with the generic parameters of an intrinsic's signature
// replaced by args: types, error sets and const parameters (std.simd's
// Vec[T, N] names its lane count N in its methods' signatures, [N]T and
// Vec[T, N]). An intrinsic signature names no Self (intrinsicSigs).
func (g *packGen) subst(t types.TypeID, args []types.GenericArg) types.TypeID {
	if len(args) == 0 {
		return t
	}
	tt := g.tt
	ty := tt.Get(t)
	switch ty.Kind {
	case types.KParam:
		if int(ty.Index) >= len(args) || args[ty.Index].IsConst {
			source.ICEf(source.Span{}, "codegen: type parameter %d of %d arguments in an intrinsic signature", ty.Index, len(args))
		}
		return args[ty.Index].Type
	case types.KRef:
		return tt.Ref(g.subst(ty.Elem, args))
	case types.KMut:
		return tt.Mut(g.subst(ty.Elem, args))
	case types.KSlice:
		return tt.Slice(g.subst(ty.Elem, args))
	case types.KMutSlice:
		return tt.MutSlice(g.subst(ty.Elem, args))
	case types.KRawPtr:
		return tt.RawPtr(g.subst(ty.Elem, args))
	case types.KOptional:
		return tt.Optional(g.subst(ty.Elem, args))
	case types.KArray:
		return tt.Array(g.substConst(ty.Len, args), g.subst(ty.Elem, args))
	case types.KErrorUnion:
		return tt.ErrorUnion(g.substErrors(ty.Errors, args), g.subst(ty.Elem, args))
	case types.KErrorSet:
		return tt.ErrorSetType(g.substErrors(ty.Errors, args))
	case types.KRecord:
		fs := make([]types.RecordField, len(ty.Fields))
		for i, f := range ty.Fields {
			fs[i] = types.RecordField{Name: f.Name, Type: g.subst(f.Type, args)}
		}
		return tt.Record(fs)
	case types.KNamed:
		if len(ty.Args) == 0 {
			return t
		}
		as := make([]types.GenericArg, len(ty.Args))
		for i, a := range ty.Args {
			if a.IsConst {
				as[i] = types.ConstGenericArg(g.substConst(a.Const, args))
			} else {
				as[i] = types.TypeArg(g.subst(a.Type, args))
			}
		}
		return tt.Named(ty.Item, as)
	case types.KFn:
		fn := *ty.Fn
		fn.Params = make([]types.FnParam, len(ty.Fn.Params))
		for i, p := range ty.Fn.Params {
			fn.Params[i] = types.FnParam{Type: g.subst(p.Type, args), Into: p.Into}
		}
		fn.Result = g.subst(ty.Fn.Result, args)
		fn.Errors = g.substErrors(ty.Fn.Errors, args)
		return tt.Fn(fn)
	}
	return t
}

// substConst is c with a const parameter replaced by its argument, which
// mono made a value; a value stays. SizeOf names no intrinsic signature's
// type (only its requires clauses, which are not types).
func (g *packGen) substConst(c types.ConstArg, args []types.GenericArg) types.ConstArg {
	switch c.Kind {
	case types.CAValue:
		return c
	case types.CAParam:
		if int(c.Param) >= len(args) || !args[c.Param].IsConst || args[c.Param].Const.Kind != types.CAValue {
			source.ICEf(source.Span{}, "codegen: const parameter %d of %d arguments in an intrinsic signature has no value", c.Param, len(args))
		}
		return args[c.Param].Const
	}
	source.ICEf(source.Span{}, "codegen: a const argument of kind %d in an intrinsic signature", c.Kind)
	return c
}

// substErrors replaces each parameter member of es by the members of the
// error set its argument names.
func (g *packGen) substErrors(es types.ErrorSetID, args []types.GenericArg) types.ErrorSetID {
	if es == types.EmptyErrors {
		return es
	}
	var out []types.ErrorMember
	param := false
	for _, m := range g.tt.Members(es) {
		if !m.IsParam {
			out = append(out, m)
			continue
		}
		param = true
		if int(m.Param) >= len(args) || args[m.Param].IsConst || g.tt.Kind(args[m.Param].Type) != types.KErrorSet {
			source.ICEf(source.Span{}, "codegen: error-set parameter %d without an error-set argument in an intrinsic signature", m.Param)
		}
		out = append(out, g.tt.Members(g.tt.Get(args[m.Param].Type).Errors)...)
	}
	if !param {
		return es
	}
	return g.tt.ErrorSet(out)
}
