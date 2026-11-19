// Why: std.simd code must run at compile time with the same results as the
// vector instructions codegen emits, so consts and tests can use it.
//
// What: simdIntrinsics, the table of every Vec and Mask intrinsic and the
// operator intrinsics, the lane helpers (vecLanes, maskLanes, putLanes,
// vpermute, vshift, widenHalf, castSat and others), and sysRefusals for
// std.mmio.
//
// How: each operation runs lane by lane with arith.go's scalar operations, so
// a value the evaluator builds is the native memory form byte for byte. A
// Vec[T, N] is N lanes of T laid out as [N]T; a Mask is the same size, each
// lane all zeros or all ones. The lane count is read from the size of the
// operand or result, never from a const generic argument, so the table needs
// no instance substitution.

package eval

import (
	"math"
	"math/big"
	"math/bits"

	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// maskKey is std.simd.Mask's stable key: its consts keep their bytes
// (constval.go serInto).
const maskKey = "std.simd.Mask"

// vecLanes reads the lanes of the Vec at l as lane type t.
func (m *Machine) vecLanes(l loc, t *tinfo) []uint64 {
	n := l.ti.size / t.size
	out := make([]uint64, n)
	for j := range out {
		out[j] = m.load(loc{a: l.a, off: l.off + uint32(uint64(j)*t.size), tag: l.tag, ti: t})
	}
	return out
}

// maskLanes reads the lanes of the Mask at l; a lane that is neither all
// zeros nor all ones is an invalid value, refused as a bool that is neither
// 0 nor 1 is.
func (m *Machine) maskLanes(l loc, t *tinfo) []bool {
	xs := m.vecLanes(l, t)
	out := make([]bool, len(xs))
	for j, x := range xs {
		switch x {
		case 0:
		case mask(t.width):
			out[j] = true
		default:
			refuse(types.RefuseRawAccess, "mask")
		}
	}
	return out
}

// putLanes writes xs as the lanes of type t of the value at l. Lanes that
// do not fill l exactly are a size clause check did not judge (WidenLo,
// NarrowSat or Reinterpret in a generic over the lane types, where a lane
// type is built on a type parameter, or a restatement check demands and does
// not judge; over a bare lane type parameter the clause is restated): a
// PanicContract panic before any lane is written, as codegen's storeVec does.
func (m *Machine) putLanes(l loc, t *tinfo, xs []uint64) {
	if uint64(len(xs))*t.size != l.ti.size {
		panicWith(types.PanicContract, "")
	}
	for j, x := range xs {
		m.store(loc{a: l.a, off: l.off + uint32(uint64(j)*t.size), tag: l.tag, ti: t}, x)
	}
}

// putMask writes the lanes of a Mask of lane type t.
func (m *Machine) putMask(l loc, t *tinfo, set []bool) {
	xs := make([]uint64, len(set))
	for j, s := range set {
		if s {
			xs[j] = mask(t.width)
		}
	}
	m.putLanes(l, t, xs)
}

// vargLoc is the location of argument i (a Vec, Mask or array value).
func (m *Machine) vargLoc(f *frame, t *mir.Terminator, i int) loc {
	return m.operandLoc(f, m.argOp(f, t, i))
}

// vchargeIO charges one intrinsic's bytes: ceil(n/16) for its n bytes of
// input and output, as for any copy.
func (m *Machine) vchargeIO(n uint64) { m.charge(c(n)) }

// vecFn is the shape of most entries: the lane type T, the call, and the
// machine; it writes Dest.
type vecFn func(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef, lane *tinfo)

func vec(run vecFn) intrinsicFunc {
	return func(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef) {
		run(m, f, t, fn, m.targ(f, fn, 0))
	}
}

// vbinary is a lane-wise binary operator that cannot panic: the float
// operators, the wrapping integer operators and the bitwise ones.
func vbinary(op mir.BinOp) intrinsicFunc {
	return vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		a, b := m.vecLanes(m.vargLoc(f, t, 0), lane), m.vecLanes(m.vargLoc(f, t, 1), lane)
		m.vchargeIO(3 * uint64(len(a)) * lane.size)
		for j := range a {
			a[j], _ = binaryOp(op, lane, lane, a[j], b[j])
		}
		m.putLanes(m.dest(f, t), lane, a)
	})
}

// vcompare is a lane-wise comparison into a Mask (fcmp oeq une olt ole ogt
// oge on floats: != is true on a NaN lane, as scalar != is).
func vcompare(op mir.BinOp) intrinsicFunc {
	return vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		a, b := m.vecLanes(m.vargLoc(f, t, 0), lane), m.vecLanes(m.vargLoc(f, t, 1), lane)
		m.vchargeIO(3 * uint64(len(a)) * lane.size)
		set := make([]bool, len(a))
		for j := range a {
			r, _ := binaryOp(op, lane, lane, a[j], b[j])
			set[j] = r == 1
		}
		m.putMask(m.dest(f, t), lane, set)
	})
}

// vmaskBinary is & | ^ on two masks.
func vmaskBinary(op func(x, y bool) bool) intrinsicFunc {
	return vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		a, b := m.maskLanes(m.vargLoc(f, t, 0), lane), m.maskLanes(m.vargLoc(f, t, 1), lane)
		m.vchargeIO(3 * uint64(len(a)) * lane.size)
		for j := range a {
			a[j] = op(a[j], b[j])
		}
		m.putMask(m.dest(f, t), lane, a)
	})
}

// vunary maps each lane of self through op.
func vunary(op func(lane *tinfo, x uint64) uint64) intrinsicFunc {
	return vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		a := m.vecLanes(m.vargLoc(f, t, 0), lane)
		m.vchargeIO(2 * uint64(len(a)) * lane.size)
		for j := range a {
			a[j] = op(lane, a[j])
		}
		m.putLanes(m.dest(f, t), lane, a)
	})
}

// vlanewise2 maps each pair of lanes of self and b through op.
func vlanewise2(op func(lane *tinfo, x, y uint64) uint64) intrinsicFunc {
	return vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		a, b := m.vecLanes(m.vargLoc(f, t, 0), lane), m.vecLanes(m.vargLoc(f, t, 1), lane)
		m.vchargeIO(3 * uint64(len(a)) * lane.size)
		for j := range a {
			a[j] = op(lane, a[j], b[j])
		}
		m.putLanes(m.dest(f, t), lane, a)
	})
}

