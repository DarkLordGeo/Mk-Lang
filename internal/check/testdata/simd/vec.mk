// Why: portable vector code needs vector types and operations the compiler
// knows, so they compile to single instructions and run in the evaluator.
//
// What: the Lane interface, Vec, Mask, CpuFeatures, Cpu, Lanes and every
// vector intrinsic: arithmetic, comparisons, shuffles, loads, stores and
// reductions.
//
// How: every bodiless function here is an intrinsic, implemented by its
// stable key in eval and codegen and compiled inline, so none is a call.
// Unless its doc says otherwise, an intrinsic cannot fail, costs a few
// instructions for N lanes, and allocates nothing. This file is
// internal/check/testdata/simd/vec.mk, installed byte for byte as
// std/simd/vec.mk; TestVecFileInstalled keeps the two equal.

/// The types a vector lane may have: the sized integers, int, uint, f32 and
/// f64. The bound is built in, so an impl of it is E0704.
export interface Lane {
}

/// N lanes of T, laid out as [N]T and aligned as T. Every operation requires N
/// to be a power of two from 2 to 64; the type is well formed for any N. Plain.
export struct Vec[T: Lane, const N: int] {
    lanes: [N]T
}

/// The result of a lane-wise comparison of two Vec[T, N]: laid out as Vec over
/// the unsigned integer of T's width, each lane all zeros or all ones. Plain.
export struct Mask[T: Lane, const N: int] {
    lanes: [N]T
}

/// A set of CPU features of the target architecture, such as avx2 or
/// sse4.1. Plain, 16 bytes.
export struct CpuFeatures {
    bits: [2]u64
}

/// Returns the running CPU's features, read once and cached. A feature
/// whose registers the OS does not save is not reported, so every feature
/// reported can run. At compile time, the target's baseline. O(1) after the
/// first call; it allocates nothing.
export Cpu(): CpuFeatures

/// The set named by names, written as in a `cpu` clause ("avx2,bmi2" or
/// "x86-64-v3"); an unknown name is E1106. A constant; it allocates nothing.
export CpuFeatures.Of(const names: str): CpuFeatures

/// Reports whether the set holds every feature name implies; an unknown name is
/// E1106, another architecture's name is false. O(1); allocates nothing.
export CpuFeatures.Has(&self, const name: str): bool

/// Reports whether the set holds every feature of other. O(1); allocates
/// nothing.
export CpuFeatures.HasAll(&self, other: &CpuFeatures): bool {
    return self.bits[0] & other.bits[0] == other.bits[0] && self.bits[1] & other.bits[1] == other.bits[1]
}

/// The natural lane count of T, Target.vector_bits / (8 * SizeOf[T]()): 16 for
/// u8 and 4 for f32 on every supported target. A constant for a concrete T,
/// usable as a const argument (simd.Vec[f32, simd.Lanes[f32]()]); of a type
/// parameter it is a value at run time and not a const argument (E0703).
export Lanes[T: Lane](): int

