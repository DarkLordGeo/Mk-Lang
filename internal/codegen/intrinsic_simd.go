// Why: std.simd's Vec and Mask operations must compile to LLVM vector
// instructions, while the types keep the plain aggregate layout and ABI of
// every other value.
//
// What: simdIntrinsic and simdCall, whose emit switches on the intrinsic
// key, with helpers to load and store vectors and masks (vec, mask,
// maskWords, storeVec, storeMask), shuffles, lane-wise min and max, casts
// (cast, castSat, clamp) and lookup16.
//
// How: a Vec[T, N] or Mask[T, N] lives in memory as bytes, like any
// aggregate (repr rAgg, so every local, field, argument and result of one
// keeps the aggregate layout and ABI); each intrinsic loads its operands as
// <N x T> with align AlignOf[T], computes in SSA, and stores its result the
// same way, so <N x T> exists only in SSA and LLVM promotes the memory to
// vector registers. A Mask is <N x iW> in memory, each lane 0 or all ones,
// and <N x i1> in SSA: a load compares against zero, a store sign-extends
// (And, Or, Xor and Not work on the words themselves, maskWords). No
// fast-math flag is emitted anywhere.

package codegen

import (
	"math"
	"strings"

	"mk/internal/llvm"
	"mk/internal/mir"
	"mk/internal/source"
	"mk/internal/types"
)

// simdCall is one std.simd intrinsic call being emitted.
type simdCall struct {
	fg   *fnGen
	g    *packGen
	m    *llvm.Module
	t    *mir.Terminator
	key  string
	args []value
	ga   []types.GenericArg
}

func (fg *fnGen) simdIntrinsic(key string, t *mir.Terminator, args []value) {
	s := &simdCall{fg: fg, g: fg.g, m: fg.g.m, t: t, key: key, args: args,
		ga: fg.b.GenericArgs[t.Callee.Fn.Args.Start:t.Callee.Fn.Args.End()]}
	s.emit()
}

func (s *simdCall) ice(format string, a ...any) {
	source.ICEf(s.fg.spanOf(s.t.Span), "codegen: %s: "+format, append([]any{s.key}, a...)...)
}

// typeArg is generic argument k, a type.
func (s *simdCall) typeArg(k int) types.TypeID {
	if k >= len(s.ga) || s.ga[k].IsConst {
		s.ice("no type argument %d", k)
	}
	return s.ga[k].Type
}

// lane facts of a lane type.
func (s *simdCall) elem(lane types.TypeID) llvm.Type { return s.g.reprOf(lane).ssa }
func (s *simdCall) isFloat(lane types.TypeID) bool   { return s.g.reprOf(lane).class == sFloat }
func (s *simdCall) signed(lane types.TypeID) bool    { return s.g.reprOf(lane).signed }
func (s *simdCall) width(lane types.TypeID) int      { return int(s.g.reprOf(lane).size) * 8 }
func (s *simdCall) laneAlign(lane types.TypeID) uint32 {
	return max(s.g.reprOf(lane).align, 1)
}

// count is the lane count of the Vec or Mask type vt of lane type lane.
func (s *simdCall) count(vt, lane types.TypeID) int {
	size, _ := s.g.sizeAlign(vt)
	ls, _ := s.g.sizeAlign(lane)
	if ls == 0 || size%ls != 0 || size/ls < 1 || size/ls > 64 {
		s.ice("%s is not lanes of %s", s.g.tt.TypeString(vt, nil), s.g.tt.TypeString(lane, nil))
	}
	return int(size / ls)
}

func (s *simdCall) vecTy(lane types.TypeID, n int) llvm.Type { return s.m.Vector(n, s.elem(lane)) }
func (s *simdCall) intVecTy(lane types.TypeID, n int) llvm.Type {
	return s.m.Vector(n, s.m.I(s.width(lane)))
}
func (s *simdCall) i1Vec(n int) llvm.Type { return s.m.Vector(n, s.m.I(1)) }

// vec loads argument i, a Vec of lane type lane, as <N x T>.
func (s *simdCall) vec(i int, lane types.TypeID) llvm.Value {
	v := s.args[i]
	a := s.fg.addrOf(v)
	return s.fg.cur.Load(s.vecTy(lane, s.count(v.t, lane)), a.v, s.laneAlign(lane))
}

// mask loads argument i, a Mask of lane type lane, as <N x i1>.
func (s *simdCall) mask(i int, lane types.TypeID) llvm.Value {
	v := s.args[i]
	a := s.fg.addrOf(v)
	n := s.count(v.t, lane)
	w := s.fg.cur.Load(s.intVecTy(lane, n), a.v, s.laneAlign(lane))
	return s.fg.cur.ICmp(llvm.INE, w, s.m.ZeroInit(w.Ty))
}

// maskWords loads argument i, a Mask of lane type lane, in its memory
// form <N x iW>: each lane 0 or all ones.
func (s *simdCall) maskWords(i int, lane types.TypeID) llvm.Value {
	v := s.args[i]
	a := s.fg.addrOf(v)
	return s.fg.cur.Load(s.intVecTy(lane, s.count(v.t, lane)), a.v, s.laneAlign(lane))
}

// dest is the result's address and type.
func (s *simdCall) dest() (addr, types.TypeID) {
	d, _ := s.fg.placeAddr(s.t.Dest)
	return d, s.fg.b.PlaceType(s.t.Dest)
}

// storeVec stores a vector result of lane type lane, then continues. A
// result whose bytes are not the destination's is a size clause no one
// judged: a generic over the lane types calls WidenLo,
// NarrowSat or Reinterpret, and check judges the clause neither there nor
// at the instance. Over the generic's bare lane type parameter the clause
// must be restated, so this is a lane type built on a type
// parameter, or a restatement check demands and does not judge (one that
// also names SizeOf of a closed type), and the
// instance breaks it. The store would write past the destination or leave
// it short, so the call panics with PanicContract instead, in every
// profile. (tests/programs/panics/simd_*_restated_closed.mk trip the
// Reinterpret and NarrowSat guards before this one.)
func (s *simdCall) storeVec(x llvm.Value, lane types.TypeID) {
	d, dt := s.dest()
	if size, _ := s.g.sizeAlign(dt); x.Ty.Len()*uint64(x.Ty.Elem(0).Bits()) != size*8 {
		s.unjudgedClause()
		return
	}
	s.fg.cur.Store(x, d.v, s.laneAlign(lane))
	s.done()
}

