// Why: a `const` str parameter can be passed on as the format of Print or
// Arena.Fmt; its text is known only per instance, so the format can be
// judged and expanded only there.
//
// What: expandFormats (judge each forwarded format and expand the call),
// expandFormat, formatProblem (E0706 at the call that wrote the literal),
// and the copies of lower's pieces, runners, writerStr and field paths.
//
// How: check accepts the forwarder's body (internal/check/format.go
// forwardedFormat) and lower leaves the call plain. Each instance gets the
// caller's text, judges it as check judges a literal, and expands the call
// exactly as lower expands a literal one (internal/lower/format.go): an
// unpadded Print straight-line, any other call into the runner's call. The
// scan, match and spec encoding are internal/fmtspec's, shared with check
// and lower; mono may not import lower (internal/archtest's DAG), so the
// synthesis is a copy of lower's formatCall.

package mono

import (
	"strings"

	"mk/internal/diag"
	"mk/internal/fmtspec"
	"mk/internal/mir"
	"mk/internal/types"
)

// fmtProblem is a forwarded format that does not match its instance's
// argument type: E0706, reported by the collector at the call that wrote
// the text (formatAnchor).
type fmtProblem struct {
	span    mir.SpanID  // the Print or Fmt call in the instance body
	local   mir.LocalID // the forwarder's const parameter holding the text
	holes   []string    // in text order
	fields  []string    // the argument type's, in field order
	problem string      // check's problem string
}

// fmtField is one field the argument type exposes, in field order.
type fmtField struct {
	name string
	k    uint32 // index in the type
	t    types.TypeID
}

// specFields are fs as fmtspec.Match takes them.
func (bl *builder) specFields(fs []fmtField) []fmtspec.Field[types.TypeID] {
	out := make([]fmtspec.Field[types.TypeID], len(fs))
	for i, f := range fs {
		ty := bl.tab.Get(f.t)
		c := fmtspec.Other
		switch {
		case ty.IsInteger():
			c = fmtspec.Int
		case ty.Kind == types.KFloat:
			c = fmtspec.Float
		}
		out[i] = fmtspec.Field[types.TypeID]{Name: f.name, Index: f.k, Type: f.t, Class: c}
	}
	return out
}

// fmtFields are a closed argument type's visible fields: a record's in its
// canonical order, a struct's exported ones in declaration order. False for
// any other type (check reported E0701 for it).
func (bl *builder) fmtFields(a types.TypeID) ([]fmtField, bool) {
	ty := bl.tab.Get(a)
	in := bl.tab.Interner()
	var fs []fmtField
	switch ty.Kind {
	case types.KRecord:
		for k, f := range ty.Fields {
			fs = append(fs, fmtField{name: in.Str(f.Name), k: uint32(k), t: f.Type})
		}
	case types.KNamed:
		sd, ok := bl.env.Items().Get(ty.Item).Decl.(*types.StructDecl)
		if !ok {
			return nil, false
		}
		for k, f := range sd.Fields {
			if f.Exported {
				fs = append(fs, fmtField{name: in.Str(f.Name), k: uint32(k), t: Subst(bl.tab, f.Type, ty.Args)})
			}
		}
	default:
		return nil, false
	}
	return fs, true
}

// fmtItems are the std items the expansion names.
type fmtItems struct {
	run, piece, arg, value, of, ptrCast types.ItemID
	// padRun and specValue are lower's: the padding runner for a
	// call with a width, FmtSpecValue for an entry a valued spec reads.
	padRun, specValue types.ItemID
	// str is lower's: Writer.Str for a straight-line Print, NoItem for
	// Arena.Fmt and when std's items are not of FmtRun's shape (writerStr).
	str types.ItemID
}