// vpanicking2 is a lane-wise binary operation that may panic: every lane is
// judged first and the first lane's panic code is raised before Dest is
// written.
func vpanicking2(op func(lane *tinfo, x, y uint64) (uint64, types.PanicCode)) intrinsicFunc {
	return vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		a, b := m.vecLanes(m.vargLoc(f, t, 0), lane), m.vecLanes(m.vargLoc(f, t, 1), lane)
		m.vchargeIO(3 * uint64(len(a)) * lane.size)
		for j := range a {
			r, code := op(lane, a[j], b[j])
			if code != types.PanicNone {
				panicWith(code, "")
			}
			a[j] = r
		}
		m.putLanes(m.dest(f, t), lane, a)
	})
}

// vreduce folds the lanes of self into one value of the result's type.
func vreduce(op func(lane *tinfo, xs []uint64) uint64) intrinsicFunc {
	return vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		a := m.vecLanes(m.vargLoc(f, t, 0), lane)
		m.vchargeIO(uint64(len(a))*lane.size + 8)
		m.store(m.dest(f, t), op(lane, a))
	})
}

// vpermute builds each lane j of the result from lane idx(j) of the
// concatenation of self and, when two, the second Vec argument (the
// permutation table); the const integer argument k (Broadcast, Rotate,
// Slide) is argument karg, or -1.
func vpermute(two bool, karg int, idx func(j, n, k int) int) intrinsicFunc {
	return vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		cat := m.vecLanes(m.vargLoc(f, t, 0), lane)
		n := len(cat)
		if two {
			cat = append(cat, m.vecLanes(m.vargLoc(f, t, 1), lane)...)
		}
		k := 0
		if karg >= 0 {
			k = int(int64(m.argU(f, t, karg)))
		}
		m.vchargeIO(uint64(len(cat)+n) * lane.size)
		out := make([]uint64, n)
		for j := range out {
			i := idx(j, n, k)
			if i < 0 || i >= len(cat) {
				panicWith(types.PanicBounds, "") // check's E0706 keeps this unreachable
			}
			out[j] = cat[i]
		}
		m.putLanes(m.dest(f, t), lane, out)
	})
}

// laneTargetOK is codegen's: check's laneTarget and the widening clause:
// check skips the lane class when T or U is a type parameter and leaves the
// clause to this backstop in the cases putLanes describes. A lane class they
// forbid panics with PanicContract.
func laneTargetOK(trunc bool, lane, u *tinfo) bool {
	lf, uf := lane.class == cFloat, u.class == cFloat
	switch {
	case trunc:
		return !lf && !uf && u.width <= lane.width
	case lf || uf:
		return lane.id == types.F32 && u.id == types.F64
	}
	return lane.signed == u.signed && u.width == 2*lane.width
}

// narrowOK is NarrowSat's clause for an instance, as codegen's: integer
// lanes, 2 * SizeOf[U]() == SizeOf[T](), and M == 2 * N no more than 64
// lanes (n is N), judged here where check left it unjudged (the cases
// putLanes describes).
func narrowOK(lane, u *tinfo, n int) bool {
	return lane.class == cInt && u.class == cInt && 2*u.width == lane.width && 2*n <= 64
}

// widenOf returns the source lane x of type from as a lane of type to,
// twice as wide: sign-extended, zero-extended, or f32 to f64.
func widenOf(from, to *tinfo, x uint64) uint64 {
	if from.class == cFloat {
		return f64bits(float64(f32of(x)))
	}
	if from.signed {
		return signExtend(x, from.width) & mask(to.width)
	}
	return x
}

// laneInt is lane x of an integer type as a sign-correct value: its
// sign-extension when signed.
func laneInt(lane *tinfo, x uint64) (v uint64, neg bool) {
	if lane.signed {
		v = signExtend(x, lane.width)
		return v, int64(v) < 0
	}
	return x, false
}

// clampTo clamps the integer value v (negative when neg) to the range of
// the integer type to and returns its bits.
func clampTo(v uint64, neg bool, to *tinfo) uint64 {
	bitsN := uint(8 * to.width)
	if to.signed {
		lo, hi := -int64(1)<<(bitsN-1), int64(1)<<(bitsN-1)-1
		if bitsN == 64 {
			if !neg && int64(v) < 0 { // an unsigned value above MaxInt64
				return uint64(hi)
			}
			return v
		}
		s := int64(v)
		if !neg && v > uint64(hi) {
			s = hi
		} else if neg && s < lo {
			s = lo
		}
		return uint64(s) & mask(to.width)
	}
	if neg {
		return 0
	}
	if v > mask(to.width) {
		return mask(to.width)
	}
	return v
}

// floatOf is lane x of a float type as a float64 (exact for f32).
func floatOf(lane *tinfo, x uint64) float64 {
	if lane.width == 4 {
		return float64(f32of(x))
	}
	return f64of(x)
}

// floatBits rounds r to the lane's float type once and gives its bits, NaN
// canonical.
func floatBits(lane *tinfo, r float64) uint64 {
	if lane.width == 4 {
		return f32bits(float32(r))
	}
	return f64bits(r)
}

// fminimum and fmaximum are llvm.minimum and llvm.maximum: NaN when either is
// NaN, -0 below +0. Not math.Min and math.Max, whose infinity cases come
// before their NaN case (Min(NaN, -Inf) is -Inf). The result is an input or
// NaN, so the f32 round trip through float64 is exact.
func fminimum(x, y float64) float64 {
	switch {
	case x != x || y != y:
		return math.NaN()
	case x == 0 && y == 0:
		if math.Signbit(x) {
			return x
		}
		return y
	case x < y:
		return x
	}
	return y
}

func fmaximum(x, y float64) float64 {
	switch {
	case x != x || y != y:
		return math.NaN()
	case x == 0 && y == 0:
		if math.Signbit(x) {
			return y
		}
		return x
	case x > y:
		return x
	}
	return y
}

