// Why: Mk's numeric conversions, checked casts, discriminant reads and
// slicing each have one exact IR form, and a checked cast must trap on every
// value the target type cannot hold.
//
// What: fnGen.cast and convert (RvCast), checkedCast (Cast[T](x)),
// discriminant (RvDiscriminant), slice (RvSlice) and intView.
//
// How: checkedCast branches each failing test to a cold PanicCast trap; f64
// to f32 panics only when a finite value rounds to an infinity. The tag load
// of a discriminant carries !noundef but no !range. A slice is unchecked for
// bounds here (lower's MIR checks them), except the str boundary check.

package codegen

import (
	"math"
	"math/big"

	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// intView is a scalar read as an integer: its width and signedness. bool
// and char are unsigned; an enum tag has its declared tag type's
// signedness, unsigned when the enum declares none.
func intView(r *repr) (int, bool) {
	return r.ssa.Bits(), r.signed && (r.class == sInt || r.class == sTag)
}

func (fg *fnGen) cast(kind mir.CastKind, a value, dt types.TypeID) llvm.Value {
	g := fg.g
	sr, dr := g.reprOf(a.t), g.reprOf(dt)
	if sr.kind != rScalar || dr.kind != rScalar {
		source.ICEf(fg.spanOf(fg.span), "codegen: cast from %s to %s", g.tt.TypeString(a.t, nil), g.tt.TypeString(dt, nil))
	}
	switch kind {
	case mir.CastLossless, mir.CastTrunc:
		return fg.convert(a.v, sr, dr)
	case mir.CastChecked:
		return fg.checkedCast(a.v, sr, dr)
	}
	source.ICEf(fg.spanOf(fg.span), "codegen: cast kind %d in an instance body", kind)
	return llvm.Value{}
}

// convert is an unchecked conversion: integer extension or truncation by the
// source's signedness, int to float, float widening or narrowing, float to
// int (only after a range test), pointer to pointer.
func (fg *fnGen) convert(v llvm.Value, sr, dr *repr) llvm.Value {
	switch {
	case sr.ssa == dr.ssa:
		return v
	case sr.class == sFloat && dr.class == sFloat:
		if dr.size > sr.size {
			return fg.cur.Cast(llvm.FPExt, v, dr.ssa)
		}
		return fg.cur.Cast(llvm.FPTrunc, v, dr.ssa)
	case sr.class == sFloat:
		if _, s := intView(dr); s {
			return fg.cur.Cast(llvm.FPToSI, v, dr.ssa)
		}
		return fg.cur.Cast(llvm.FPToUI, v, dr.ssa)
	case dr.class == sFloat:
		if _, s := intView(sr); s {
			return fg.cur.Cast(llvm.SIToFP, v, dr.ssa)
		}
		return fg.cur.Cast(llvm.UIToFP, v, dr.ssa)
	case sr.ssa.IsPtr() || dr.ssa.IsPtr():
		source.ICEf(fg.spanOf(fg.span), "codegen: conversion between a pointer and %s", dr.ssa)
	}
	_, s := intView(sr)
	return fg.intTo(v, s, dr.ssa)
}

// f32RoundsToInf is the bits of the least f64 that rounds to an f32
// infinity: MaxF32 (2^128 - 2^104) plus half its ulp, 2^128 - 2^103.
const f32RoundsToInf = 0x47EFFFFFF0000000

// checkedCast is Cast[T](x): every failing test branches to a
// PanicCast trap.
func (fg *fnGen) checkedCast(v llvm.Value, sr, dr *repr) llvm.Value {
	m := fg.g.m
	switch {
	case dr.class == sChar:
		u := fg.checkedCast(v, sr, &repr{kind: rScalar, class: sInt, ssa: m.I(32), mem: m.I(32), size: 4, align: 4})
		over := fg.cur.ICmp(llvm.IUGT, u, m.ConstInt(m.I(32), 0x10FFFF))
		sur := fg.cur.ICmp(llvm.IULT, fg.cur.Bin(llvm.Sub, u, m.ConstInt(m.I(32), 0xD800), 0), m.ConstInt(m.I(32), 0x800))
		fg.trap(fg.cur.Bin(llvm.Or, over, sur, 0), types.PanicCast)
		return u
	case dr.class == sTag:
		tag := fg.checkedCast(v, sr, &repr{kind: rScalar, class: sInt, ssa: dr.ssa, mem: dr.mem, size: dr.size, align: dr.align, signed: dr.signed})
		trapB, cont := fg.newBlock(), fg.newBlock()
		var cases []llvm.Case
		seen := map[uint64]bool{}
		for _, d := range dr.discrs {
			if !seen[d] {
				seen[d] = true
				cases = append(cases, llvm.Case{Val: m.ConstInt(dr.ssa, d), Target: cont})
			}
		}
		// The default edge is the trap: weight 1 against 1048575 per case,
		// like every other check's branch.
		ws := []uint32{1}
		for range cases {
			ws = append(ws, 1048575)
		}
		fg.cur.Switch(tag, trapB, cases, llvm.Prof(m.Metadata().BranchWeights(ws...)))
		fg.cur = trapB
		fg.panicCall(types.PanicCast, nil)
		fg.cur = cont
		return tag
	case sr.class == sFloat && dr.class == sFloat:
		if dr.size >= sr.size {
			return fg.convert(v, sr, dr)
		}
		// f64 to f32: panic iff a finite value rounds to an infinity.
		// Rounding to nearest even takes |v| to an infinity exactly when
		// |v| >= f32RoundsToInf (MaxF32 plus half its ulp, the midpoint,
		// which ties to the even infinity), so the hot path is one ordered
		// compare of |v| and one branch: a NaN fails it and converts. Past
		// it, on the cold path, only the infinity itself passes through; any
		// other v traps with PanicCast at the cast's site, and the cast has
		// no other trap.
		ax := fg.cur.CallFunc(m.IntrinsicFn("fabs", sr.ssa), []llvm.Value{v})
		cold, conv := fg.newBlock(), fg.newBlock()
		fg.cur.CondBr(fg.cur.FCmp(llvm.FOGE, ax, m.ConstF64(f32RoundsToInf)), cold, conv, fg.trapWeights(true))
		fg.cur = cold
		fg.trap(fg.cur.FCmp(llvm.FONE, ax, m.ConstF64(math.Float64bits(math.Inf(1)))), types.PanicCast)
		fg.cur.Br(conv)
		fg.cur = conv
		return fg.cur.Cast(llvm.FPTrunc, v, dr.ssa)
	case sr.class == sFloat:
		n, signed := intView(dr)
		fc := func(x float64) llvm.Value {
			if sr.size == 4 {
				return m.ConstF32(math.Float32bits(float32(x)))
			}
			return m.ConstF64(math.Float64bits(x))
		}
		upper := math.Ldexp(1, n)
		if signed {
			upper = math.Ldexp(1, n-1)
		}
		// One compare and one branch on the hot path (floatFits); the two
		// exact tests below, each with its trap, run only on the cold path.
		conv := fg.floatFits(v, sr, signed, upper)
		fg.trap(fg.cur.FCmp(llvm.FUGE, v, fc(upper)), types.PanicCast)
		switch {
		case !signed:
			fg.trap(fg.cur.FCmp(llvm.FULE, v, fc(-1)), types.PanicCast)
		case sr.size == 8 && n <= 32 || sr.size == 4 && n <= 16:
			fg.trap(fg.cur.FCmp(llvm.FULE, v, fc(-math.Ldexp(1, n-1)-1)), types.PanicCast)
		default:
			fg.trap(fg.cur.FCmp(llvm.FULT, v, fc(-math.Ldexp(1, n-1))), types.PanicCast)
		}
		fg.cur.Br(conv)
		fg.cur = conv
		return fg.convert(v, sr, dr)
	case dr.class == sFloat:
		return fg.convert(v, sr, dr)
	}
	sb, ss := intView(sr)
	tb, ts := intView(dr)
	bound := func(bits int, signed, hi bool) *big.Int {
		x := new(big.Int)
		switch {
		case signed && hi:
			x.Lsh(big.NewInt(1), uint(bits-1)).Sub(x, big.NewInt(1))
		case signed:
			x.Lsh(big.NewInt(1), uint(bits-1)).Neg(x)
		case hi:
			x.Lsh(big.NewInt(1), uint(bits)).Sub(x, big.NewInt(1))
		}
		return x
	}
	smax, smin := bound(sb, ss, true), bound(sb, ss, false)
	tmax, tmin := bound(tb, ts, true), bound(tb, ts, false)
	asConst := func(x *big.Int) llvm.Value {
		if x.Sign() < 0 {
			return m.ConstSInt(sr.ssa, x.Int64())
		}
		return m.ConstInt(sr.ssa, x.Uint64())
	}
	if smax.Cmp(tmax) > 0 {
		p := llvm.IUGT
		if ss {
			p = llvm.ISGT
		}
		fg.trap(fg.cur.ICmp(p, v, asConst(tmax)), types.PanicCast)
	}
	if smin.Cmp(tmin) < 0 {
		fg.trap(fg.cur.ICmp(llvm.ISLT, v, asConst(tmin)), types.PanicCast)
	}
	return fg.intTo(v, ss, dr.ssa)
}

// floatFits is the hot path of a checked float-to-integer cast: one
// compare and one branch, to the returned block (the conversion) when v
// surely fits, else to a cold block, where emission continues: there the
// caller's two exact tests decide, each with its trap, and then branch to
// the returned block. It lets through only values that pass both exact
// tests, so which inputs trap, and the trap's code, site and order among
// the function's other traps, are the exact tests' own:
//   - signed (upper is 2^(n-1)): |v| < upper, ordered. Such a v is in
//     (-2^(n-1), 2^(n-1)) and truncates into the type. NaN, the
//     infinities and |v| >= upper go cold, where -2^(n-1) (and from f64 to
//     at most 32 bits, or from f32 to at most 16, everything above
//     -2^(n-1)-1) still converts.
//   - unsigned (upper is 2^n): v's bits below upper's, read as unsigned
//     integers. Floats with the sign bit clear order as their bits, +Inf
//     and the NaNs above every finite one, so this is +0 <= v < 2^n. Every
//     float with the sign bit set goes cold, where -0 and (-1, 0) still
//     convert to 0.
//
// On x86_64 at -O2 the hot path is andpd, ucomisd and one jcc (signed) or
// movq, cmp and one jcc (unsigned), where the two exact tests alone are two
// ucomisd and two jcc; an or of the two tests in one branch is no better,
// since the backend splits it back into two (TestFloatCastOneBranch).
func (fg *fnGen) floatFits(v llvm.Value, sr *repr, signed bool, upper float64) *llvm.Block {
	m := fg.g.m
	var slow llvm.Value
	if signed {
		c := m.ConstF64(math.Float64bits(upper))
		if sr.size == 4 {
			c = m.ConstF32(math.Float32bits(float32(upper)))
		}
		a := fg.cur.CallFunc(m.IntrinsicFn("fabs", sr.ssa), []llvm.Value{v})
		slow = fg.cur.FCmp(llvm.FUGE, a, c)
	} else {
		w := m.I(8 * int(sr.size))
		bits := math.Float64bits(upper)
		if sr.size == 4 {
			bits = uint64(math.Float32bits(float32(upper)))
		}
		slow = fg.cur.ICmp(llvm.IUGE, fg.cur.Cast(llvm.BitCast, v, w), m.ConstInt(w, bits))
	}
	cold, conv := fg.newBlock(), fg.newBlock()
	fg.cur.CondBr(slow, cold, conv, fg.trapWeights(true))
	fg.cur = cold
	return conv
}

// discriminant is RvDiscriminant (u64): an enum's tag, ptr != null for a
// niche optional, the some byte of another optional, the code of E!T or an
// error-set value. The tag load carries !noundef only.
func (fg *fnGen) discriminant(p mir.Place) llvm.Value {
	g, m := fg.g, fg.g.m
	a, t := fg.readAddr(p)
	r := g.reprOf(t)
	i64 := m.I(64)
	if r.kind == rScalar {
		switch r.class {
		case sTag, sBool:
			return fg.cur.Cast(llvm.ZExt, fg.loadScalar(a, r, true), i64)
		case sRaw:
			pv := fg.cur.Load(m.Ptr(), a.v, a.align)
			return fg.cur.Cast(llvm.ZExt, fg.cur.ICmp(llvm.INE, pv, m.Null()), i64)
		}
		source.ICEf(fg.spanOf(fg.span), "codegen: discriminant of %s", g.tt.TypeString(t, nil))
	}
	l := g.layout(t)
	if !l.HasTag || l.Tag.Size == 0 {
		source.ICEf(fg.spanOf(fg.span), "codegen: discriminant of %s, which has no tag", g.tt.TypeString(t, nil))
	}
	ta := addr{v: fg.gep(a.v, l.Tag.Offset), align: alignAt(a.align, l.Tag.Offset)}
	tv := fg.cur.Load(m.I(8*int(l.Tag.Size)), ta.v, ta.align, m.Metadata().NoUndef())
	return fg.intTo(tv, false, i64)
}

// slice is RvSlice: the base's data pointer and length (an array's address
// and N; a slice's or str's two words), lo defaulting to 0 and hi to the
// length, unchecked for bounds (lower's explicit MIR checks them);
// for str only, each end must be the length or not a continuation byte
// (PanicStrBoundary). The result is { base + lo*stride, hi - lo }.
func (fg *fnGen) slice(rv *mir.Rvalue) (llvm.Value, llvm.Value) {
	g, m := fg.g, fg.g.m
	i64 := m.I(64)
	a, t := fg.placeAddr(rv.Place)
	ty := g.tt.Get(t)
	var base, n llvm.Value
	var elem types.TypeID
	if ty.Kind == types.KArray {
		base, n, elem = a.v, m.ConstInt(i64, g.constLen(ty.Len)), ty.Elem
	} else {
		base = fg.cur.Load(m.Ptr(), a.v, alignAt(a.align, 0), m.Metadata().NonNull(), m.Metadata().NoUndef())
		n = fg.loadLen(fg.gep(a.v, 8), alignAt(a.align, 8), t)
		elem = types.U8
		if ty.Kind != types.KStr {
			elem = ty.Elem
		}
	}
	idx := func(o mir.Operand) llvm.Value {
		v := fg.operand(o)
		return fg.intTo(v.v, g.reprOf(v.t).signed, i64)
	}
	lo, hi := m.ConstInt(i64, 0), n
	if rv.HasLo {
		lo = idx(rv.A)
	}
	if rv.HasHi {
		hi = idx(rv.B)
	}
	if ty.Kind == types.KStr {
		for _, at := range []llvm.Value{lo, hi} {
			chk, cont := fg.newBlock(), fg.newBlock()
			fg.cur.CondBr(fg.cur.ICmp(llvm.IULT, at, n), chk, cont)
			fg.cur = chk
			b := fg.cur.Load(m.I(8), fg.cur.GEP8(base, at, llvm.GEPInbounds|llvm.GEPNUW), 1)
			cb := fg.cur.ICmp(llvm.IEQ, fg.cur.Bin(llvm.And, b, m.ConstInt(m.I(8), 0xC0), 0), m.ConstInt(m.I(8), 0x80))
			fg.trap(cb, types.PanicStrBoundary)
			fg.cur.Br(cont)
			fg.cur = cont
		}
	}
	es, _ := g.sizeAlign(elem)
	ptr := base
	if es > 0 {
		off := fg.cur.Bin(llvm.Mul, lo, m.ConstInt(i64, es), llvm.NUW|llvm.NSW)
		// 0 <= lo <= len (lower checked or proved it): inbounds nuw.
		ptr = fg.cur.GEP8(base, off, llvm.GEPInbounds|llvm.GEPNUW)
	}
	return ptr, fg.cur.Bin(llvm.Sub, hi, lo, llvm.NUW|llvm.NSW)
}
