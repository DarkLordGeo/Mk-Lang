// Why: Print and Arena.Fmt take a format string and an argument record; a
// generic runtime formatter per call would be slow and large, so the thin
// body of each call is synthesized here, after check has accepted its
// format string.
//
// What: formatter and formatCall, which rewrite a Print(w, f, args) or
// a.Fmt(f, args) call into straight-line writes or a call of the format
// runner (formatLines, writerStr, wholeLocal, fmtWrites); fmtFields and
// fmtPieces, which scan the format string against the argument's fields;
// strConst, dataConst and DataConst for byte constants.
//
// How: a call Print(w, f, args) that has no width becomes FmtRun's walk
// unrolled, straight-line:
//
//	ta = args                                  (the plain argument record)
//	r_j = &ta.k_j; p_j = call raw.Of[T_j](r_j); q_j = call raw.PtrCast[T_j, u8](p_j)
//	                                            for each entry j (fmtspec.Args)
//	then for each piece i, in text order, each call with the Print call's
//	error edge (the last one writes its destination):
//	call Writer.Str(w, lit_i)                   when lit_i is not empty
//	call V_j[T_j](q_j, spec_i, w)               when piece i has a hole, on entry j
//
// V_j is FmtValue, or FmtSpecValue for an entry a valued spec reads, the
// function FmtRun would call through entry j's FmtArg; w is
// reborrowed by each call. So the bytes, the Format calls with their
// arguments and their order, and the error returned are FmtRun's: the
// arguments are evaluated once, into ta, before anything is written; a
// failing call's error leaves the statement and nothing after it runs; an
// empty literal is no call, as Writer.Bytes of 0 bytes does nothing. A
// call with a width, a.Fmt(f, args), a text that writes nothing, and a
// writer that is not a whole local (or a std without Writer.Str of
// FmtRun's shape) keep the runner:
//
//	g_j = FmtArg{p: q_j, f: const fn V_j[T_j]}  for each entry j
//	aa = [g_0, ...]; sa = aa[..]                (a stack [k]FmtArg)
//	pc_i = FmtPiece{lit, arg, spec}; pa = [pc_0, ...]; sp = pa[..]
//	dest = call FmtRun(w, sp, sa)               (with the Print call's error edge)
//
// with FmtPadRun for a width, and FmtArena (FmtArenaPad) for a.Fmt, which
// also builds the Text. The piece table is a stack array built per call,
// not a private const [n]FmtPiece, because lower has no layout query to
// encode a struct constant (only str's layout is fixed); the bytes the
// runner reads are the same. internal/mono/format.go expands a forwarded
// format the same way.

package lower

