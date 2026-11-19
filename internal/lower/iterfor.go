// Why: `for x in it` over a user iterator, and a loop variable that
// destructures its element, are their own loop shapes.
//
// What: forIter (a for over an iterator), ifaceCall (the call of Next on
// it) and loopPattern (a destructured loop variable).
//
// How: the shape is forBind's: a header that calls Next and switches on its
// discriminant. The iterator is evaluated once into a hidden var that lives
// for the whole loop.

package lower

import (
	"mk/internal/ast"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// forIter is `for p in e` over an iterator: e evaluated once into a hidden
// var that lives for the whole loop, Next called on it before
// each iteration, the payload bound or destructured until none.
func (bl *builder) forIter(s *ast.ForStmt, h *ast.InHead, src *ast.RefSource, c Call) {
	hs := headerSpan(s)
	at := src.X.Span()
	adj := bl.in.Adjust(src.ID())
	bl.pushFrame(hs) // the source's own temporaries
	var p mir.Place
	if bl.isPlaceExpr(src.X) {
		p = bl.place(src.X)
	} else {
		p = bl.tempOf(src.X)
	}
	for n := adj.Deref; n > 0; n-- {
		p = bl.deref(p, at)
	}
	itT := bl.b.PlaceType(p)
	var it mir.LocalID
	if p.Proj.Len == 0 && bl.b.Locals[p.Local].Kind == mir.LocalTemp && !bl.isPlaceExpr(src.X) {
		it = p.Local // the source's own temporary is the hidden var
	} else {
		it = bl.newTemp(itT, at)
		bl.assign(bl.b.Place(it), use(bl.valueOf(p, true, at)), at)
	}
	bl.b.Locals[it].Var = true                                       // the mut it borrow of each Next call
	bl.endOnceTemps(hs, func(l mir.LocalID) bool { return l == it }) // only it lives on

	header, exit := bl.newBlock(), bl.newBlock()
	bl.gotoBlock(header, hs)
	bl.beginLoop(s, header, header, exit)
	bl.cur = header
	bl.pushFrame(hs)
	kind, rt := mir.Exclusive, bl.tab.Mut(itT)
	if adj.Kind == AdjRefShared {
		kind, rt = mir.Shared, bl.tab.Ref(itT)
	}
	r := bl.newTemp(rt, hs)
	bl.assign(bl.b.Place(r), mir.Rvalue{Kind: mir.RvRef, Borrow: kind, Place: bl.b.Place(it), Origin: mir.OriginPlain, Type: rt}, hs)
	resT := bl.in.Type(src.ID())
	var dest mir.Place
	switch c.Kind {
	case CallFunc:
		dest = bl.directCall(c.Item, c.Args, []mir.Operand{bl.valueOfTemp(r, hs)}, resT, hs)
	case CallIface:
		dest = bl.ifaceCall(c, []mir.Operand{bl.valueOfTemp(r, hs)}, resT, hs)
	default:
		source.ICEf(hs, "lower: for over an iterator with call kind %v", c.Kind)
	}
	d := bl.discr(dest, hs)
	hdr := bl.temps[len(bl.temps)-1].temps
	bl.loops[len(bl.loops)-1].hdr = hdr
	some, none := bl.newBlock(), bl.newBlock()
	bl.switchOn(d, []mir.SwitchCase{{Value: 1, Target: some}}, none, hs)
	bl.cur = none
	bl.endTemps(hdr, hs)
	bl.gotoBlock(exit, hs)
	bl.cur = some
	bl.openScope(scopeLoopBody)
	payload := bl.somePayload(dest)
	switch {
	case h.Pat != nil:
		bl.loopPattern(h.Pat, payload, true, h.Pat.Span())
	case !blank(h.Key):
		bl.bindTo(h.Key, payload, ByValue, h.Key.Span())
	}
	bl.blockStmts(s.Body)
	bl.closeScope(closeBrace(s.Body))
	if bl.open() {
		bl.endTemps(hdr, closeBrace(s.Body))
		bl.gotoBlock(header, closeBrace(s.Body))
	}
	bl.dropFrame()
	bl.endLoop(true)
}

// ifaceCall emits an interface method call with operands ops (mono makes
// it direct per instance) and returns its destination.
func (bl *builder) ifaceCall(c Call, ops []mir.Operand, ret types.TypeID, at source.Span) mir.Place {
	dest := bl.newTemp(ret, at)
	as := make([]mir.Arg, len(ops))
	for i, o := range ops {
		as[i] = mir.Arg{Operand: o, Mode: bl.argMode(o, at)}
	}
	target := bl.newBlock()
	callee := mir.Callee{Kind: mir.CallInterface, Iface: c.Iface, Method: c.Method, SelfType: c.SelfType}
	bl.term(mir.Terminator{Kind: mir.TermCall, Callee: callee, Args: bl.b.AddArgs(as...), Dest: bl.b.Place(dest), Target: target, Spawn: mir.NoLocal}, at)
	bl.cur = target
	return bl.b.Place(dest)
}

// loopPattern destructures one element into the loop body's scope: the
// element is moved (take) or copied into a hidden local of the
// scope, which ends with the iteration, and the pattern binds from it as a
// destructure's does.
func (bl *builder) loopPattern(rp *ast.RecordPat, elem mir.Place, take bool, at source.Span) {
	t := bl.b.PlaceType(elem)
	l := bl.hiddenLocal(t, mir.LocalTemp, at)
	sc := &bl.scopes[len(bl.scopes)-1]
	sc.locals = append(sc.locals, l)
	var v mir.Operand
	if take {
		v = bl.valueOf(elem, true, at)
	} else {
		v = mir.Copy(elem)
	}
	bl.assign(bl.b.Place(l), use(v), at)
	dt := &destructTake{}
	bl.destructureInto(rp, bl.b.Place(l), false, dt)
	if dt.took || bl.plain(t, at) || !dt.has {
		return
	}
	h := bl.newTemp(bl.b.PlaceType(dt.leaf), dt.at)
	bl.assign(bl.b.Place(h), use(mir.Move(dt.leaf)), dt.at)
}
