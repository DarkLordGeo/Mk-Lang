// Why: a language runtime written in Mk (a JavaScript engine, for one)
// needs overflow flags, wide products, wrapping and saturating casts at the
// cost of one or two instructions.
//
// What: the integer bound Integer, AddOverflow, SubOverflow, MulOverflow,
// WrapI32, WrapU32, CastSat, CastSatF32, MulWide, DivWide, AddCarry and
// SubBorrow.
//
// How: each function is bodiless, so check marks it an intrinsic: codegen
// lowers it by its stable key and the evaluator runs its definition
// (internal/eval/intrinsic_math.go). Each is safe, O(1), and allocates
// nothing; only DivWide can panic.

/// The integer types: i8 to i64, u8 to u64, int and uint. The compiler supplies
/// the set; an impl of Integer is E0704.
export interface Integer {
}

/// a + b: value is the sum wrapped to T, and overflow is whether the exact
/// sum does not fit T. O(1); allocates nothing.
export AddOverflow[T: Integer](a: T, b: T): (value: T, overflow: bool)
/// a - b: value is the difference wrapped to T, and overflow is whether
/// the exact difference does not fit T. O(1); allocates nothing.
export SubOverflow[T: Integer](a: T, b: T): (value: T, overflow: bool)
/// a * b: value is the product wrapped to T, and overflow is whether the
/// exact product does not fit T. O(1); allocates nothing.
export MulOverflow[T: Integer](a: T, b: T): (value: T, overflow: bool)
/// x converted to i32 with wrapping: NaN and the infinities give 0, any other x
/// is truncated toward zero and reduced modulo 2^32 into i32's range. This is
/// ECMA-262's ToInt32. O(1); allocates nothing.
export WrapI32(x: f64): i32
/// x converted to u32 with wrapping: NaN and the infinities give 0, any other x
/// is truncated toward zero and reduced modulo 2^32 into u32's range. This is
/// ECMA-262's ToUint32. O(1); allocates nothing.
export WrapU32(x: f64): u32
/// x truncated toward zero and clamped to T's range: below it gives T's least
/// value, above it T's greatest, and NaN gives 0. O(1); allocates nothing.
export CastSat[T: Integer](x: f64): T
/// CastSat of an f32. O(1); allocates nothing.
export CastSatF32[T: Integer](x: f32): T
/// The full 128-bit product of a and b, as its high and low halves. O(1);
/// allocates nothing.
export MulWide(a: u64, b: u64): (hi: u64, lo: u64)
/// The 128-bit value hi * 2^64 + lo divided by d: quotient q and remainder r.
/// Panics with a division by zero when d is 0, and otherwise with an overflow
/// when hi >= d, so the quotient always fits a u64. O(1); allocates nothing.
export DivWide(hi: u64, lo: u64, d: u64): (q: u64, r: u64)
/// x + y + c with a carry out: sum is the low 64 bits, and carry is 1 when the
/// total is 2^64 or more, else 0. Pass the carry of the limb below as c (0 or
/// 1), so a chain of calls adds numbers of many limbs. O(1); allocates nothing.
export AddCarry(x: u64, y: u64, c: u64): (sum: u64, carry: u64)
/// x - y - b with a borrow out: diff is the result modulo 2^64, and borrow is 1
/// when x is less than y + b, else 0. Pass the borrow of the limb below as b (0
/// or 1). O(1); allocates nothing.
export SubBorrow(x: u64, y: u64, b: u64): (diff: u64, borrow: u64)
