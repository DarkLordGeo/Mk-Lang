// Why: memory safety rests on the explicit run-time checks lower emits: every
// index and slice is bounds-checked, and an enum value that came from C is
// checked against the declared tags.
//
// What: indexPlace and indexLocal (xs[i]), sliceExpr (xs[a..b]),
// boundsCheck, checkCmp and toUint; counterLoopFor and counterIndexed (the
// counter index elision); enumTagCheck and externEntryChecks (enum tags from
// C).
//
// How: an index is brought to uint by value-defined casts, so a negative one
// becomes at least 2^63 and fails one unsigned compare per bound; each site
// gets its own panic block. xs[i] with i the counter of an enclosing
// for i in lo..xs.Len() needs no check.

package lower

import (
	"mk/internal/ast"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// indexPlace is xs[i] on a slice or array.
func (bl *builder) indexPlace(x *ast.IndexExpr) mir.Place {
	ie, ok := x.Args[0].(ast.Expr)
	if !ok {
		source.ICEf(x.Span(), "lower: index by a type")
	}
	var base mir.Place
	if bl.isPlaceExpr(x.X) {
		n := len(bl.laterInPlace)
		bl.laterInPlace = append(bl.laterInPlace, ie)
		base = bl.place(x.X)
		bl.laterInPlace = bl.laterInPlace[:n]
	} else {
		save := bl.laterInPlace
		bl.laterInPlace = nil // a value runs to completion here
		base = bl.tempOf(x.X)
		bl.laterInPlace = save
		for n := bl.in.Adjust(x.X.ID()).Deref; n > 0; n-- {
			base = bl.deref(base, x.Span())
		}
	}
	bt := bl.tab.Get(bl.b.PlaceType(base))
	var elem types.TypeID
	var idx mir.LocalID
	switch bt.Kind {
	case types.KSlice, types.KMutSlice, types.KArray:
		elem = bt.Elem
		idx = bl.indexLocal(ie)
	case types.KStr:
		elem = types.U8
		idx = bl.indexLocal(ie)
	case types.KNamed:
		// The index runs before Items or ItemsMut borrows the implementer:
		// that borrow is a `&p` or `mut p` operand, formed last, so
		// `self.slots[self.Pick()] += 1` reads self before the loan of
		// self.slots exists, as it does over an array. So do
		// the outer levels' index expressions and slice bounds,
		// `self.rows[self.Pick()][self.Pick()]`.
		elem = bl.typeOf(x)
		idx = bl.indexLocal(ie)
		bl.evalLaterInPlace()
		base, _ = bl.itemsOf(base, elem, bl.placeMut, mir.NoLoop, x.Span())
	default:
		source.ICEf(x.Span(), "lower: index of a %v", bt.Kind)
	}
	if !bl.counterIndexed(x.X, ie) {
		bl.boundsCheck(base, idx, x.Span())
	}
	return bl.extend(base, mir.Projection{Kind: mir.ProjIndex, A: uint32(idx), Type: elem})
}

// indexLocal is a local holding the index's value. A user local or
// parameter is used as is, unless it is var and a later index expression or
// slice bound of the same place may write it: the place's ProjIndex step
// reads its local wherever the place is read, so ys[i][Bump(mut i)] would
// check i before Bump and access ys[i] after it (the place is
// evaluated once, with one index value). Then i is read into a slot here.
func (bl *builder) indexLocal(ie ast.Expr) mir.LocalID {
	if l, ok := bl.takePreEval(ie); ok {
		return l
	}
	if id, ok := unparen(ie).(*ast.Ident); ok && bl.in.Adjust(id.ID()).Deref == 0 && bl.in.Convert(id.ID()).Kind == ConvNone {
		l := mir.NoLocal
		switch u := bl.in.Use(id.ID()); u.Kind {
		case UseLocal:
			l = bl.remapped(bl.localOf(u.Binding, id.Span()))
		case UseParam:
			l = bl.remapped(mir.LocalID(u.Param))
		}
		if l != mir.NoLocal {
			if !bl.b.Locals[l].Var || bl.laterPlaceInert() {
				return l
			}
			return bl.slot(mir.Copy(bl.b.Place(l)), ie.Span()).Place.Local
		}
	}
	op := bl.subExpr(func() mir.Operand { return bl.operand(ie) })
	l := bl.newTemp(bl.b.OperandType(op), ie.Span())
	bl.assign(bl.b.Place(l), use(op), ie.Span())
	return l
}

// evalLaterInPlace runs, in source order, the later index expressions and
// slice bounds of the place being built, before an inner implementer's
// Items or ItemsMut borrow is formed (a `&p` or `mut p` operand is
// formed after all others), and keeps each value in preEval. When each is
// a literal or names a local, parameter or const, nothing moves: none runs
// code or reads a place the borrow holds. Otherwise all move: a field read
// may read the borrowed implementer, and an inert read keeps its place
// before a later call that may write what it reads (`a[f()][i][Bump(mut i)]`).
func (bl *builder) evalLaterInPlace() {
	plain := true
	for _, e := range bl.laterInPlace {
		plain = plain && bl.plainOperand(e)
	}
	if plain {
		return
	}
	for i := len(bl.laterInPlace) - 1; i >= 0; i-- {
		e := bl.laterInPlace[i]
		if _, done := bl.preEval[e.ID()]; done {
			continue
		}
		op := bl.subExpr(func() mir.Operand { return bl.operand(e) })
		l := bl.newTemp(bl.b.OperandType(op), e.Span())
		bl.assign(bl.b.Place(l), use(op), e.Span())
		if bl.preEval == nil {
			bl.preEval = map[ast.NodeID]mir.LocalID{}
		}
		bl.preEval[e.ID()] = l
	}
}

// plainOperand reports whether e is a literal, a negated one, or a name of
// a local, parameter, const or const parameter: an operand that runs no
// code and reads no place a borrow of an implementer can hold.
func (bl *builder) plainOperand(e ast.Expr) bool {
	switch x := unparen(e).(type) {
	case *ast.BasicLit:
		return true
	case *ast.UnaryExpr:
		_, lit := x.X.(*ast.BasicLit)
		return x.Op == ast.TokMinus && lit
	case *ast.Ident:
		switch bl.in.Use(x.ID()).Kind {
		case UseLocal, UseParam, UseConst, UseConstParam:
			return true
		}
	}
	return false
}

// takePreEval is the local evalLaterInPlace gave e, if it ran e.
func (bl *builder) takePreEval(e ast.Expr) (mir.LocalID, bool) {
	l, ok := bl.preEval[e.ID()]
	if ok {
		delete(bl.preEval, e.ID())
	}
	return l, ok
}

// laterPlaceInert reports whether every later index expression and slice
// bound of the place being built runs no code (inert reads write nothing).
func (bl *builder) laterPlaceInert() bool {
	for _, e := range bl.laterInPlace {
		if !bl.inert(e) {
			return false
		}
	}
	return true
}

// subExpr lowers an index expression or slice bound, which runs to
// completion where it stands: the place around it is not pending inside it.
func (bl *builder) subExpr(f func() mir.Operand) mir.Operand {
	save, saveMut := bl.laterInPlace, bl.placeMut
	bl.laterInPlace, bl.placeMut = nil, false
	op := f()
	bl.laterInPlace, bl.placeMut = save, saveMut
	return op
}

// bound is the operand of a slice bound: the local evalLaterInPlace gave
// it, or the bound lowered where it stands.
func (bl *builder) bound(e ast.Expr) mir.Operand {
	if l, ok := bl.takePreEval(e); ok {
		return mir.Copy(bl.b.Place(l))
	}
	return bl.subExpr(func() mir.Operand { return bl.operand(e) })
}

// toUint brings an integer operand to uint by value-defined casts.
func (bl *builder) toUint(op mir.Operand, at source.Span) mir.Operand {
	t := bl.tab.Get(bl.b.OperandType(op))
	cast := func(o mir.Operand, k mir.CastKind, to types.TypeID) mir.Operand {
		l := bl.newTemp(to, at)
		bl.assign(bl.b.Place(l), mir.Rvalue{Kind: mir.RvCast, Cast: k, A: o, Type: to}, at)
		return mir.Copy(bl.b.Place(l))
	}
	switch {
	case bl.b.OperandType(op) == types.Uint:
		return op
	case !t.Signed:
		return cast(op, mir.CastLossless, types.Uint)
	case t.Kind == types.KIntPtr || t.Bits == 64:
		return cast(op, mir.CastTrunc, types.Uint)
	default:
		return cast(cast(op, mir.CastLossless, types.Int), mir.CastTrunc, types.Uint)
	}
}

// lenUint is RvLen(p) as a uint operand.
func (bl *builder) lenUint(p mir.Place, at source.Span) mir.Operand {
	n := bl.newTemp(types.Int, at)
	bl.assign(bl.b.Place(n), mir.Rvalue{Kind: mir.RvLen, Place: p, Type: types.Int}, at)
	return bl.toUint(mir.Copy(bl.b.Place(n)), at)
}

// checkLess emits a < b (uint) and a switch to the shared panic block.
func (bl *builder) checkCmp(op mir.BinOp, a, b mir.Operand, panic mir.BlockID, at source.Span) {
	k := bl.newTemp(types.Bool, at)
	bl.assign(bl.b.Place(k), mir.Rvalue{Kind: mir.RvBinary, Bin: op, A: a, B: b, Type: types.Bool}, at)
	ok := bl.newBlock()
	bl.boolSwitch(mir.Copy(bl.b.Place(k)), ok, panic, at)
	bl.cur = ok
}

// boundsCheck is i < len(base), panicking with PanicBounds.
func (bl *builder) boundsCheck(base mir.Place, idx mir.LocalID, at source.Span) {
	iu := bl.toUint(mir.Copy(bl.b.Place(idx)), at)
	nu := bl.lenUint(base, at)
	bl.checkCmp(mir.Lt, iu, nu, bl.panicBlock(types.PanicBounds, nil, at), at)
}

// sliceExpr is xs[a..b] and mut xs[a..b]: a <= b <= len, then RvSlice.
func (bl *builder) sliceExpr(x *ast.SliceExpr, kind mir.BorrowKind) mir.Rvalue {
	var base mir.Place
	if bl.isPlaceExpr(x.X) {
		n := len(bl.laterInPlace)
		for _, e := range []ast.Expr{x.Hi, x.Lo} { // reverse order
			if e != nil {
				bl.laterInPlace = append(bl.laterInPlace, e)
			}
		}
		if kind == mir.Exclusive {
			// mut l[0][a..b]: the base is reached for a write.
			base = bl.mutPlace(x.X)
		} else {
			base = bl.place(x.X)
		}
		bl.laterInPlace = bl.laterInPlace[:n]
	} else {
		save := bl.laterInPlace
		bl.laterInPlace = nil // a value runs to completion here
		base = bl.tempOf(x.X)
		bl.laterInPlace = save
		for n := bl.in.Adjust(x.X.ID()).Deref; n > 0; n-- {
			base = bl.deref(base, x.Span())
		}
	}
	rv := mir.Rvalue{Kind: mir.RvSlice, Borrow: kind, Type: bl.typeOf(x)}
	// An omitted a is uint 0 and an omitted b the length. A test
	// against an omitted bound is not emitted: 0 <= u holds for every uint,
	// and len <= len always, so xs[..] has no check, xs[..b] tests b <= len
	// only, and xs[a..] tests a <= len only.
	var lo mir.Operand
	if x.Lo != nil {
		rv.HasLo = true
		rv.A = bl.bound(x.Lo)
		if x.Hi != nil && bl.readsLate(rv.A) && !bl.inertFor(x.Hi, rv.A.Place) {
			// xs[i..Next(mut i)]: a reads i before b runs, and the
			// bounds check and the RvSlice both use that one value.
			rv.A = bl.slot(rv.A, x.Lo.Span())
		}
		lo = bl.toUint(rv.A, x.Lo.Span())
	}
	if x.Hi != nil {
		rv.HasHi = true
		rv.B = bl.bound(x.Hi)
	}
	if rk := bl.tab.Kind(rv.Type); bl.tab.Kind(bl.b.PlaceType(base)) == types.KNamed && (rk == types.KSlice || rk == types.KMutSlice) {
		// A Slice implementer: the place is Items() or ItemsMut() of it, as
		// for an index. That borrow is formed after the bounds run, so
		// `mut self.slots[self.Pick()..]` reads self before the loan of
		// self.slots exists. A bound that is a plain read
		// of a place is read into a temporary first, as an index is, or
		// `mut b[0..b.n]` would read b.n under the loan of b. The bounds
		// check below then reads the view's Len.
		if rv.HasLo && bl.readsLate(rv.A) {
			late := bl.readsLate(lo) // a uint bound: lo is rv.A itself
			rv.A = bl.slot(rv.A, x.Lo.Span())
			if late {
				lo = rv.A
			}
		}
		if rv.HasHi && bl.readsLate(rv.B) {
			rv.B = bl.slot(rv.B, x.Hi.Span())
		}
		base, _ = bl.itemsOf(base, bl.tab.Get(rv.Type).Elem, kind == mir.Exclusive, mir.NoLoop, x.Span())
	}
	rv.Place = base
	if kind == mir.Exclusive {
		if t := bl.tab.Get(rv.Type); t.Kind == types.KSlice {
			rv.Type = bl.tab.MutSlice(t.Elem)
		}
	}
	at := x.Span()
	if x.Lo == nil && x.Hi == nil {
		return rv
	}
	nu := bl.lenUint(base, at)
	panic := bl.panicBlock(types.PanicBounds, nil, at)
	switch {
	case x.Hi == nil:
		bl.checkCmp(mir.Le, lo, nu, panic, at)
	case x.Lo == nil:
		bl.checkCmp(mir.Le, bl.toUint(rv.B, x.Hi.Span()), nu, panic, at)
	default:
		hu := bl.toUint(rv.B, x.Hi.Span())
		bl.checkCmp(mir.Le, lo, hu, panic, at)
		bl.checkCmp(mir.Le, hu, nu, panic, at)
	}
	return rv
}

// counterCtx is an enclosing for i in lo..xs.Len() whose counter may index
// xs with no check: counter is i's binding
// node, xs the Use of the indexed local or parameter.
type counterCtx struct {
	counter ast.NodeID
	xs      Use
}

// counterLoopFor decides the elision's conditions on a range loop's head: hi is the
// builtin Len of a local or parameter of type []T, mut []T or [N]T (never a
// Slice implementer), used directly; lo is a non-negative integer literal
// or of an unsigned type; and xs is not var or the body writes no place
// rooted at its name (assignment, mut borrow, mut receiver call: the
// syntactic scan, O(body)).
func (bl *builder) counterLoopFor(s *ast.ForStmt, h *ast.InHead, src *ast.RangeSource) (counterCtx, bool) {
	call, ok := unparen(src.Hi).(*ast.CallExpr)
	if !ok || len(call.Args) != 0 || bl.in.Call(call.ID()).Builtin != BLen {
		return counterCtx{}, false
	}
	xs, ok := unparen(recvExpr(call)).(*ast.Ident)
	if !ok || bl.in.Adjust(xs.ID()).Deref != 0 {
		return counterCtx{}, false
	}
	u := bl.in.Use(xs.ID())
	var isVar bool
	switch u.Kind {
	case UseLocal:
		isVar = bl.b.Locals[bl.localOf(u.Binding, xs.Span())].Var
	case UseParam:
		isVar = bl.b.Locals[u.Param].Var
	default:
		return counterCtx{}, false
	}
	switch bl.tab.Kind(bl.typeOf(xs)) {
	case types.KSlice, types.KMutSlice, types.KArray:
	default:
		return counterCtx{}, false
	}
	lo := unparen(src.Lo)
	lt := bl.tab.Get(bl.typeOf(lo))
	if !lt.IsInteger() {
		return counterCtx{}, false
	}
	if lt.Signed {
		l := bl.in.Literal(lo.ID())
		if l.Kind != LitScalar || int64(l.Bits) < 0 || l.Bits>>63 != 0 {
			return counterCtx{}, false
		}
		if lt.Kind != types.KIntPtr && lt.Bits < 64 && l.Bits>>(lt.Bits-1) != 0 {
			return counterCtx{}, false
		}
	}
	if isVar && writtenNames(s.Body)[xs.Name] {
		return counterCtx{}, false
	}
	return counterCtx{counter: h.Key.ID(), xs: u}, true
}

// counterIndexed is the counter index elision: xs[i] with i the
// variable of an enclosing for i in lo..xs.Len() (counterLoopFor), used
// directly as the index, and xs that same local or parameter.
func (bl *builder) counterIndexed(xs, i ast.Expr) bool {
	x, ok := unparen(xs).(*ast.Ident)
	if !ok || bl.in.Adjust(x.ID()).Deref != 0 {
		return false
	}
	iv, ok := unparen(i).(*ast.Ident)
	if !ok || bl.in.Adjust(iv.ID()).Deref != 0 || bl.in.Convert(iv.ID()).Kind != ConvNone {
		return false
	}
	iu, xu := bl.in.Use(iv.ID()), bl.in.Use(x.ID())
	if iu.Kind != UseLocal {
		return false
	}
	for k := len(bl.counters) - 1; k >= 0; k-- {
		c := bl.counters[k]
		if c.counter == iu.Binding && c.xs.Kind == xu.Kind && c.xs.Binding == xu.Binding && c.xs.Param == xu.Param {
			return true
		}
	}
	return false
}

// enumOf is the declaration of t when t is an enum type, else nil.
func (bl *builder) enumOf(t types.TypeID) *types.EnumDecl {
	ty := bl.tab.Get(t)
	if ty.Kind != types.KNamed {
		return nil
	}
	d, _ := bl.cfg.Items.Get(ty.Item).Decl.(*types.EnumDecl)
	return d
}

// isCExtern reports whether item is a bodiless extern (a C function), whose
// enum results are checked. std intrinsics are not C calls.
func (bl *builder) isCExtern(item types.ItemID) bool {
	if item == types.NoItem {
		return false
	}
	d, ok := bl.cfg.Items.Get(item).Decl.(*types.FuncDecl)
	return ok && d.Sig.Extern && d.Bodiless && !d.Intrinsic
}

// enumTagCheck is the check of an enum value that came from C: its
// logical discriminant is one of the declared ones, else PanicEnumTag.
func (bl *builder) enumTagCheck(p mir.Place, d *types.EnumDecl, at source.Span) {
	ok := bl.newBlock()
	cases := make([]mir.SwitchCase, 0, len(d.Variants))
	seen := map[uint64]bool{}
	for _, v := range d.Variants {
		if !seen[v.Discr] {
			seen[v.Discr] = true
			cases = append(cases, mir.SwitchCase{Value: v.Discr, Target: ok})
		}
	}
	bl.switchOn(bl.discr(p, at), cases, bl.panicBlock(types.PanicEnumTag, nil, at), at)
	bl.cur = ok
}

// externEntryChecks are the enum tag checks at the entry of a function C
// may call (an extern with a body), one per enum-typed parameter, before
// any user statement.
func (bl *builder) externEntryChecks() {
	if bl.kind != BodyFunc || !bl.sig.Extern {
		return
	}
	for i, p := range bl.sig.Params {
		if d := bl.enumOf(p.Type); d != nil {
			bl.enumTagCheck(bl.b.Place(mir.LocalID(i+1)), d, p.Span)
		}
	}
}