// formatItems finds them for a call of item (Print or Arena.Fmt); false
// when item is neither, or std lacks one (a fake prelude: inert).
func (bl *builder) formatItems(item types.ItemID) (fmtItems, bool) {
	k := bl.opt.Known
	if k == nil || item == types.NoItem {
		return fmtItems{}, false
	}
	var fi fmtItems
	pad := ""
	switch item {
	case k.Print:
		fi.run, pad = k.FmtRun, "FmtPadRun"
	case k.ArenaFmt:
		fi.run, pad = k.FmtArena, "FmtArenaPad"
	default:
		return fmtItems{}, false
	}
	fi.piece, fi.arg, fi.value = k.FmtPiece, k.FmtArg, k.FmtValue
	fi.padRun, fi.specValue = bl.sibling(k.FmtRun, pad), bl.sibling(k.FmtValue, "FmtSpecValue")
	var ok1, ok2 bool
	fi.of, ok1 = bl.env.Items().ByKey(types.ItemKey("std.raw", "Of"))
	fi.ptrCast, ok2 = bl.env.Items().ByKey(types.ItemKey("std.raw", "PtrCast"))
	if fi.run == types.NoItem || fi.piece == types.NoItem || fi.arg == types.NoItem || fi.value == types.NoItem || !ok1 || !ok2 {
		return fmtItems{}, false
	}
	if item == k.Print {
		fi.str = bl.writerStr(k.Print, fi)
	}
	return fi, true
}

// writerStr is lower's (internal/lower/format.go): std's Writer.Str when it
// and every value function take exactly what FmtRun gives them and fail
// with Print's error set; NoItem otherwise.
func (bl *builder) writerStr(print types.ItemID, fi fmtItems) types.ItemID {
	sig := func(id types.ItemID) *types.Signature {
		if id == types.NoItem {
			return nil
		}
		if fd, ok := bl.env.Items().Get(id).Decl.(*types.FuncDecl); ok {
			return &fd.Sig
		}
		return nil
	}
	ps := sig(print)
	if ps == nil || len(ps.Params) == 0 {
		return types.NoItem
	}
	key := bl.env.Items().Get(fi.run).Key.String()
	dot := strings.LastIndexByte(key, '.')
	if dot < 0 {
		return types.NoItem
	}
	str, ok := bl.env.Items().ByKey(types.MemberKey(key[:dot], "Writer", "Str"))
	if !ok {
		return types.NoItem
	}
	wt := ps.Params[0].Type // mut Writer
	fits := func(id types.ItemID, params ...types.TypeID) bool {
		s := sig(id)
		if s == nil || len(s.Params) != len(params) || s.Result != types.Void || s.Errors != ps.Errors || s.Variadic {
			return false
		}
		for i, p := range s.Params {
			if p.Const || p.Type != params[i] {
				return false
			}
		}
		return true
	}
	if !fits(str, wt, types.Str) {
		return types.NoItem
	}
	run := sig(fi.run)
	if run == nil || len(run.Params) != 3 || run.Params[0].Type != wt {
		return types.NoItem
	}
	argDecl, ok := bl.env.Items().Get(fi.arg).Decl.(*types.StructDecl)
	if !ok || len(argDecl.Fields) != 2 {
		return types.NoItem
	}
	ptrT := argDecl.Fields[0].Type
	for _, v := range []types.ItemID{fi.value, fi.specValue} {
		if v != types.NoItem && !fits(v, ptrT, types.U32, wt) {
			return types.NoItem
		}
	}
	return str
}

// sibling is the item name in the module of item, or NoItem (lower's).
func (bl *builder) sibling(item types.ItemID, name string) types.ItemID {
	if item == types.NoItem {
		return types.NoItem
	}
	key := bl.env.Items().Get(item).Key.String()
	dot := strings.LastIndexByte(key, '.')
	if dot < 0 {
		return types.NoItem
	}
	id, ok := bl.env.Items().ByKey(types.ItemKey(key[:dot], name))
	if !ok {
		return types.NoItem
	}
	return id
}