// minMax is lane-wise Min (max false) or Max on one lane pair.
func minMax(lane *tinfo, x, y uint64, max bool) uint64 {
	if lane.class == cFloat {
		if max {
			return floatBits(lane, fmaximum(floatOf(lane, x), floatOf(lane, y)))
		}
		return floatBits(lane, fminimum(floatOf(lane, x), floatOf(lane, y)))
	}
	var less bool
	if lane.signed {
		less = int64(signExtend(x, lane.width)) < int64(signExtend(y, lane.width))
	} else {
		less = x < y
	}
	if less != max {
		return x
	}
	return y
}

// addSat and subSat saturate to the lane's range.
func addSat(lane *tinfo, x, y uint64, sub bool) uint64 {
	if lane.signed {
		a, b := int64(signExtend(x, lane.width)), int64(signExtend(y, lane.width))
		if lane.width < 8 {
			r := a + b
			if sub {
				r = a - b
			}
			return clampTo(uint64(r), r < 0, lane)
		}
		if sub {
			r := a - b
			if (a^b)&(a^r) < 0 {
				return satEdge(a < 0)
			}
			return uint64(r)
		}
		r := a + b
		if (a^r)&(b^r) < 0 {
			return satEdge(a < 0)
		}
		return uint64(r)
	}
	if sub {
		if y > x {
			return 0
		}
		return x - y
	}
	r, carry := bits.Add64(x, y, 0)
	if carry != 0 || r > mask(lane.width) {
		return mask(lane.width)
	}
	return r
}

// satEdge is i64's MIN when neg, else its MAX.
func satEdge(neg bool) uint64 {
	if neg {
		return 1 << 63
	}
	return 1<<63 - 1
}

// fma32 is a*b+c rounded once to f32: the exact value in math/big at 1,024
// bits (a product of two f32 and an f32 addend span at most 405 bits), then
// one rounding to nearest even. NaN, infinite and exact-zero
// results are exact in float64 and take math.FMA, which also gives an
// exact zero its IEEE sign.
func fma32(a, b, c float32) float32 {
	fa, fb, fc := float64(a), float64(b), float64(c)
	if !isFinite(fa) || !isFinite(fb) || !isFinite(fc) {
		return float32(math.FMA(fa, fb, fc))
	}
	x := new(big.Float).SetPrec(1024).SetFloat64(fa)
	x.Mul(x, new(big.Float).SetPrec(1024).SetFloat64(fb))
	x.Add(x, new(big.Float).SetPrec(1024).SetFloat64(fc))
	if x.Sign() == 0 {
		return float32(math.FMA(fa, fb, fc))
	}
	r, _ := x.Float32()
	return r
}

func isFinite(x float64) bool { return !math.IsInf(x, 0) && !math.IsNaN(x) }

// reduceSumTree is ReduceSum's pairwise tree: lanes [0, N/2) plus [N/2, N)
// lane-wise, recursively down to one lane. codegen emits the same
// order as shufflevector and fadd.
func reduceSumTree(lane *tinfo, xs []uint64) uint64 {
	v := append([]uint64(nil), xs...)
	for n := len(v); n > 1; n /= 2 {
		h := n / 2
		for j := 0; j < h; j++ {
			v[j] = floatBinary(mir.Add, lane.width, v[j], v[j+h])
		}
	}
	return v[0]
}

// sliceLanes is the slice argument i: the location of its element 0 (with
// the data pointer's tag) and its length.
func (m *Machine) sliceArg(f *frame, t *mir.Terminator, i int, elem *tinfo) (base loc, n uint64) {
	d, n := m.sliceWords(m.vargLoc(f, t, i))
	tag := m.takeVal()
	return loc{a: ptrAlloc(d), off: ptrOff(d), tag: tag, ti: elem}, n
}

// elemAt is element i of the slice whose element 0 is at base.
func elemAt(base loc, i uint64) loc {
	return loc{a: base.a, off: base.off + uint32(i*base.ti.size), tag: base.tag, ti: base.ti}
}

// spanOK is the check of FromSlice and CopyTo: 0 <= i <= len - n, once for
// the whole span.
func spanOK(i, length uint64, n int) bool {
	return int64(i) >= 0 && length >= uint64(n) && i <= length-uint64(n)
}

// cpuBits is the CpuFeatures word pair of a feature set of arch: bit k of
// the pair is the k-th row of arch in types.CPUFeatures. A level name
// (x86-64-v3, no LLVM spelling) has no bit of its own, so a set read from
// the running CPU and a level's closure compare by their features alone.
// codegen computes the same bits (intrinsic_simd.go).
func cpuBits(arch types.Arch, names []string) [2]uint64 {
	var w [2]uint64
	k := 0
	for _, row := range types.CPUFeatures() {
		if row.Arch != arch {
			continue
		}
		if row.LLVM != "" {
			for _, n := range names {
				if n == row.Name {
					w[k/64] |= 1 << (k % 64)
				}
			}
		}
		k++
	}
	return w
}

// constStr is the str value of a const argument (Of's and Has's names).
func (m *Machine) constStr(f *frame, t *mir.Terminator, i int) string {
	d, n := m.sliceWords(m.vargLoc(f, t, i))
	return string(m.readBytes(d, n, m.takeVal()))
}

// cpuNames splits a cpu clause's comma list, spaces allowed after commas.
func cpuNames(s string) []string {
	var out []string
	for len(s) > 0 {
		i := 0
		for i < len(s) && s[i] != ',' {
			i++
		}
		n := s[:i]
		for len(n) > 0 && n[0] == ' ' {
			n = n[1:]
		}
		for len(n) > 0 && n[len(n)-1] == ' ' {
			n = n[:len(n)-1]
		}
		if n != "" {
			out = append(out, n)
		}
		if i == len(s) {
			break
		}
		s = s[i+1:]
	}
	return out
}

// storeBits writes a CpuFeatures value (its bits: [2]u64 field at offset 0).
func (m *Machine) storeBits(dst loc, w [2]uint64) {
	u64 := m.ti(types.U64)
	m.store(loc{a: dst.a, off: dst.off, tag: dst.tag, ti: u64}, w[0])
	m.store(loc{a: dst.a, off: dst.off + 8, tag: dst.tag, ti: u64}, w[1])
}