import (
	"strings"

	"mk/internal/ast"
	"mk/internal/fmtspec"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// fmtItems are the std items a formatting call is synthesized with.
type fmtItems struct {
	run, piece, arg, value, of, ptrCast types.ItemID
	// padRun is the padding runner (FmtPadRun or FmtArenaPad) for a call
	// with a width, specValue FmtSpecValue for an entry a valued spec reads
	// (fmtspec.Padded, SpecTargets); NoItem when std lacks them.
	padRun, specValue types.ItemID
	// str is Writer.Str, which writes a Print call's literals when the call
	// is lowered straight-line (formatLines); NoItem for Arena.Fmt, and when
	// std lacks it or it or a value function is not of the shape FmtRun
	// calls (the call then keeps the runner).
	str types.ItemID
}

// formatter reports whether c is a call of Print or Arena.Fmt and finds the
// items its synthesis needs. False when c is neither, or when std does not
// declare them all (a fake prelude in tests): the call stays a plain call
// then, as KnownItems says a rule whose items are absent is inert.
func (bl *builder) formatter(c Call) (fmtItems, bool) {
	k := bl.cfg.Known
	if k == nil || c.Kind != CallFunc || c.Item == types.NoItem {
		return fmtItems{}, false
	}
	var fi fmtItems
	pad := ""
	switch c.Item {
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
	fi.of, ok1 = bl.cfg.Items.ByKey(types.ItemKey("std.raw", "Of"))
	fi.ptrCast, ok2 = bl.cfg.Items.ByKey(types.ItemKey("std.raw", "PtrCast"))
	if fi.run == types.NoItem || fi.piece == types.NoItem || fi.arg == types.NoItem || fi.value == types.NoItem || !ok1 || !ok2 {
		return fmtItems{}, false
	}
	if c.Item == k.Print {
		fi.str = bl.writerStr(k.Print, fi)
	}
	return fi, true
}

// writerStr is std's Writer.Str, found by key in the module of the runner
// items, when it and every value function a hole may call take exactly
// what FmtRun gives them (Str: the mut Writer and a str; FmtValue and
// FmtSpecValue: the *u8, the u32 spec and the mut Writer) and fail with
// Print's own error set, so each straight-line call can take the Print
// call's error edge. NoItem otherwise.
func (bl *builder) writerStr(print types.ItemID, fi fmtItems) types.ItemID {
	ps := bl.cfg.callee(print)
	if ps == nil || len(ps.Params) == 0 {
		return types.NoItem
	}
	key := bl.cfg.Items.Get(fi.run).Key.String()
	dot := strings.LastIndexByte(key, '.')
	if dot < 0 {
		return types.NoItem
	}
	str, ok := bl.cfg.Items.ByKey(types.MemberKey(key[:dot], "Writer", "Str"))
	if !ok {
		return types.NoItem
	}
	wt := ps.Params[0].Type // mut Writer
	fits := func(id types.ItemID, params ...types.TypeID) bool {
		s := bl.cfg.callee(id)
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
	run := bl.cfg.callee(fi.run)
	if run == nil || len(run.Params) != 3 || run.Params[0].Type != wt {
		return types.NoItem
	}
	argDecl, ok := bl.cfg.Items.Get(fi.arg).Decl.(*types.StructDecl)
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

// sibling is the item name in the module of item (the prelude's runner
// items; the padding runners are found by key, not through KnownItems), or
// NoItem.
func (bl *builder) sibling(item types.ItemID, name string) types.ItemID {
	if item == types.NoItem {
		return types.NoItem
	}
	key := bl.cfg.Items.Get(item).Key.String()
	dot := strings.LastIndexByte(key, '.')
	if dot < 0 {
		return types.NoItem
	}
	id, ok := bl.cfg.Items.ByKey(types.ItemKey(key[:dot], name))
	if !ok {
		return types.NoItem
	}
	return id
}

// formatKnown reports whether a format call's operands hold its text: false
// only for a forwarded `const` str parameter, which mono expands.
func (bl *builder) formatKnown(ps []pendingArg) bool {
	if len(ps) != 3 {
		return true // formatCall's own ICE names the shape
	}
	_, ok := bl.strBytes(ps[1].op)
	return ok
}

// fmtField is one field the argument type exposes, in field order.
type fmtField struct {
	name string
	k    uint32 // the field's index in the type
	t    types.TypeID
}

// fmtFields are the fields of the argument type a format string names: a
// record's fields in its canonical order, a struct's exported fields in
// declaration order (check's formatFields).
func (bl *builder) fmtFields(a types.TypeID, at source.Span) []fmtField {
	ty := bl.tab.Get(a)
	var fs []fmtField
	switch ty.Kind {
	case types.KRecord:
		for k, f := range ty.Fields {
			fs = append(fs, fmtField{name: bl.cfg.Interner.Str(f.Name), k: uint32(k), t: f.Type})
		}
	case types.KNamed:
		sd, ok := bl.cfg.Items.Get(ty.Item).Decl.(*types.StructDecl)
		if !ok {
			source.ICEf(at, "lower: format arguments of a named type that is not a struct")
		}
		for k, f := range sd.Fields {
			if f.Exported {
				fs = append(fs, fmtField{name: bl.cfg.Interner.Str(f.Name), k: uint32(k), t: bl.fieldType(a, uint32(k))})
			}
		}
	default:
		source.ICEf(at, "lower: format arguments of kind %v", ty.Kind)
	}
	return fs
}

// fmtPiece is one FmtPiece value.
type fmtPiece struct {
	lit  []byte
	arg  int32
	spec uint32
}

// fmtPieces scans a format string check accepted (fmtspec) against
// the argument type's fields: the literal before each hole with "{{" and
// "}}" unescaped, the hole's index in the argument array and its spec
// word, then the trailing literal if it is not empty, with arg -1; and the
// argument array's targets (fmtspec.Args: each field a one-name hole uses,
// then each field path). ok is false on a string check would
// reject.
func (bl *builder) fmtPieces(f []byte, fields []fmtField, at source.Span) (ps []fmtPiece, targets []fmtspec.Resolved[types.TypeID], specOf []bool, padded bool, ok bool) {
	holes, pieces, problem := fmtspec.Scan(f)
	if problem != "" {
		return nil, nil, nil, false, false
	}
	rs, problem := fmtspec.Match(holes, bl.specFields(fields), func(t types.TypeID) ([]fmtspec.Field[types.TypeID], bool, bool) {
		deref := false
		if ty := bl.tab.Get(t); ty.Kind == types.KRef {
			t, deref = ty.Elem, true
		}
		if k := bl.tab.Get(t).Kind; k != types.KRecord && k != types.KNamed {
			return nil, false, false
		}
		if k := bl.tab.Get(t); k.Kind == types.KNamed {
			if _, isStruct := bl.cfg.Items.Get(k.Item).Decl.(*types.StructDecl); !isStruct {
				return nil, false, false
			}
		}
		return bl.specFields(bl.fmtFields(t, at)), deref, true
	}, func(types.TypeID) bool { return true })
	if problem != "" {
		return nil, nil, nil, false, false
	}
	targets, argOf := fmtspec.Args(holes, rs, len(fields))
	for _, p := range pieces {
		q := fmtPiece{lit: p.Lit, arg: -1}
		if p.Hole >= 0 {
			q.arg, q.spec = int32(argOf[p.Hole]), fmtspec.Encode(holes[p.Hole].Spec, rs[p.Hole].Field.Class)
		}
		ps = append(ps, q)
	}
	return ps, targets, fmtspec.SpecTargets(holes, argOf, len(targets)), fmtspec.Padded(holes), true
}

// specFields are fs as fmtspec.Match takes them.
func (bl *builder) specFields(fs []fmtField) []fmtspec.Field[types.TypeID] {
	out := make([]fmtspec.Field[types.TypeID], len(fs))
	for i, x := range fs {
		out[i] = fmtspec.Field[types.TypeID]{Name: x.name, Index: x.k, Type: x.t, Class: fmtClass(bl.tab.Get(x.t))}
	}
	return out
}

// fmtClass is what a spec needs of a field's type (fmtspec.Class).
func fmtClass(ty *types.Type) fmtspec.Class {
	switch {
	case ty.IsInteger():
		return fmtspec.Int
	case ty.Kind == types.KFloat:
		return fmtspec.Float
	}
	return fmtspec.Other
}

// fmtLine is one call of a Print call's straight-line body: the literal lit
// through Writer.Str when lit is not nil, else a hole through its value
// function fn[t], given the field's address p and the hole's spec word, as
// FmtRun would have called fn through the hole's FmtArg.
type fmtLine struct {
	lit  []byte
	fn   types.ItemID
	t    types.TypeID
	p    mir.Place
	spec uint32
}

// formatCall rewrites the operands of a Print or Arena.Fmt call (ps: the
// writer or arena, the format string, the arguments, all already formed in
// evaluation order) into the runner's (the same first operand, the piece slice,
// the argument slice), emitting the thunk's statements, and returns the
// runner's callee. An unpadded Print call is lowered straight-line instead
// (the header): formatCall emits the argument record and the field
// addresses, and returns the calls for formatLines, which call.go emits
// once the call's destination and error edge are known; the callee is then
// unused.
func (bl *builder) formatCall(x *ast.CallExpr, c Call, fi fmtItems, ps []pendingArg) (mir.Callee, []fmtLine) {
	at := x.Span()
	if len(ps) != 3 || len(c.Args) != 1 {
		source.ICEf(at, "lower: a format call with %d operands and %d generic arguments", len(ps), len(c.Args))
	}
	text, ok := bl.strBytes(ps[1].op)
	if !ok {
		source.ICEf(at, "lower: the format string is not a str constant")
	}
	aT := c.Args[0].Type
	fields := bl.fmtFields(aT, at)
	pieces, targets, specOf, padded, ok := bl.fmtPieces(text, fields, at)
	if !ok {
		source.ICEf(at, "lower: a format string check accepted does not scan")
	}
	// The padding runner only for a call with a width, and
	// FmtSpecValue only for an entry a valued spec reads, so a program that
	// pads nothing links neither.
	runItem, valueOf := fi.run, func(j int) types.ItemID { return fi.value }
	if padded {
		if fi.padRun == types.NoItem {
			source.ICEf(at, "lower: a padded format call and no padding runner in std")
		}
		runItem = fi.padRun
	}
	if fi.specValue != types.NoItem {
		valueOf = func(j int) types.ItemID {
			if specOf[j] {
				return fi.specValue
			}
			return fi.value
		}
	}
	run := bl.cfg.callee(runItem)
	if run == nil || len(run.Params) != 3 {
		source.ICEf(at, "lower: the format runner has no 3-parameter signature")
	}
	pieceSlice, argSlice := run.Params[1].Type, run.Params[2].Type
	pieceT, argT := bl.tab.Get(pieceSlice).Elem, bl.tab.Get(argSlice).Elem
	argDecl, ok := bl.cfg.Items.Get(fi.arg).Decl.(*types.StructDecl)
	if !ok || len(argDecl.Fields) != 2 {
		source.ICEf(at, "lower: FmtArg is not a struct of two fields")
	}
	ptrT, fnT := argDecl.Fields[0].Type, argDecl.Fields[1].Type
	// Straight-line: a Print call with no width, when std has Writer.Str of
	// FmtRun's shape (writerStr), the writer is a whole local that each
	// call can reborrow, and the text writes something (an empty text, or
	// one of only empty literals, keeps the runner, which calls nothing).
	straight := !padded && fi.str != types.NoItem && wholeLocal(ps[0].op) && fmtWrites(pieces)

	// The argument record, in a temporary whose fields the FmtArgs point at;
	// it lives to the end of the statement, past the runner's call. A field
	// path's FmtArg points into it, through a shared reference where the
	// path steps through one. Straight-line, the value functions are
	// given the same addresses directly.
	ta := bl.newTemp(aT, at)
	bl.assign(bl.b.Place(ta), use(ps[2].op), at)
	gs := make([]mir.Operand, len(targets))
	qs := make([]mir.Place, len(targets))
	for j, tg := range targets {
		place := bl.b.Place(ta)
		for _, s := range tg.Path {
			if s.Deref {
				place = bl.deref(place, at)
			}
			place = bl.field(place, s.Index, s.Type)
		}
		ft := tg.Field.Type
		r := bl.newTemp(bl.tab.Ref(ft), at)
		bl.assign(bl.b.Place(r), mir.Rvalue{Kind: mir.RvRef, Borrow: mir.Shared, Place: place, Origin: mir.OriginPlain, Type: bl.tab.Ref(ft)}, at)
		p := bl.directCall(fi.of, []types.GenericArg{types.TypeArg(ft)}, []mir.Operand{mir.Copy(bl.b.Place(r))}, bl.tab.RawPtr(ft), at)
		q := bl.directCall(fi.ptrCast, []types.GenericArg{types.TypeArg(ft), types.TypeArg(types.U8)}, []mir.Operand{mir.Copy(p)}, ptrT, at)
		qs[j] = q
		if straight {
			continue
		}
		fv := types.ConstValue{Type: fnT, Align: 8, Bytes: make([]byte, 8),
			Relocs: []types.Reloc{{Offset: 0, Kind: types.RelocFn, Fn: types.FnRef{Item: valueOf(j), Args: []types.GenericArg{types.TypeArg(ft)}}}}}
		if err := fv.Canonicalize(8); err != nil {
			source.ICEf(at, "lower: FmtValue constant: %v", err)
		}
		g := bl.newTemp(argT, at)
		bl.assign(bl.b.Place(g), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: fi.arg, Type: argT,
			Ops: bl.b.AddOperands(mir.Copy(q), mir.Const(fnT, bl.cfg.Consts.Intern(fv)))}, at)
		gs[j] = mir.Copy(bl.b.Place(g))
	}
	if straight {
		// FmtRun's walk, unrolled: each literal (an empty one writes
		// nothing: Writer.Bytes of 0 bytes makes no call and changes no
		// field), then the hole's value function on its entry.
		var lines []fmtLine
		for _, p := range pieces {
			if len(p.lit) > 0 {
				lines = append(lines, fmtLine{lit: p.lit})
			}
			if p.arg >= 0 {
				lines = append(lines, fmtLine{fn: valueOf(int(p.arg)), t: targets[p.arg].Field.Type, p: qs[p.arg], spec: p.spec})
			}
		}
		return mir.Callee{}, lines
	}
	sa := bl.stackSlice(argT, argSlice, gs, at)

	pcs := make([]mir.Operand, len(pieces))
	for i, p := range pieces {
		l := bl.newTemp(pieceT, at)
		bl.assign(bl.b.Place(l), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggStruct, Item: fi.piece, Type: pieceT,
			Ops: bl.b.AddOperands(bl.strConst(p.lit, at), mir.Imm(types.I32, uint64(uint32(p.arg))), mir.Imm(types.U32, uint64(p.spec)))}, at)
		pcs[i] = mir.Copy(bl.b.Place(l))
	}
	sp := bl.stackSlice(pieceT, pieceSlice, pcs, at)

	ps[1].op, ps[1].mode = sp, mir.ArgCopy
	ps[2].op, ps[2].mode = sa, mir.ArgCopy
	return mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: runItem}}, nil
}