// unjudgedClause ends the call with a PanicContract panic: its requires
// clause, false for this instance, was not judged at compile time.
func (s *simdCall) unjudgedClause() {
	s.fg.trap(s.m.ConstInt(s.m.I(1), 1), types.PanicContract)
	s.done()
}

// laneTargetOK is check's laneTarget and the
// widening clause SizeOf[U]() == 2 * SizeOf[T]() for an instance. check
// skips the lane class when T or U is a type parameter and
// leaves the clause to this backstop in storeVec's cases; a lane
// class they forbid is not a valid LLVM cast (fpext to an integer, zext to
// a float). Trunc: integer lanes, U no wider than T. The widening forms:
// integers of one signedness twice as wide, or f32 to f64.
func (s *simdCall) laneTargetOK(trunc bool, lane, u types.TypeID) bool {
	lf, uf := s.isFloat(lane), s.isFloat(u)
	switch {
	case trunc:
		return !lf && !uf && s.width(u) <= s.width(lane)
	case lf || uf:
		return lane == types.F32 && u == types.F64
	}
	return s.signed(lane) == s.signed(u) && s.width(u) == 2*s.width(lane)
}

// storeMask stores an <N x i1> result as a Mask of lane type lane.
func (s *simdCall) storeMask(x llvm.Value, lane types.TypeID) {
	w := s.fg.cur.Cast(llvm.SExt, x, s.intVecTy(lane, int(x.Ty.Len())))
	s.storeVec(w, lane)
}

// ret stores a scalar result (bool as i8), then continues.
func (s *simdCall) ret(v llvm.Value) {
	d, dt := s.dest()
	if s.g.reprOf(dt).kind != rZero {
		s.fg.store(d, value{t: dt, kind: rScalar, v: v})
	}
	s.done()
}

func (s *simdCall) done() { s.fg.cur.Br(s.fg.blocks[s.t.Target]) }

// constArg is the integer value of the const argument i (Lane's i, the
// permutations' k), an immediate or a const after mono's substitution.
func (s *simdCall) constArg(i int) int64 {
	op := s.fg.b.Args[s.t.Args.Start+uint32(i)].Operand
	switch op.Kind {
	case mir.OpImm:
		return int64(op.Bits)
	case mir.OpConst:
		cv := s.g.env.Consts().Get(op.Const)
		if len(cv.Bytes) >= 8 {
			return int64(types.LoadUint(cv.Bytes, 8))
		}
	}
	s.ice("argument %d is no constant", i)
	return 0
}

// constInts is the [N]int const argument i (Shuffle's idx).
func (s *simdCall) constInts(i, n int) []int {
	op := s.fg.b.Args[s.t.Args.Start+uint32(i)].Operand
	if op.Kind != mir.OpConst {
		s.ice("argument %d is no constant array", i)
	}
	b := s.g.env.Consts().Get(op.Const).Bytes
	if len(b) < 8*n {
		s.ice("argument %d holds %d bytes for %d indices", i, len(b), n)
	}
	out := make([]int, n)
	for j := range out {
		out[j] = int(int64(types.LoadUint(b[8*j:], 8)))
	}
	return out
}

func (s *simdCall) i32(x int) llvm.Value    { return s.m.ConstInt(s.m.I(32), uint64(x)) }
func (s *simdCall) i64(x uint64) llvm.Value { return s.m.ConstInt(s.m.I(64), x) }

// splat is <n x x>: insertelement into poison, then a zero-mask shuffle.
func (s *simdCall) splat(x llvm.Value, n int) llvm.Value {
	vt := s.m.Vector(n, x.Ty)
	v := s.fg.cur.InsertElement(s.m.Poison(vt), x, s.i32(0))
	return s.fg.cur.ShuffleVector(v, s.m.Poison(vt), make([]int, n))
}

// constSplat is the constant vector of n lanes of the integer bits x.
func (s *simdCall) constSplat(t llvm.Type, n int, x uint64) llvm.Value {
	vs := make([]llvm.Value, n)
	for j := range vs {
		vs[j] = s.m.ConstInt(t, x)
	}
	return s.m.ConstVector(t, vs...)
}

// iota is the constant <0, 1, .., n-1> of the integer type t.
func (s *simdCall) iota(t llvm.Type, n int) llvm.Value {
	vs := make([]llvm.Value, n)
	for j := range vs {
		vs[j] = s.m.ConstInt(t, uint64(j))
	}
	return s.m.ConstVector(t, vs...)
}

// call calls the LLVM intrinsic name, overloaded on the types given.
func (s *simdCall) call(name string, over []llvm.Type, args ...llvm.Value) llvm.Value {
	return s.fg.cur.CallFunc(s.m.IntrinsicFn(name, over...), args)
}

// anyLane is llvm.vector.reduce.or of an <N x i1>: true when a lane is set.
func (s *simdCall) anyLane(c llvm.Value) llvm.Value {
	return s.call("vector.reduce.or", []llvm.Type{c.Ty}, c)
}

// ext widens an integer vector to the element type to, by signedness.
func (s *simdCall) ext(v llvm.Value, signed bool, to llvm.Type) llvm.Value {
	n := int(v.Ty.Len())
	from := v.Ty.Elem(0).Bits()
	vt := s.m.Vector(n, to)
	switch {
	case from == to.Bits():
		return v
	case from > to.Bits():
		return s.fg.cur.Cast(llvm.Trunc, v, vt)
	case signed:
		return s.fg.cur.Cast(llvm.SExt, v, vt)
	}
	return s.fg.cur.Cast(llvm.ZExt, v, vt)
}

// slice is argument i, a []T or mut []T: its data pointer and length.
func (s *simdCall) slice(i int) (llvm.Value, llvm.Value) {
	a := s.fg.addrOf(s.args[i])
	p := s.fg.cur.Load(s.m.Ptr(), a.v, alignAt(a.align, 0), s.m.Metadata().NonNull(), s.m.Metadata().NoUndef())
	n := s.fg.cur.Load(s.m.I(64), s.fg.gep(a.v, 8), alignAt(a.align, 8), s.m.Metadata().NoUndef())
	return p, n
}