// simdIntrinsics is the std.simd table by stable key (the declarations are
// in std/simd/vec.mk).
var simdIntrinsics = map[string]intrinsicFunc{
	// Construction, memory and lanes.
	"std.simd.Vec:Splat": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		dst := m.dest(f, t)
		x := m.argU(f, t, 0)
		out := make([]uint64, dst.ti.size/lane.size)
		for j := range out {
			out[j] = x
		}
		m.vchargeIO(dst.ti.size)
		m.putLanes(dst, lane, out)
	}),
	"std.simd.Vec:Zero": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		dst := m.dest(f, t)
		m.vchargeIO(dst.ti.size)
		m.putLanes(dst, lane, make([]uint64, dst.ti.size/lane.size))
	}),
	"std.simd.Vec:Iota": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		dst := m.dest(f, t)
		out := make([]uint64, dst.ti.size/lane.size)
		for j := range out {
			if lane.class == cFloat {
				out[j] = floatBits(lane, float64(j))
			} else {
				out[j] = uint64(j) & mask(lane.width)
			}
		}
		m.vchargeIO(dst.ti.size)
		m.putLanes(dst, lane, out)
	}),
	"std.simd.Vec:FromArray": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		a := m.vecLanes(m.vargLoc(f, t, 0), lane)
		m.vchargeIO(2 * uint64(len(a)) * lane.size)
		m.putLanes(m.dest(f, t), lane, a)
	}),
	"std.simd.Vec:ToArray": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		a := m.vecLanes(m.vargLoc(f, t, 0), lane)
		m.vchargeIO(2 * uint64(len(a)) * lane.size)
		m.putLanes(m.dest(f, t), lane, a)
	}),
	"std.simd.Vec:FromSlice": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		dst := m.dest(f, t)
		n := int(dst.ti.size / lane.size)
		base, length := m.sliceArg(f, t, 0, lane)
		i := m.argU(f, t, 1)
		if !spanOK(i, length, n) {
			panicWith(types.PanicBounds, "")
		}
		m.vchargeIO(2 * dst.ti.size)
		out := make([]uint64, n)
		for j := range out {
			out[j] = m.load(elemAt(base, i+uint64(j)))
		}
		m.putLanes(dst, lane, out)
	}),
	"std.simd.Vec:FromSliceOr": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		dst := m.dest(f, t)
		base, length := m.sliceArg(f, t, 0, lane)
		i := m.argU(f, t, 1)
		fill := m.vecLanes(m.vargLoc(f, t, 2), lane)
		if int64(i) < 0 || i > length {
			panicWith(types.PanicBounds, "")
		}
		k := min(length-i, uint64(len(fill)))
		m.vchargeIO(3 * dst.ti.size)
		for j := uint64(0); j < k; j++ {
			fill[j] = m.load(elemAt(base, i+j))
		}
		m.putLanes(dst, lane, fill)
	}),
	"std.simd.Vec:CopyTo": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		a := m.vecLanes(m.vargLoc(f, t, 0), lane)
		base, length := m.sliceArg(f, t, 1, lane)
		i := m.argU(f, t, 2)
		if !spanOK(i, length, len(a)) {
			panicWith(types.PanicBounds, "")
		}
		m.vchargeIO(2 * uint64(len(a)) * lane.size)
		for j, x := range a {
			m.store(elemAt(base, i+uint64(j)), x)
		}
	}),
	"std.simd.Vec:CopyToMasked": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		a := m.vecLanes(m.vargLoc(f, t, 0), lane)
		base, length := m.sliceArg(f, t, 1, lane)
		i := m.argU(f, t, 2)
		set := m.maskLanes(m.vargLoc(f, t, 3), lane)
		if int64(i) < 0 {
			panicWith(types.PanicBounds, "")
		}
		for j, s := range set {
			if s && (i+uint64(j) >= length || i+uint64(j) < i) {
				panicWith(types.PanicBounds, "")
			}
		}
		m.vchargeIO(3 * uint64(len(a)) * lane.size)
		for j, x := range a {
			if set[j] {
				m.store(elemAt(base, i+uint64(j)), x)
			}
		}
	}),
	"std.simd.Vec:Gather": vec(func(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef, lane *tinfo) {
		it := m.targ(f, fn, 2)
		base, length := m.sliceArg(f, t, 0, lane)
		idx := m.vecLanes(m.vargLoc(f, t, 1), it)
		for j, x := range idx {
			if it.signed {
				x = signExtend(x, it.width)
			}
			if x >= length { // unsigned after widening to 64 bits: a negative index is huge
				panicWith(types.PanicBounds, "")
			}
			idx[j] = x
		}
		m.vchargeIO(uint64(len(idx)) * (it.size + 2*lane.size))
		out := make([]uint64, len(idx))
		for j, x := range idx {
			out[j] = m.load(elemAt(base, x))
		}
		m.putLanes(m.dest(f, t), lane, out)
	}),
	"std.simd.Vec:Lane": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		a := m.vecLanes(m.vargLoc(f, t, 0), lane)
		i := int64(m.argU(f, t, 1))
		if i < 0 || i >= int64(len(a)) {
			panicWith(types.PanicBounds, "") // the requires clause keeps this unreachable
		}
		m.vchargeIO(uint64(len(a))*lane.size + lane.size)
		m.store(m.dest(f, t), a[i])
	}),
	"std.simd.Vec:With": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		a := m.vecLanes(m.vargLoc(f, t, 0), lane)
		i := int64(m.argU(f, t, 1))
		if i < 0 || i >= int64(len(a)) {
			panicWith(types.PanicBounds, "")
		}
		a[i] = m.argU(f, t, 2)
		m.vchargeIO(2 * uint64(len(a)) * lane.size)
		m.putLanes(m.dest(f, t), lane, a)
	}),

	// Permutations (the index functions; the evaluator and codegen read
	// the same table).
	"std.simd.Vec:Shuffle": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		cat := m.vecLanes(m.vargLoc(f, t, 0), lane)
		n := len(cat)
		cat = append(cat, m.vecLanes(m.vargLoc(f, t, 1), lane)...)
		idx := m.vecLanes(m.vargLoc(f, t, 2), m.ti(types.Int))
		m.vchargeIO(uint64(3*n)*lane.size + uint64(8*n))
		out := make([]uint64, n)
		for j := range out {
			k := int64(idx[j])
			if k < 0 || k >= int64(len(cat)) {
				panicWith(types.PanicBounds, "") // check's E0706 keeps this unreachable
			}
			out[j] = cat[k]
		}
		m.putLanes(m.dest(f, t), lane, out)
	}),
	"std.simd.Vec:Reverse":   vpermute(false, -1, func(j, n, _ int) int { return n - 1 - j }),
	"std.simd.Vec:Broadcast": vpermute(false, 1, func(_, _, k int) int { return k }),
	"std.simd.Vec:Rotate":    vpermute(false, 1, func(j, n, k int) int { return (j + k) % n }),
	"std.simd.Vec:Slide": vpermute(true, 2, func(j, n, k int) int {
		if j < k {
			return n + n - k + j // lane N-k+j of prev, the second operand
		}
		return j - k
	}),
	"std.simd.Vec:InterleaveLo": vpermute(true, -1, func(j, n, _ int) int {
		if j%2 == 0 {
			return j / 2
		}
		return n + j/2
	}),
	"std.simd.Vec:InterleaveHi": vpermute(true, -1, func(j, n, _ int) int {
		if j%2 == 0 {
			return n/2 + j/2
		}
		return n + n/2 + j/2
	}),
	"std.simd.Vec:DeinterleaveEven": vpermute(true, -1, func(j, _, _ int) int { return 2 * j }),
	"std.simd.Vec:DeinterleaveOdd":  vpermute(true, -1, func(j, _, _ int) int { return 2*j + 1 }),
	"std.simd.Lookup16": func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
		u8 := m.ti(types.U8)
		idx := m.vecLanes(m.vargLoc(f, t, 0), u8)
		table := m.vecLanes(m.vargLoc(f, t, 1), u8)
		m.vchargeIO(uint64(2*len(idx) + 16))
		for j, x := range idx {
			if x < 16 {
				idx[j] = table[x]
			} else {
				idx[j] = 0
			}
		}
		m.putLanes(m.dest(f, t), u8, idx)
	},

	// Reductions: the tree for ReduceSum, left to right for the others
	// (associative and exact).
	"std.simd.Vec:ReduceSum": vreduce(reduceSumTree),
	"std.simd.Vec:ReduceSumWrap": vreduce(func(lane *tinfo, xs []uint64) uint64 {
		var s uint64
		for _, x := range xs {
			s += x
		}
		return s & mask(lane.width)
	}),
	"std.simd.Vec:ReduceSumWide": vreduce(func(lane *tinfo, xs []uint64) uint64 {
		var s uint64
		for _, x := range xs {
			v, _ := laneInt(lane, x)
			s += v // at most 64 lanes of 32 bits: exact in i64
		}
		return s
	}),
	"std.simd.Vec:ReduceMin": vreduce(func(lane *tinfo, xs []uint64) uint64 {
		r := xs[0]
		for _, x := range xs[1:] {
			r = minMax(lane, r, x, false)
		}
		return r
	}),
	"std.simd.Vec:ReduceMax": vreduce(func(lane *tinfo, xs []uint64) uint64 {
		r := xs[0]
		for _, x := range xs[1:] {
			r = minMax(lane, r, x, true)
		}
		return r
	}),
	"std.simd.Vec:ReduceAnd": vreduce(func(lane *tinfo, xs []uint64) uint64 {
		r := mask(lane.width)
		for _, x := range xs {
			r &= x
		}
		return r
	}),
	"std.simd.Vec:ReduceOr": vreduce(func(_ *tinfo, xs []uint64) uint64 {
		var r uint64
		for _, x := range xs {
			r |= x
		}
		return r
	}),
	"std.simd.Vec:ReduceXor": vreduce(func(_ *tinfo, xs []uint64) uint64 {
		var r uint64
		for _, x := range xs {
			r ^= x
		}
		return r
	}),

	// Arithmetic.
	"std.simd.Vec:Min": vlanewise2(func(lane *tinfo, x, y uint64) uint64 { return minMax(lane, x, y, false) }),
	"std.simd.Vec:Max": vlanewise2(func(lane *tinfo, x, y uint64) uint64 { return minMax(lane, x, y, true) }),
	"std.simd.Vec:Abs": vunary(func(lane *tinfo, x uint64) uint64 {
		return x &^ (1 << (8*uint(lane.width) - 1)) // llvm.fabs: the sign bit cleared
	}),
	"std.simd.Vec:AbsWrap": vunary(func(lane *tinfo, x uint64) uint64 {
		if v, neg := laneInt(lane, x); neg {
			return -v & mask(lane.width) // MIN stays MIN
		}
		return x
	}),
	"std.simd.Vec:AddSat": vlanewise2(func(lane *tinfo, x, y uint64) uint64 { return addSat(lane, x, y, false) }),
	"std.simd.Vec:SubSat": vlanewise2(func(lane *tinfo, x, y uint64) uint64 { return addSat(lane, x, y, true) }),
	"std.simd.Vec:MulHigh": vlanewise2(func(lane *tinfo, x, y uint64) uint64 {
		w := 8 * uint(lane.width) // at most 32 (E0701), so the product fits
		if lane.signed {
			p := int64(signExtend(x, lane.width)) * int64(signExtend(y, lane.width))
			return uint64(p>>w) & mask(lane.width)
		}
		return (x * y >> w) & mask(lane.width)
	}),
	"std.simd.Vec:Fma": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		a, b, cc := m.vecLanes(m.vargLoc(f, t, 0), lane), m.vecLanes(m.vargLoc(f, t, 1), lane), m.vecLanes(m.vargLoc(f, t, 2), lane)
		m.vchargeIO(4 * uint64(len(a)) * lane.size)
		for j := range a {
			if lane.width == 4 {
				a[j] = f32bits(fma32(f32of(a[j]), f32of(b[j]), f32of(cc[j])))
			} else {
				a[j] = f64bits(math.FMA(f64of(a[j]), f64of(b[j]), f64of(cc[j])))
			}
		}
		m.putLanes(m.dest(f, t), lane, a)
	}),
	"std.simd.Vec:Sqrt": vunary(func(lane *tinfo, x uint64) uint64 {
		return floatBits(lane, math.Sqrt(floatOf(lane, x))) // f32 through f64 rounds correctly
	}),
	"std.simd.Vec:Floor":       vunary(func(lane *tinfo, x uint64) uint64 { return floatBits(lane, math.Floor(floatOf(lane, x))) }),
	"std.simd.Vec:Ceil":        vunary(func(lane *tinfo, x uint64) uint64 { return floatBits(lane, math.Ceil(floatOf(lane, x))) }),
	"std.simd.Vec:RoundEven":   vunary(func(lane *tinfo, x uint64) uint64 { return floatBits(lane, math.RoundToEven(floatOf(lane, x))) }),
	"std.simd.Vec:RoundToZero": vunary(func(lane *tinfo, x uint64) uint64 { return floatBits(lane, math.Trunc(floatOf(lane, x))) }),
	// DivTrunc panics as scalar / does, a zero divisor lane or a MIN / -1
	// pair, with scalar /'s code for both (types.PanicDivideByZero).
	"std.simd.Vec:DivTrunc": vpanicking2(func(lane *tinfo, x, y uint64) (uint64, types.PanicCode) {
		return intBinary(mir.Div, lane.width, lane.signed, lane, x, y)
	}),
	"std.simd.Vec:ShlEach": vpanicking2(func(lane *tinfo, x, y uint64) (uint64, types.PanicCode) {
		return shiftEach(lane, x, y, true)
	}),
	"std.simd.Vec:ShrEach": vpanicking2(func(lane *tinfo, x, y uint64) (uint64, types.PanicCode) {
		return shiftEach(lane, x, y, false)
	}),
	"std.simd.Vec:WidenLo":    widenHalf(false, false),
	"std.simd.Vec:WidenHi":    widenHalf(true, false),
	"std.simd.Vec:MulWidenLo": widenHalf(false, true),
	"std.simd.Vec:MulWidenHi": widenHalf(true, true),
	"std.simd.Vec:NarrowSat": vec(func(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef, lane *tinfo) {
		u := m.targ(f, fn, 2)
		a := append(m.vecLanes(m.vargLoc(f, t, 0), lane), m.vecLanes(m.vargLoc(f, t, 1), lane)...)
		if !narrowOK(lane, u, len(a)/2) {
			panicWith(types.PanicContract, "") // an unjudged clause (narrowOK)
		}
		m.vchargeIO(uint64(len(a)) * (lane.size + u.size))
		for j, x := range a {
			v, neg := laneInt(lane, x)
			a[j] = clampTo(v, neg, u)
		}
		m.putLanes(m.dest(f, t), u, a)
	}),

	// Conversions.
	"std.simd.Vec:Cast": vconvert(func(from, to *tinfo, x uint64) (uint64, types.PanicCode) {
		return cast(mir.CastChecked, from, to, x)
	}),
	"std.simd.Vec:Trunc": vconvert(func(from, to *tinfo, x uint64) (uint64, types.PanicCode) {
		if !laneTargetOK(true, from, to) {
			return 0, types.PanicContract
		}
		return x & mask(to.width), types.PanicNone
	}),
	"std.simd.Vec:CastSat": vconvert(castSat),
	"std.simd.Vec:Reinterpret": vec(func(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef, lane *tinfo) {
		src, dst := m.vargLoc(f, t, 0), m.dest(f, t)
		m.vchargeIO(2 * dst.ti.size)
		// The bytes unchanged: read as the source lanes, written as the
		// result's lanes over the same little-endian bytes.
		u := m.targ(f, fn, 2)
		// M * SizeOf[U]() == N * SizeOf[T]() && M >= 2 && M <= 64, as
		// codegen judges it: an unjudged clause (putLanes).
		if src.ti.size != dst.ti.size || u.size == 0 || dst.ti.size%u.size != 0 || dst.ti.size/u.size < 2 || dst.ti.size/u.size > 64 {
			panicWith(types.PanicContract, "")
		}
		b := make([]byte, dst.ti.size)
		for j, x := range m.vecLanes(src, lane) {
			types.StoreUint(b[uint64(j)*lane.size:], int(lane.size), x)
		}
		out := make([]uint64, dst.ti.size/u.size)
		for j := range out {
			out[j] = types.LoadUint(b[uint64(j)*u.size:], int(u.size))
		}
		m.putLanes(dst, u, out)
	}),

	// Masks.
	"std.simd.Mask:FromBits": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		dst := m.dest(f, t)
		b := m.argU(f, t, 0)
		set := make([]bool, dst.ti.size/lane.size)
		for j := range set {
			set[j] = b>>uint(j)&1 == 1 // bits at N and above are ignored
		}
		m.vchargeIO(dst.ti.size + 8)
		m.putMask(dst, lane, set)
	}),
	"std.simd.Mask:Bits": vmaskReduce(func(set []bool) uint64 {
		var b uint64
		for j, s := range set {
			if s {
				b |= 1 << uint(j)
			}
		}
		return b
	}),
	"std.simd.Mask:Any": vmaskReduce(func(set []bool) uint64 {
		for _, s := range set {
			if s {
				return 1
			}
		}
		return 0
	}),
	"std.simd.Mask:All": vmaskReduce(func(set []bool) uint64 {
		for _, s := range set {
			if !s {
				return 0
			}
		}
		return 1
	}),
	"std.simd.Mask:CountSet": vmaskReduce(func(set []bool) uint64 {
		var n uint64
		for _, s := range set {
			n += b2u(s)
		}
		return n
	}),
	"std.simd.Mask:FirstSet": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		set := m.maskLanes(m.vargLoc(f, t, 0), lane)
		dst := m.dest(f, t)
		m.vchargeIO(uint64(len(set))*lane.size + dst.ti.size)
		lay := dst.ti.layout
		for j, s := range set {
			if s {
				m.writeTag(dst, lay, 1)
				fl := lay.Variants[1].Fields[0]
				m.store(m.at(dst, fl.Offset, fl.Type), uint64(j))
				return
			}
		}
		m.writeTag(dst, lay, 0)
	}),
	"std.simd.Mask:Select": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		set := m.maskLanes(m.vargLoc(f, t, 0), lane)
		a, b := m.vecLanes(m.vargLoc(f, t, 1), lane), m.vecLanes(m.vargLoc(f, t, 2), lane)
		m.vchargeIO(4 * uint64(len(a)) * lane.size)
		for j := range a {
			if !set[j] {
				a[j] = b[j]
			}
		}
		m.putLanes(m.dest(f, t), lane, a)
	}),
	"std.simd.Mask:Cast": vec(func(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef, lane *tinfo) {
		set := m.maskLanes(m.vargLoc(f, t, 0), lane)
		u := m.targ(f, fn, 2)
		m.vchargeIO(uint64(len(set)) * (lane.size + u.size))
		m.putMask(m.dest(f, t), u, set)
	}),

	// The operator intrinsics.
	"std.simd.Vec:Add":     vbinary(mir.Add),
	"std.simd.Vec:Sub":     vbinary(mir.Sub),
	"std.simd.Vec:Mul":     vbinary(mir.Mul),
	"std.simd.Vec:Div":     vbinary(mir.Div),
	"std.simd.Vec:AddWrap": vbinary(mir.AddWrap),
	"std.simd.Vec:SubWrap": vbinary(mir.SubWrap),
	"std.simd.Vec:MulWrap": vbinary(mir.MulWrap),
	"std.simd.Vec:And":     vbinary(mir.BitAnd),
	"std.simd.Vec:Or":      vbinary(mir.BitOr),
	"std.simd.Vec:Xor":     vbinary(mir.BitXor),
	"std.simd.Vec:Shl":     vshift(mir.Shl),
	"std.simd.Vec:Shr":     vshift(mir.Shr),
	"std.simd.Vec:Neg": vunary(func(lane *tinfo, x uint64) uint64 {
		r, _ := unary(mir.Neg, lane, x) // float lanes only (E0308)
		return r
	}),
	"std.simd.Vec:Not":  vunary(func(lane *tinfo, x uint64) uint64 { return ^x & mask(lane.width) }),
	"std.simd.Vec:Eq":   vcompare(mir.Eq),
	"std.simd.Vec:Ne":   vcompare(mir.Ne),
	"std.simd.Vec:Lt":   vcompare(mir.Lt),
	"std.simd.Vec:Le":   vcompare(mir.Le),
	"std.simd.Vec:Gt":   vcompare(mir.Gt),
	"std.simd.Vec:Ge":   vcompare(mir.Ge),
	"std.simd.Mask:And": vmaskBinary(func(x, y bool) bool { return x && y }),
	"std.simd.Mask:Or":  vmaskBinary(func(x, y bool) bool { return x || y }),
	"std.simd.Mask:Xor": vmaskBinary(func(x, y bool) bool { return x != y }),
	"std.simd.Mask:Not": vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		a := m.maskLanes(m.vargLoc(f, t, 0), lane)
		m.vchargeIO(2 * uint64(len(a)) * lane.size)
		for j := range a {
			a[j] = !a[j]
		}
		m.putMask(m.dest(f, t), lane, a)
	}),

	// CPU features: at compile time the target's baseline, recorded as
	// reads of the target's arch and os, which fix it.
	"std.simd.Cpu": func(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef) {
		it := m.items().Get(fn.Item)
		s := m.seg()
		for _, fl := range []types.TargetField{types.TargetArch, types.TargetOS} {
			s.reads.Add(types.Read{Kind: types.ReadTarget, Key: it.Key, Name: fl.String(), Hash: m.cfg.Program.TargetHash(fl)})
		}
		m.charge(1)
		tg := m.cfg.Target
		m.storeBits(m.dest(f, t), cpuBits(tg.Arch, types.CPUBaseline(tg)))
	},
	// Lanes: Target.vector_bits / (8 * SizeOf[T]()), recorded as a read of
	// vector_bits (a concrete T is folded by check; this is a T an
	// instance fixed).
	"std.simd.Lanes": func(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef) {
		it := m.items().Get(fn.Item)
		m.seg().reads.Add(types.Read{Kind: types.ReadTarget, Key: it.Key, Name: types.TargetVectorBits.String(), Hash: m.cfg.Program.TargetHash(types.TargetVectorBits)})
		m.charge(1)
		lane := m.targ(f, fn, 0)
		vb := uint64(m.cfg.Target.VectorBits)
		if lane.size == 0 || vb == 0 {
			source.ICEf(f.curSpan(), "eval: simd.Lanes of a type of size %d on a target of vector width %d", lane.size, vb)
		}
		m.store(m.dest(f, t), vb/(8*lane.size))
	},
	"std.simd.CpuFeatures:Of": func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
		set, _ := types.CPUClosure(m.cfg.Target.Arch, cpuNames(m.constStr(f, t, 0)))
		m.charge(1)
		m.storeBits(m.dest(f, t), cpuBits(m.cfg.Target.Arch, set))
	},
	"std.simd.CpuFeatures:Has": func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef) {
		u64 := m.ti(types.U64)
		self := m.ptrLoc(m.argU(f, t, 0), u64)
		have := [2]uint64{m.load(self), m.load(loc{a: self.a, off: self.off + 8, tag: self.tag, ti: u64})}
		name := m.constStr(f, t, 1)
		m.charge(1)
		ok := false
		if _, known := types.CPUFeatureByName(m.cfg.Target.Arch, name); known {
			set, _ := types.CPUClosure(m.cfg.Target.Arch, []string{name})
			want := cpuBits(m.cfg.Target.Arch, set)
			ok = have[0]&want[0] == want[0] && have[1]&want[1] == want[1]
		}
		m.store(m.dest(f, t), b2u(ok)) // another architecture's name is false
	},
}

