// Why: the range analysis removes overflow and bounds checks it can prove
// never fail, and it needs a value domain for integer locals that is exact
// within one step and never wraps.
//
// What: ival (an interval [lo, hi] of mathematical values), wide (a 128-bit
// two's complement integer with saturating mul), intInfo (a type's range) and
// binRange and the other transfer functions of each integer rvalue.
//
// How: interval abstract interpretation in the style of Cousot and Cousot
// (POPL 1977). One transfer runs in exact 128-bit arithmetic, then is clamped
// to the local's type; every interval of a supported integer type packs into
// two int64 words, with hi == math.MaxInt64 standing for top on u64.

package lower

import (
	"math"
	"math/bits"

	"mk/internal/mir"
	"mk/internal/types"
)

// ival is an interval of mathematical values, within its local's type. For
// a 64-bit unsigned type hi == math.MaxInt64 means "up to 2^64-1" (top), so
// every interval of every supported integer type fits two int64 words.
// lo > hi is the empty interval: no value, the state is infeasible.
type ival struct{ lo, hi int64 }

func (v ival) empty() bool { return v.lo > v.hi }

// wide is a 128-bit two's complement integer: the exact value of one
// transfer before it is clamped to a type. Every wide the analysis makes has
// magnitude at most 2^126 (mul saturates), so add never overflows.
type wide struct {
	hi int64
	lo uint64
}

func wInt(v int64) wide {
	if v < 0 {
		return wide{-1, uint64(v)}
	}
	return wide{0, uint64(v)}
}

func wUint(v uint64) wide { return wide{0, v} }

var (
	wSatHi = wide{1 << 62, 0}  // 2^126
	wSatLo = wide{-1 << 62, 0} // -2^126
)

func (a wide) add(b wide) wide {
	lo, c := bits.Add64(a.lo, b.lo, 0)
	return wide{a.hi + b.hi + int64(c), lo}.sat()
}

func (a wide) neg() wide {
	lo, b := bits.Sub64(0, a.lo, 0)
	return wide{-a.hi - int64(b), lo}
}

func (a wide) sub(b wide) wide { return a.add(b.neg()) }

func (a wide) cmp(b wide) int {
	switch {
	case a.hi < b.hi:
		return -1
	case a.hi > b.hi:
		return 1
	case a.lo < b.lo:
		return -1
	case a.lo > b.lo:
		return 1
	}
	return 0
}

func (a wide) sat() wide {
	if a.cmp(wSatHi) > 0 {
		return wSatHi
	}
	if a.cmp(wSatLo) < 0 {
		return wSatLo
	}
	return a
}

func (a wide) neg0() bool { return a.hi < 0 }

// mag is |a| when it fits 64 bits, with ok false past that.
func (a wide) mag() (uint64, bool) {
	if a.hi < 0 {
		a = a.neg()
	}
	return a.lo, a.hi == 0
}

// mul is a*b, saturated at 2^126 in magnitude.
func (a wide) mul(b wide) wide {
	ma, oka := a.mag()
	mb, okb := b.mag()
	neg := a.neg0() != b.neg0()
	if !oka || !okb {
		if ma == 0 && oka || mb == 0 && okb {
			return wide{}
		}
		if neg {
			return wSatLo
		}
		return wSatHi
	}
	hi, lo := bits.Mul64(ma, mb)
	var r wide
	if hi >= 1<<62 {
		r = wSatHi
	} else {
		r = wide{int64(hi), lo}
	}
	if neg {
		return r.neg()
	}
	return r
}

// shr is a >> k, arithmetic, for 0 <= k < 64.
func (a wide) shr(k uint) wide {
	if k == 0 {
		return a
	}
	return wide{a.hi >> k, a.lo>>k | uint64(a.hi)<<(64-k)}
}

func wMin(a, b wide) wide {
	if a.cmp(b) <= 0 {
		return a
	}
	return b
}