// elemAddr is the address of element i (i64) of a slice of lane type lane.
func (s *simdCall) elemAddr(p, i llvm.Value, lane types.TypeID) llvm.Value {
	size, _ := s.g.sizeAlign(lane)
	off := s.fg.cur.Bin(llvm.Mul, i, s.i64(size), 0)
	return s.fg.cur.GEPBytes(p, off, true)
}

// spanCheck is FromSlice's and CopyTo's one check: 0 <= i <= len - n,
// one branch to the bounds panic. Both compares are signed:
// len - n cannot overflow (len >= 0, n <= 64) and is negative when the
// slice is shorter than n, and a loop `for i + n <= xs.Len()` with i
// counting up from 0 proves both false, so LLVM removes the check there.
func (s *simdCall) spanCheck(i, length llvm.Value, n int) {
	neg := s.fg.cur.ICmp(llvm.ISLT, i, s.i64(0))
	past := s.fg.cur.ICmp(llvm.ISGT, i, s.fg.cur.Bin(llvm.Sub, length, s.i64(uint64(n)), llvm.NSW))
	s.fg.trap(s.fg.cur.Bin(llvm.Or, neg, past, 0), types.PanicBounds)
}

// shuffle1 is a permutation of one vector by the index function idx.
func (s *simdCall) shuffle(a, b llvm.Value, n int, idx func(j int) int) llvm.Value {
	mask := make([]int, n)
	for j := range mask {
		mask[j] = idx(j)
	}
	return s.fg.cur.ShuffleVector(a, b, mask)
}

// half is lanes [lo, lo+n) of v.
func (s *simdCall) half(v llvm.Value, lo, n int) llvm.Value {
	return s.shuffle(v, s.m.Poison(v.Ty), n, func(j int) int { return lo + j })
}

// minmax is the lane-wise min (max false) or max of two vectors.
func (s *simdCall) minmax(a, b llvm.Value, lane types.TypeID, max bool) llvm.Value {
	var name string
	switch {
	case s.isFloat(lane) && max:
		name = "maximum"
	case s.isFloat(lane):
		name = "minimum"
	case max && s.signed(lane):
		name = "smax"
	case max:
		name = "umax"
	case s.signed(lane):
		name = "smin"
	default:
		name = "umin"
	}
	return s.call(name, []llvm.Type{a.Ty}, a, b)
}