// vshift is << or >> of every lane by one scalar amount k of type K (the
// generic argument after T and N): the scalar rule, one check for the
// whole operation, then the shift of each lane; >> is arithmetic on signed
// lanes.
func vshift(op mir.BinOp) intrinsicFunc {
	return vec(func(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef, lane *tinfo) {
		kt := m.targ(f, fn, 2)
		a := m.vecLanes(m.vargLoc(f, t, 0), lane)
		k := m.argU(f, t, 1)
		m.vchargeIO(2 * uint64(len(a)) * lane.size)
		for j := range a {
			r, code := intBinary(op, lane.width, lane.signed, kt, a[j], k)
			if code != types.PanicNone {
				panicWith(code, "")
			}
			a[j] = r
		}
		m.putLanes(m.dest(f, t), lane, a)
	})
}

// shiftEach is ShlEach or ShrEach on one lane pair: an amount lane at or
// above the lane width, read as unsigned, panics.
func shiftEach(lane *tinfo, x, k uint64, left bool) (uint64, types.PanicCode) {
	if k >= uint64(8*lane.width) {
		return 0, types.PanicShiftAmount
	}
	if left {
		return (x << k) & mask(lane.width), types.PanicNone
	}
	if lane.signed {
		return uint64(int64(signExtend(x, lane.width))>>k) & mask(lane.width), types.PanicNone
	}
	return x >> k, types.PanicNone
}