func wMax(a, b wide) wide {
	if a.cmp(b) >= 0 {
		return a
	}
	return b
}

// wi is a pair of wides: an interval during one transfer.
type wi struct{ lo, hi wide }

// intInfo is what the analysis needs of an integer (or bool) type.
type intInfo struct {
	ok       bool
	signed   bool
	bits     uint
	min, max wide
}

func intOf(tab *types.Table, t types.TypeID) intInfo {
	if t == types.Bool {
		return intInfo{ok: true, bits: 1, min: wInt(0), max: wInt(1)}
	}
	ty := tab.Get(t)
	if !ty.IsInteger() {
		return intInfo{}
	}
	w := uint(ty.Bits)
	if ty.Kind == types.KIntPtr || w == 0 {
		w = 64
	}
	in := intInfo{ok: true, signed: ty.Signed, bits: w}
	if ty.Signed {
		in.min = wInt(-1 << (w - 1))
		in.max = wInt(1<<(w-1) - 1)
	} else {
		in.min = wInt(0)
		in.max = wUint(math.MaxUint64 >> (64 - w))
	}
	return in
}

// full is the type's whole range.
func (in intInfo) full() wi { return wi{in.min, in.max} }

// fits reports whether r lies in the type.
func (in intInfo) fits(r wi) bool { return r.lo.cmp(in.min) >= 0 && r.hi.cmp(in.max) <= 0 }

// clamp is r intersected with the type; an r outside the type entirely is
// the empty interval.
func (in intInfo) clamp(r wi) wi {
	return wi{wMax(r.lo, in.min), wMin(r.hi, in.max)}
}

// store packs a wide interval of the type into an ival.
func (in intInfo) store(r wi) ival {
	r = in.clamp(r)
	if r.lo.cmp(r.hi) > 0 {
		return ival{1, 0}
	}
	return ival{toI64(r.lo, false), toI64(r.hi, true)}
}

// toI64 packs a wide within [-2^63, 2^64-1]: a value past MaxInt64 becomes
// MaxInt64, which is top for hi and a smaller (sound) bound for lo.
func toI64(w wide, hi bool) int64 {
	if w.cmp(wInt(math.MaxInt64)) >= 0 {
		return math.MaxInt64
	}
	if w.cmp(wInt(math.MinInt64)) <= 0 {
		return math.MinInt64
	}
	return int64(w.lo)
}

// load unpacks an ival of the type.
func (in intInfo) load(v ival) wi {
	r := wi{wInt(v.lo), wInt(v.hi)}
	if !in.signed && in.bits == 64 && v.hi == math.MaxInt64 {
		r.hi = in.max
	}
	return r
}

// immValue is an OpImm's value in its type.
func (in intInfo) immValue(bitsv uint64) wide {
	if in.bits < 64 {
		bitsv &= 1<<in.bits - 1
	}
	if in.signed && in.bits < 64 && bitsv>>(in.bits-1)&1 == 1 {
		return wInt(int64(bitsv | ^uint64(0)<<in.bits))
	}
	if in.signed {
		return wInt(int64(bitsv))
	}
	return wUint(bitsv)
}

func one(v wide) wi { return wi{v, v} }