// wholeLocal reports whether a writer operand is a whole local, which
// every straight-line call reborrows as FmtRun's own w is passed on.
func wholeLocal(o mir.Operand) bool {
	return (o.Kind == mir.OpCopy || o.Kind == mir.OpMove) && o.Place.Proj.Len == 0
}

// fmtWrites reports whether some piece has a literal byte or a hole.
func fmtWrites(ps []fmtPiece) bool {
	for _, p := range ps {
		if len(p.lit) > 0 || p.arg >= 0 {
			return true
		}
	}
	return false
}

// formatLines emits a Print call's straight-line body: each line's call in
// order, each with the Print call's error edge, so the first error leaves
// the statement as FmtRun's try returned it, and nothing after it runs.
// The writer w is reborrowed by every call; the last call writes the
// Print call's destination, the others a void temporary of their own.
func (bl *builder) formatLines(lines []fmtLine, fi fmtItems, w mir.Arg, dest mir.Place, c Call, edge *errEdge, at source.Span) {
	if c.Errors != types.EmptyErrors && edge == nil {
		source.ICEf(at, "lower: a straight-line Print call with no error edge, E0802 (check gap)")
	}
	wa := mir.Arg{Operand: mir.Copy(w.Operand.Place), Mode: mir.ArgReborrow}
	for i, ln := range lines {
		d := dest
		if i < len(lines)-1 {
			d = bl.b.Place(bl.newTemp(types.Void, at))
		}
		var fn mir.FnRef
		var args mir.Range
		if ln.lit != nil {
			fn = mir.FnRef{Item: fi.str}
			args = bl.b.AddArgs(wa, mir.Arg{Operand: bl.strConst(ln.lit, at), Mode: mir.ArgCopy})
		} else {
			fn = mir.FnRef{Item: ln.fn, Args: bl.b.AddGenericArgs(types.TypeArg(ln.t))}
			args = bl.b.AddArgs(mir.Arg{Operand: mir.Copy(ln.p), Mode: mir.ArgCopy},
				mir.Arg{Operand: mir.Imm(types.U32, uint64(ln.spec)), Mode: mir.ArgCopy}, wa)
		}
		target := bl.newBlock()
		tm := mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: fn}, Args: args, Dest: d, Target: target, Spawn: mir.NoLocal}
		if c.Errors != types.EmptyErrors {
			tm.HasErr, tm.Err = true, mir.ErrorEdge{Kind: edge.kind, Block: edge.block, Error: edge.err}
		}
		bl.term(tm, at)
		bl.cur = target
	}
}

