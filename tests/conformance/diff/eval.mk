// verdict: ok
// note: TestDiffEvalNative (internal/corpus) runs every function named Case<Name> through the evaluator and natively, one process per case, and compares the value bytes or the panic code

// The helpers (Max, Min, Zero, ...) hide each operand behind a call, so no check can fold a case
// to a constant before the evaluator or the native code runs it. Cases whose name ends in Trap are
// expected to panic in both (E0901 with facts.panic in the evaluator, exit 101 natively); the
// others return a value. No expected value is written here: the two executions are each other's
// oracle. Covered: wrapping and checked arithmetic, divide (zero, MIN / -1), shifts, casts, float
// NaN and infinity, the absence of fused multiply-add, str and array bounds, plain structs, and
// str.ParseFloat's grammar and rounding: total underflow to a signed zero, `5.` and `.5`,
// the words, and overflow, hex, `_`, `1e` and `.` failing, which Parsed turns into its sentinel.

struct Rgb {
    r: u8
    g: u8
    b: u8
}

struct Two {
    a: i32
    b: i32
}

Max(): i64 {
    return 9223372036854775807
}

Min(): i64 {
    return -9223372036854775807 - 1
}

Zero(): i64 {
    return 0
}

NegOne(): i64 {
    return -1
}

One(): u64 {
    return 1
}

Shift(n: u64): u64 {
    return n
}

Big32(): i32 {
    return 2147483647
}

Byte(n: u8): u8 {
    return n
}

Int(n: int): int {
    return n
}

F(x: f64): f64 {
    return x
}

Three(): int {
    return 3
}

CaseAddWrap(): i64 {
    return Max() +% 1
}

CaseSubWrap(): i64 {
    return Min() -% 1
}

CaseMulWrap(): u8 {
    return Byte(200) *% 3
}

CaseAddTrap(): i64 {
    return Max() + 1
}

CaseSubTrap(): u64 {
    return One() - 2
}

CaseMulTrap(): i32 {
    return Big32() * 2
}

CaseNegTrap(): i64 {
    return -Min()
}

CaseDivTrunc(): int {
    return Int(-7) / 2
}

CaseRemSign(): int {
    return Int(-7) % 2
}

CaseDivZeroTrap(): i64 {
    return 7 / Zero()
}

CaseRemZeroTrap(): i64 {
    return 7 % Zero()
}

CaseDivMinTrap(): i64 {
    return Min() / NegOne()
}

CaseRemMinTrap(): i64 {
    return Min() % NegOne()
}

CaseShlTop(): u64 {
    return One() << Shift(63)
}

CaseShrSigned(): int {
    return Int(-16) >> 2
}

CaseShrUnsigned(): u64 {
    return (Shift(0) -% 1) >> 60
}

CaseShiftAmountTrap(): u64 {
    return One() << Shift(64)
}

// `<<` discards the bits shifted out, with no overflow check; only the amount is checked.
// So MAX << 1 is -2 in both executions, not a panic.
CaseShlSignedDropsBits(): i64 {
    return Max() << 1
}

CaseCastFits(): i8 {
    return Cast[i8](Int(100))
}

CaseCastTrap(): i8 {
    return Cast[i8](Int(200))
}

CaseCastTruncTowardZero(): i32 {
    return Cast[i32](F(-2147483648.5))
}

CaseCastUnsignedNegHalf(): u32 {
    return Cast[u32](F(-0.5))
}

CaseCastHighTrap(): i32 {
    return Cast[i32](F(2147483648.0))
}

CaseCastNaNTrap(): i64 {
    return Cast[i64](F(0.0) / F(0.0))
}

CaseCastF32Rounds(): f32 {
    return Cast[f32](F(0.1))
}

CaseCastF32OverflowTrap(): f32 {
    return Cast[f32](F(1e300))
}

// Cast[f32] panics only when rounding gives an infinity. 3.4028235e38 is above the largest
// f32 (3.4028234663852886e38) but below the midpoint to 2^128, so it rounds down and fits;
// 3.4028236e38 is above the midpoint and overflows. NaN and the infinities pass through.
CaseCastF32MaxRoundsDown(): f32 {
    return Cast[f32](F(3.4028235e38))
}

CaseCastF32NegMaxRoundsDown(): f32 {
    return Cast[f32](F(-3.4028235e38))
}

CaseCastF32PastMidpointTrap(): f32 {
    return Cast[f32](F(3.4028236e38))
}

CaseCastF32Inf(): f32 {
    return Cast[f32](F(1.0) / F(0.0))
}

CaseCastF32NaN(): f32 {
    return Cast[f32](F(0.0) / F(0.0))
}

CaseCastIntToF64Rounds(): f64 {
    return Cast[f64](Int(9007199254740993))
}

CaseTruncLowBits(): u8 {
    return Trunc[u8](Int(300))
}

CaseNaN(): f64 {
    return F(0.0) / F(0.0)
}

CaseNegNaN(): f64 {
    return -(F(0.0) / F(0.0))
}

CaseInfMinusInf(): f64 {
    return F(1.0) / F(0.0) - F(1.0) / F(0.0)
}

CaseNegZero(): f64 {
    return F(0.0) * F(-1.0)
}

CaseNoFusedMultiplyAdd(): f64 {
    x := F(134217729.0)
    return x * x - F(18014398777917440.0)
}

CaseBool(): bool {
    return Int(3) > Int(2)
}

CaseChar(): char {
    return 'é'
}

CaseStr(): str {
    s := "héllo"
    return s[0..3]
}

CaseStrBoundaryTrap(): str {
    s := "héllo"
    return s[0..Int(2)]
}

CaseBoundsTrap(): i32 {
    a: [3]i32 = [1, 2, 3]
    return a[Three()]
}

CaseRgb(): Rgb {
    return Rgb(r: Byte(1), g: Byte(2), b: Byte(255))
}

CaseTwo(): Two {
    return Two(a: Big32(), b: -Big32())
}

Text(s: str): str {
    return s
}

// Parsed is s's value, or the sentinel 12345 when s does not parse (ParseError).
Parsed(s: str): f64 {
    v := Text(s).ParseFloat() else {
        return F(12345.0)
    }
    return v
}

CaseParseUnderflow(): f64 {
    return Parsed("1e-400")
}

CaseParseNegUnderflow(): f64 {
    return Parsed("-1e-400")
}

CaseParseTrailingDot(): f64 {
    return Parsed("5.")
}

CaseParseLeadingDot(): f64 {
    return Parsed(".5")
}

CaseParseOverflow(): f64 {
    return Parsed("1e400")
}

CaseParseOverflowHalfway(): f64 {
    return Parsed("1.7976931348623159e308")
}

CaseParseMax(): f64 {
    return Parsed("1.7976931348623157e308")
}

CaseParseMinSubnormal(): f64 {
    return Parsed("4.9406564584124654e-324")
}

CaseParseHex(): f64 {
    return Parsed("0x1p3")
}

CaseParseUnderscore(): f64 {
    return Parsed("1_0")
}

CaseParseNoExponentDigits(): f64 {
    return Parsed("1e")
}

CaseParseDotAlone(): f64 {
    return Parsed(".")
}

CaseParseNegInfinity(): f64 {
    return Parsed("-Infinity")
}

CaseParseNaN(): f64 {
    return Parsed("nan")
}

CaseParseTenth(): f64 {
    return Parsed("0.1")
}

CaseParseLongDigits(): f64 {
    return Parsed("123456789012345678901234567890")
}