// binRange is the exact range of a op b for the arithmetic and bitwise
// operators, before the result type is applied; ok false when the analysis
// knows nothing better than the type.
func binRange(op mir.BinOp, a, b wi, in intInfo) (wi, bool) {
	switch op {
	case mir.Add, mir.AddWrap, mir.AddNoWrap:
		return wi{a.lo.add(b.lo), a.hi.add(b.hi)}, true
	case mir.Sub, mir.SubWrap, mir.SubNoWrap:
		return wi{a.lo.sub(b.hi), a.hi.sub(b.lo)}, true
	case mir.Mul, mir.MulWrap, mir.MulNoWrap:
		p := [4]wide{a.lo.mul(b.lo), a.lo.mul(b.hi), a.hi.mul(b.lo), a.hi.mul(b.hi)}
		r := wi{p[0], p[0]}
		for _, v := range p[1:] {
			r.lo, r.hi = wMin(r.lo, v), wMax(r.hi, v)
		}
		return r, true
	case mir.Div:
		return divRange(a, b)
	case mir.Rem:
		return remRange(a, b)
	case mir.BitAnd:
		z := wInt(0)
		switch {
		case !a.lo.neg0() && !b.lo.neg0():
			return wi{z, wMin(a.hi, b.hi)}, true
		case !b.lo.neg0():
			return wi{z, b.hi}, true
		case !a.lo.neg0():
			return wi{z, a.hi}, true
		}
	case mir.BitOr, mir.BitXor:
		if !a.lo.neg0() && !b.lo.neg0() {
			m := wMax(a.hi, b.hi)
			if m.hi == 0 {
				top := wUint(math.MaxUint64 >> uint(bits.LeadingZeros64(m.lo)))
				if m.lo == 0 {
					top = wInt(0)
				}
				lo := wInt(0)
				if op == mir.BitOr {
					lo = wMax(a.lo, b.lo)
				}
				return wi{lo, top}, true
			}
		}
	case mir.Shr:
		if k, ok := shiftAmount(b, in); ok {
			return wi{wMin(a.lo.shr(k.lo), a.lo.shr(k.hi)), wMax(a.hi.shr(k.lo), a.hi.shr(k.hi))}, true
		}
	case mir.Shl:
		if k, ok := shiftAmount(b, in); ok && k.hi < 63 {
			lo := a.lo.mul(wInt(1 << k.lo))
			if a.lo.neg0() {
				lo = a.lo.mul(wInt(1 << k.hi))
			}
			hi := a.hi.mul(wInt(1 << k.hi))
			if a.hi.neg0() {
				hi = a.hi.mul(wInt(1 << k.lo))
			}
			return wi{lo, hi}, true
		}
	}
	return wi{}, false
}

// shiftAmount is a shift's amount range when it lies in [0, width): a
// shift that did not trap had such an amount.
func shiftAmount(b wi, in intInfo) (struct{ lo, hi uint }, bool) {
	k := struct{ lo, hi uint }{}
	if b.lo.neg0() || b.hi.cmp(wInt(int64(in.bits)-1)) > 0 {
		lo, hi := wMax(b.lo, wInt(0)), wMin(b.hi, wInt(int64(in.bits)-1))
		if lo.cmp(hi) > 0 {
			return k, false
		}
		b = wi{lo, hi}
	}
	k.lo, k.hi = uint(b.lo.lo), uint(b.hi.lo)
	return k, true
}

// divRange is a / b truncated, for a divisor range of one sign (zero traps).
func divRange(a, b wi) (wi, bool) {
	if b.lo.cmp(wInt(1)) < 0 || b.hi.hi != 0 || a.lo.hi != a.lo.hi>>63 || a.hi.hi != a.hi.hi>>63 {
		return wi{}, false
	}
	// b >= 1 and every bound fits 64 bits signed: a/b is monotone in a, and
	// for a fixed a its magnitude falls as b grows.
	al, ah := int64(a.lo.lo), int64(a.hi.lo)
	if a.lo.hi == 0 && a.lo.lo > math.MaxInt64 || a.hi.hi == 0 && a.hi.lo > math.MaxInt64 {
		return wi{}, false
	}
	bl, bh := int64(b.lo.lo), int64(b.hi.lo)
	if b.hi.lo > math.MaxInt64 {
		return wi{}, false
	}
	lo, hi := al/bh, ah/bl
	if al < 0 {
		lo = al / bl
	}
	if ah < 0 {
		hi = ah / bh
	}
	return wi{wInt(lo), wInt(hi)}, true
}

