// Why: std.math's intrinsics (std/math/intrinsics.mk: overflow-flag
// arithmetic, WrapI32, WrapU32, DivWide, CastSat) lower to a few machine
// instructions natively and need the same results at compile time.
//
// What: the std.math intrinsics, registered in init.
//
// How: each runs its definition. The overflow-flag forms take T's width and
// sign from the type argument; WrapI32 and WrapU32 read the f64's bits
// (exponent and mantissa, the slow path codegen also has), so the tests'
// oracle, ECMA-262's ToInt32 and ToUint32 steps over float arithmetic, is
// a second derivation; DivWide panics with divide-by-zero for d == 0 and
// with overflow for hi >= d (a quotient that does not fit, as x86_64's
// `div` faults for both).

package eval

import (
	"math"
	"math/bits"

	"mk/internal/mir"
	"mk/internal/types"
)

// ovfOp is AddOverflow's, SubOverflow's or MulOverflow's arithmetic.
type ovfOp uint8

const (
	ovfAdd ovfOp = iota
	ovfSub
	ovfMul
)

// sext is the low w bytes of x sign-extended to 64 bits.
func sext(x uint64, w int) int64 {
	s := 64 - 8*uint(w)
	return int64(x<<s) >> s
}

// overflowOp is op over a and b, integers of w bytes (signed or not) as
// zero-extended bit patterns: the result wrapped to w bytes, and whether
// the exact result does not fit.
func overflowOp(op ovfOp, a, b uint64, w int, signed bool) (uint64, bool) {
	a, b = a&mask(w), b&mask(w)
	if signed {
		x, y := sext(a, w), sext(b, w)
		var r int64
		var ov bool
		switch op {
		case ovfAdd:
			r = x + y
			ov = (x >= 0) == (y >= 0) && (r >= 0) != (x >= 0)
		case ovfSub:
			r = x - y
			ov = (x >= 0) != (y >= 0) && (r >= 0) != (x >= 0)
		default:
			r = x * y
			ov = x != 0 && (r/x != y || x == -1 && y == math.MinInt64)
		}
		if w < 8 {
			lim := int64(1) << (8*uint(w) - 1)
			ov = r < -lim || r >= lim // exact in int64 below 64 bits
		}
		return uint64(r) & mask(w), ov
	}
	var r uint64
	var ov bool
	switch op {
	case ovfAdd:
		var c uint64
		r, c = bits.Add64(a, b, 0)
		ov = c != 0
	case ovfSub:
		var c uint64
		r, c = bits.Sub64(a, b, 0)
		ov = c != 0
	default:
		var hi uint64
		hi, r = bits.Mul64(a, b)
		ov = hi != 0
	}
	if w < 8 {
		ov = ov || r > mask(w)
	}
	return r & mask(w), ov
}

// wrap32 is ToUint32 of the f64 with bits x, from its exponent and
// mantissa: 0 for NaN, the infinities and |x| < 1, else the integer part
// modulo 2^32, negated for a negative x.
func wrap32(x uint64) uint32 {
	exp := int(x>>52) & 0x7ff
	if exp == 0x7ff || exp < 1023 {
		return 0
	}
	mant := x&(1<<52-1) | 1<<52
	var v uint64
	if sh := exp - 1075; sh >= 0 {
		if sh < 64 {
			v = mant << uint(sh)
		}
	} else {
		v = mant >> uint(-sh)
	}
	r := uint32(v)
	if x>>63 != 0 {
		r = -r
	}
	return r
}

// storeFields writes vals into the result record at dst by field name.
func (m *Machine) storeFields(dst loc, vals map[string]uint64) {
	for i, fd := range dst.ti.ty.Fields {
		lf := dst.ti.layout.Fields[i]
		m.store(m.at(dst, lf.Offset, lf.Type), vals[m.cfg.Interner.Str(fd.Name)])
	}
}

// overflowIntrinsic is one overflow-flag form: (value, overflow).
func overflowIntrinsic(op ovfOp) intrinsicFunc {
	return func(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef) {
		ti := m.targ(f, fn, 0)
		v, ov := overflowOp(op, m.argU(f, t, 0), m.argU(f, t, 1), ti.width, ti.signed)
		m.storeFields(m.dest(f, t), map[string]uint64{"value": v, "overflow": b2u(ov)})
	}
}

func init() {
	intrinsics["std.math.AddOverflow"] = overflowIntrinsic(ovfAdd)
	intrinsics["std.math.SubOverflow"] = overflowIntrinsic(ovfSub)
	intrinsics["std.math.MulOverflow"] = overflowIntrinsic(ovfMul)
	intrinsics["std.math.WrapI32"] = func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
		m.store(m.dest(f, t), uint64(wrap32(m.argU(f, t, 0))))
	}
	intrinsics["std.math.WrapU32"] = intrinsics["std.math.WrapI32"]
	// CastSat and CastSatF32 are std.simd's lane CastSat (intrinsic_simd.go)
	// from the argument's float type to T.
	castSatIntrinsic := func(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef) {
		from := m.operandTI(f, m.argOp(f, t, 0))
		v, _ := castSat(from, m.targ(f, fn, 0), m.argU(f, t, 0))
		m.store(m.dest(f, t), v)
	}
	intrinsics["std.math.CastSat"] = castSatIntrinsic
	intrinsics["std.math.CastSatF32"] = castSatIntrinsic
	intrinsics["std.math.MulWide"] = func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
		hi, lo := bits.Mul64(m.argU(f, t, 0), m.argU(f, t, 1))
		m.storeFields(m.dest(f, t), map[string]uint64{"hi": hi, "lo": lo})
	}
	intrinsics["std.math.DivWide"] = func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
		hi, lo, d := m.argU(f, t, 0), m.argU(f, t, 1), m.argU(f, t, 2)
		switch {
		case d == 0:
			panicWith(types.PanicDivideByZero, "math.DivWide by 0")
		case hi >= d:
			panicWith(types.PanicOverflow, "math.DivWide: the quotient does not fit a u64 (hi >= d)")
		}
		q, r := bits.Div64(hi, lo, d)
		m.storeFields(m.dest(f, t), map[string]uint64{"q": q, "r": r})
	}
	intrinsics["std.math.AddCarry"] = carryIntrinsic(carryAdd, "sum")
	intrinsics["std.math.SubBorrow"] = carryIntrinsic(carrySub, "diff")
}