func (s *simdCall) emit() {
	key := strings.TrimPrefix(s.key, "std.simd.")
	switch key {
	case "Lookup16":
		s.lookup16()
		return
	case "Cpu":
		s.cpu()
		return
	case "Lanes":
		// Target.vector_bits / (8 * SizeOf[T]()), a constant per
		// instance (a concrete T is folded by check before lower).
		size, _ := s.g.sizeAlign(s.typeArg(0))
		vb := uint64(s.g.cfg.Target.VectorBits)
		if size == 0 || vb == 0 {
			s.ice("Lanes of a type of size %d on a target of vector width %d", size, vb)
		}
		s.ret(s.i64(vb / (8 * size)))
		return
	case "CpuFeatures:Of":
		// A constant: the bits of the names' closure.
		tg := s.g.cfg.Target
		set, _ := types.CPUClosure(tg.Arch, cpuNames(s.constStr(0)))
		w := cpuBits(tg.Arch, set)
		d, _ := s.dest()
		s.fg.cur.Store(s.i64(w[0]), d.v, alignAt(d.align, 0))
		s.fg.cur.Store(s.i64(w[1]), s.fg.gep(d.v, 8), alignAt(d.align, 8))
		s.done()
		return
	case "CpuFeatures:Has":
		// The set holds every feature of the name's closure; another
		// architecture's name is false.
		tg := s.g.cfg.Target
		name := s.constStr(1)
		if _, ok := types.CPUFeatureByName(tg.Arch, name); !ok {
			s.ret(s.m.ConstInt(s.m.I(1), 0))
			return
		}
		set, _ := types.CPUClosure(tg.Arch, []string{name})
		w := cpuBits(tg.Arch, set)
		p := s.args[0].v
		b := s.fg.cur
		ok := s.m.ConstInt(s.m.I(1), 1)
		for k := 0; k < 2; k++ {
			x := b.Load(s.m.I(64), s.fg.gep(p, uint64(8*k)), 8)
			hit := b.ICmp(llvm.IEQ, b.Bin(llvm.And, x, s.i64(w[k]), 0), s.i64(w[k]))
			ok = b.Bin(llvm.And, ok, hit, 0)
		}
		s.ret(ok)
		return
	}
	lane := s.typeArg(0)
	b := s.fg.cur
	switch key {
	// Construction, memory and lanes.
	case "Vec:Splat":
		_, dt := s.dest()
		s.storeVec(s.splat(s.args[0].v, s.count(dt, lane)), lane)
	case "Vec:Zero":
		_, dt := s.dest()
		s.storeVec(s.m.ZeroInit(s.vecTy(lane, s.count(dt, lane))), lane)
	case "Vec:Iota":
		_, dt := s.dest()
		n := s.count(dt, lane)
		vs := make([]llvm.Value, n)
		for j := range vs {
			if s.isFloat(lane) {
				vs[j] = s.floatConst(lane, float64(j))
			} else {
				vs[j] = s.m.ConstInt(s.elem(lane), uint64(j))
			}
		}
		s.storeVec(s.m.ConstVector(s.elem(lane), vs...), lane)
	case "Vec:FromArray", "Vec:ToArray":
		s.storeVec(s.vec(0, lane), lane)
	case "Vec:FromSlice":
		_, dt := s.dest()
		n := s.count(dt, lane)
		p, length := s.slice(0)
		i := s.args[1].v
		s.spanCheck(i, length, n)
		s.storeVec(s.fg.cur.Load(s.vecTy(lane, n), s.elemAddr(p, i, lane), s.laneAlign(lane)), lane)
	case "Vec:FromSliceOr":
		_, dt := s.dest()
		n := s.count(dt, lane)
		p, length := s.slice(0)
		i := s.args[1].v
		s.fg.trap(s.fg.cur.ICmp(llvm.IUGT, i, length), types.PanicBounds) // 0 <= i <= len
		fill := s.vec(2, lane)
		b = s.fg.cur
		k := b.Bin(llvm.Sub, length, i, 0)
		on := b.ICmp(llvm.IULT, s.iota(s.m.I(64), n), s.splat(k, n))
		v := s.call("masked.load", []llvm.Type{fill.Ty, s.m.Ptr()}, s.elemAddr(p, i, lane), s.i32(int(s.laneAlign(lane))), on, fill)
		s.storeVec(v, lane)
	case "Vec:CopyTo":
		v := s.vec(0, lane)
		p, length := s.slice(1)
		i := s.args[2].v
		s.spanCheck(i, length, int(v.Ty.Len()))
		s.fg.cur.Store(v, s.elemAddr(p, i, lane), s.laneAlign(lane))
		s.done()
	case "Vec:CopyToMasked":
		v := s.vec(0, lane)
		n := int(v.Ty.Len())
		p, length := s.slice(1)
		i := s.args[2].v
		on := s.mask(3, lane)
		b = s.fg.cur
		idx := b.Bin(llvm.Add, s.splat(i, n), s.iota(s.m.I(64), n), 0)
		out := b.Bin(llvm.And, b.ICmp(llvm.IUGE, idx, s.splat(length, n)), on, 0)
		bad := b.Bin(llvm.Or, s.anyLane(out), b.ICmp(llvm.ISLT, i, s.i64(0)), 0)
		s.fg.trap(bad, types.PanicBounds)
		s.call("masked.store", []llvm.Type{v.Ty, s.m.Ptr()}, v, s.elemAddr(p, i, lane), s.i32(int(s.laneAlign(lane))), on)
		s.done()
	case "Vec:Gather":
		it := s.typeArg(2)
		p, length := s.slice(0)
		idx := s.ext(s.vec(1, it), s.signed(it), s.m.I(64))
		n := int(idx.Ty.Len())
		b = s.fg.cur
		s.fg.trap(s.anyLane(b.ICmp(llvm.IUGE, idx, s.splat(length, n))), types.PanicBounds)
		b = s.fg.cur
		size, _ := s.g.sizeAlign(lane)
		ptrs := b.GEPVector(p, b.Bin(llvm.Mul, idx, s.constSplat(s.m.I(64), n, size), 0))
		vt := s.vecTy(lane, n)
		all := s.constSplat(s.m.I(1), n, 1)
		s.storeVec(s.call("masked.gather", []llvm.Type{vt, ptrs.Ty}, ptrs, s.i32(int(s.laneAlign(lane))), all, s.m.Poison(vt)), lane)
	case "Vec:Lane":
		s.ret(b.ExtractElement(s.vec(0, lane), s.i32(int(s.constArg(1)))))
	case "Vec:With":
		v := s.vec(0, lane)
		s.storeVec(s.fg.cur.InsertElement(v, s.args[2].v, s.i32(int(s.constArg(1)))), lane)

	// Permutations: the evaluator's index functions (intrinsic_simd.go in
	// eval), as shufflevector masks.
	case "Vec:Shuffle":
		a, c := s.vec(0, lane), s.vec(1, lane)
		n := int(a.Ty.Len())
		idx := s.constInts(2, n)
		for _, k := range idx {
			if k < 0 || k >= 2*n {
				s.ice("Shuffle index %d outside 0 to %d (check's E0706)", k, 2*n-1)
			}
		}
		s.storeVec(s.fg.cur.ShuffleVector(a, c, idx), lane)
	case "Vec:Reverse":
		a := s.vec(0, lane)
		n := int(a.Ty.Len())
		s.storeVec(s.shuffle(a, a, n, func(j int) int { return n - 1 - j }), lane)
	case "Vec:Broadcast":
		a := s.vec(0, lane)
		k := int(s.constArg(1))
		s.storeVec(s.shuffle(a, a, int(a.Ty.Len()), func(int) int { return k }), lane)
	case "Vec:Rotate":
		a := s.vec(0, lane)
		n, k := int(a.Ty.Len()), int(s.constArg(1))
		s.storeVec(s.shuffle(a, a, n, func(j int) int { return (j + k) % n }), lane)
	case "Vec:Slide":
		a, prev := s.vec(0, lane), s.vec(1, lane)
		n, k := int(a.Ty.Len()), int(s.constArg(2))
		s.storeVec(s.shuffle(a, prev, n, func(j int) int {
			if j < k {
				return n + n - k + j
			}
			return j - k
		}), lane)
	case "Vec:InterleaveLo", "Vec:InterleaveHi", "Vec:DeinterleaveEven", "Vec:DeinterleaveOdd":
		a, c := s.vec(0, lane), s.vec(1, lane)
		n := int(a.Ty.Len())
		s.storeVec(s.shuffle(a, c, n, func(j int) int {
			switch key {
			case "Vec:InterleaveLo":
				if j%2 == 0 {
					return j / 2
				}
				return n + j/2
			case "Vec:InterleaveHi":
				if j%2 == 0 {
					return n/2 + j/2
				}
				return n + n/2 + j/2
			case "Vec:DeinterleaveEven":
				return 2 * j
			}
			return 2*j + 1
		}), lane)

	// Reductions.
	case "Vec:ReduceSum":
		v := s.vec(0, lane)
		for n := int(v.Ty.Len()); n > 1; n /= 2 {
			h := n / 2
			lo, hi := s.half(v, 0, h), s.half(v, h, h)
			v = s.fg.cur.Bin(llvm.FAdd, lo, hi, 0)
		}
		s.ret(s.fg.cur.ExtractElement(v, s.i32(0)))
	case "Vec:ReduceSumWrap":
		v := s.vec(0, lane)
		s.ret(s.call("vector.reduce.add", []llvm.Type{v.Ty}, v))
	case "Vec:ReduceSumWide":
		v := s.ext(s.vec(0, lane), s.signed(lane), s.m.I(64))
		s.ret(s.call("vector.reduce.add", []llvm.Type{v.Ty}, v))
	case "Vec:ReduceMin", "Vec:ReduceMax":
		v := s.vec(0, lane)
		max := key == "Vec:ReduceMax"
		var op string
		switch {
		case s.isFloat(lane) && max:
			op = "fmaximum"
		case s.isFloat(lane):
			op = "fminimum"
		case s.signed(lane) && max:
			op = "smax"
		case s.signed(lane):
			op = "smin"
		case max:
			op = "umax"
		default:
			op = "umin"
		}
		s.ret(s.call("vector.reduce."+op, []llvm.Type{v.Ty}, v))
	case "Vec:ReduceAnd", "Vec:ReduceOr", "Vec:ReduceXor":
		v := s.vec(0, lane)
		s.ret(s.call("vector.reduce."+strings.ToLower(strings.TrimPrefix(key, "Vec:Reduce")), []llvm.Type{v.Ty}, v))

	// Arithmetic.
	case "Vec:Min", "Vec:Max":
		a, c := s.vec(0, lane), s.vec(1, lane)
		s.storeVec(s.minmax(a, c, lane, key == "Vec:Max"), lane)
	case "Vec:Abs":
		v := s.vec(0, lane)
		s.storeVec(s.call("fabs", []llvm.Type{v.Ty}, v), lane)
	case "Vec:AbsWrap":
		v := s.vec(0, lane)
		s.storeVec(s.call("abs", []llvm.Type{v.Ty}, v, s.m.ConstInt(s.m.I(1), 0)), lane)
	case "Vec:AddSat", "Vec:SubSat":
		a, c := s.vec(0, lane), s.vec(1, lane)
		name := "uadd.sat"
		switch {
		case key == "Vec:AddSat" && s.signed(lane):
			name = "sadd.sat"
		case key == "Vec:SubSat" && s.signed(lane):
			name = "ssub.sat"
		case key == "Vec:SubSat":
			name = "usub.sat"
		}
		s.storeVec(s.call(name, []llvm.Type{a.Ty}, a, c), lane)
	case "Vec:MulHigh":
		a, c := s.vec(0, lane), s.vec(1, lane)
		w := s.width(lane)
		sg := s.signed(lane)
		wide := s.m.I(2 * w)
		p := s.fg.cur.Bin(llvm.Mul, s.ext(a, sg, wide), s.ext(c, sg, wide), 0)
		sh := llvm.LShr
		if sg {
			sh = llvm.AShr
		}
		p = s.fg.cur.Bin(sh, p, s.constSplat(wide, int(a.Ty.Len()), uint64(w)), 0)
		s.storeVec(s.ext(p, sg, s.elem(lane)), lane)
	case "Vec:Fma":
		a, c, d := s.vec(0, lane), s.vec(1, lane), s.vec(2, lane)
		s.storeVec(s.call("fma", []llvm.Type{a.Ty}, a, c, d), lane)
	case "Vec:Sqrt", "Vec:Floor", "Vec:Ceil", "Vec:RoundEven", "Vec:RoundToZero":
		v := s.vec(0, lane)
		name := map[string]string{"Vec:Sqrt": "sqrt", "Vec:Floor": "floor", "Vec:Ceil": "ceil",
			"Vec:RoundEven": "roundeven", "Vec:RoundToZero": "trunc"}[key]
		s.storeVec(s.call(name, []llvm.Type{v.Ty}, v), lane)
	case "Vec:DivTrunc":
		a, c := s.vec(0, lane), s.vec(1, lane)
		n := int(a.Ty.Len())
		b = s.fg.cur
		bad := b.ICmp(llvm.IEQ, c, s.m.ZeroInit(c.Ty))
		op := llvm.UDiv
		if s.signed(lane) {
			w := s.width(lane)
			minV := s.constSplat(s.elem(lane), n, uint64(1)<<(w-1))
			ones := s.constSplat(s.elem(lane), n, ^uint64(0))
			over := b.Bin(llvm.And, b.ICmp(llvm.IEQ, a, minV), b.ICmp(llvm.IEQ, c, ones), 0)
			bad = b.Bin(llvm.Or, bad, over, 0)
			op = llvm.SDiv
		}
		// Scalar /'s code for both faults (types.PanicDivideByZero).
		s.fg.trap(s.anyLane(bad), types.PanicDivideByZero)
		s.storeVec(s.fg.cur.Bin(op, a, c, 0), lane)
	case "Vec:ShlEach", "Vec:ShrEach":
		a, k := s.vec(0, lane), s.vec(1, lane)
		n := int(a.Ty.Len())
		s.fg.trap(s.anyLane(s.fg.cur.ICmp(llvm.IUGE, k, s.constSplat(s.elem(lane), n, uint64(s.width(lane))))), types.PanicShiftAmount)
		op := llvm.Shl
		switch {
		case key == "Vec:ShrEach" && s.signed(lane):
			op = llvm.AShr
		case key == "Vec:ShrEach":
			op = llvm.LShr
		}
		s.storeVec(s.fg.cur.Bin(op, a, k, 0), lane)
	case "Vec:WidenLo", "Vec:WidenHi", "Vec:MulWidenLo", "Vec:MulWidenHi":
		u := s.typeArg(2)
		a := s.vec(0, lane)
		n := int(a.Ty.Len())
		// The clause's lane class and M >= 2 (M is n / 2; storeVec judges
		// the size), as the evaluator's widenHalf.
		if !s.laneTargetOK(false, lane, u) || n < 4 {
			s.unjudgedClause()
			break
		}
		lo := 0
		if strings.HasSuffix(key, "Hi") {
			lo = n / 2
		}
		widen := func(v llvm.Value) llvm.Value {
			h := s.half(v, lo, n/2)
			if s.isFloat(lane) {
				return s.fg.cur.Cast(llvm.FPExt, h, s.vecTy(u, n/2))
			}
			return s.ext(h, s.signed(lane), s.elem(u))
		}
		r := widen(a)
		if strings.HasPrefix(key, "Vec:Mul") {
			r = s.fg.cur.Bin(llvm.Mul, r, widen(s.vec(1, lane)), 0)
		}
		s.storeVec(r, u)
	case "Vec:NarrowSat":
		u := s.typeArg(2)
		a, c := s.vec(0, lane), s.vec(1, lane)
		n := int(a.Ty.Len())
		// The clause, where check left it unjudged (storeVec's cases):
		// integer lanes, 2 * SizeOf[U]() == SizeOf[T](), M == 2 * N <= 64
		// (eval's narrowOK).
		if s.isFloat(lane) || s.isFloat(u) || 2*s.width(u) != s.width(lane) || 2*n > 64 {
			s.unjudgedClause()
			break
		}
		v := s.shuffle(a, c, 2*n, func(j int) int { return j })
		s.storeVec(s.clamp(v, lane, u), u)

	// Conversions.
	case "Vec:Cast":
		s.storeVec(s.cast(s.vec(0, lane), lane, s.typeArg(2)), s.typeArg(2))
	case "Vec:Trunc":
		u := s.typeArg(2)
		if !s.laneTargetOK(true, lane, u) {
			s.unjudgedClause()
			break
		}
		s.storeVec(s.ext(s.vec(0, lane), false, s.elem(u)), u)
	case "Vec:CastSat":
		s.storeVec(s.castSat(s.vec(0, lane), lane, s.typeArg(2)), s.typeArg(2))
	case "Vec:Reinterpret":
		u := s.typeArg(2)
		v := s.vec(0, lane)
		_, dt := s.dest()
		// The same bytes, M * SizeOf[U]() == N * SizeOf[T](): where check
		// left it unjudged in a generic over T or U (storeVec's
		// cases), checked here before count and the bitcast, which
		// would ICE.
		src, _ := s.g.sizeAlign(s.args[0].t)
		dst, _ := s.g.sizeAlign(dt)
		if us, _ := s.g.sizeAlign(u); src != dst || us == 0 || dst%us != 0 || dst/us < 2 || dst/us > 64 {
			s.unjudgedClause()
			break
		}
		s.storeVec(s.fg.cur.Cast(llvm.BitCast, v, s.vecTy(u, s.count(dt, u))), u)

	// Masks.
	case "Mask:FromBits":
		_, dt := s.dest()
		n := s.count(dt, lane)
		bits := s.args[0].v
		if n < 64 {
			bits = s.fg.cur.Cast(llvm.Trunc, bits, s.m.I(n))
		}
		s.storeMask(s.fg.cur.Cast(llvm.BitCast, bits, s.i1Vec(n)), lane)
	case "Mask:Bits":
		x := s.maskBits(0, lane)
		s.ret(s.zext64(x))
	case "Mask:Any":
		s.ret(s.anyLane(s.mask(0, lane)))
	case "Mask:All":
		c := s.mask(0, lane)
		s.ret(s.call("vector.reduce.and", []llvm.Type{c.Ty}, c))
	case "Mask:CountSet":
		x := s.maskBits(0, lane)
		s.ret(s.zext64(s.call("ctpop", []llvm.Type{x.Ty}, x)))
	case "Mask:FirstSet":
		x := s.maskBits(0, lane)
		b = s.fg.cur
		some := b.ICmp(llvm.INE, x, s.m.ZeroInit(x.Ty))
		at := s.zext64(s.call("cttz", []llvm.Type{x.Ty}, x, s.m.ConstInt(s.m.I(1), 0)))
		d, dt := s.dest()
		l := s.g.layout(dt)
		b = s.fg.cur
		b.Store(b.Cast(llvm.ZExt, some, s.m.I(8*int(l.Tag.Size))), s.fg.gep(d.v, l.Tag.Offset), alignAt(d.align, l.Tag.Offset))
		off := s.g.variant(dt, 1).Fields[0].Offset
		b.Store(at, s.fg.gep(d.v, off), alignAt(d.align, off))
		s.done()
	case "Mask:Select":
		c := s.mask(0, lane)
		a, e := s.vec(1, lane), s.vec(2, lane)
		s.storeVec(s.fg.cur.Select(c, a, e), lane)
	case "Mask:Cast":
		s.storeMask(s.mask(0, lane), s.typeArg(2))

	// The operator intrinsics.
	case "Vec:Add", "Vec:Sub", "Vec:Mul", "Vec:Div", "Vec:AddWrap", "Vec:SubWrap", "Vec:MulWrap", "Vec:And", "Vec:Or", "Vec:Xor":
		a, c := s.vec(0, lane), s.vec(1, lane)
		op := map[string]llvm.BinOp{"Vec:Add": llvm.FAdd, "Vec:Sub": llvm.FSub, "Vec:Mul": llvm.FMul, "Vec:Div": llvm.FDiv,
			"Vec:AddWrap": llvm.Add, "Vec:SubWrap": llvm.Sub, "Vec:MulWrap": llvm.Mul,
			"Vec:And": llvm.And, "Vec:Or": llvm.Or, "Vec:Xor": llvm.Xor}[key]
		s.storeVec(s.fg.cur.Bin(op, a, c, 0), lane)
	case "Vec:Shl", "Vec:Shr":
		kt := s.typeArg(2)
		a := s.vec(0, lane)
		k := s.args[1].v
		kr := s.g.reprOf(kt)
		// The scalar rule: one check for the operation, an amount below 0 or
		// at least the lane width.
		s.fg.trap(s.fg.cur.ICmp(llvm.IUGE, s.fg.intTo(k, kr.signed, s.m.I(64)), s.i64(uint64(s.width(lane)))), types.PanicShiftAmount)
		amt := s.splat(s.fg.intTo(k, kr.signed, s.elem(lane)), int(a.Ty.Len()))
		op := llvm.Shl
		switch {
		case key == "Vec:Shr" && s.signed(lane):
			op = llvm.AShr
		case key == "Vec:Shr":
			op = llvm.LShr
		}
		s.storeVec(s.fg.cur.Bin(op, a, amt, 0), lane)
	case "Vec:Neg":
		v := s.vec(0, lane)
		s.storeVec(s.fg.cur.Bin(llvm.FNeg, v, v, 0), lane)
	case "Vec:Not":
		v := s.vec(0, lane)
		s.storeVec(s.fg.cur.Bin(llvm.Xor, v, s.constSplat(s.elem(lane), int(v.Ty.Len()), ^uint64(0)), 0), lane)
	case "Vec:Eq", "Vec:Ne", "Vec:Lt", "Vec:Le", "Vec:Gt", "Vec:Ge":
		a, c := s.vec(0, lane), s.vec(1, lane)
		if s.isFloat(lane) {
			p := map[string]llvm.FPred{"Vec:Eq": llvm.FOEQ, "Vec:Ne": llvm.FUNE, "Vec:Lt": llvm.FOLT,
				"Vec:Le": llvm.FOLE, "Vec:Gt": llvm.FOGT, "Vec:Ge": llvm.FOGE}[key]
			s.storeMask(s.fg.cur.FCmp(p, a, c), lane)
			return
		}
		p := map[string]llvm.IPred{"Vec:Eq": llvm.IEQ, "Vec:Ne": llvm.INE, "Vec:Lt": llvm.IULT,
			"Vec:Le": llvm.IULE, "Vec:Gt": llvm.IUGT, "Vec:Ge": llvm.IUGE}[key]
		if s.signed(lane) {
			p = map[string]llvm.IPred{"Vec:Eq": llvm.IEQ, "Vec:Ne": llvm.INE, "Vec:Lt": llvm.ISLT,
				"Vec:Le": llvm.ISLE, "Vec:Gt": llvm.ISGT, "Vec:Ge": llvm.ISGE}[key]
		}
		s.storeMask(s.fg.cur.ICmp(p, a, c), lane)
	case "Mask:And", "Mask:Or", "Mask:Xor":
		// On the lane words, not on <N x i1>: each word is 0 or all ones,
		// so the bitwise result is the lane-wise one, already in memory
		// form. Through <N x i1>, instcombine strips the sext and compare
		// around a loop-carried Mask and leaves a phi of <N x i1>, which
		// x86 keeps packed in another register shape and converts back at
		// every use (pack, shift, test; then sign-extend for Select).
		a, c := s.maskWords(0, lane), s.maskWords(1, lane)
		op := map[string]llvm.BinOp{"Mask:And": llvm.And, "Mask:Or": llvm.Or, "Mask:Xor": llvm.Xor}[key]
		s.storeVec(s.fg.cur.Bin(op, a, c, 0), lane)
	case "Mask:Not":
		w := s.maskWords(0, lane)
		s.storeVec(s.fg.cur.Bin(llvm.Xor, w, s.constSplat(w.Ty.Elem(0), int(w.Ty.Len()), ^uint64(0)), 0), lane)
	default:
		s.ice("no lowering")
	}
}