// remRange is a % b truncated: |r| < max|b| with r's sign a's (zero traps).
func remRange(a, b wi) (wi, bool) {
	ml, okl := b.lo.mag()
	mh, okh := b.hi.mag()
	if !okl || !okh {
		return wi{}, false
	}
	m := ml
	if mh > m {
		m = mh
	}
	if m == 0 {
		return wi{}, false
	}
	lim := wUint(m - 1)
	r := wi{wMax(a.lo, lim.neg()), wMin(a.hi, lim)}
	if !a.lo.neg0() {
		r.lo = wInt(0)
	}
	if a.hi.neg0() || a.hi.cmp(wInt(0)) == 0 {
		r.hi = wInt(0)
	}
	if r.lo.cmp(r.hi) > 0 {
		return wi{}, false
	}
	return r, true
}

// cmpRange decides a op b over two intervals: 1 always true, 0 always
// false, -1 unknown.
func cmpRange(op mir.BinOp, a, b wi) int {
	lt := a.hi.cmp(b.lo) < 0  // a < b always
	ge := a.lo.cmp(b.hi) >= 0 // a >= b always
	le := a.hi.cmp(b.lo) <= 0 // a <= b always
	gt := a.lo.cmp(b.hi) > 0  // a > b always
	switch op {
	case mir.Lt:
		return tri(lt, ge)
	case mir.Le:
		return tri(le, gt)
	case mir.Gt:
		return tri(gt, le)
	case mir.Ge:
		return tri(ge, lt)
	case mir.Eq:
		return tri(a.lo.cmp(a.hi) == 0 && b.lo.cmp(b.hi) == 0 && a.lo.cmp(b.lo) == 0, lt || gt)
	case mir.Ne:
		return tri(lt || gt, a.lo.cmp(a.hi) == 0 && b.lo.cmp(b.hi) == 0 && a.lo.cmp(b.lo) == 0)
	}
	return -1
}

func tri(yes, no bool) int {
	switch {
	case yes:
		return 1
	case no:
		return 0
	}
	return -1
}

// refineCmp narrows a and b by "a op b holds" (truth false applies the
// negation); either may come back empty, which makes the edge infeasible.
func refineCmp(op mir.BinOp, truth bool, a, b wi) (wi, wi) {
	if !truth {
		switch op {
		case mir.Lt:
			op = mir.Ge
		case mir.Le:
			op = mir.Gt
		case mir.Gt:
			op = mir.Le
		case mir.Ge:
			op = mir.Lt
		case mir.Eq:
			op = mir.Ne
		case mir.Ne:
			op = mir.Eq
		}
	}
	one := wInt(1)
	switch op {
	case mir.Lt: // a <= b-1, b >= a+1
		a.hi, b.lo = wMin(a.hi, b.hi.sub(one)), wMax(b.lo, a.lo.add(one))
	case mir.Le:
		a.hi, b.lo = wMin(a.hi, b.hi), wMax(b.lo, a.lo)
	case mir.Gt:
		a.lo, b.hi = wMax(a.lo, b.lo.add(one)), wMin(b.hi, a.hi.sub(one))
	case mir.Ge:
		a.lo, b.hi = wMax(a.lo, b.lo), wMin(b.hi, a.hi)
	case mir.Eq:
		lo, hi := wMax(a.lo, b.lo), wMin(a.hi, b.hi)
		a, b = wi{lo, hi}, wi{lo, hi}
	case mir.Ne: // only a singleton at an end can shrink the other
		if b.lo.cmp(b.hi) == 0 {
			if a.lo.cmp(b.lo) == 0 {
				a.lo = a.lo.add(one)
			} else if a.hi.cmp(b.lo) == 0 {
				a.hi = a.hi.sub(one)
			}
		}
		if a.lo.cmp(a.hi) == 0 {
			if b.lo.cmp(a.lo) == 0 {
				b.lo = b.lo.add(one)
			} else if b.hi.cmp(a.lo) == 0 {
				b.hi = b.hi.sub(one)
			}
		}
	}
	return a, b
}