// widenHalf is WidenLo, WidenHi, MulWidenLo and MulWidenHi: the low or high
// half of the lanes, each extended to U (the generic argument after T and
// N), and for the Mul forms the exact product of the two halves.
func widenHalf(high, mul bool) intrinsicFunc {
	return vec(func(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef, lane *tinfo) {
		u := m.targ(f, fn, 2)
		a := m.vecLanes(m.vargLoc(f, t, 0), lane)
		// The clause's lane class and M >= 2 (M is half of N; putLanes
		// judges the size).
		if !laneTargetOK(false, lane, u) || len(a) < 4 {
			panicWith(types.PanicContract, "")
		}
		var b []uint64
		if mul {
			b = m.vecLanes(m.vargLoc(f, t, 1), lane)
		}
		h := len(a) / 2
		lo := 0
		if high {
			lo = h
		}
		m.vchargeIO(uint64(len(a))*lane.size*2 + uint64(h)*u.size)
		out := make([]uint64, h)
		for j := range out {
			x := widenOf(lane, u, a[lo+j])
			if mul {
				r, _ := intBinary(mir.MulWrap, u.width, u.signed, u, x, widenOf(lane, u, b[lo+j]))
				x = r // exact: two W-bit factors in 2W bits
			}
			out[j] = x
		}
		m.putLanes(m.dest(f, t), u, out)
	})
}