// maskBits loads Mask argument i as the integer iN of its lanes (lane j
// is bit j).
func (s *simdCall) maskBits(i int, lane types.TypeID) llvm.Value {
	c := s.mask(i, lane)
	return s.fg.cur.Cast(llvm.BitCast, c, s.m.I(int(c.Ty.Len())))
}

func (s *simdCall) zext64(x llvm.Value) llvm.Value {
	if x.Ty.Bits() == 64 {
		return x
	}
	return s.fg.cur.Cast(llvm.ZExt, x, s.m.I(64))
}

// floatConst is the float constant f of lane type lane.
func (s *simdCall) floatConst(lane types.TypeID, f float64) llvm.Value {
	if s.width(lane) == 32 {
		return s.m.ConstF32(math.Float32bits(float32(f)))
	}
	return s.m.ConstF64(math.Float64bits(f))
}

// clamp clamps the integer vector v of lane type from to the range of the
// integer type to, read by from's signedness, and truncates (NarrowSat,
// CastSat between integers; LLVM selects packss or packus).
func (s *simdCall) clamp(v llvm.Value, from, to types.TypeID) llvm.Value {
	n := int(v.Ty.Len())
	fw, tw := s.width(from), s.width(to)
	et := s.elem(from)
	if tw > fw {
		// Every value of from fits a wider to, except a negative lane into
		// an unsigned to, which clamps to 0 (as the evaluator's clampTo).
		if s.signed(from) && !s.signed(to) {
			v = s.call("smax", []llvm.Type{v.Ty}, v, s.m.ZeroInit(v.Ty))
			return s.ext(v, false, s.elem(to))
		}
		return s.ext(v, s.signed(from), s.elem(to))
	}
	hi := uint64(1)<<(tw-1) - 1
	if !s.signed(to) {
		hi = uint64(1)<<tw - 1
		if tw == 64 {
			hi = ^uint64(0)
		}
	}
	if s.signed(from) {
		lo := uint64(0)
		if s.signed(to) {
			lo = -(uint64(1) << (tw - 1))
		}
		if tw < fw || !s.signed(to) {
			v = s.call("smax", []llvm.Type{v.Ty}, v, s.constSplat(et, n, lo))
		}
		if tw < fw {
			v = s.call("smin", []llvm.Type{v.Ty}, v, s.constSplat(et, n, hi))
		}
	} else if tw < fw || s.signed(to) {
		v = s.call("umin", []llvm.Type{v.Ty}, v, s.constSplat(et, n, hi))
	}
	return s.ext(v, false, s.elem(to))
}

