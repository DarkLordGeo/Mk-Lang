// Why: Format on an error-set value must print the actual member's text,
// but the error set itself has no single Format body.
//
// What: the rewrite of an interface call of Format whose Self is an error
// set.
//
// How: the call becomes a switch on the error's code; each member's block
// calls that member's provided Format.

package mono

import (
	"mk/internal/mir"
	"mk/internal/types"
)

// isErrSetFormat reports whether call t is Format on an error set of more
// than one member (a set of one error is that error's type, with an impl).
func (bl *builder) isErrSetFormat(t *mir.Terminator) bool {
	k := bl.opt.Known
	c := &t.Callee
	if k == nil || k.Format == types.NoItem || c.Kind != mir.CallInterface || c.Iface != k.Format || bl.tab.Kind(c.SelfType) != types.KErrorSet {
		return false
	}
	ms := bl.tab.Members(bl.tab.Get(c.SelfType).Errors)
	return len(ms) > 1 || len(ms) == 1 && ms[0].IsParam
}

// errSetFormats rewrites the error-set Format calls ifaceCall left in
// bl.errSets; reports whether any was.
func (bl *builder) errSetFormats() bool {
	if len(bl.errSets) == 0 {
		return false
	}
	for _, bi := range bl.errSets {
		bl.errSetFormat(bi)
	}
	bl.errSets = nil
	return true
}

// errSetFormat is the rewrite of block bi's call, Format(recv: &S, w):
//
//	r0 = copy recv; d = discriminant(*r0)
//	switch d [code(Mj) -> bj] otherwise -> unreachable
//	bj: vj = Mj((*r0 as Mj).f..); rj = &vj
//	    dest = call Mj's Format(copy rj, w) -> next, the original error edge
//
// The member's value is a copy of its payload (error payloads are plain):
// its Format takes &Mj, and a downcast place keeps the set's type. Lower's
// errSetFormat emits the same shape at a concrete site.
func (bl *builder) errSetFormat(bi mir.BlockID) {
	b := bl.b
	t := b.Blocks[bi].Term
	set := t.Callee.SelfType
	ms := bl.tab.Members(bl.tab.Get(set).Errors)
	args := append([]mir.Arg(nil), b.Args[t.Args.Start:t.Args.End()]...)
	if len(args) != 2 {
		ice("Format on an error set with %d arguments", len(args))
	}
	span, origin, def := t.Span, b.Blocks[bi].Origin, b.Blocks[bi].Defer
	temp := func(ty types.TypeID) mir.LocalID {
		return b.AddLocal(mir.Local{Type: ty, Kind: mir.LocalTemp, Span: span})
	}
	assign := func(blk mir.BlockID, p mir.Place, rv mir.Rvalue) {
		b.Blocks[blk].Stmts = append(b.Blocks[blk].Stmts, mir.Statement{Kind: mir.StAssign, Place: p, Rvalue: rv, Span: span})
	}
	rt := b.OperandType(args[0].Operand)
	r0 := temp(rt)
	assign(bi, b.Place(r0), mir.Rvalue{Kind: mir.RvUse, A: args[0].Operand, Type: rt})
	val := b.Place(r0, mir.Projection{Kind: mir.ProjDeref, Type: set})
	d := temp(types.U64)
	assign(bi, b.Place(d), mir.Rvalue{Kind: mir.RvDiscriminant, Place: val, Type: types.U64})
	un := b.AddBlock(origin, def)
	b.Blocks[un].Term = mir.Terminator{Kind: mir.TermUnreachable, Span: span}
	cases := make([]mir.SwitchCase, 0, len(ms))
	for _, mem := range ms {
		if mem.IsParam {
			ice("Format of an error set with a parameter member in an instance")
		}
		e := bl.env.Items().Get(mem.Item)
		ed, _ := e.Decl.(*types.ErrorDecl)
		code := types.ErrorCode(e.Key)
		if ed != nil && ed.Code != 0 {
			code = ed.Code
		}
		mt := bl.tab.ErrorSetType(bl.tab.ErrorSet([]types.ErrorMember{{Item: mem.Item}}))
		fn, fargs, ok := bl.env.ImplMethod(bl.opt.Known.Format, 0, mt)
		if !ok {
			ice("no Format impl for the error %s of a set", e.Key)
		}
		bj := b.AddBlock(origin, def)
		cases = append(cases, mir.SwitchCase{Value: uint64(code), Target: bj})
		dc := mir.Projection{Kind: mir.ProjDowncast, Sel: mir.SelCase, A: uint32(mem.Item), Type: set}
		var ops []mir.Operand
		if ed != nil {
			for k, f := range ed.Fields {
				ops = append(ops, mir.Copy(b.Place(r0, mir.Projection{Kind: mir.ProjDeref, Type: set}, dc, mir.Projection{Kind: mir.ProjField, A: uint32(k), Type: f.Type})))
			}
		}
		v := temp(mt)
		assign(bj, b.Place(v), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggError, Item: mem.Item, Type: mt, Ops: b.AddOperands(ops...)})
		ref := bl.tab.Ref(mt)
		rj := temp(ref)
		assign(bj, b.Place(rj), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Shared, Place: b.Place(v), Type: ref})
		call := t
		call.Callee = mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: fn, Args: b.AddGenericArgs(fargs...)}}
		call.Args = b.AddArgs(mir.Arg{Operand: mir.Copy(b.Place(rj)), Mode: mir.ArgCopy}, args[1])
		b.Blocks[bj].Term = call
	}
	b.Blocks[bi].Term = mir.Terminator{Kind: mir.TermSwitch, On: mir.Copy(b.Place(d)), Cases: b.AddCases(cases...), Default: un, Span: span}
}
