// Why: the Slice[T] namespace functions work on every implementer, but
// each is instantiated over T only and its body sees self as []T or
// mut []T, while check types self as &Slice[T] or mut Slice[T] and lower
// passes the receiver in its own type; mono must make the two sides meet.
//
// What: adaptSlices and its helpers (ifaceNamed, sliceFn), the
// per-instance adaptation of callee and callers:
//
//   - in the callee, &Slice[X] and mut Slice[X] become []X and mut []X (the
//     substituter's sliceSelf), &(*self) and mut (*self) become a reslice of
//     self, Items() and ItemsMut() on Self = Slice[X] become the slice itself,
//     and a call of the bodiless Slice[T].Len becomes RvLen;
//   - in every caller, the receiver of a Slice[T] namespace function becomes
//     that slice: a reslice of a slice or array (through one deref for a
//     borrow), or a call of the implementer's Items or ItemsMut, which splits
//     the block.
//
// How: only Slice[T] declares Type.Name functions on an interface (no user
// interface may declare such functions), so the rules key on an interface
// owner, never on the name Slice.

package mono

import (
	"mk/internal/mir"
	"mk/internal/types"
)

// ifaceArg is X when t is I[X] for an interface I and the substituter adapts
// Slice[T] selves.
func (s *substituter) ifaceArg(t types.TypeID) (types.TypeID, bool) {
	if !s.sliceSelf {
		return types.NoType, false
	}
	return ifaceNamedArg(s.tab, t)
}

// ifaceNamed reports whether t is an interface type I[...]: in MIR, only the
// Self of a Slice[T] namespace function.
func ifaceNamed(tab *types.Table, t types.TypeID) bool {
	if t == types.NoType {
		return false
	}
	ty := tab.Get(t)
	if ty.Kind != types.KNamed || tab.Items() == nil {
		return false
	}
	_, ok := tab.Items().Get(ty.Item).Decl.(*types.InterfaceDecl)
	return ok
}

// ifaceNamedArg is X of an interface type I[X].
func ifaceNamedArg(tab *types.Table, t types.TypeID) (types.TypeID, bool) {
	if !ifaceNamed(tab, t) {
		return types.NoType, false
	}
	ty := tab.Get(t)
	if len(ty.Args) != 1 || ty.Args[0].IsConst {
		ice("interface self type %s is not I[X]", tab.TypeString(t, nil))
	}
	return ty.Args[0].Type, true
}

// mentionsIface reports whether t still names an interface type at any depth.
func mentionsIface(tab *types.Table, t types.TypeID) bool {
	if t == types.NoType {
		return false
	}
	ty := tab.Get(t)
	switch ty.Kind {
	case types.KNamed:
		if ifaceNamed(tab, t) {
			return true
		}
		for _, a := range ty.Args {
			if !a.IsConst && mentionsIface(tab, a.Type) {
				return true
			}
		}
	case types.KRef, types.KMut, types.KSlice, types.KMutSlice, types.KRawPtr, types.KOptional, types.KArray, types.KErrorUnion:
		return mentionsIface(tab, ty.Elem)
	case types.KRecord:
		for _, f := range ty.Fields {
			if mentionsIface(tab, f.Type) {
				return true
			}
		}
	case types.KFn:
		for _, p := range ty.Fn.Params {
			if mentionsIface(tab, p.Type) {
				return true
			}
		}
		return mentionsIface(tab, ty.Fn.Result)
	}
	return false
}

// sliceFn reports whether item is a Slice[T] namespace function: a function
// whose owner is an interface.
func sliceFn(items *types.Items, item types.ItemID) bool {
	it := items.Get(item)
	if it.Owner == types.NoItem {
		return false
	}
	_, ok := items.Get(it.Owner).Decl.(*types.InterfaceDecl)
	return ok
}

// callsSliceFn reports whether a body calls a Slice[T] namespace function,
// so a non-generic caller is rewritten too (needsRewrite).
func (bl *builder) callsSliceFn() bool {
	items := bl.env.Items()
	for i := range bl.gb.Blocks {
		t := &bl.gb.Blocks[i].Term
		if isCall(t) && t.Callee.Kind == mir.CallDirect && sliceFn(items, t.Callee.Fn.Item) {
			return true
		}
	}
	return false
}