// cast is Vec.Cast: scalar Cast lane by lane, one vector range check and
// reduce.or for the whole vector, then
// the conversion.
func (s *simdCall) cast(v llvm.Value, from, to types.TypeID) llvm.Value {
	n := int(v.Ty.Len())
	ff, tf := s.isFloat(from), s.isFloat(to)
	fw, tw := s.width(from), s.width(to)
	vt := s.vecTy(to, n)
	switch {
	case !ff && !tf:
		et := s.elem(from)
		var bad llvm.Value
		or := func(c llvm.Value) {
			if bad.IsZero() {
				bad = c
			} else {
				bad = s.fg.cur.Bin(llvm.Or, bad, c, 0)
			}
		}
		if s.signed(from) {
			if !s.signed(to) {
				or(s.fg.cur.ICmp(llvm.ISLT, v, s.m.ZeroInit(v.Ty)))
				if tw < fw {
					or(s.fg.cur.ICmp(llvm.ISGT, v, s.constSplat(et, n, uint64(1)<<tw-1)))
				}
			} else if tw < fw {
				or(s.fg.cur.ICmp(llvm.ISLT, v, s.constSplat(et, n, -(uint64(1)<<(tw-1)))))
				or(s.fg.cur.ICmp(llvm.ISGT, v, s.constSplat(et, n, uint64(1)<<(tw-1)-1)))
			}
		} else {
			limit := uint64(0)
			check := false
			switch {
			case s.signed(to) && tw <= fw:
				limit, check = uint64(1)<<(tw-1)-1, true
			case !s.signed(to) && tw < fw:
				limit, check = uint64(1)<<tw-1, true
			}
			if check {
				or(s.fg.cur.ICmp(llvm.IUGT, v, s.constSplat(et, n, limit)))
			}
		}
		if !bad.IsZero() {
			s.fg.trap(s.anyLane(bad), types.PanicCast)
		}
		return s.ext(v, s.signed(from), s.elem(to))
	case !ff && tf:
		if s.signed(from) {
			return s.fg.cur.Cast(llvm.SIToFP, v, vt)
		}
		return s.fg.cur.Cast(llvm.UIToFP, v, vt)
	case ff && tf:
		if fw == tw {
			return v
		}
		if tw > fw {
			return s.fg.cur.Cast(llvm.FPExt, v, vt)
		}
		// Lane by lane: a finite lane that rounds to an infinity panics.
		r := s.fg.cur.Cast(llvm.FPTrunc, v, vt)
		inf32 := s.splatFloat(to, n, math.Inf(1))
		inf64 := s.splatFloat(from, n, math.Inf(1))
		b := s.fg.cur
		toInf := b.FCmp(llvm.FOEQ, s.call("fabs", []llvm.Type{r.Ty}, r), inf32)
		b = s.fg.cur
		wasFinite := b.FCmp(llvm.FONE, s.call("fabs", []llvm.Type{v.Ty}, v), inf64)
		s.fg.trap(s.anyLane(s.fg.cur.Bin(llvm.And, toInf, wasFinite, 0)), types.PanicCast)
		return r
	}
	// Float to integer: a NaN lane, or a lane whose truncation is outside
	// [lo, hi).
	var lo, hi float64
	if s.signed(to) {
		lo, hi = -pow2(tw-1), pow2(tw-1)
	} else {
		lo, hi = 0, pow2(tw)
	}
	tr := s.call("trunc", []llvm.Type{v.Ty}, v)
	b := s.fg.cur
	bad := b.FCmp(llvm.FUNO, v, v)
	bad = b.Bin(llvm.Or, bad, b.FCmp(llvm.FOLT, tr, s.splatFloat(from, n, lo)), 0)
	bad = b.Bin(llvm.Or, bad, b.FCmp(llvm.FOGE, tr, s.splatFloat(from, n, hi)), 0)
	s.fg.trap(s.anyLane(bad), types.PanicCast)
	if s.signed(to) {
		return s.fg.cur.Cast(llvm.FPToSI, v, vt)
	}
	return s.fg.cur.Cast(llvm.FPToUI, v, vt)
}

