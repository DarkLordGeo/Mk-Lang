// Why: a compile-time evaluation must compute the same bits as the native
// program and trap on exactly the operations native code traps on, on the
// target's widths and in every build profile.
//
// What: binaryOp, intBinary, floatBinary, floatCompare, unary and cast, the
// scalar arithmetic of the evaluator, with mir.BinOp's trap semantics, plus
// mask and intFits.
//
// How: a value is a uint64 bit pattern zero-extended from its width; signed
// operations sign-extend first. Overflow, division by zero and out-of-range
// checked casts return a types.PanicCode. Floats are converted back to their
// width after every operation, so no multiply and add fuse, and every NaN an
// operation makes is canonical.

package eval

import (
	"math"
	"math/bits"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

func mask(width int) uint64 {
	if width >= 8 {
		return ^uint64(0)
	}
	return 1<<(8*uint(width)) - 1
}

// binaryOp computes a op b for operands of type ti (b of type bt, which
// differs only for shifts). Values are bit patterns zero-extended from their
// width. A trap returns its panic code.
func binaryOp(op mir.BinOp, ti, bt *tinfo, a, b uint64) (uint64, types.PanicCode) {
	switch ti.class {
	case cInt:
		return intBinary(op, ti.width, ti.signed, bt, a, b)
	case cFloat:
		return floatBinary(op, ti.width, a, b), types.PanicNone
	case cBool, cChar, cPtr:
		switch op {
		case mir.Eq:
			return b2u(a == b), types.PanicNone
		case mir.Ne:
			return b2u(a != b), types.PanicNone
		}
		if ti.class == cBool {
			switch op {
			case mir.BitAnd:
				return a & b, types.PanicNone
			case mir.BitOr:
				return a | b, types.PanicNone
			case mir.BitXor:
				return a ^ b, types.PanicNone
			}
		}
		if ti.class == cPtr {
			// Ordering only within one allocation, or null with null.
			if ptrAlloc(a) != ptrAlloc(b) {
				refuse(types.RefuseRawAccess, "")
			}
			a, b = uint64(ptrOff(a)), uint64(ptrOff(b))
		}
		if ti.class == cChar || ti.class == cPtr {
			switch op {
			case mir.Lt:
				return b2u(a < b), types.PanicNone
			case mir.Le:
				return b2u(a <= b), types.PanicNone
			case mir.Gt:
				return b2u(a > b), types.PanicNone
			case mir.Ge:
				return b2u(a >= b), types.PanicNone
			}
		}
	}
	source.ICEf(source.Span{}, "eval: binary %d on %s", op, ti.ty.Kind)
	return 0, 0
}

func b2u(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}

func intBinary(op mir.BinOp, w int, signed bool, bt *tinfo, a, b uint64) (uint64, types.PanicCode) {
	mk := mask(w)
	bitsN := 8 * uint(w)
	sa, sb := int64(signExtend(a, w)), int64(signExtend(b, w))
	switch op {
	case mir.Add, mir.Sub, mir.Mul:
		if signed {
			if w < 8 {
				var r int64
				switch op {
				case mir.Add:
					r = sa + sb
				case mir.Sub:
					r = sa - sb
				default:
					r = sa * sb // a 32x32 product fits in 63 bits
				}
				lo, hi := -int64(1)<<(bitsN-1), int64(1)<<(bitsN-1)-1
				if r < lo || r > hi {
					return 0, types.PanicOverflow
				}
				return uint64(r) & mk, types.PanicNone
			}
			switch op {
			case mir.Add:
				r := sa + sb
				if (sa^r)&(sb^r) < 0 { // overflow: r's sign differs from both operands'
					return 0, types.PanicOverflow
				}
				return uint64(r), types.PanicNone
			case mir.Sub:
				r := sa - sb
				if (sa^sb)&(sa^r) < 0 {
					return 0, types.PanicOverflow
				}
				return uint64(r), types.PanicNone
			}
			hi, lo := bits.Mul64(a, b)
			if sa < 0 {
				hi -= b
			}
			if sb < 0 {
				hi -= a
			}
			want := uint64(0)
			if int64(lo) < 0 {
				want = ^uint64(0)
			}
			if hi != want {
				return 0, types.PanicOverflow
			}
			return lo, types.PanicNone
		}
		if w < 8 {
			var r uint64
			switch op {
			case mir.Add:
				r = a + b
			case mir.Sub:
				if b > a {
					return 0, types.PanicOverflow
				}
				r = a - b
			default:
				r = a * b
			}
			if r > mk {
				return 0, types.PanicOverflow
			}
			return r, types.PanicNone
		}
		switch op {
		case mir.Add:
			r, carry := bits.Add64(a, b, 0)
			if carry != 0 {
				return 0, types.PanicOverflow
			}
			return r, types.PanicNone
		case mir.Sub:
			r, borrow := bits.Sub64(a, b, 0)
			if borrow != 0 {
				return 0, types.PanicOverflow
			}
			return r, types.PanicNone
		}
		hi, lo := bits.Mul64(a, b)
		if hi != 0 {
			return 0, types.PanicOverflow
		}
		return lo, types.PanicNone
	case mir.AddWrap, mir.AddNoWrap: // proved in range by its producer, so it runs as AddWrap
		return (a + b) & mk, types.PanicNone
	case mir.SubWrap, mir.SubNoWrap: // as AddNoWrap
		return (a - b) & mk, types.PanicNone
	case mir.MulWrap, mir.MulNoWrap:
		return (a * b) & mk, types.PanicNone
	case mir.Div, mir.Rem:
		if b == 0 {
			return 0, types.PanicDivideByZero
		}
		if signed {
			minV := -int64(1) << (bitsN - 1)
			if sa == minV && sb == -1 {
				return 0, types.PanicDivideByZero
			}
			if op == mir.Div {
				return uint64(sa/sb) & mk, types.PanicNone
			}
			return uint64(sa%sb) & mk, types.PanicNone
		}
		if op == mir.Div {
			return a / b, types.PanicNone
		}
		return a % b, types.PanicNone
	case mir.Shl, mir.Shr:
		amt := b
		if bt.signed && int64(signExtend(b, bt.width)) < 0 {
			return 0, types.PanicShiftAmount
		}
		if amt >= uint64(bitsN) {
			return 0, types.PanicShiftAmount
		}
		if op == mir.Shl {
			return (a << amt) & mk, types.PanicNone
		}
		if signed {
			return uint64(sa>>amt) & mk, types.PanicNone
		}
		return a >> amt, types.PanicNone
	case mir.BitAnd:
		return a & b, types.PanicNone
	case mir.BitOr:
		return a | b, types.PanicNone
	case mir.BitXor:
		return a ^ b, types.PanicNone
	case mir.Eq:
		return b2u(a == b), types.PanicNone
	case mir.Ne:
		return b2u(a != b), types.PanicNone
	}
	var lt, eq bool
	if signed {
		lt, eq = sa < sb, sa == sb
	} else {
		lt, eq = a < b, a == b
	}
	switch op {
	case mir.Lt:
		return b2u(lt), types.PanicNone
	case mir.Le:
		return b2u(lt || eq), types.PanicNone
	case mir.Gt:
		return b2u(!lt && !eq), types.PanicNone
	case mir.Ge:
		return b2u(!lt), types.PanicNone
	}
	source.ICEf(source.Span{}, "eval: integer binary %d", op)
	return 0, 0
}

// f32 and f64 results are converted explicitly after every operation, so no
// multiply and add can fuse, and every NaN is canonical.
func f32of(x uint64) float32 { return math.Float32frombits(uint32(x)) }
func f64of(x uint64) float64 { return math.Float64frombits(x) }

func f32bits(f float32) uint64 {
	if f != f {
		return uint64(types.CanonicalNaN32)
	}
	return uint64(math.Float32bits(f))
}

func f64bits(f float64) uint64 {
	if f != f {
		return types.CanonicalNaN64
	}
	return math.Float64bits(f)
}

func floatBinary(op mir.BinOp, w int, a, b uint64) uint64 {
	if w == 4 {
		x, y := f32of(a), f32of(b)
		switch op {
		case mir.Add:
			return f32bits(float32(x + y))
		case mir.Sub:
			return f32bits(float32(x - y))
		case mir.Mul:
			return f32bits(float32(x * y))
		case mir.Div:
			return f32bits(float32(x / y))
		case mir.Rem:
			return f32bits(float32(math.Mod(float64(x), float64(y))))
		}
		return floatCompare(op, float64(x), float64(y))
	}
	x, y := f64of(a), f64of(b)
	switch op {
	case mir.Add:
		return f64bits(float64(x + y))
	case mir.Sub:
		return f64bits(float64(x - y))
	case mir.Mul:
		return f64bits(float64(x * y))
	case mir.Div:
		return f64bits(float64(x / y))
	case mir.Rem:
		return f64bits(math.Mod(x, y))
	}
	return floatCompare(op, x, y)
}

func floatCompare(op mir.BinOp, x, y float64) uint64 {
	switch op {
	case mir.Eq:
		return b2u(x == y)
	case mir.Ne:
		return b2u(x != y)
	case mir.Lt:
		return b2u(x < y)
	case mir.Le:
		return b2u(x <= y)
	case mir.Gt:
		return b2u(x > y)
	case mir.Ge:
		return b2u(x >= y)
	}
	source.ICEf(source.Span{}, "eval: float binary %d", op)
	return 0
}

// unary computes op a on type ti.
func unary(op mir.UnOp, ti *tinfo, a uint64) (uint64, types.PanicCode) {
	switch op {
	case mir.Not:
		if ti.class == cBool {
			return a ^ 1, types.PanicNone
		}
		return ^a & mask(ti.width), types.PanicNone
	case mir.BitNot:
		return ^a & mask(ti.width), types.PanicNone
	case mir.Neg:
		switch ti.class {
		case cFloat:
			// Negation flips the sign bit only, a NaN's included, as
			// native fneg does: it is not arithmetic, so its NaN
			// is not made canonical.
			if ti.width == 4 {
				return (a ^ 1<<31) & mask(32), types.PanicNone
			}
			return a ^ 1<<63, types.PanicNone
		case cInt:
			if !ti.signed {
				if a != 0 {
					return 0, types.PanicOverflow
				}
				return 0, types.PanicNone
			}
			if a == 1<<(8*uint(ti.width)-1) {
				return 0, types.PanicOverflow
			}
			return uint64(-int64(signExtend(a, ti.width))) & mask(ti.width), types.PanicNone
		}
	}
	source.ICEf(source.Span{}, "eval: unary %d on %s", op, ti.ty.Kind)
	return 0, 0
}

// cast converts the scalar a of type from to type to.
func cast(k mir.CastKind, from, to *tinfo, a uint64) (uint64, types.PanicCode) {
	switch {
	case isIntLike(from) && isIntLike(to):
		val := a // the source value: sign-extended when signed
		if from.signed {
			val = signExtend(a, from.width)
		}
		neg := from.signed && int64(val) < 0
		if k == mir.CastChecked && !intFits(val, neg, to) {
			return 0, types.PanicCast
		}
		if to.class == cChar && k == mir.CastChecked && (val >= 0xd800 && val <= 0xdfff) {
			return 0, types.PanicCast
		}
		return val & mask(to.width), types.PanicNone
	case from.class == cFloat && to.class == cFloat:
		if from.width == to.width {
			return a, types.PanicNone
		}
		if to.width == 8 {
			return f64bits(float64(f32of(a))), types.PanicNone
		}
		// Round to nearest even; a checked cast panics only when a
		// finite value rounds to an infinity (IEEE 754 overflow). NaN and
		// the infinities pass through.
		x := f64of(a)
		r := float32(x)
		if k == mir.CastChecked && math.IsInf(float64(r), 0) && !math.IsInf(x, 0) {
			return 0, types.PanicCast
		}
		return f32bits(r), types.PanicNone
	case isIntLike(from) && to.class == cFloat:
		var f64 float64
		var f32 float32
		if from.signed {
			v := int64(signExtend(a, from.width))
			f64, f32 = float64(v), float32(v)
		} else {
			f64, f32 = float64(a), float32(a)
		}
		if to.width == 8 {
			return f64bits(f64), types.PanicNone
		}
		return f32bits(f32), types.PanicNone
	case from.class == cFloat && isIntLike(to):
		x := f64of(a)
		if from.width == 4 {
			x = float64(f32of(a))
		}
		if x != x {
			if k == mir.CastChecked {
				return 0, types.PanicCast
			}
			return 0, types.PanicNone
		}
		t := math.Trunc(x)
		bitsN := 8 * uint(to.width)
		var lo, hi float64 // the open-at-hi range [lo, hi)
		if to.signed {
			lo, hi = -math.Ldexp(1, int(bitsN-1)), math.Ldexp(1, int(bitsN-1))
		} else {
			lo, hi = 0, math.Ldexp(1, int(bitsN))
		}
		if t < lo || t >= hi {
			if k == mir.CastChecked {
				return 0, types.PanicCast
			}
			return 0, types.PanicNone
		}
		if to.signed {
			return uint64(int64(t)) & mask(to.width), types.PanicNone
		}
		return uint64(t) & mask(to.width), types.PanicNone
	}
	if from.width == to.width {
		return a, types.PanicNone
	}
	source.ICEf(source.Span{}, "eval: cast from %s to %s", from.ty.Kind, to.ty.Kind)
	return 0, 0
}

func isIntLike(ti *tinfo) bool {
	return ti.class == cInt || ti.class == cBool || ti.class == cChar
}

// intFits reports whether the source value v (sign-extended, negative
// when neg) fits the integer type to; a char holds at most 0x10ffff.
func intFits(v uint64, neg bool, to *tinfo) bool {
	bitsN := 8 * uint(to.width)
	if neg {
		if !to.signed {
			return false
		}
		return int64(v) >= -int64(1)<<(bitsN-1)
	}
	if to.class == cChar {
		return v <= 0x10ffff
	}
	if to.signed {
		return v <= uint64(1)<<(bitsN-1)-1
	}
	return v <= mask(to.width)
}
