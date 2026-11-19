// Why: an error type's provided Format method has no source body, and an
// error-set value must format as whichever member it holds; both are built
// here as MIR.
//
// What: errorFormatBody (the body of an error's provided Format),
// errSetFormat (the switch on an error set's code that calls each member's
// Format), memberFormat, tryCall and isErrSetFormat.
//
// How: the body writes each literal piece with `try w.Str(piece)` and each
// field with its type's Format, then returns ok. The error-set switch has the
// same shape as mono's errSetFormat, which handles sets with a parameter
// member per instance.

package lower

import (
	"mk/internal/check"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// errorFormatBody builds the provided Format: parameters 1 and 2 are self: &E and
// w: mut Writer; each piece is `try w.Str(piece)`, each field between two
// pieces `try (*self).f.Format(w)` through the field type's impl method (or
// the member switch of an error-set field), then `return ok`.
func (bl *builder) errorFormatBody(plan *check.ErrorFormat) {
	if plan == nil {
		source.ICEf(bl.in.Root().Span(), "lower: a provided Format body with no plan")
	}
	at := bl.cfg.Items.Get(plan.Error).Span
	self, w := mir.LocalID(1), mir.LocalID(2)
	for i, p := range plan.Pieces {
		if p != "" {
			bl.pushFrame(at)
			args := []mir.Arg{{Operand: mir.Copy(bl.b.Place(w)), Mode: mir.ArgReborrow}, {Operand: bl.strConst([]byte(p), at), Mode: mir.ArgCopy}}
			bl.tryCall(mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: plan.Str}}, args, plan.Errors, at)
			bl.popFrame()
		}
		if i >= len(plan.Fields) || !bl.open() {
			continue
		}
		f := plan.Fields[i]
		bl.pushFrame(at)
		sv := bl.deref(bl.b.Place(self), at)
		fp := bl.field(bl.downcast(sv, mir.SelCase, uint32(plan.Error)), f.Index, f.Type)
		rt := bl.tab.Ref(f.Type)
		fr := bl.newTemp(rt, at)
		bl.assign(bl.b.Place(fr), bl.ref(mir.Shared, fp, rt), at)
		wa := mir.Arg{Operand: mir.Copy(bl.b.Place(w)), Mode: mir.ArgReborrow}
		if f.Method == types.NoItem {
			e := bl.newTemp(bl.tab.ErrorSetType(plan.Errors), at)
			dest := bl.newTemp(types.Void, at)
			tb := bl.newBlock()
			bl.errSetFormat(mir.Copy(bl.b.Place(fr)), f.Type, wa, bl.b.Place(dest), &errEdge{kind: mir.EdgeTry, block: tb, err: e}, at)
			succ := bl.cur
			bl.cur = tb
			bl.errorExit(bl.valueOfTemp(e, at), plan.Errors, at, mir.ExitTry, false)
			bl.cur = succ
		} else {
			args := []mir.Arg{{Operand: mir.Copy(bl.b.Place(fr)), Mode: mir.ArgCopy}, wa}
			bl.tryCall(mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: f.Method, Args: bl.b.AddGenericArgs(f.Args...)}}, args, plan.Errors, at)
		}
		bl.popFrame()
	}
	if bl.open() {
		bl.successExit(nil, at, mir.ExitNormal)
	}
}

// tryCall emits `try callee(args)` of a void result: the error edge returns
// the error with exit kind try (as tryExpr), and lowering continues after
// the call. errs is the callee's set.
func (bl *builder) tryCall(callee mir.Callee, args []mir.Arg, errs types.ErrorSetID, at source.Span) {
	e := bl.newTemp(bl.tab.ErrorSetType(errs), at)
	dest := bl.newTemp(types.Void, at)
	tb, target := bl.newBlock(), bl.newBlock()
	bl.term(mir.Terminator{Kind: mir.TermCall, Callee: callee, Args: bl.b.AddArgs(args...), Dest: bl.b.Place(dest), Target: target, Spawn: mir.NoLocal,
		HasErr: true, Err: mir.ErrorEdge{Kind: mir.EdgeTry, Block: tb, Error: e}}, at)
	bl.cur = tb
	bl.errorExit(bl.valueOfTemp(e, at), errs, at, mir.ExitTry, false)
	bl.cur = target
}