// adaptSlices runs both halves of the Slice[T] adaptation over the substituted
// body, after the fields unroll (so no block is appended inside a fields
// range). Blocks appended by a split are visited too. It reports whether
// anything changed.
func (bl *builder) adaptSlices() bool {
	b := bl.b
	changed := false
	for bi := 0; bi < len(b.Blocks); bi++ {
		for si := range b.Blocks[bi].Stmts {
			if bl.selfReslice(&b.Blocks[bi].Stmts[si].Rvalue) {
				changed = true
			}
		}
		t := &b.Blocks[bi].Term
		if !isCall(t) {
			continue
		}
		switch {
		case t.Kind == mir.TermTailCall:
			if t.Callee.Kind == mir.CallInterface || t.Callee.Kind == mir.CallDirect && sliceFn(bl.env.Items(), t.Callee.Fn.Item) {
				noTail(t, "a Slice[T] function")
			}
		case t.Callee.Kind == mir.CallInterface && ifaceNamed(bl.tab, t.Callee.SelfType):
			bl.itemsIdentity(mir.BlockID(bi))
			changed = true
		case t.Callee.Kind == mir.CallDirect && sliceFn(bl.env.Items(), t.Callee.Fn.Item):
			if bl.sliceCall(mir.BlockID(bi)) {
				changed = true
			}
		}
	}
	for i := range b.Locals {
		if mentionsIface(bl.tab, b.Locals[i].Type) {
			ice("instance %s: local _%d keeps the interface type %s", nameOf(bl.env, bl.item, bl.args), i, bl.tab.TypeString(b.Locals[i].Type, nil))
		}
	}
	return changed
}

// selfReslice turns &(*p) or mut (*p), where *p has an interface type (the
// self of a Slice[T] function, now a slice), into a reslice of p.
func (bl *builder) selfReslice(rv *mir.Rvalue) bool {
	if rv.Kind != mir.RvRef || rv.Place.Proj.Len == 0 {
		return false
	}
	last := bl.b.Projections[rv.Place.Proj.End()-1]
	if last.Kind != mir.ProjDeref || !ifaceNamed(bl.tab, last.Type) {
		return false
	}
	p := rv.Place
	p.Proj.Len--
	if k := bl.tab.Get(bl.b.PlaceType(p)).Kind; k != types.KSlice && k != types.KMutSlice {
		ice("instance %s: %s of a %s self", nameOf(bl.env, bl.item, bl.args), "reference", bl.tab.TypeString(bl.b.PlaceType(p), nil))
	}
	*rv = mir.Rvalue{Kind: mir.RvSlice, Borrow: rv.Borrow, Origin: rv.Origin, Loop: rv.Loop, Place: p}
	return true
}

// itemsIdentity rewrites Items() or ItemsMut() on Self = Slice[X] in block bi
// into Dest = the receiver slice, chosen by the destination's type: copy for
// []X from []X, a reslice otherwise.
func (bl *builder) itemsIdentity(bi mir.BlockID) {
	b := bl.b
	t := &b.Blocks[bi].Term
	if t.Args.Len != 1 || t.HasErr {
		ice("instance %s: Slice self call with %d arguments", nameOf(bl.env, bl.item, bl.args), t.Args.Len)
	}
	recv := b.Args[t.Args.Start].Operand
	if recv.Kind != mir.OpCopy && recv.Kind != mir.OpMove {
		ice("instance %s: Slice self call on a constant", nameOf(bl.env, bl.item, bl.args))
	}
	rt, dt := bl.b.OperandType(recv), b.PlaceType(t.Dest)
	var rv mir.Rvalue
	switch dk, rk := bl.tab.Get(dt).Kind, bl.tab.Get(rt).Kind; {
	case dk == types.KSlice && rk == types.KSlice:
		rv = mir.Rvalue{Kind: mir.RvUse, A: mir.Copy(recv.Place)}
	case dk == types.KSlice && rk == types.KMutSlice:
		rv = mir.Rvalue{Kind: mir.RvSlice, Borrow: mir.Shared, Place: recv.Place}
	case dk == types.KMutSlice && rk == types.KMutSlice:
		rv = mir.Rvalue{Kind: mir.RvSlice, Borrow: mir.Exclusive, Place: recv.Place}
	default:
		ice("instance %s: Slice self call into %s from %s", nameOf(bl.env, bl.item, bl.args), bl.tab.TypeString(dt, nil), bl.tab.TypeString(rt, nil))
	}
	blk := &b.Blocks[bi]
	blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StAssign, Place: t.Dest, Rvalue: rv, Span: t.Span})
	*t = mir.Terminator{Kind: mir.TermGoto, Span: t.Span, Target: t.Target}
}