// expandFormats rewrites every Print and Arena.Fmt call left plain by lower
// whose format is now a constant into the runner's call, and
// returns the ones whose text does not match their argument type, which
// stay unexpanded (the collector reports them; no build reaches codegen
// with one). gb's call at the same span names the forwarded parameter.
func (bl *builder) expandFormats() []fmtProblem {
	b := bl.b
	var probs []fmtProblem
	n := len(b.Blocks)
	for bi := 0; bi < n; bi++ {
		t := b.Blocks[bi].Term
		if !isCall(&t) || t.Callee.Kind != mir.CallDirect || t.Args.Len != 3 {
			continue
		}
		fi, ok := bl.formatItems(t.Callee.Fn.Item)
		if ok {
			noTail(&t, "Print or Arena.Fmt")
		}
		if !ok || t.Callee.Fn.Args.Len != 1 {
			continue
		}
		text, ok := strBytes(bl.env.Consts(), b.Args[t.Args.Start+1].Operand)
		if !ok {
			continue
		}
		a := b.GenericArgs[t.Callee.Fn.Args.Start].Type
		fs, ok := bl.fmtFields(a)
		if !ok {
			// check refuses a record that is not a record or a struct at
			// the call that wrote the text; never left unexpanded.
			probs = append(probs, fmtProblem{span: t.Span, local: bl.formatParam(t.Span), problem: "the argument is not a record or a struct"})
			continue
		}
		hs, pieces, problem := fmtspec.Scan(text)
		var rs []fmtspec.Resolved[types.TypeID]
		if problem == "" {
			rs, problem = fmtspec.Match(hs, bl.specFields(fs), bl.pathFields, bl.isFormat)
		}
		if problem != "" {
			p := fmtProblem{span: t.Span, local: bl.formatParam(t.Span), problem: problem}
			for _, h := range hs {
				p.holes = append(p.holes, h.Name())
			}
			for _, f := range fs {
				p.fields = append(p.fields, f.name)
			}
			probs = append(probs, p)
			continue
		}
		bl.expandFormat(mir.BlockID(bi), t, fi, a, fs, hs, pieces, rs)
	}
	return probs
}

// formatParam is the local of the const parameter the generic body passes
// as the format of its call at span; NoLocal when it passes something else.
func (bl *builder) formatParam(span mir.SpanID) mir.LocalID {
	for _, blk := range bl.gb.Blocks {
		t := blk.Term
		if isCall(&t) && t.Span == span && t.Args.Len == 3 {
			o := bl.gb.Args[t.Args.Start+1].Operand
			if (o.Kind == mir.OpCopy || o.Kind == mir.OpMove) && o.Place.Proj.Len == 0 {
				return o.Place.Local
			}
		}
	}
	return mir.NoLocal
}

// fmtEmit appends statements and call blocks after a block of the body.
type fmtEmit struct {
	b      *mir.Body
	cur    mir.BlockID
	span   mir.SpanID
	origin mir.BlockOrigin
	def    mir.DeferID
}

func (e *fmtEmit) temp(t types.TypeID) mir.LocalID {
	return e.b.AddLocal(mir.Local{Type: t, Kind: mir.LocalTemp, Span: e.span})
}

func (e *fmtEmit) assign(p mir.Place, rv mir.Rvalue) {
	blk := &e.b.Blocks[e.cur]
	blk.Stmts = append(blk.Stmts, mir.Statement{Kind: mir.StAssign, Place: p, Rvalue: rv, Span: e.span})
}

// call ends the current block with a call of item (no error edge) into a
// fresh temporary of type ret, and continues in a new block of the same
// origin and defer copy.
func (e *fmtEmit) call(item types.ItemID, gargs []types.GenericArg, ops []mir.Operand, ret types.TypeID) mir.Place {
	dest := e.temp(ret)
	as := make([]mir.Arg, len(ops))
	for i, o := range ops {
		as[i] = mir.Arg{Operand: o, Mode: mir.ArgCopy}
	}
	next := e.b.AddBlock(e.origin, e.def)
	e.b.Blocks[e.cur].Term = mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect,
		Fn: mir.FnRef{Item: item, Args: e.b.AddGenericArgs(gargs...)}},
		Args: e.b.AddArgs(as...), Dest: e.b.Place(dest), Target: next, Spawn: mir.NoLocal, Span: e.span}
	e.cur = next
	return e.b.Place(dest)
}

// stackSlice is elems in a stack [n]elem array and a shared slice of it.
func (e *fmtEmit) stackSlice(bl *builder, elem, slice types.TypeID, elems []mir.Operand) mir.Operand {
	at := bl.tab.Array(types.ConstArg{Kind: types.CAValue, Value: intConst(bl.env.Consts(), int64(len(elems)))}, elem)
	arr := e.temp(at)
	e.assign(e.b.Place(arr), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggArray, Type: at, Ops: e.b.AddOperands(elems...)})
	s := e.temp(slice)
	e.assign(e.b.Place(s), mir.Rvalue{Kind: mir.RvSlice, Borrow: mir.Shared, Place: e.b.Place(arr), Type: slice})
	return mir.Copy(e.b.Place(s))
}