// stackSlice is elems in a stack [n]elem array and a shared slice of it.
func (bl *builder) stackSlice(elem, slice types.TypeID, elems []mir.Operand, at source.Span) mir.Operand {
	at2 := bl.tab.Array(types.ConstArg{Kind: types.CAValue, Value: bl.intConst(int64(len(elems)))}, elem)
	arr := bl.newTemp(at2, at)
	bl.assign(bl.b.Place(arr), mir.Rvalue{Kind: mir.RvAggregate, Agg: mir.AggArray, Type: at2, Ops: bl.b.AddOperands(elems...)}, at)
	s := bl.newTemp(slice, at)
	bl.assign(bl.b.Place(s), mir.Rvalue{Kind: mir.RvSlice, Borrow: mir.Shared, Place: bl.b.Place(arr), Type: slice}, at)
	return mir.Copy(bl.b.Place(s))
}

// directCall emits a call of item with no error edge into a fresh
// temporary of type ret, and continues after it.
func (bl *builder) directCall(item types.ItemID, args []types.GenericArg, ops []mir.Operand, ret types.TypeID, at source.Span) mir.Place {
	dest := bl.newTemp(ret, at)
	as := make([]mir.Arg, len(ops))
	for i, o := range ops {
		as[i] = mir.Arg{Operand: o, Mode: bl.argMode(o, at)}
	}
	target := bl.newBlock()
	bl.term(mir.Terminator{Kind: mir.TermCall, Callee: mir.Callee{Kind: mir.CallDirect, Fn: mir.FnRef{Item: item, Args: bl.b.AddGenericArgs(args...)}},
		Args: bl.b.AddArgs(as...), Dest: bl.b.Place(dest), Target: target, Spawn: mir.NoLocal}, at)
	bl.cur = target
	return bl.b.Place(dest)
}