// vconvert converts each lane of self to U (the generic argument after T
// and N), judging every lane before Dest is written.
func vconvert(op func(from, to *tinfo, x uint64) (uint64, types.PanicCode)) intrinsicFunc {
	return vec(func(m *Machine, f *frame, t *mir.Terminator, fn types.FnRef, lane *tinfo) {
		u := m.targ(f, fn, 2)
		a := m.vecLanes(m.vargLoc(f, t, 0), lane)
		m.vchargeIO(uint64(len(a)) * (lane.size + u.size))
		for j := range a {
			r, code := op(lane, u, a[j])
			if code != types.PanicNone {
				panicWith(code, "")
			}
			a[j] = r
		}
		m.putLanes(m.dest(f, t), u, a)
	})
}

// castSat is CastSat on one lane: integer to integer clamps; float to
// integer truncates toward zero and clamps, NaN giving 0 (llvm.fptosi.sat,
// fptoui.sat); integer to float and float to float convert as Cast does
// without its panic (the nearest value, an infinity when out of range).
func castSat(from, to *tinfo, x uint64) (uint64, types.PanicCode) {
	switch {
	case from.class != cFloat && to.class != cFloat:
		v, neg := laneInt(from, x)
		return clampTo(v, neg, to), types.PanicNone
	case from.class == cFloat && to.class != cFloat:
		r := floatOf(from, x)
		if r != r {
			return 0, types.PanicNone
		}
		r = math.Trunc(r)
		bitsN := 8 * to.width
		if to.signed {
			lo, hi := -math.Ldexp(1, bitsN-1), math.Ldexp(1, bitsN-1)
			switch {
			case r < lo:
				return clampTo(1<<63, true, to), types.PanicNone
			case r >= hi:
				return clampTo(1<<63-1, false, to), types.PanicNone
			}
			return uint64(int64(r)) & mask(to.width), types.PanicNone
		}
		switch {
		case r <= 0:
			return 0, types.PanicNone
		case r >= math.Ldexp(1, bitsN):
			return mask(to.width), types.PanicNone
		}
		return uint64(r) & mask(to.width), types.PanicNone
	case from.class == cFloat && to.class == cFloat && from.width == 8 && to.width == 4:
		return f32bits(float32(f64of(x))), types.PanicNone
	}
	return cast(mir.CastChecked, from, to, x) // integer to float, f32 to f64: never panics
}

// vmaskReduce folds the lanes of a mask into one scalar result.
func vmaskReduce(op func(set []bool) uint64) intrinsicFunc {
	return vec(func(m *Machine, f *frame, t *mir.Terminator, _ types.FnRef, lane *tinfo) {
		set := m.maskLanes(m.vargLoc(f, t, 0), lane)
		m.vchargeIO(uint64(len(set))*lane.size + 8)
		m.store(m.dest(f, t), op(set))
	})
}

// sysRefusals are the systems intrinsics eval refuses: every std.mmio
// intrinsic, since a device access has no compile-time meaning. They are
// kept apart from refusals, which holds the runtime's list
// (TestRuntimeIntrinsicsRefused).
var sysRefusals = map[string]types.Refusal{
	"std.mmio.Load":      types.RefuseVolatile,
	"std.mmio.Store":     types.RefuseVolatile,
	"std.mmio.Fence":     types.RefuseVolatile,
	"std.mmio.Reg:New":   types.RefuseVolatile,
	"std.mmio.Reg:Read":  types.RefuseVolatile,
	"std.mmio.Reg:Write": types.RefuseVolatile,
}