// expandFormat is lower's formatCall over the instance: the record in a
// temporary and each entry's address (raw.Of, raw.PtrCast); then, for an
// unpadded Print, lower's straight-line calls with the original call's
// error edge, the last with its destination and target; else one FmtArg
// per entry (the address and FmtValue[T]), the piece table, and the
// original call made a call of the runner with the same destination,
// target and error edge.
func (bl *builder) expandFormat(bi mir.BlockID, t mir.Terminator, fi fmtItems, a types.TypeID, fs []fmtField, hs []fmtspec.Hole, pieces []fmtspec.Piece, rs []fmtspec.Resolved[types.TypeID]) {
	b := bl.b
	// The runner and the value functions as lower picks them.
	runItem := fi.run
	if fmtspec.Padded(hs) {
		if fi.padRun == types.NoItem {
			ice("a padded format call and no padding runner in std")
		}
		runItem = fi.padRun
	}
	run, ok := bl.env.Items().Get(runItem).Decl.(*types.FuncDecl)
	if !ok || len(run.Sig.Params) != 3 {
		ice("the format runner has no 3-parameter signature")
	}
	pieceSlice, argSlice := run.Sig.Params[1].Type, run.Sig.Params[2].Type
	pieceT, argT := bl.tab.Get(pieceSlice).Elem, bl.tab.Get(argSlice).Elem
	argDecl, ok := bl.env.Items().Get(fi.arg).Decl.(*types.StructDecl)
	if !ok || len(argDecl.Fields) != 2 {
		ice("FmtArg is not a struct of two fields")
	}
	ptrT, fnT := argDecl.Fields[0].Type, argDecl.Fields[1].Type
	blk := b.Blocks[bi]
	e := &fmtEmit{b: b, cur: bi, span: t.Span, origin: blk.Origin, def: blk.Defer}
	args := append([]mir.Arg(nil), b.Args[t.Args.Start:t.Args.End()]...)

	// Straight-line as lower makes it: an unpadded Print, not spawned (a
	// spawned call is one task, never several), whose writer is a whole
	// local and whose text writes something.
	writes := false
	for _, p := range pieces {
		if len(p.Lit) > 0 || p.Hole >= 0 {
			writes = true
		}
	}
	w := args[0].Operand
	straight := runItem == fi.run && fi.str != types.NoItem && writes && t.Spawn == mir.NoLocal &&
		(w.Kind == mir.OpCopy || w.Kind == mir.OpMove) && w.Place.Proj.Len == 0

	ta := e.temp(a)
	e.assign(b.Place(ta), mir.Rvalue{Kind: mir.RvUse, A: args[2].Operand})
	// One FmtArg per target (fmtspec.Args): the fields one-name holes use,
	// then the field paths, each a place in the record temporary, through a
	// shared reference where the path steps through one.
	targets, argOf := fmtspec.Args(hs, rs, len(fs))
	specOf := fmtspec.SpecTargets(hs, argOf, len(targets))
	gs := make([]mir.Operand, len(targets))
	qs := make([]mir.Place, len(targets))
	values := make([]types.ItemID, len(targets))
	for j, tg := range targets {
		value := fi.value
		if specOf[j] && fi.specValue != types.NoItem {
			value = fi.specValue
		}
		var proj []mir.Projection
		for _, s := range tg.Path {
			if s.Deref {
				proj = append(proj, mir.Projection{Kind: mir.ProjDeref, Type: bl.tab.Get(projType(a, proj)).Elem})
			}
			proj = append(proj, mir.Projection{Kind: mir.ProjField, A: s.Index, Type: s.Type})
		}
		ft := tg.Field.Type
		ref := bl.tab.Ref(ft)
		r := e.temp(ref)
		e.assign(b.Place(r), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Shared,
			Place: b.Place(ta, proj...), Origin: mir.OriginPlain, Type: ref})
		p := e.call(fi.of, []types.GenericArg{types.TypeArg(ft)}, []mir.Operand{mir.Copy(b.Place(r))}, bl.tab.RawPtr(ft))
		q := e.call(fi.ptrCast, []types.GenericArg{types.TypeArg(ft), types.TypeArg(types.U8)}, []mir.Operand{mir.Copy(p)}, ptrT)
		qs[j], values[j] = q, value
		if straight {
			continue
		}
		fv := types.ConstValue{Type: fnT, Align: 8, Bytes: make([]byte, 8),
			Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocFn, Fn: types.FnRef{Item: value, Args: []types.GenericArg{types.TypeArg(ft)}}}}}
		if err := fv.Canonicalize(8); err != nil {
			ice("FmtValue constant: %v", err)
		}
		g := e.temp(argT)
		e.assign(b.Place(g), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: fi.arg, Type: argT,
			Ops: b.AddOperands(mir.Copy(q), mir.Const(fnT, bl.env.Consts().Intern(fv)))})
		gs[j] = mir.Copy(b.Place(g))
	}
	if straight {
		// FmtRun's walk, unrolled as lower's formatLines emits it: each
		// non-empty literal through Writer.Str, each hole through its value
		// function, every call with the original call's error edge and the
		// writer reborrowed; the last call writes its destination and goes
		// to its target.
		wa := mir.Arg{Operand: mir.Copy(w.Place), Mode: mir.ArgReborrow}
		var calls []mir.Terminator
		for _, p := range pieces {
			if len(p.Lit) > 0 {
				tm := t
				tm.Callee = mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: fi.str}}
				tm.Args = b.AddArgs(wa, mir.Arg{Operand: mir.Const(types.Str, bl.strConst(string(p.Lit))), Mode: mir.ArgCopy})
				calls = append(calls, tm)
			}
			if p.Hole >= 0 {
				j := argOf[p.Hole]
				tm := t
				tm.Callee = mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: values[j], Args: b.AddGenericArgs(types.TypeArg(targets[j].Field.Type))}}
				tm.Args = b.AddArgs(mir.Arg{Operand: mir.Copy(qs[j]), Mode: mir.ArgCopy},
					mir.Arg{Operand: mir.Imm(types.U32, uint64(fmtspec.Encode(hs[p.Hole].Spec, rs[p.Hole].Field.Class))), Mode: mir.ArgCopy}, wa)
				calls = append(calls, tm)
			}
		}
		for i, tm := range calls {
			if i < len(calls)-1 {
				tm.Dest = b.Place(e.temp(types.Void))
				tm.Target = b.AddBlock(e.origin, e.def)
			}
			b.Blocks[e.cur].Term = tm
			e.cur = tm.Target
		}
		return
	}
	sa := e.stackSlice(bl, argT, argSlice, gs)

	var pcs []mir.Operand
	piece := func(lit []byte, arg int32, spec uint32) {
		l := e.temp(pieceT)
		e.assign(b.Place(l), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: fi.piece, Type: pieceT,
			Ops: b.AddOperands(mir.Const(types.Str, bl.strConst(string(lit))), mir.Imm(types.I32, uint64(uint32(arg))), mir.Imm(types.U32, uint64(spec)))})
		pcs = append(pcs, mir.Copy(b.Place(l)))
	}
	// The pieces as lower's fmtPieces makes them (fmtspec.Scan).
	for _, p := range pieces {
		if p.Hole < 0 {
			piece(p.Lit, -1, 0)
			continue
		}
		piece(p.Lit, int32(argOf[p.Hole]), fmtspec.Encode(hs[p.Hole].Spec, rs[p.Hole].Field.Class))
	}
	sp := e.stackSlice(bl, pieceT, pieceSlice, pcs)

	t.Callee = mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: runItem}}
	t.Args = b.AddArgs(args[0], mir.Arg{Operand: sp, Mode: mir.ArgCopy}, mir.Arg{Operand: sa, Mode: mir.ArgCopy})
	b.Blocks[e.cur].Term = t
}