// castSat is Vec.CastSat: integers clamp; floats to integers saturate
// with NaN 0 (llvm.fptosi.sat); the rest convert as Cast without a panic.
func (s *simdCall) castSat(v llvm.Value, from, to types.TypeID) llvm.Value {
	n := int(v.Ty.Len())
	ff, tf := s.isFloat(from), s.isFloat(to)
	vt := s.vecTy(to, n)
	switch {
	case !ff && !tf:
		return s.clamp(v, from, to)
	case ff && !tf:
		name := "fptoui.sat"
		if s.signed(to) {
			name = "fptosi.sat"
		}
		return s.call(name, []llvm.Type{vt, v.Ty}, v)
	case !ff && tf:
		if s.signed(from) {
			return s.fg.cur.Cast(llvm.SIToFP, v, vt)
		}
		return s.fg.cur.Cast(llvm.UIToFP, v, vt)
	}
	switch fw, tw := s.width(from), s.width(to); {
	case fw < tw:
		return s.fg.cur.Cast(llvm.FPExt, v, vt)
	case fw > tw:
		return s.fg.cur.Cast(llvm.FPTrunc, v, vt)
	}
	return v
}

func pow2(k int) float64 { return math.Ldexp(1, k) }

// splatFloat is n lanes of the float f.
func (s *simdCall) splatFloat(lane types.TypeID, n int, f float64) llvm.Value {
	vs := make([]llvm.Value, n)
	for j := range vs {
		vs[j] = s.floatConst(lane, f)
	}
	return s.m.ConstVector(s.elem(lane), vs...)
}

