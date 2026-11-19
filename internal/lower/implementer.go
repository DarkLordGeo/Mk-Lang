// Why: a type that implements Slice[T] can be indexed and looped over like a
// slice, and lower must find the Items or ItemsMut method itself, since check
// does not record which method an index or a for loop uses.
//
// What: sliceMethod and sliceIfaceCallee find the method; implArgs,
// matchType and matchConst bind the impl's generic arguments; itemsOf calls
// Items or ItemsMut for a for loop.
//
// How: the method is the one named Items or ItemsMut in the impl of
// Known.Slice for the type, found from facts that need no Checker. A for
// loop's holder borrows the Self place, and the Items call's result lasts
// for the whole loop.

package lower

import (
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// sliceMethod finds Items (or ItemsMut) of the impl Slice[elem] for the
// named type t: the impl member keyed ImplMemberKey(impl, name). It returns
// the method item and its generic arguments: the impl's parameters bound by
// matching the impl's target type against t (implArgs), so an impl whose
// parameters differ from the type's (impl[K] Slice[u8] for Buf[K, u8], or
// impl Slice[int] for Ints) gets its own.
func (bl *builder) sliceMethod(t, elem types.TypeID, mutable bool, at source.Span) (types.ItemID, []types.GenericArg) {
	name := "Items"
	if mutable {
		name = "ItemsMut"
	}
	ty := bl.tab.Get(t)
	if ty.Kind != types.KNamed || bl.cfg.Known == nil || bl.cfg.Known.Slice == types.NoItem {
		source.ICEf(at, "lower: Slice method %s of a non-implementer (request L-1)", name)
	}
	impls := bl.cfg.Impls
	if impls == nil {
		source.ICEf(at, "lower: no Config.Impls to find %s (request L-1)", name)
	}
	impl := impls(bl.cfg.Known.Slice, []types.GenericArg{types.TypeArg(elem)}, t)
	if impl == types.NoItem && bl.b.GenericCount > 0 {
		// A generic body: the interface method, resolved per instance by
		// mono.
		return types.NoItem, nil
	}
	if impl == types.NoItem {
		source.ICEf(at, "lower: no impl Slice for %s (request L-1)", bl.tab.TypeString(t, bl.sig.Generics))
	}
	key := types.ImplMemberKey(bl.cfg.Items.Get(impl).Key, name)
	m, ok := bl.cfg.Items.ByKey(key)
	if !ok {
		source.ICEf(at, "lower: impl Slice for %s declares no %s (request L-1)", bl.tab.TypeString(t, bl.sig.Generics), name)
	}
	return m, bl.implArgs(impl, t, at)
}

// implArgs binds each generic parameter of impl by matching the impl's
// target type (in the impl's own parameters) against t; a method of the
// impl takes the impl's generics as its own (no method of Slice[T] is
// generic itself). A parameter the target type does not mention is an ICE:
// check's coherence rule makes every impl parameter appear in it.
func (bl *builder) implArgs(impl types.ItemID, t types.TypeID, at source.Span) []types.GenericArg {
	d, ok := bl.cfg.Items.Get(impl).Decl.(*types.ImplDecl)
	if !ok {
		source.ICEf(at, "lower: impl item %d has no ImplDecl", impl)
	}
	if len(d.Generics) == 0 {
		return nil
	}
	out := make([]types.GenericArg, len(d.Generics))
	bound := make([]bool, len(d.Generics))
	if !bl.matchType(d.For, t, out, bound) {
		source.ICEf(at, "lower: the impl's target type does not match %s", bl.tab.TypeString(t, bl.sig.Generics))
	}
	for i, b := range bound {
		if !b {
			source.ICEf(at, "lower: impl parameter %d is not in its target type", i)
		}
	}
	return out
}

// matchType walks pattern (in an impl's parameters) and t together,
// binding each KParam of pattern to the matching part of t.
func (bl *builder) matchType(pattern, t types.TypeID, out []types.GenericArg, bound []bool) bool {
	pt, at := bl.tab.Get(pattern), bl.tab.Get(t)
	if pt.Kind == types.KParam {
		i := int(pt.Index)
		if i >= len(out) {
			return false
		}
		if bound[i] {
			return !out[i].IsConst && out[i].Type == t
		}
		out[i], bound[i] = types.TypeArg(t), true
		return true
	}
	if pattern == t {
		return true
	}
	if pt.Kind != at.Kind {
		return false
	}
	switch pt.Kind {
	case types.KRef, types.KMut, types.KSlice, types.KMutSlice, types.KRawPtr, types.KOptional, types.KErrorUnion:
		return pt.Errors == at.Errors && bl.matchType(pt.Elem, at.Elem, out, bound)
	case types.KArray:
		return bl.matchConst(pt.Len, at.Len, out, bound) && bl.matchType(pt.Elem, at.Elem, out, bound)
	case types.KNamed:
		if pt.Item != at.Item || len(pt.Args) != len(at.Args) {
			return false
		}
		for k := range pt.Args {
			pa, aa := pt.Args[k], at.Args[k]
			switch {
			case pa.IsConst != aa.IsConst:
				return false
			case pa.IsConst:
				if !bl.matchConst(pa.Const, aa.Const, out, bound) {
					return false
				}
			case !bl.matchType(pa.Type, aa.Type, out, bound):
				return false
			}
		}
		return true
	}
	return false
}

// matchConst is matchType for a const argument: a CAParam binds.
func (bl *builder) matchConst(p, a types.ConstArg, out []types.GenericArg, bound []bool) bool {
	if p.Kind == types.CAParam {
		i := int(p.Param)
		if i >= len(out) {
			return false
		}
		if bound[i] {
			return out[i].IsConst && out[i].Const == a
		}
		out[i], bound[i] = types.GenericArg{IsConst: true, Const: a}, true
		return true
	}
	return p == a
}

// itemsOf calls Items(&q) or ItemsMut(mut q) on the Self place q into a
// fresh temporary of type []elem or mut []elem; with holder set, the
// borrow is the loop's holder (OriginLoopHolder) and the call reborrows it.
func (bl *builder) itemsOf(q mir.Place, elem types.TypeID, mutable bool, loop mir.LoopID, at source.Span) (mir.Place, mir.LocalID) {
	st := bl.b.PlaceType(q)
	m, gen := bl.sliceMethod(st, elem, mutable, at)
	kind, rt, res := mir.Shared, bl.tab.Ref(st), bl.tab.Slice(elem)
	if mutable {
		kind, rt, res = mir.Exclusive, bl.tab.Mut(st), bl.tab.MutSlice(elem)
	}
	var h mir.LocalID
	origin := mir.OriginPlain
	if loop != mir.NoLoop {
		h = bl.b.AddLocal(mir.Local{Type: rt, Kind: mir.LocalLoopHolder, Loop: loop, Span: bl.span(at)})
		bl.temps[len(bl.temps)-1].temps = append(bl.temps[len(bl.temps)-1].temps, h)
		origin = mir.OriginLoopHolder
	} else {
		h = bl.newTemp(rt, at)
	}
	bl.assign(bl.b.Place(h), mir.Rvalue{Kind: mir.RvRef, Borrow: kind, Place: q, Origin: origin, Loop: loop, Type: rt}, at)
	arg := mir.Arg{Operand: mir.Copy(bl.b.Place(h)), Mode: mir.ArgCopy}
	if mutable {
		arg.Mode = mir.ArgReborrow
		if loop == mir.NoLoop {
			arg = mir.Arg{Operand: mir.Move(bl.b.Place(h)), Mode: mir.ArgMove}
		}
	}
	s := bl.newTemp(res, at)
	next := bl.newBlock()
	callee := mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: m, Args: bl.b.AddGenericArgs(gen...)}}
	if m == types.NoItem {
		callee = bl.sliceIfaceCallee(st, mutable, at)
	}
	bl.term(mir.Terminator{Kind: mir.TermCall, Callee: callee,
		Args: bl.b.AddArgs(arg), Dest: bl.b.Place(s), Target: next, Spawn: mir.NoLocal}, at)
	bl.cur = next
	if loop == mir.NoLoop {
		return bl.b.Place(s), mir.NoLocal
	}
	return bl.b.Place(s), h
}

// sliceIfaceCallee is Slice's Items or ItemsMut as an interface call on
// self type st.
func (bl *builder) sliceIfaceCallee(st types.TypeID, mutable bool, at source.Span) mir.Callee {
	name := source.SymItems
	if mutable {
		name = source.SymItemsMut
	}
	if d, ok := bl.cfg.Items.Get(bl.cfg.Known.Slice).Decl.(*types.InterfaceDecl); ok {
		for k := range d.Methods {
			if d.Methods[k].Name == name {
				return mir.Callee{Kind: mir.CallInterface, Iface: bl.cfg.Known.Slice, Method: uint32(k), SelfType: st}
			}
		}
	}
	source.ICEf(at, "lower: Slice declares no %s (request L-1)", bl.cfg.Interner.Str(name))
	return mir.Callee{}
}