// sliceCall adapts a call of a Slice[T] namespace function in block bi: the
// receiver becomes the callee's slice self, and a call of the bodiless Len
// becomes RvLen of it.
func (bl *builder) sliceCall(bi mir.BlockID) bool {
	b := bl.b
	t := &b.Blocks[bi].Term
	f := t.Callee.Fn
	items := bl.env.Items()
	fd, ok := items.Get(f.Item).Decl.(*types.FuncDecl)
	if !ok || len(fd.Sig.Params) == 0 || t.Args.Len == 0 {
		ice("call of %s: no self", items.Get(f.Item).Key)
	}
	fargs := b.GenericArgs[f.Args.Start:f.Args.End()]
	want := Subst(bl.tab, fd.Sig.Params[0].Type, fargs)
	if x, ok := bl.selfOf(want); ok {
		want = x
	} else {
		ice("call of %s: self %s is not &Slice[X] or mut Slice[X]", items.Get(f.Item).Key, bl.tab.TypeString(want, nil))
	}
	changed := false
	if bl.b.OperandType(b.Args[t.Args.Start].Operand) != want {
		bi = bl.convertRecv(bi, want)
		t = &b.Blocks[bi].Term
		changed = true
	}
	if !fd.Bodiless && !fd.Intrinsic {
		return changed
	}
	// Slice[T].Len is the one bodiless Slice[T] function:
	// the length of the slice self.
	if bl.tab.Interner().Str(items.Get(f.Item).Name) != "Len" || t.HasErr || t.Spawn != mir.NoLocal {
		ice("call of the bodiless %s", items.Get(f.Item).Key)
	}
	recv := b.Args[t.Args.Start].Operand
	if recv.Kind != mir.OpCopy && recv.Kind != mir.OpMove {
		ice("call of %s on a constant", items.Get(f.Item).Key)
	}
	blk := &b.Blocks[bi]
	blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StAssign, Place: t.Dest,
		Rvalue: mir.Rvalue{Kind: mir.RvLen, Place: recv.Place}, Span: t.Span})
	*t = mir.Terminator{Kind: mir.TermGoto, Span: t.Span, Target: t.Target}
	return true
}

// selfOf maps a substituted &Slice[X] or mut Slice[X] to []X or mut []X.
func (bl *builder) selfOf(t types.TypeID) (types.TypeID, bool) {
	ty := bl.tab.Get(t)
	if ty.Kind != types.KRef && ty.Kind != types.KMut {
		return types.NoType, false
	}
	x, ok := ifaceNamedArg(bl.tab, ty.Elem)
	if !ok {
		return types.NoType, false
	}
	if ty.Kind == types.KRef {
		return bl.tab.Slice(x), true
	}
	return bl.tab.MutSlice(x), true
}