// strBytes reads a str constant operand's bytes back.
func strBytes(ct *types.ConstTable, o mir.Operand) ([]byte, bool) {
	if o.Kind != mir.OpConst {
		return nil, false
	}
	v := ct.Get(o.Const)
	if v.Type != types.Str || len(v.Bytes) != 16 {
		return nil, false
	}
	n := types.LoadUint(v.Bytes[8:], 8)
	if n == 0 {
		return []byte{}, true
	}
	if len(v.Relocs) != 1 || v.Relocs[0].Kind != types.RelocData {
		return nil, false
	}
	d := ct.Get(v.Relocs[0].Data)
	if uint64(len(d.Bytes)) != n {
		return nil, false
	}
	return d.Bytes, true
}

// formatProblem reports one forwarded format of node i that does not match:
// E0706 with check's message and facts, at the call that wrote the
// text (formatAnchor).
func (c *collector) formatProblem(i int, p fmtProblem) {
	body, span := c.formatAnchor(i, p)
	d := diag.New(diag.E0706, body.Span(span), "the format string does not match the arguments: "+p.problem).
		Fact("holes", diag.TextList(p.holes...)).Fact("fields", diag.TextList(p.fields...)).Text("problem", p.problem).
		Text("instance", c.nodes[i].inst.Name).
		Alt("name every field of the arguments in a hole, and give each hole a field")
	c.diags = append(c.diags, d)
}

