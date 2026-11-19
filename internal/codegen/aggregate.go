// Why: struct, array, enum and error values are built in place at the byte
// offsets the layout gives, and var globals and str constants need IR
// globals of their own; this file keeps those value builders together.
//
// What: fnGen.aggregate (RvAggregate), repeat ([A; N]), widenErrors and
// widenErrorSide (CastWidenErrors), packGen.varGlobal and defineGlobal (var
// globals) and fnGen.strValue (str constants).
//
// How: every field is stored at its types.Layout offset, and tags and error
// codes come from the layout too. An aggregate whose operand may alias its
// destination is built in a temporary first. A var global is a global, never
// constant or unnamed_addr, defined once by the pack the driver assigns it to
// and declared everywhere else.

package codegen

import (
	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// aggregate builds an RvAggregate into its destination: the operands first,
// then the destination address; a temporary when an operand may alias the
// destination.
func (fg *fnGen) aggregate(p mir.Place, dt types.TypeID, rv *mir.Rvalue) {
	g, m := fg.g, fg.g.m
	ops := fg.b.Operands[rv.Ops.Start:rv.Ops.End()]
	vals := make([]value, len(ops))
	alias := false
	for i, o := range ops {
		vals[i] = fg.operand(o)
		alias = alias || fg.mayAlias(p, o) || (o.Kind != mir.OpConst && o.Kind != mir.OpImm && o.Place.Local == p.Local)
	}
	dr := g.reprOf(dt)
	if dr.kind == rZero {
		return
	}
	if dr.kind == rScalar {
		var v llvm.Value
		switch rv.Agg {
		case mir.AggEnum:
			v = m.ConstInt(dr.ssa, g.layout(dt).Variants[rv.Variant].Discr)
		case mir.AggSome:
			v = vals[0].v
		case mir.AggNone:
			v = m.Null()
		default:
			source.ICEf(fg.spanOf(fg.span), "codegen: aggregate %d of scalar %s", rv.Agg, g.tt.TypeString(dt, nil))
		}
		d, _ := fg.placeAddr(p)
		fg.storeScalar(d, dr, v)
		return
	}
	final, _ := fg.placeAddr(p)
	d := final
	if alias {
		d = fg.temp(dt)
	}
	at := func(off uint64) addr { return addr{v: fg.gep(d.v, off), align: alignAt(d.align, off)} }
	putFields := func(fields []types.FieldLayout, vs []value) {
		if len(fields) < len(vs) {
			source.ICEf(fg.spanOf(fg.span), "codegen: %d operands for %d fields of %s", len(vs), len(fields), g.tt.TypeString(dt, nil))
		}
		for i, v := range vs {
			fg.store(at(fields[i].Offset), v)
		}
	}
	tag := func(bits uint64) {
		l := g.layout(dt)
		fg.cur.Store(m.ConstInt(m.I(8*int(l.Tag.Size)), bits), at(l.Tag.Offset).v, alignAt(d.align, l.Tag.Offset))
	}
	switch rv.Agg {
	case mir.AggStruct, mir.AggRecord:
		putFields(g.layout(dt).Fields, vals)
	case mir.AggEnum:
		v := g.layout(dt).Variants[rv.Variant]
		tag(v.Discr)
		putFields(v.Fields, vals)
	case mir.AggError:
		tag(uint64(g.errorCode(rv.Item)))
		putFields(g.errorVariant(dt, rv.Item).Fields, vals)
	case mir.AggArray:
		es, _ := g.sizeAlign(g.tt.Get(dt).Elem)
		for i, v := range vals {
			fg.store(at(uint64(i)*es), v)
		}
	case mir.AggSome:
		tag(1)
		putFields(g.variant(dt, 1).Fields, vals)
	case mir.AggNone:
		tag(0)
	case mir.AggOk:
		tag(0)
		if len(vals) > 0 {
			putFields(g.variant(dt, 0).Fields, vals)
		}
	case mir.AggErr:
		// The error-set value into the error side of dt (one copy when
		// the offsets agree, else per error).
		fg.widenErrors(d, dt, vals[0])
	default:
		source.ICEf(fg.spanOf(fg.span), "codegen: aggregate kind %d", rv.Agg)
	}
	if alias {
		size, _ := g.sizeAlign(dt)
		fg.memcpy(final, d, size)
	}
}

// zeroBits reports an operand whose value is all zero bytes.
func (fg *fnGen) zeroBits(o mir.Operand) bool {
	switch o.Kind {
	case mir.OpImm:
		return o.Bits == 0
	case mir.OpConst:
		cv := fg.g.env.Consts().Get(o.Const)
		if len(cv.Relocs) > 0 {
			return false
		}
		for _, b := range cv.Bytes {
			if b != 0 {
				return false
			}
		}
		return true
	}
	return false
}

// repeat is [A; N]: llvm.memset for an all-zero element, else N stores (up
// to 8 unrolled) or a counted loop.
func (fg *fnGen) repeat(p mir.Place, dt types.TypeID, rv *mir.Rvalue) {
	g, m := fg.g, fg.g.m
	n := g.constLen(rv.Len)
	v := fg.operand(rv.A)
	if v.kind == rAgg && fg.mayAlias(p, rv.A) {
		tmp := fg.temp(v.t)
		fg.store(tmp, v)
		v.a = tmp
	}
	d, _ := fg.placeAddr(p)
	es, _ := g.sizeAlign(v.t)
	if es == 0 || n == 0 {
		return
	}
	if fg.zeroBits(rv.A) {
		fg.memset(d, 0, es*n)
		return
	}
	if n <= 8 {
		for i := uint64(0); i < n; i++ {
			fg.store(addr{v: fg.gep(d.v, i*es), align: alignAt(d.align, i*es)}, v)
		}
		return
	}
	i64 := m.I(64)
	ctr := fg.tempBytes(8, 8)
	fg.cur.Store(m.ConstInt(i64, 0), ctr.v, 8)
	loop, body, done := fg.newBlock(), fg.newBlock(), fg.newBlock()
	fg.cur.Br(loop)
	fg.cur = loop
	iv := fg.cur.Load(i64, ctr.v, 8)
	fg.cur.CondBr(fg.cur.ICmp(llvm.IULT, iv, m.ConstInt(i64, n)), body, done)
	fg.cur = body
	iv = fg.cur.Load(i64, ctr.v, 8)
	off := fg.cur.Bin(llvm.Mul, iv, m.ConstInt(i64, es), llvm.NUW|llvm.NSW)
	_, ea := g.sizeAlign(v.t)
	fg.store(addr{v: fg.cur.GEPBytes(d.v, off, true), align: min(ea, d.align)}, v)
	fg.cur.Store(fg.cur.Bin(llvm.Add, iv, m.ConstInt(i64, 1), llvm.NUW|llvm.NSW), ctr.v, 8)
	fg.cur.Br(loop)
	fg.cur = done
}

// widenErrors is CastWidenErrors (a subset error set, or E1!T, to a
// superset): the code and the present error's payload bytes, which must sit
// at the same offsets in both layouts, else each error is copied field by
// field after a switch on the code.
func (fg *fnGen) widenErrors(d addr, dt types.TypeID, src value) {
	fg.widen(d, dt, src, false)
}

// widenErrorSide copies the error side of src (an E1!T holding an error)
// into dt (an E2!void or E2!U with E1 a subset of E2): the code and the
// present error's payload; the ok variant is not copied (a spawn thunk's
// error into its scope's E!void).
func (fg *fnGen) widenErrorSide(d addr, dt types.TypeID, src value) {
	fg.widen(d, dt, src, true)
}

func (fg *fnGen) widen(d addr, dt types.TypeID, src value, errOnly bool) {
	g, m := fg.g, fg.g.m
	ls, ld := g.layout(src.t), g.layout(dt)
	sa := fg.addrOf(src)
	skip := func(v types.VariantLayout) bool {
		return errOnly && v.Discr == 0 && g.tt.Kind(src.t) == types.KErrorUnion
	}
	same := ls.Tag.Offset == ld.Tag.Offset && ls.Tag.Size == ld.Tag.Size
	for _, v := range ls.Variants {
		if skip(v) {
			continue
		}
		found := false
		for _, w := range ld.Variants {
			if w.Discr == v.Discr && len(w.Fields) == len(v.Fields) {
				found = true
				for i := range v.Fields {
					found = found && w.Fields[i].Offset == v.Fields[i].Offset
				}
			}
		}
		same = same && found
	}
	if same {
		size, _ := g.sizeAlign(src.t)
		if errOnly {
			ds, _ := g.sizeAlign(dt)
			size = min(size, ds)
		}
		fg.memcpy(d, sa, size)
		return
	}
	code := fg.cur.Load(m.I(32), fg.gep(sa.v, ls.Tag.Offset), alignAt(sa.align, ls.Tag.Offset), m.Metadata().NoUndef())
	fg.cur.Store(code, fg.gep(d.v, ld.Tag.Offset), alignAt(d.align, ld.Tag.Offset))
	done := fg.newBlock()
	var cases []llvm.Case
	type arm struct {
		b    *llvm.Block
		from types.VariantLayout
	}
	var arms []arm
	for _, v := range ls.Variants {
		if skip(v) {
			continue
		}
		b := fg.newBlock()
		cases = append(cases, llvm.Case{Val: m.ConstInt(m.I(32), v.Discr), Target: b})
		arms = append(arms, arm{b, v})
	}
	fg.cur.Switch(code, done, cases)
	for _, a := range arms {
		fg.cur = a.b
		var to types.VariantLayout
		for _, w := range ld.Variants {
			if w.Discr == a.from.Discr {
				to = w
			}
		}
		if len(to.Fields) != len(a.from.Fields) {
			source.ICEf(fg.spanOf(fg.span), "codegen: %s does not hold every error of %s", g.tt.TypeString(dt, nil), g.tt.TypeString(src.t, nil))
		}
		for i, f := range a.from.Fields {
			fv := fg.loadAt(addr{v: fg.gep(sa.v, f.Offset), align: alignAt(sa.align, f.Offset)}, f.Type)
			fg.store(addr{v: fg.gep(d.v, to.Fields[i].Offset), align: alignAt(d.align, to.Fields[i].Offset)}, fv)
		}
		fg.cur.Br(done)
	}
	fg.cur = done
}

// varGlobal is the address of a var global: defined, with its evaluated
// value, by the pack the driver assigns it to (Unit.Globals), else declared.
// Both are memoized in g.globals: a pack declares or defines each global
// once however many of its members reference it.
func (g *packGen) varGlobal(item types.ItemID, d *types.GlobalDecl) llvm.Value {
	if v, ok := g.globals[item]; ok {
		return v
	}
	if d.Extern {
		if gl, ok := g.unitGlobal(item); ok && gl.Define && gl.Pack == g.p {
			return g.defineExternGlobal(item, d) // an export extern var
		}
		return g.externGlobal(item, d) // the linker defines it
	}
	it := g.items.Get(item)
	name := "mk." + it.Key.String()
	for _, gl := range g.u.Globals {
		if gl.Item != item {
			continue
		}
		if gl.Pack == g.p {
			return g.defineGlobal(item, d)
		}
		if gl.Local {
			source.ICEf(it.Span, "codegen: var global %s is Local to pack %d but referenced from pack %d", it.Key, gl.Pack, g.p)
		}
		vis := llvm.Hidden
		if g.cfg.Target.OS == types.OSWindows {
			vis = llvm.Default
		}
		r := g.reprOf(d.Type)
		v := g.m.GlobalRef(g.m.AddGlobal(name, llvm.GlobalAttrs{Linkage: llvm.External, Visibility: vis,
			DSOLocal: true, Type: g.m.Array(max(r.size, 1), g.m.I(8))}))
		g.globals[item] = v
		return v
	}
	source.ICEf(it.Span, "codegen: var global %s is in no pack (Unit.Globals)", it.Key)
	return llvm.Value{}
}

// defineGlobal defines var global item in this pack once: a global, never
// constant or unnamed_addr.
func (g *packGen) defineGlobal(item types.ItemID, d *types.GlobalDecl) llvm.Value {
	if v, ok := g.globals[item]; ok {
		return v
	}
	if d.Extern {
		if gl, ok := g.unitGlobal(item); ok && gl.Define && gl.Pack == g.p {
			return g.defineExternGlobal(item, d) // an export extern var
		}
		return g.externGlobal(item, d) // the linker defines it
	}
	it := g.items.Get(item)
	m := g.m
	r := g.reprOf(d.Type)
	var init llvm.Value
	if d.HasValue {
		cv := g.env.Consts().Get(d.Value)
		init = g.constInit(cv)
	} else {
		init = m.ZeroInit(m.Array(max(r.size, 1), m.I(8)))
	}
	lk, vis, dso := llvm.External, llvm.Hidden, true
	if g.cfg.Target.OS == types.OSWindows {
		vis = llvm.Default
	}
	for _, gl := range g.u.Globals {
		if gl.Item == item && gl.Local && g.cfg.Profile.Optimized() && d.Section == "" {
			lk, vis, dso = llvm.Internal, llvm.Default, false // a section item is never internal
		}
	}
	align, sec := r.align, d.Section
	if sec == "" && g.bigStatic(align) { // over a page on Windows; a section clause keeps the user's placement
		init, sec, align = g.padStatic(init, r.size, align), staticSection(align, false), pageBytes
	}
	gl := m.AddGlobal("mk."+it.Key.String(), llvm.GlobalAttrs{Linkage: lk, Visibility: vis, DSOLocal: dso,
		Align: align, Type: init.Ty, Init: init, Section: sec})
	v := m.GlobalRef(gl)
	if d.Section != "" {
		m.Used(v) // kept by the optimizer and --gc-sections
	} else if sec != "" {
		m.CompilerUsed(v) // pinned (staticPin)
	}
	g.globals[item] = v
	return v
}

// strValue is a str constant: a private { ptr, i64 } over the bytes.
func (fg *fnGen) strValue(s string) value {
	g, m := fg.g, fg.g.m
	init := m.ConstStruct(false, g.bytesGlobal([]byte(s)), m.ConstInt(m.I(64), uint64(len(s))))
	g.nconst++
	gl := m.AddGlobal("mk.c.s"+itoa(g.nconst), llvm.GlobalAttrs{Linkage: llvm.Private, Constant: true,
		UnnamedAddr: true, Align: 8, Type: init.Ty, Init: init})
	return value{t: types.Str, kind: rAgg, a: addr{v: m.GlobalRef(gl), align: 8}}
}