// convertRecv makes the receiver of the call ending block bi a value of
// want ([]X or mut []X) and returns the block that now ends with the call:
// bi itself after a reslice, or a new block after a call of the
// implementer's Items or ItemsMut.
func (bl *builder) convertRecv(bi mir.BlockID, want types.TypeID) mir.BlockID {
	b := bl.b
	tab := bl.tab
	t := &b.Blocks[bi].Term
	ai := t.Args.Start
	recv := b.Args[ai]
	rt := b.OperandType(recv.Operand)
	excl := tab.Get(want).Kind == types.KMutSlice
	what := func() string {
		return nameOf(bl.env, bl.item, bl.args) + ": receiver " + tab.TypeString(rt, nil) + " for " + tab.TypeString(want, nil)
	}
	if recv.Operand.Kind != mir.OpCopy && recv.Operand.Kind != mir.OpMove {
		ice("instance %s is a constant", what())
	}
	tmp := b.AddLocal(mir.Local{Type: want, Kind: mir.LocalTemp, Loop: mir.NoLoop, Span: t.Span})
	pass := mir.Arg{Operand: mir.Copy(mir.LocalPlace(tmp)), Mode: mir.ArgCopy}
	if excl {
		pass = mir.Arg{Operand: mir.Move(mir.LocalPlace(tmp)), Mode: mir.ArgMove}
	}
	reslice := func(p mir.Place, pt types.TypeID) mir.BlockID {
		ty := tab.Get(pt)
		switch {
		case ty.Kind != types.KArray && ty.Kind != types.KSlice && ty.Kind != types.KMutSlice:
			ice("instance %s: no slice of %s", what(), tab.TypeString(pt, nil))
		case tab.MutSlice(ty.Elem) != want && tab.Slice(ty.Elem) != want:
			ice("instance %s: element type differs", what())
		case excl && ty.Kind == types.KSlice:
			ice("instance %s: a mut self on a shared slice (E0507)", what())
		}
		k := mir.Shared
		if excl {
			k = mir.Exclusive
		}
		blk := &b.Blocks[bi]
		blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StAssign, Place: mir.LocalPlace(tmp),
			Rvalue: mir.Rvalue{Kind: mir.RvSlice, Borrow: k, Place: p}, Span: t.Span})
		b.Args[ai] = pass
		return bi
	}
	ty := tab.Get(rt)
	switch ty.Kind {
	case types.KArray, types.KSlice, types.KMutSlice:
		return reslice(recv.Operand.Place, rt)
	case types.KRef, types.KMut:
		et := tab.Get(ty.Elem)
		if et.Kind == types.KArray || et.Kind == types.KSlice || et.Kind == types.KMutSlice {
			if excl && ty.Kind == types.KRef {
				ice("instance %s: a mut self through a shared reference (E0507)", what())
			}
			return reslice(bl.deref(recv.Operand.Place, ty.Elem), ty.Elem)
		}
		if et.Kind != types.KNamed {
			break
		}
		if excl && ty.Kind == types.KRef {
			ice("instance %s: a mut self through a shared reference (E0507)", what())
		}
		return bl.itemsCall(bi, ty.Elem, want, tmp, pass, what)
	}
	ice("instance %s: no adaptation", what())
	return bi
}

// deref is p followed by one deref step to type to.
func (bl *builder) deref(p mir.Place, to types.TypeID) mir.Place {
	b := bl.b
	steps := append([]mir.Projection(nil), b.Proj(p)...)
	steps = append(steps, mir.Projection{Kind: mir.ProjDeref, Type: to})
	return b.Place(p.Local, steps...)
}

// itemsCall splits block bi: it now ends with tmp = impl.Items(recv) (or
// ItemsMut), the receiver argument unchanged, and a new block holds the
// original call with tmp as its receiver. The interface method is the one
// whose self mode and result match want ([]X: &self, mut []X: mut self).
func (bl *builder) itemsCall(bi mir.BlockID, self, want types.TypeID, tmp mir.LocalID, pass mir.Arg, what func() string) mir.BlockID {
	b := bl.b
	tab := bl.tab
	items := bl.env.Items()
	t := b.Blocks[bi].Term
	owner := items.Get(t.Callee.Fn.Item).Owner
	d := items.Get(owner).Decl.(*types.InterfaceDecl)
	excl := tab.Get(want).Kind == types.KMutSlice
	m := -1
	for i, ms := range d.Methods {
		if len(ms.Params) != 1 || ms.Errors != types.EmptyErrors {
			continue
		}
		sk, rk := tab.Get(ms.Params[0].Type).Kind, tab.Get(ms.Result).Kind
		if excl && sk == types.KMut && rk == types.KMutSlice || !excl && sk == types.KRef && rk == types.KSlice {
			if m >= 0 {
				ice("instance %s: two interface methods give the slice", what())
			}
			m = i
		}
	}
	if m < 0 {
		ice("instance %s: no interface method gives the slice", what())
	}
	fn, fargs, ok := bl.env.ImplMethod(owner, uint32(m), self)
	if !ok {
		ice("instance %s: no impl of %s method %d for %s", what(), items.Get(owner).Key, m, tab.TypeString(self, nil))
	}
	recv := b.Args[t.Args.Start]
	b.Args[t.Args.Start] = pass
	nb := b.AddBlock(b.Blocks[bi].Origin, b.Blocks[bi].Defer)
	b.Blocks[nb].Term = t
	b.Blocks[bi].Term = mir.Terminator{Kind: mir.TermCall, Span: t.Span, Target: nb, Spawn: mir.NoLocal,
		Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: fn, Args: b.AddGenericArgs(fargs...)}},
		Args:   b.AddArgs(recv), Dest: mir.LocalPlace(tmp)}
	return nb
}
