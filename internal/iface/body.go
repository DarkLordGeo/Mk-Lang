// Why: lowered MIR bodies are cached between builds and hashed into object
// pack keys, so they need an exact stored form and a hash that ignores what
// cannot change the generated code.
//
// What: writeBody and readBody (the store form: every field of mir.Body in
// declaration order, spans rebased onto anchors, an exact round trip) and
// Hasher.Body (the hash form mk.body).
//
// How: mk.body never writes the positions in Spans, local names, loop
// labels or the body's own item, but writes every SpanID and the length of
// Spans. Operands and rvalues are stored as a mask of their non-zero fields
// followed by those fields, which keeps warm decodes short.

package iface

import (
	"sync"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// bodyFn writes a FnRef of a body: in hash form 0 for none, else 1 and the
// mk.fn hash (a fn literal by its content); in store form the item
// and the generic-argument range.
func (w *walker) bodyFn(b *mir.Body, f mir.FnRef) {
	if w.store() {
		w.item(f.Item)
		w.rng(f.Args)
		return
	}
	if f.Item == types.NoItem {
		// A CallInterface callee: its Args are the method's own type
		// arguments (set by lowering). Their types are in the body's
		// GenericArgs, hashed whole; the range says which call takes them.
		w.e.Uvarint(0)
		w.rng(f.Args)
		return
	}
	w.e.Uvarint(1)
	w.e.Hash(w.h.FnRef(types.FnRef{Item: f.Item, Args: b.GenericArgs[f.Args.Start:f.Args.End()]}))
}

func (w *walker) rng(r mir.Range) {
	w.e.Uvarint(uint64(r.Start))
	w.e.Uvarint(uint64(r.Len))
}

func (w *walker) id32(v uint32) { w.e.Uvarint(uint64(v)) }

func (w *walker) place(p mir.Place) {
	w.id32(uint32(p.Local))
	w.rng(p.Proj)
}

func (w *walker) operand(o *mir.Operand) {
	if w.store() {
		w.operandStore(o)
		return
	}
	w.e.Uvarint(uint64(o.Kind))
	w.place(o.Place)
	w.typ(o.Type)
	w.cnst(o.Const)
	w.e.Uvarint(o.Bits)
}

func (w *walker) rvalue(b *mir.Body, rv *mir.Rvalue) {
	if w.store() {
		w.rvalueStore(b, rv)
		return
	}
	e := w.e
	e.Uvarint(uint64(rv.Kind))
	e.Uvarint(uint64(rv.Borrow))
	e.Uvarint(uint64(rv.Origin))
	w.id32(uint32(rv.Loop))
	w.place(rv.Place)
	w.operand(&rv.A)
	w.operand(&rv.B)
	e.Bool(rv.HasLo)
	e.Bool(rv.HasHi)
	e.Uvarint(uint64(rv.Un))
	e.Uvarint(uint64(rv.Bin))
	e.Uvarint(uint64(rv.Cast))
	w.typ(rv.Type)
	e.Uvarint(uint64(rv.Agg))
	w.item(rv.Item)
	w.id32(rv.Variant)
	w.rng(rv.Ops)
	w.constArg(rv.Len)
	w.id32(rv.Param)
	e.Bool(rv.Field.Loop)
	w.id32(rv.Field.Index)
	w.bodyFn(b, rv.Fn)
}

// Store form of operands and rvalues: a mask of the fields that are not
// zero, then those fields in declaration order. Most fields of an operand or
// an rvalue are zero for its kind, and decoding every field of every
// statement would be most of a warm build's decode time. A field left out
// decodes as its zero value, which is what it was, so the round trip stays
// exact. The hash form does not use the mask.

// Operand fields, as mask bits.
const (
	opKind = 1 << iota
	opPlace
	opType
	opConst
	opBits
)

func (w *walker) operandStore(o *mir.Operand) {
	var m uint64
	if o.Kind != 0 {
		m |= opKind
	}
	if o.Place != (mir.Place{}) {
		m |= opPlace
	}
	if o.Type != types.NoType {
		m |= opType
	}
	if o.Const != types.NoConst {
		m |= opConst
	}
	if o.Bits != 0 {
		m |= opBits
	}
	e := w.e
	e.Uvarint(m)
	if m&opKind != 0 {
		e.Uvarint(uint64(o.Kind))
	}
	if m&opPlace != 0 {
		w.place(o.Place)
	}
	if m&opType != 0 {
		w.typ(o.Type)
	}
	if m&opConst != 0 {
		w.cnst(o.Const)
	}
	if m&opBits != 0 {
		e.Uvarint(o.Bits)
	}
}

// Rvalue fields, as mask bits.
const (
	rvKind = 1 << iota
	rvBorrow
	rvOrigin
	rvLoop
	rvPlace
	rvA
	rvB
	rvHasLo
	rvHasHi
	rvUn
	rvBin
	rvCast
	rvType
	rvAgg
	rvItem
	rvVariant
	rvOps
	rvLen
	rvParam
	rvFieldLoop
	rvFieldIndex
	rvFn
	rvAll = rvFn<<1 - 1
)

func (w *walker) rvalueStore(b *mir.Body, rv *mir.Rvalue) {
	var m uint64
	set := func(bit uint64, nonzero bool) {
		if nonzero {
			m |= bit
		}
	}
	set(rvKind, rv.Kind != 0)
	set(rvBorrow, rv.Borrow != 0)
	set(rvOrigin, rv.Origin != 0)
	set(rvLoop, rv.Loop != 0)
	set(rvPlace, rv.Place != (mir.Place{}))
	set(rvA, rv.A != (mir.Operand{}))
	set(rvB, rv.B != (mir.Operand{}))
	set(rvHasLo, rv.HasLo)
	set(rvHasHi, rv.HasHi)
	set(rvUn, rv.Un != 0)
	set(rvBin, rv.Bin != 0)
	set(rvCast, rv.Cast != 0)
	set(rvType, rv.Type != types.NoType)
	set(rvAgg, rv.Agg != 0)
	set(rvItem, rv.Item != types.NoItem)
	set(rvVariant, rv.Variant != 0)
	set(rvOps, rv.Ops != (mir.Range{}))
	set(rvLen, rv.Len != (types.ConstArg{}))
	set(rvParam, rv.Param != 0)
	set(rvFieldLoop, rv.Field.Loop)
	set(rvFieldIndex, rv.Field.Index != 0)
	set(rvFn, rv.Fn != (mir.FnRef{}))
	e := w.e
	e.Uvarint(m)
	if m&rvKind != 0 {
		e.Uvarint(uint64(rv.Kind))
	}
	if m&rvBorrow != 0 {
		e.Uvarint(uint64(rv.Borrow))
	}
	if m&rvOrigin != 0 {
		e.Uvarint(uint64(rv.Origin))
	}
	if m&rvLoop != 0 {
		w.id32(uint32(rv.Loop))
	}
	if m&rvPlace != 0 {
		w.place(rv.Place)
	}
	if m&rvA != 0 {
		w.operand(&rv.A)
	}
	if m&rvB != 0 {
		w.operand(&rv.B)
	}
	// HasLo, HasHi and Field.Loop are their bits.
	if m&rvUn != 0 {
		e.Uvarint(uint64(rv.Un))
	}
	if m&rvBin != 0 {
		e.Uvarint(uint64(rv.Bin))
	}
	if m&rvCast != 0 {
		e.Uvarint(uint64(rv.Cast))
	}
	if m&rvType != 0 {
		w.typ(rv.Type)
	}
	if m&rvAgg != 0 {
		e.Uvarint(uint64(rv.Agg))
	}
	if m&rvItem != 0 {
		w.item(rv.Item)
	}
	if m&rvVariant != 0 {
		w.id32(rv.Variant)
	}
	if m&rvOps != 0 {
		w.rng(rv.Ops)
	}
	if m&rvLen != 0 {
		w.constArg(rv.Len)
	}
	if m&rvParam != 0 {
		w.id32(rv.Param)
	}
	if m&rvFieldIndex != 0 {
		w.id32(rv.Field.Index)
	}
	if m&rvFn != 0 {
		w.bodyFn(b, rv.Fn)
	}
}

func (w *walker) term(b *mir.Body, t *mir.Terminator) {
	e := w.e
	e.Uvarint(uint64(t.Kind))
	w.id32(uint32(t.Span))
	w.id32(uint32(t.Target))
	e.Bool(t.ElseExit)
	w.operand(&t.On)
	w.rng(t.Cases)
	w.id32(uint32(t.Default))
	c := &t.Callee
	e.Uvarint(uint64(c.Kind))
	w.bodyFn(b, c.Fn)
	w.item(c.Iface)
	w.id32(c.Method)
	w.typ(c.SelfType)
	w.operand(&c.Value)
	e.Uvarint(uint64(c.Builtin))
	w.rng(t.Args)
	w.place(t.Dest)
	e.Bool(t.HasErr)
	e.Uvarint(uint64(t.Err.Kind))
	w.id32(uint32(t.Err.Block))
	w.id32(uint32(t.Err.Error))
	w.id32(uint32(t.Spawn))
	w.id32(t.Param)
	w.typ(t.Type)
	w.id32(uint32(t.Yes))
	w.id32(uint32(t.No))
	w.id32(uint32(t.Value))
	e.Uvarint(uint64(t.Exit))
	e.Uvarint(uint64(t.Panic))
	e.Bool(t.HasMsg)
	w.operand(&t.Msg)
	e.Bool(t.NewError)
}

// writeBody writes a body. In store form it begins with the body's item and
// writes local names, loop labels and every span; in hash form none of them.
func (w *walker) writeBody(b *mir.Body) {
	e := w.e
	if w.store() {
		w.item(b.Item)
	}
	e.Uvarint(uint64(b.GenericCount))
	e.Uvarint(uint64(b.ParamCount))
	e.Uvarint(uint64(len(b.Locals)))
	for i := range b.Locals {
		l := &b.Locals[i]
		w.typ(l.Type)
		e.Uvarint(uint64(l.Kind))
		e.Uvarint(uint64(l.Param))
		if w.store() {
			w.sym(l.Name)
		}
		e.Bool(l.Var)
		w.id32(uint32(l.Loop))
		w.id32(uint32(l.Span))
	}
	e.Uvarint(uint64(len(b.Blocks)))
	for i := range b.Blocks {
		bl := &b.Blocks[i]
		e.Uvarint(uint64(len(bl.Stmts)))
		for j := range bl.Stmts {
			st := &bl.Stmts[j]
			e.Uvarint(uint64(st.Kind))
			w.place(st.Place)
			w.rvalue(b, &st.Rvalue)
			w.id32(uint32(st.Local))
			e.Bool(st.Cancel)
			w.id32(uint32(st.Span))
			if st.Kind == mir.StAsm {
				w.id32(uint32(st.Asm))
			}
		}
		w.term(b, &bl.Term)
		e.Uvarint(uint64(bl.Origin))
		w.id32(uint32(bl.Defer))
	}
	e.Uvarint(uint64(len(b.Projections)))
	for _, p := range b.Projections {
		e.Uvarint(uint64(p.Kind))
		e.Bool(p.FieldLoop)
		e.Uvarint(uint64(p.Sel))
		if p.Kind == mir.ProjDowncast && p.Sel == mir.SelCase {
			w.item(types.ItemID(p.A)) // A is the case's error item
		} else {
			w.id32(p.A)
		}
		w.typ(p.Type)
	}
	w.genericArgs(b.GenericArgs)
	e.Uvarint(uint64(len(b.Operands)))
	for i := range b.Operands {
		w.operand(&b.Operands[i])
	}
	e.Uvarint(uint64(len(b.Args)))
	for i := range b.Args {
		w.operand(&b.Args[i].Operand)
		e.Uvarint(uint64(b.Args[i].Mode))
	}
	e.Uvarint(uint64(len(b.Cases)))
	for _, c := range b.Cases {
		e.Uvarint(c.Value)
		w.id32(uint32(c.Target))
	}
	e.Uvarint(uint64(len(b.Spans)))
	for _, sp := range b.Spans {
		w.span(sp)
	}
	e.Uvarint(uint64(len(b.Loops)))
	for i := range b.Loops {
		l := &b.Loops[i]
		w.id32(uint32(l.Header))
		w.id32(uint32(l.Exit))
		w.id32(uint32(l.Holder))
		if w.store() {
			w.sym(l.Label)
		}
		w.id32(uint32(l.Span))
		w.typ(l.FieldsOf)
		w.id32(l.FieldsParam)
	}
	e.Uvarint(uint64(len(b.Defers)))
	for _, df := range b.Defers {
		e.Bool(df.Onerr)
		w.id32(uint32(df.Span))
	}
	w.writeAsms(b.Asms)
}

// Body is mk.body over a MIR body: the positions in Spans, local
// names, loop labels and the body's own item are never written; every
// SpanID field and the length of Spans are, because span IDs index
// the rows panic sites and line tables are built from.
func (h *Hasher) Body(b *mir.Body) source.Hash {
	p := bodyBufs.Get().(*[]byte)
	e := Encoder{buf: (*p)[:0]}
	w := walker{e: &e, h: h}
	w.writeBody(b)
	sum := e.sum(DomBody)
	if cap(e.buf) <= maxPooledBody {
		*p = e.buf
		bodyBufs.Put(p)
	}
	return sum
}

// bodyBufs reuses Body's encoding buffers: a build hashes every instance body
// for its pack key, and without the pool, growing a fresh buffer each time is
// a large share of that hashing. The bytes hashed are the same. A buffer over
// maxPooledBody is dropped.
var bodyBufs = sync.Pool{New: func() any { return new([]byte) }}

const maxPooledBody = 1 << 20

func (r *reader) rng(d *Decoder) mir.Range { return mir.Range{Start: d.U32(), Len: d.U32()} }

func (r *reader) place(d *Decoder) mir.Place {
	return mir.Place{Local: mir.LocalID(d.U32()), Proj: r.rng(d)}
}

func (r *reader) operand(d *Decoder) mir.Operand {
	var o mir.Operand
	m := d.Uvarint()
	if m&^uint64(opBits<<1-1) != 0 {
		d.Failf("operand mask %#x", m)
		return o
	}
	if m&opKind != 0 {
		o.Kind = mir.OperandKind(d.U8())
	}
	if m&opPlace != 0 {
		o.Place = r.place(d)
	}
	if m&opType != 0 {
		o.Type = r.typ(d, 0)
	}
	if m&opConst != 0 {
		o.Const = r.cnst(d, 0)
	}
	if m&opBits != 0 {
		o.Bits = d.Uvarint()
	}
	return o
}

func (r *reader) bodyFn(d *Decoder) mir.FnRef {
	return mir.FnRef{Item: r.item(d), Args: r.rng(d)}
}

func (r *reader) rvalue(d *Decoder) mir.Rvalue {
	var rv mir.Rvalue
	m := d.Uvarint()
	if m&^uint64(rvAll) != 0 {
		d.Failf("rvalue mask %#x", m)
		return rv
	}
	if m&rvKind != 0 {
		rv.Kind = mir.RvalueKind(d.U8())
	}
	if m&rvBorrow != 0 {
		rv.Borrow = mir.BorrowKind(d.U8())
	}
	if m&rvOrigin != 0 {
		rv.Origin = mir.BorrowOrigin(d.U8())
	}
	if m&rvLoop != 0 {
		rv.Loop = mir.LoopID(d.U32())
	}
	if m&rvPlace != 0 {
		rv.Place = r.place(d)
	}
	if m&rvA != 0 {
		rv.A = r.operand(d)
	}
	if m&rvB != 0 {
		rv.B = r.operand(d)
	}
	rv.HasLo = m&rvHasLo != 0
	rv.HasHi = m&rvHasHi != 0
	if m&rvUn != 0 {
		rv.Un = mir.UnOp(d.U8())
	}
	if m&rvBin != 0 {
		rv.Bin = mir.BinOp(d.U8())
	}
	if m&rvCast != 0 {
		rv.Cast = mir.CastKind(d.U8())
	}
	if m&rvType != 0 {
		rv.Type = r.typ(d, 0)
	}
	if m&rvAgg != 0 {
		rv.Agg = mir.AggKind(d.U8())
	}
	if m&rvItem != 0 {
		rv.Item = r.item(d)
	}
	if m&rvVariant != 0 {
		rv.Variant = d.U32()
	}
	if m&rvOps != 0 {
		rv.Ops = r.rng(d)
	}
	if m&rvLen != 0 {
		rv.Len = r.readConstArg(d, 0)
	}
	if m&rvParam != 0 {
		rv.Param = d.U32()
	}
	rv.Field.Loop = m&rvFieldLoop != 0
	if m&rvFieldIndex != 0 {
		rv.Field.Index = d.U32()
	}
	if m&rvFn != 0 {
		rv.Fn = r.bodyFn(d)
	}
	return rv
}

func (r *reader) term(d *Decoder) mir.Terminator {
	t := mir.Terminator{Kind: mir.TermKind(d.U8())}
	t.Span = mir.SpanID(d.U32())
	t.Target = mir.BlockID(d.U32())
	t.ElseExit = d.Bool()
	t.On = r.operand(d)
	t.Cases = r.rng(d)
	t.Default = mir.BlockID(d.U32())
	t.Callee.Kind = mir.CalleeKind(d.U8())
	t.Callee.Fn = r.bodyFn(d)
	t.Callee.Iface = r.item(d)
	t.Callee.Method = d.U32()
	t.Callee.SelfType = r.typ(d, 0)
	t.Callee.Value = r.operand(d)
	t.Callee.Builtin = mir.Builtin(d.U8())
	t.Args = r.rng(d)
	t.Dest = r.place(d)
	t.HasErr = d.Bool()
	t.Err = mir.ErrorEdge{Kind: mir.ErrorEdgeKind(d.U8()), Block: mir.BlockID(d.U32()), Error: mir.LocalID(d.U32())}
	t.Spawn = mir.LocalID(d.U32())
	t.Param = d.U32()
	t.Type = r.typ(d, 0)
	t.Yes = mir.BlockID(d.U32())
	t.No = mir.BlockID(d.U32())
	t.Value = mir.LocalID(d.U32())
	t.Exit = mir.ExitKind(d.U8())
	t.Panic = types.PanicCode(d.U32())
	t.HasMsg = d.Bool()
	t.Msg = r.operand(d)
	t.NewError = d.Bool()
	return t
}

// readBody reads a store-form body.
func (r *reader) readBody(d *Decoder) *mir.Body {
	// Every list is sized from its count, and the dry pass (decodeRecord)
	// validates without keeping anything, so a large result decodes without
	// growing its lists (BenchmarkDecodeFuncResult measures it).
	keep := !r.dry
	b := &mir.Body{Item: r.item(d), GenericCount: d.U32(), ParamCount: d.U32()}
	n := d.Count(7)
	if keep && n > 0 {
		b.Locals = make([]mir.Local, 0, n)
	}
	for i := 0; i < n && d.Err == nil; i++ {
		l := mir.Local{Type: r.typ(d, 0), Kind: mir.LocalKind(d.U8()),
			Param: types.ParamIndex(d.U32()), Name: r.sym(d), Var: d.Bool(), Loop: mir.LoopID(d.U32()), Span: mir.SpanID(d.U32())}
		if keep {
			b.Locals = append(b.Locals, l)
		}
	}
	n = d.Count(4)
	if keep && n > 0 {
		b.Blocks = make([]mir.Block, 0, n)
	}
	for i := 0; i < n && d.Err == nil; i++ {
		var bl mir.Block
		m := d.Count(8)
		if keep && m > 0 {
			bl.Stmts = make([]mir.Statement, 0, m)
		}
		for j := 0; j < m && d.Err == nil; j++ {
			st := mir.Statement{Kind: mir.StatementKind(d.U8()), Place: r.place(d), Rvalue: r.rvalue(d)}
			st.Local = mir.LocalID(d.U32())
			st.Cancel = d.Bool()
			st.Span = mir.SpanID(d.U32())
			if st.Kind == mir.StAsm {
				st.Asm = mir.AsmID(d.U32())
			}
			if keep {
				bl.Stmts = append(bl.Stmts, st)
			}
		}
		bl.Term = r.term(d)
		bl.Origin = mir.BlockOrigin(d.U8())
		bl.Defer = mir.DeferID(d.U32())
		if keep {
			b.Blocks = append(b.Blocks, bl)
		}
	}
	n = d.Count(5)
	if keep && n > 0 {
		b.Projections = make([]mir.Projection, 0, n)
	}
	for i := 0; i < n && d.Err == nil; i++ {
		p := mir.Projection{Kind: mir.ProjKind(d.U8()), FieldLoop: d.Bool(), Sel: mir.VariantSel(d.U8())}
		if p.Kind == mir.ProjDowncast && p.Sel == mir.SelCase {
			p.A = uint32(r.item(d))
		} else {
			p.A = d.U32()
		}
		p.Type = r.typ(d, 0)
		if keep {
			b.Projections = append(b.Projections, p)
		}
	}
	b.GenericArgs = r.readGenericArgs(d, 0)
	n = d.Count(1) // an operand is at least its mask
	if keep && n > 0 {
		b.Operands = make([]mir.Operand, 0, n)
	}
	for i := 0; i < n && d.Err == nil; i++ {
		o := r.operand(d)
		if keep {
			b.Operands = append(b.Operands, o)
		}
	}
	n = d.Count(2) // its operand mask and its mode
	if keep && n > 0 {
		b.Args = make([]mir.Arg, 0, n)
	}
	for i := 0; i < n && d.Err == nil; i++ {
		a := mir.Arg{Operand: r.operand(d), Mode: mir.ArgMode(d.U8())}
		if keep {
			b.Args = append(b.Args, a)
		}
	}
	n = d.Count(2)
	if keep && n > 0 {
		b.Cases = make([]mir.SwitchCase, 0, n)
	}
	for i := 0; i < n && d.Err == nil; i++ {
		c := mir.SwitchCase{Value: d.Uvarint(), Target: mir.BlockID(d.U32())}
		if keep {
			b.Cases = append(b.Cases, c)
		}
	}
	n = d.Count(1)
	if keep && n > 0 {
		b.Spans = make([]source.Span, 0, n)
	}
	for i := 0; i < n && d.Err == nil; i++ {
		sp := r.span(d)
		if keep {
			b.Spans = append(b.Spans, sp)
		}
	}
	n = d.Count(7)
	if keep && n > 0 {
		b.Loops = make([]mir.LoopInfo, 0, n)
	}
	for i := 0; i < n && d.Err == nil; i++ {
		lp := mir.LoopInfo{Header: mir.BlockID(d.U32()), Exit: mir.BlockID(d.U32()),
			Holder: mir.LocalID(d.U32()), Label: r.sym(d), Span: mir.SpanID(d.U32()), FieldsOf: r.typ(d, 0), FieldsParam: d.U32()}
		if keep {
			b.Loops = append(b.Loops, lp)
		}
	}
	n = d.Count(2)
	if keep && n > 0 {
		b.Defers = make([]mir.DeferInfo, 0, n)
	}
	for i := 0; i < n && d.Err == nil; i++ {
		df := mir.DeferInfo{Onerr: d.Bool(), Span: mir.SpanID(d.U32())}
		if keep {
			b.Defers = append(b.Defers, df)
		}
	}
	b.Asms = r.readAsms(d, keep)
	return b
}