// errSetFormat is `(*recv).Format(w)` for recv: &S, S an error set: a
// switch on the error's code, and in each member's block a call of that
// member's provided Format into dest with edge (nil for none), then a join,
// where lowering continues. The shape is mono's errSetFormat.
func (bl *builder) errSetFormat(recv mir.Operand, set types.TypeID, w mir.Arg, dest mir.Place, edge *errEdge, at source.Span) {
	ms := bl.tab.Members(bl.tab.Get(set).Errors)
	r := bl.newTemp(bl.b.OperandType(recv), at)
	bl.assign(bl.b.Place(r), use(recv), at)
	val := bl.deref(bl.b.Place(r), at)
	d := bl.discr(val, at)
	join := bl.newBlock()
	cases := make([]mir.SwitchCase, 0, len(ms))
	blocks := make([]mir.BlockID, len(ms))
	for j, mem := range ms {
		if mem.IsParam {
			source.ICEf(at, "lower: Format of an error set with a parameter member")
		}
		blocks[j] = bl.newBlock()
		cases = append(cases, mir.SwitchCase{Value: uint64(bl.errorCode(mem.Item, at)), Target: blocks[j]})
	}
	un := bl.newBlock()
	bl.switchOn(d, cases, un, at)
	bl.cur = un
	bl.term(mir.Terminator{Kind: mir.TermUnreachable}, at)
	for j, mem := range ms {
		bl.cur = blocks[j]
		meth := bl.memberFormat(mem.Item, at)
		// The member's own value, a copy of the payload (error payloads are
		// plain): Mj's Format takes &Mj, and a downcast place keeps the
		// set's type.
		mt := errorType(bl.tab, mem.Item)
		dc := bl.downcast(val, mir.SelCase, uint32(mem.Item))
		var ops []mir.Operand
		if ed, ok := bl.cfg.Items.Get(mem.Item).Decl.(*types.ErrorDecl); ok {
			for k, f := range ed.Fields {
				ops = append(ops, mir.Copy(bl.field(dc, uint32(k), f.Type)))
			}
		}
		v := bl.newTemp(mt, at)
		bl.assign(bl.b.Place(v), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggError, Item: mem.Item, Type: mt, Ops: bl.b.AddOperands(ops...)}, at)
		rt := bl.tab.Ref(mt)
		rj := bl.newTemp(rt, at)
		bl.assign(bl.b.Place(rj), bl.ref(mir.Shared, bl.b.Place(v), rt), at)
		tm := mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: meth}},
			Args: bl.b.AddArgs(mir.Arg{Operand: mir.Copy(bl.b.Place(rj)), Mode: mir.ArgCopy}, w), Dest: dest, Target: join, Spawn: mir.NoLocal}
		if edge != nil {
			tm.HasErr, tm.Err = true, mir.ErrorEdge{Kind: edge.kind, Block: edge.block, Error: edge.err}
		}
		bl.term(tm, at)
	}
	bl.cur = join
}

// memberFormat is an error's Format impl method: its provided one, found
// through Config.Impls (check's ImplOf), as sliceMethod finds Items.
func (bl *builder) memberFormat(e types.ItemID, at source.Span) types.ItemID {
	k := bl.cfg.Known
	if bl.cfg.Impls == nil || k == nil || k.Format == types.NoItem {
		source.ICEf(at, "lower: Format of an error set with no impl lookup")
	}
	impl := bl.cfg.Impls(k.Format, nil, errorType(bl.tab, e))
	if impl == types.NoItem {
		source.ICEf(at, "lower: the error %s of a set has no Format (check gap)", bl.cfg.Items.Get(e).Key)
	}
	m, ok := bl.cfg.Items.ByKey(types.ImplMemberKey(bl.cfg.Items.Get(impl).Key, "Format"))
	if !ok {
		source.ICEf(at, "lower: the Format impl %s has no method", bl.cfg.Items.Get(impl).Key)
	}
	return m
}

// errorType is the type of error e's values: the set of e alone.
func errorType(tab *types.Table, e types.ItemID) types.TypeID {
	return tab.ErrorSetType(tab.ErrorSet([]types.ErrorMember{{Item: e}}))
}

// isErrSetFormat reports whether c is Format on an error set of more than
// one member, none of them a parameter; a set of one error calls its impl
// directly, and a set with a parameter member stays an interface call that
// mono rewrites per instance.
func (bl *builder) isErrSetFormat(c Call) bool {
	k := bl.cfg.Known
	if c.Kind != CallIface || k == nil || k.Format == types.NoItem || c.Iface != k.Format || bl.tab.Kind(c.SelfType) != types.KErrorSet {
		return false
	}
	ms := bl.tab.Members(bl.tab.Get(c.SelfType).Errors)
	for _, m := range ms {
		if m.IsParam {
			return false
		}
	}
	return len(ms) > 1
}