// intConst interns an int value (an array length).
func (bl *builder) intConst(v int64) types.ConstID {
	n := make([]byte, 8)
	types.StoreUint(n, 8, uint64(v))
	return bl.cfg.Consts.Intern(types.ConstValue{Type: types.Int, Align: 8, Bytes: n})
}

// strConst is a str constant of bs, in check's strConst form: a {ptr, len}
// value with one RelocData to a [N]u8 constant, the empty one included (a
// [0]u8 constant). Codegen gives every slice and str data pointer
// !nonnull, so a null word made Windows release builds drop the rest of a
// function after Print("{i}\n", ..) (an empty first literal); a dangling
// word 1 with no reloc made the evaluator read an integer and refuse it as
// a pointer (E0901 pointer-bytes in Writer.Bytes, std's
// TestEvalArenaFmtEmptyPiece).
func (bl *builder) strConst(bs []byte, at source.Span) mir.Operand {
	return bl.dataConst(types.Str, bs, at)
}

// dataConst is a str or []u8 constant of bs, in strConst's form.
func (bl *builder) dataConst(t types.TypeID, bs []byte, at source.Span) mir.Operand {
	return DataConst(bl.tab, bl.cfg.Consts, t, bs, at)
}

// DataConst is a str or []u8 constant of bs interned in consts, in
// strConst's form, for MIR built outside lower (the driver's report
// wrapper around Main).
func DataConst(tab *types.Table, consts *types.ConstTable, t types.TypeID, bs []byte, at source.Span) mir.Operand {
	n := make([]byte, 8)
	types.StoreUint(n, 8, uint64(len(bs)))
	count := consts.Intern(types.ConstValue{Type: types.Int, Align: 8, Bytes: n})
	v := types.ConstValue{Type: t, Align: 8, Bytes: make([]byte, 16)}
	types.StoreUint(v.Bytes[8:], 8, uint64(len(bs)))
	arr := tab.Array(types.ConstArg{Kind: types.CAValue, Value: count}, types.U8)
	data := consts.Intern(types.ConstValue{Type: arr, Align: 1, Bytes: append([]byte(nil), bs...)})
	v.Relocs = []types.Reloc{{Offset: 0, Kind: types.RelocData, Data: data}}
	if err := v.Canonicalize(8); err != nil {
		source.ICEf(at, "lower: %s constant: %v", tab.TypeString(t, nil), err)
	}
	return mir.Const(t, consts.Intern(v))
}