// lookup16 is simd.Lookup16: lane j is table[idx[j]] when idx[j] < 16,
// else 0. On hosted x86_64, whose baseline holds ssse3, it is pshufb per 16
// lanes after mapping every index of 16 or more to 0x80 (pshufb's zero);
// elsewhere lane by lane.
func (s *simdCall) lookup16() {
	u8 := s.m.I(8)
	idx, table := s.vec(0, types.U8), s.vec(1, types.U8)
	n := int(idx.Ty.Len())
	tg := s.g.cfg.Target
	if tg.Arch == types.ArchX64 && tg.OS != types.OSFreestanding {
		b := s.fg.cur
		in := b.ICmp(llvm.IULT, idx, s.constSplat(u8, n, 16))
		sel := b.Select(in, idx, s.constSplat(u8, n, 0x80))
		var parts []llvm.Value
		for lo := 0; lo < n; lo += 16 {
			chunk := sel
			if n > 16 {
				chunk = s.half(sel, lo, 16)
			}
			parts = append(parts, s.call("x86.ssse3.pshuf.b.128", nil, table, chunk))
		}
		for len(parts) > 1 { // concatenate pairwise, in order
			var next []llvm.Value
			for k := 0; k < len(parts); k += 2 {
				w := int(parts[k].Ty.Len())
				next = append(next, s.shuffle(parts[k], parts[k+1], 2*w, func(j int) int { return j }))
			}
			parts = next
		}
		s.storeVec(parts[0], types.U8)
		return
	}
	b := s.fg.cur
	out := s.m.ZeroInit(idx.Ty)
	for j := 0; j < n; j++ {
		x := b.ExtractElement(idx, s.i32(j))
		in := b.ICmp(llvm.IULT, x, s.m.ConstInt(u8, 16))
		at := b.Bin(llvm.And, x, s.m.ConstInt(u8, 15), 0)
		v := b.Select(in, b.ExtractElement(table, at), s.m.ConstInt(u8, 0))
		out = b.InsertElement(out, v, s.i32(j))
	}
	s.storeVec(out, types.U8)
}