// formatAnchor is where a forwarded format's text was written: walking up
// from node i through the first requester of each instance, while the
// caller's generic body passes its own const parameter on at the call that
// made the instance, to the call that passes a constant. When the chain
// cannot be followed (no requester, an argument of another form) it stops
// at the last call known, or at the Print call itself.
func (c *collector) formatAnchor(i int, p fmtProblem) (*mir.Body, mir.SpanID) {
	body, span := c.nodes[i].inst.Body, p.span
	local := p.local
	for n := i; local != mir.NoLocal; {
		nd := c.nodes[n]
		if nd.parent < 0 || nd.siteBody == nil {
			break
		}
		body, span = nd.siteBody, nd.siteSpan
		par := c.nodes[nd.parent]
		local = c.forwardedParam(par.inst.Item, nd.inst.Item, nd.siteSpan, local)
		n = nd.parent
	}
	return body, span
}

// forwardedParam is the const parameter of caller that caller's generic
// body passes at its call of callee at span as callee's parameter local;
// NoLocal when it passes anything else (a constant: the anchor).
func (c *collector) forwardedParam(caller, callee types.ItemID, span mir.SpanID, local mir.LocalID) mir.LocalID {
	gb := c.env.Body(caller)
	sig := c.sigOf(caller)
	if gb == nil || sig == nil || local == 0 {
		return mir.NoLocal
	}
	for _, blk := range gb.Blocks {
		t := blk.Term
		if !isCall(&t) || t.Span != span || t.Callee.Kind != mir.CallDirect {
			continue
		}
		ai := int(local) - 1 // a parameter's local is its 1-based index
		if ai >= int(t.Args.Len) {
			return mir.NoLocal
		}
		o := gb.Args[int(t.Args.Start)+ai].Operand
		if (o.Kind == mir.OpCopy || o.Kind == mir.OpMove) && o.Place.Proj.Len == 0 {
			l := o.Place.Local
			if int(l) >= 1 && int(l) <= len(sig.Params) && sig.Params[l-1].Const {
				return l
			}
		}
		return mir.NoLocal
	}
	return mir.NoLocal
}

// projType is the type of the record temporary of type a after proj.
func projType(a types.TypeID, proj []mir.Projection) types.TypeID {
	if len(proj) == 0 {
		return a
	}
	return proj[len(proj)-1].Type
}

// pathFields are the fields a hole's path may name inside a value of type t
// (as check's pathFields): a record's or struct's, through one shared
// reference.
func (bl *builder) pathFields(t types.TypeID) ([]fmtspec.Field[types.TypeID], bool, bool) {
	deref := false
	if ty := bl.tab.Get(t); ty.Kind == types.KRef {
		t, deref = ty.Elem, true
	}
	fs, ok := bl.fmtFields(t)
	if !ok {
		return nil, false, false
	}
	return bl.specFields(fs), deref, true
}

// isFormat reports whether the concrete t implements Format (check's impl
// table, as a method call on t resolves): each hole's value is formatted
// through FmtValue[t]. With no Format known (a stand-in
// std) nothing can be judged and every value passes; check judged it.
func (bl *builder) isFormat(t types.TypeID) bool {
	k := bl.opt.Known
	if k == nil || k.Format == types.NoItem {
		return true
	}
	if bl.tab.Kind(t) == types.KErrorSet {
		// A set formats when each member does (errfmt.go); a set of one
		// error is that error's type.
		if ms := bl.tab.Members(bl.tab.Get(t).Errors); len(ms) > 1 {
			for _, m := range ms {
				if m.IsParam || !bl.isFormat(bl.tab.ErrorSetType(bl.tab.ErrorSet([]types.ErrorMember{m}))) {
					return false
				}
			}
			return true
		}
	}
	_, _, ok := bl.env.ImplMethod(k.Format, 0, t)
	return ok
}