// Construction, memory and lanes. FromSlice and CopyTo check the whole
// span once (i >= 0 and i + N <= xs.Len(), else the bounds panic);
// FromSliceOr reads the lanes that fit and takes the rest from fill;
// CopyToMasked stores the set lanes and checks only those; Gather checks
// every index.
/// A vector whose every lane is x. Inline; allocates nothing.
export Vec[T, N].Splat(x: T): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// A vector of zero lanes. Inline; allocates nothing.
export Vec[T, N].Zero(): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The vector whose lane j is j. Inline; allocates nothing.
export Vec[T, N].Iota(): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The vector of a's elements, lane j from a[j]. Inline; allocates nothing.
export Vec[T, N].FromArray(a: [N]T): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The lanes as an array. Inline; allocates nothing.
export Vec[T, N].ToArray(self): [N]T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The N elements of xs from i; panics (code 2) unless i >= 0 and i + N <=
/// xs.Len(), checked once. Inline; allocates nothing.
export Vec[T, N].FromSlice(xs: []T, i: int): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The elements of xs from i up to xs.Len(), the remaining lanes from fill;
/// panics (code 2) unless 0 <= i <= xs.Len(). Inline; allocates nothing.
export Vec[T, N].FromSliceOr(xs: []T, i: int, fill: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Stores the lanes to xs from i; panics (code 2) unless i >= 0 and i + N <=
/// xs.Len(), checked once. Inline; allocates nothing.
export Vec[T, N].CopyTo(self, xs: mut []T, i: int) requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Stores the lanes set in m to xs from i, checking only those against xs's
/// bounds (panic, code 2). Inline; allocates nothing.
export Vec[T, N].CopyToMasked(self, xs: mut []T, i: int, m: Mask[T, N]) requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The vector whose lane j is xs[idx lane j]; every index is checked (panic,
/// code 2). The index lanes I are integers (E0701 otherwise). Inline;
/// allocates nothing.
export Vec[T, N].Gather[I: Lane](xs: []T, idx: Vec[I, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Lane i; i is a constant from 0 to N - 1 (E0706 otherwise). Inline; allocates
/// nothing.
export Vec[T, N].Lane(self, const i: int): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires i >= 0 && i < N
/// self with lane i replaced by x; i is a constant from 0 to N - 1. Inline;
/// allocates nothing.
export Vec[T, N].With(self, const i: int, x: T): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires i >= 0 && i < N

// Permutations: indices into the concatenation of self and b, 0 to 2N - 1.
/// The vector whose lane j is lane idx[j] of self (0 to N - 1) or of b (N to 2N
/// - 1); an index outside is E0706. Inline; allocates nothing.
export Vec[T, N].Shuffle(self, b: Vec[T, N], const idx: [N]int): Vec[T, N] from self, b requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The lanes in reverse order. Inline; allocates nothing.
export Vec[T, N].Reverse(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// A vector whose every lane is lane i of self; i is a constant. Inline;
/// allocates nothing.
export Vec[T, N].Broadcast(self, const i: int): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires i >= 0 && i < N
/// The lanes rotated down by k: lane j is lane (j + k) mod N. Inline; allocates
/// nothing.
export Vec[T, N].Rotate(self, const k: int): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires k >= 0 && k < N
/// The last k lanes of prev followed by the first N - k of self. Inline;
/// allocates nothing.
export Vec[T, N].Slide(self, prev: Vec[T, N], const k: int): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires k >= 0 && k <= N
/// The low halves of self and b, lane by lane alternately: self 0, b 0, self 1,
/// b 1, and so on. Inline; allocates nothing.
export Vec[T, N].InterleaveLo(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The high halves of self and b, lane by lane alternately. Inline; allocates
/// nothing.
export Vec[T, N].InterleaveHi(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The even lanes of self, then the even lanes of b. Inline; allocates nothing.
export Vec[T, N].DeinterleaveEven(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The odd lanes of self, then the odd lanes of b. Inline; allocates nothing.
export Vec[T, N].DeinterleaveOdd(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Maps each lane of idx below 16 through table, and every other lane to 0
/// (pshufb). Inline; allocates nothing.
export Lookup16[const N: int](idx: Vec[u8, N], table: Vec[u8, 16]): Vec[u8, N] requires N == 16 || N == 32 || N == 64

// Reductions: ReduceSum is the pairwise tree, lanes [0, N/2) plus [N/2, N)
// down to one; the others are exact in any order.
/// The float lanes added as a pairwise tree, each half to the other, down to
/// one lane (E0701 for integer lanes). Inline; allocates nothing.
export Vec[T, N].ReduceSum(self): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The integer lanes added, wrapping; an integer sum has no trapping form,
/// so use ReduceSumWide for an exact one (E0701 for float lanes). Inline;
/// allocates nothing.
export Vec[T, N].ReduceSumWrap(self): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The integer lanes added exactly in i64; lanes of at most 32 bits only
/// (E0701 otherwise). Inline; allocates nothing.
export Vec[T, N].ReduceSumWide(self): i64 requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The least lane; NaN when a lane is NaN, -0 below +0. Inline; allocates
/// nothing.
export Vec[T, N].ReduceMin(self): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The greatest lane; NaN when a lane is NaN, -0 below +0. Inline; allocates
/// nothing.
export Vec[T, N].ReduceMax(self): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The integer lanes ANDed (E0701 for float lanes). Inline; allocates nothing.
export Vec[T, N].ReduceAnd(self): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The integer lanes ORed (E0701 for float lanes). Inline; allocates nothing.
export Vec[T, N].ReduceOr(self): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The integer lanes XORed (E0701 for float lanes). Inline; allocates nothing.
export Vec[T, N].ReduceXor(self): T requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64

// Arithmetic. Min and Max on floats propagate NaN and order -0 below +0.
/// The lesser of each pair of lanes; on floats NaN when either is NaN, -0 below
/// +0. Inline; allocates nothing.
export Vec[T, N].Min(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The greater of each pair of lanes; on floats NaN when either is NaN, -0
/// below +0. Inline; allocates nothing.
export Vec[T, N].Max(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The absolute value of each float lane (E0701 for integer lanes); AbsWrap
/// is the integer form. Inline; allocates nothing.
export Vec[T, N].Abs(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The absolute value of each signed integer lane, MIN left as it is (E0701
/// for other lanes). Inline; allocates nothing.
export Vec[T, N].AbsWrap(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Each pair of integer lanes added, clamped to the lane range (E0701 for
/// float lanes). Inline; allocates nothing.
export Vec[T, N].AddSat(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Each pair of integer lanes subtracted, clamped to the lane range (E0701
/// for float lanes). Inline; allocates nothing.
export Vec[T, N].SubSat(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The high half of each product of integer lanes of at most 32 bits (E0701
/// otherwise). Inline; allocates nothing.
export Vec[T, N].MulHigh(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// self * b + c rounded once, per float lane (E0701 for integer lanes). It
/// compiles only in a function whose features hold fma, E1106 elsewhere,
/// since Mk links no fma routine to fall back on. Inline; allocates nothing.
export Vec[T, N].Fma(self, b: Vec[T, N], c: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The correctly rounded square root of each float lane (E0701 for integer
/// lanes). Inline; allocates nothing.
export Vec[T, N].Sqrt(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Each float lane rounded down (E0701 for integer lanes). On x86_64 it
/// compiles only where the function's features hold sse4.1, which the hosted
/// baseline does, and is E1106 elsewhere: Mk links no floor routine to fall
/// back on. Inline; allocates nothing.
export Vec[T, N].Floor(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Each float lane rounded up (E0701 for integer lanes); on x86_64 it
/// needs sse4.1, as Floor does (E1106 without). Inline; allocates nothing.
export Vec[T, N].Ceil(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Each float lane rounded to nearest, ties to even (E0701 for integer
/// lanes); on x86_64 it needs sse4.1, as Floor does (E1106 without).
/// Inline; allocates nothing.
export Vec[T, N].RoundEven(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Each float lane rounded toward zero (E0701 for integer lanes); on x86_64
/// it needs sse4.1, as Floor does (E1106 without). Inline; allocates nothing.
export Vec[T, N].RoundToZero(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Each pair of integer lanes divided, truncating (E0701 for float lanes,
/// which take /); panics on a zero divisor lane or a MIN / -1 pair. Inline;
/// allocates nothing.
export Vec[T, N].DivTrunc(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Each integer lane shifted left by the matching lane of k (E0701 for
/// float lanes); panics on an amount at or above the lane width. Inline;
/// allocates nothing.
export Vec[T, N].ShlEach(self, k: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Each integer lane shifted right by the matching lane of k, arithmetic
/// on signed lanes (E0701 for float lanes); panics on an amount at or above
/// the lane width. Inline; allocates nothing.
export Vec[T, N].ShrEach(self, k: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The exact products of the low halves of integer lanes of self and b, as
/// lanes twice as wide (U of T's class and signedness, E0701 otherwise).
/// Inline; allocates nothing.
export Vec[T, N].MulWidenLo[U: Lane, const M: int](self, b: Vec[T, N]): Vec[U, M] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires M * 2 == N && M >= 2 && SizeOf[U]() == 2 * SizeOf[T]()
/// The exact products of the high halves of integer lanes of self and b, as
/// lanes twice as wide (U of T's class and signedness, E0701 otherwise).
/// Inline; allocates nothing.
export Vec[T, N].MulWidenHi[U: Lane, const M: int](self, b: Vec[T, N]): Vec[U, M] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires M * 2 == N && M >= 2 && SizeOf[U]() == 2 * SizeOf[T]()
/// The low half of the lanes as lanes twice as wide: a signed lane
/// sign-extended, an unsigned one zero-extended, an f32 to the same f64. U
/// has T's class and signedness (E0701 otherwise). Inline; allocates nothing.
export Vec[T, N].WidenLo[U: Lane, const M: int](self): Vec[U, M] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires M * 2 == N && M >= 2 && SizeOf[U]() == 2 * SizeOf[T]()
/// The high half of the lanes as lanes twice as wide, as WidenLo converts
/// them. Inline; allocates nothing.
export Vec[T, N].WidenHi[U: Lane, const M: int](self): Vec[U, M] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires M * 2 == N && M >= 2 && SizeOf[U]() == 2 * SizeOf[T]()
/// The integer lanes of self, then of b, each clamped to the range of U,
/// half as wide (E0701 for float lanes). Inline; allocates nothing.
export Vec[T, N].NarrowSat[U: Lane, const M: int](self, b: Vec[T, N]): Vec[U, M] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires M == 2 * N && M <= 64 && 2 * SizeOf[U]() == SizeOf[T]()

// Conversions. Cast converts lane by lane as scalar Cast does, and panics
// when a lane does not fit.
/// Each lane converted as scalar Cast does: panics when a lane does not fit,
/// passes NaN and the infinities to a float lane. Inline; allocates nothing.
export Vec[T, N].Cast[U: Lane](self): Vec[U, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Each integer lane cut to U, keeping its low bits (E0701 for float lanes).
/// Inline; allocates nothing.
export Vec[T, N].Trunc[U: Lane](self): Vec[U, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Each lane converted to U, saturating at U's range; NaN gives 0. Inline;
/// allocates nothing.
export Vec[T, N].CastSat[U: Lane](self): Vec[U, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The same bytes as M lanes of U (M * SizeOf[U]() == N * SizeOf[T]()). Inline;
/// allocates nothing.
export Vec[T, N].Reinterpret[U: Lane, const M: int](self): Vec[U, M] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64 requires M * SizeOf[U]() == N * SizeOf[T]() && M >= 2 && M <= 64

// Masks.
/// The mask whose lane j is set when bit j of b is; bits from N up are ignored.
/// Inline; allocates nothing.
export Mask[T, N].FromBits(b: u64): Mask[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The lanes as bits: bit j set when lane j is. Inline; allocates nothing.
export Mask[T, N].Bits(self): u64 requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Reports whether any lane is set. Inline; allocates nothing.
export Mask[T, N].Any(self): bool requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Reports whether every lane is set. Inline; allocates nothing.
export Mask[T, N].All(self): bool requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The number of lanes set. Inline; allocates nothing.
export Mask[T, N].CountSet(self): int requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The index of the lowest lane set, or none. Inline; allocates nothing.
export Mask[T, N].FirstSet(self): ?int requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// Lane j of a where lane j of self is set, else of b. Inline; allocates
/// nothing.
export Mask[T, N].Select(self, a: Vec[T, N], b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
/// The same lanes as a mask over U. Inline; allocates nothing.
export Mask[T, N].Cast[U: Lane](self): Mask[U, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64

// The operator intrinsics: check
// records each operator on a Vec or Mask as a call of one of these, and
// they are private, so only std.simd can name them.
Vec[T, N].Add(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Sub(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Mul(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Div(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].AddWrap(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].SubWrap(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].MulWrap(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].And(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Or(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Xor(self, b: Vec[T, N]): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Shl[K: Lane](self, k: K): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Shr[K: Lane](self, k: K): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Neg(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Not(self): Vec[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Eq(self, b: Vec[T, N]): Mask[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Ne(self, b: Vec[T, N]): Mask[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Lt(self, b: Vec[T, N]): Mask[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Le(self, b: Vec[T, N]): Mask[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Gt(self, b: Vec[T, N]): Mask[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Vec[T, N].Ge(self, b: Vec[T, N]): Mask[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Mask[T, N].And(self, b: Mask[T, N]): Mask[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Mask[T, N].Or(self, b: Mask[T, N]): Mask[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Mask[T, N].Xor(self, b: Mask[T, N]): Mask[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
Mask[T, N].Not(self): Mask[T, N] requires N == 2 || N == 4 || N == 8 || N == 16 || N == 32 || N == 64