// strBytes reads a str constant operand's bytes back.
func (bl *builder) strBytes(o mir.Operand) ([]byte, bool) {
	if o.Kind != mir.OpConst {
		return nil, false
	}
	v := bl.cfg.Consts.Get(o.Const)
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
	d := bl.cfg.Consts.Get(v.Relocs[0].Data)
	if uint64(len(d.Bytes)) != n {
		return nil, false
	}
	return d.Bytes, true
}

// hasParam reports whether t mentions a type parameter or Self: a format
// call over such an argument type (a record `(e)` with e: E) stays a plain
// call, which mono expands per instance as it does a forwarded const format.
func (bl *builder) hasParam(t types.TypeID) bool {
	ty := bl.tab.Get(t)
	switch ty.Kind {
	case types.KParam, types.KSelf:
		return true
	case types.KRef, types.KMut, types.KSlice, types.KMutSlice, types.KRawPtr, types.KOptional, types.KArray, types.KErrorUnion:
		return bl.hasParam(ty.Elem)
	case types.KRecord:
		for _, f := range ty.Fields {
			if bl.hasParam(f.Type) {
				return true
			}
		}
	case types.KNamed:
		for _, a := range ty.Args {
			if !a.IsConst && bl.hasParam(a.Type) {
				return true
			}
		}
	}
	return false
}
