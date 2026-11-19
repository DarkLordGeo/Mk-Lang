// Why: formatted output must cost no allocation and link only the code a
// program's format strings use; the compiler lowers each Print call to
// direct calls or data, and this file holds what those call.
//
// What: Print (an intrinsic), FmtPiece and FmtArg, the runners FmtRun and
// FmtPadRun, FmtValue and FmtSpecValue, the integer writers (FmtU64, FmtI64,
// FmtHex, FmtIntSpec), FmtFloatSpec, the counting Writer used to measure
// padded holes, and the unrounded scaling (FpPrescale, FpUscale, FpShortest,
// FpFixedWidth, Pow10Tab) shared with core_fmtfloat.mk and core_parse.mk.
//
// How: the compiler writes an unpadded call's pieces as direct calls (each
// literal, then each hole's FmtValue instance), and builds a padded call's
// piece table (literal, argument index, packed spec) and argument array of
// (address, FmtValue instance) for FmtPadRun. A padded hole is formatted
// once into a counting Writer and once for real. Decimal output takes two
// digits per division from DigitPairs; radix digits come from a table.

import std.raw

/// One literal of a format string and the hole after it. The compiler
/// builds a constant array of them for each Print call. The bits of spec:
/// 0-3 the kind (0 default, 1 x, 2 .N, 3 X, 4 b, 5 o), 4 sign, 5 zero,
/// 6-7 alignment (0 left, 1 right, 2 center), 8-15 N, 16-23 width and
/// 24-31 fill. A hole with no spec is 0, {x:x} is 1 and {x:.N} is
/// 2 | (N << 8).
export struct FmtPiece {
    export lit: str
    export arg: i32
    export spec: u32
}

/// One argument of a Print call: the address of its field and the function
/// that formats it.
export struct FmtArg {
    export p: *u8
    export f: fn(*u8, u32, mut Writer): WriteError!void
}

/// Writes f to w with each {hole} replaced by its field of args, formatted by
/// its Format or by the hole's spec. WriteError comes from the Writer or from
/// a user's Format. O(output length) plus the Format calls; allocates
/// nothing. An intrinsic: the compiler writes each piece in turn (through
/// FmtPadRun's tables when a hole has a width), stopping at the first error.
///
/// f is checked against args at compile time: every hole names a field or
/// a field path of args, every field is used, and every spec is valid, else
/// E0706. {{ and }} write literal braces. A string with no holes takes no
/// record: Print(w, "done").
export Print[A: Fields[Format] + Plain](w: mut Writer, const f: str, args: A): WriteError!void

/// Formats the T at p for a hole with no radix, precision, sign or zero
/// flag (width, fill and alignment are applied around it). f64 and f32 are
/// written by FmtF64 and FmtF32, any other T by its Format. Holes with a
/// radix, precision, + or 0 use FmtSpecValue, so a program that prints
/// plainly does not link that code. A float is read as its bits, so printing
/// an integer in a binary built without float registers (a kernel) is not
/// E1108. O(output length) plus the Format call.
export FmtValue[T: Format](p: *u8, spec: u32, w: mut Writer): WriteError!void {
    unsafe "p is the address of a live T (lower passes raw.Of(&args.field)); an f64 is 8 bytes and an f32 4" {
        if T is f64 {
            try FmtFloatBits(w, raw.Read(raw.PtrCast[T, u64](raw.PtrCast[u8, T](p))), true, 0)
            return
        }
        if T is f32 {
            try FmtFloatBits(w, u64(raw.Read(raw.PtrCast[T, u32](raw.PtrCast[u8, T](p)))), false, 0)
            return
        }
        try (*raw.PtrCast[u8, T](p)).Format(w)
    }
}

/// Formats the T at p by spec's radix, precision, sign and zero flag. An
/// integer is written in its radix (a negative one as the bits of its
/// declared width), with its sign and zero padding; a float with its
/// precision, sign and zero padding. Any other T may only have a width,
/// fill and alignment, and goes to FmtValue, as does a spec with none of
/// these flags. O(output length) plus the Format call.
export FmtSpecValue[T: Format](p: *u8, spec: u32, w: mut Writer): WriteError!void {
    if spec & 63 == 0 {
        try FmtValue[T](p, spec, w)
        return
    }
    unsafe "p is the address of a live T (lower passes raw.Of(&args.field))" {
        q := raw.PtrCast[u8, T](p)
        if T is u8 {
            v := u64(raw.Read(q))
            try FmtIntSpec(w, v, v, false, spec)
            return
        }
        if T is u16 {
            v := u64(raw.Read(q))
            try FmtIntSpec(w, v, v, false, spec)
            return
        }
        if T is u32 {
            v := u64(raw.Read(q))
            try FmtIntSpec(w, v, v, false, spec)
            return
        }
        if T is u64 {
            v := raw.Read(q)
            try FmtIntSpec(w, v, v, false, spec)
            return
        }
        if T is uint {
            v := Trunc[u64](raw.Read(q))
            try FmtIntSpec(w, v, v, false, spec)
            return
        }
        if T is i8 {
            v := raw.Read(q)
            try FmtIntSpec(w, u64(Trunc[u8](v)), IntMag(i64(v)), v < 0, spec)
            return
        }
        if T is i16 {
            v := raw.Read(q)
            try FmtIntSpec(w, u64(Trunc[u16](v)), IntMag(i64(v)), v < 0, spec)
            return
        }
        if T is i32 {
            v := raw.Read(q)
            try FmtIntSpec(w, u64(Trunc[u32](v)), IntMag(i64(v)), v < 0, spec)
            return
        }
        if T is i64 {
            v := raw.Read(q)
            try FmtIntSpec(w, Trunc[u64](v), IntMag(v), v < 0, spec)
            return
        }
        if T is int {
            v := raw.Read(q)
            try FmtIntSpec(w, Trunc[u64](v), IntMag(Trunc[i64](v)), v < 0, spec)
            return
        }
        if T is f64 {
            try FmtFloatSpec(w, raw.Read(raw.PtrCast[T, u64](raw.PtrCast[u8, T](p))), true, spec)
            return
        }
        if T is f32 {
            try FmtFloatSpec(w, u64(raw.Read(raw.PtrCast[T, u32](raw.PtrCast[u8, T](p)))), false, spec)
            return
        }
    }
    try FmtValue[T](p, spec, w)
}

// IntMag is |x| as a u64; the two's complement covers the minimum.
IntMag(x: i64): u64 {
    if x < 0 {
        return ~Trunc[u64](x) + 1
    }
    return Trunc[u64](x)
}

// FmtIntSpec writes an integer by spec: the radix types (x, X, b, o) the
// bits, unsigned and with no prefix, one shift and mask per digit (4 bits
// for x and X, 1 for b, 3 for o); the default type mag in decimal, two
// digits per division as FmtU64 does, after a "-" when neg, or a "+" when
// spec asks for a sign; with the zero flag, zero digits after the sign up
// to the width. No division by a radix known only at run time.
FmtIntSpec(w: mut Writer, bits: u64, mag: u64, neg: bool, spec: u32): WriteError!void {
    ty := spec & 15
    radix := ty == 1 || ty >= 3
    var buf: [64]u8 = [0; 64]
    var i := 64
    if radix {
        var shift := 4
        if ty == 4 {
            shift = 1
        } else if ty == 5 {
            shift = 3
        }
        mask := (Trunc[u64](1) << shift) - 1
        // The statement form, not a value arm: TestStdEvalParity_SeesPlantedBug
        // plants its bug by replacing this exact first line. letter + 10 is
        // the byte of digit ten, 'a' (0x57 + 10) or 'A' (0x37 + 10), and it
        // picks the table, so the planted 0x37 still turns x into X.
        var letter: u8 = 0x57
        if ty == 3 {
            letter = 0x37
        }
        // One load from the table per digit, not a branch on d < 10, which
        // a random hex digit mispredicts about 3 times in 8. The index is
        // v & mask & 15, so it is below 16 at every shift.
        digits := if letter == 0x37 { HexUpper.AsBytes() } else { HexLower.AsBytes() }
        var v := bits
        for {
            i -= 1
            buf[i] = digits[Trunc[int](v & mask & 15)]
            v = v >> shift
            if v == 0 {
                break
            }
        }
    } else {
        var v := mag
        pairs := DigitPairs.AsBytes()
        for v >= 100 {
            q := v / 100
            r := Trunc[int](v % 100) * 2
            i -= 2
            buf[i] = pairs[r]
            buf[i + 1] = pairs[r + 1]
            v = q
        }
        if v >= 10 {
            r := Trunc[int](v) * 2
            i -= 2
            buf[i] = pairs[r]
            buf[i + 1] = pairs[r + 1]
        } else {
            i -= 1
            buf[i] = Trunc[u8](v + 0x30)
        }
    }
    var n := 64 - i
    if !radix && neg {
        try w.Str("-")
        n += 1
    } else if spec & 16 != 0 {
        try w.Str("+")
        n += 1
    }
    if spec & 32 != 0 {
        try FmtZeros(w, Trunc[int]((spec >> 16) & 255) - n)
    }
    try w.Bytes(buf[i..64])
}

// FmtFloatSpec writes the IEEE bits b of an f64 (wide) or an f32 by spec:
// the precision as FmtFloatBits reads it; a "-" for a set sign bit (-0
// included) or a "+" when spec asks for a sign; with the zero flag, zero
// digits after the sign up to the width, measured through a counting
// Writer. nan prints "nan" with no sign, and neither nan nor inf takes zero
// digits: with the zero flag they get spaces before them up to the width
// here, since FmtPadRun leaves a hole with the zero flag to its value and
// does not measure it (two formattings, not four).
FmtFloatSpec(w: mut Writer, b: u64, wide: bool, spec: u32): WriteError!void {
    var top: u64 = 0x8000000000000000
    var inf: u64 = 0x7FF0000000000000
    if !wide {
        top = 0x80000000
        inf = 0x7F800000
    }
    abs := b & ~top
    zero := spec & 32 != 0
    width := Trunc[int]((spec >> 16) & 255)
    if abs > inf {
        if zero {
            try FmtFill(w, 0x20, width - 3)
        }
        try FmtFloatBits(w, b, wide, spec)
        return
    }
    if spec & 48 == 0 {
        try FmtFloatBits(w, b, wide, spec)
        return
    }
    var sign := ""
    if b & top != 0 {
        sign = "-"
    } else if spec & 16 != 0 {
        sign = "+"
    }
    n := sign.Len()
    if zero && abs == inf {
        try FmtFill(w, 0x20, width - n - 3)
    }
    if n > 0 {
        try w.Str(sign)
    }
    if zero && abs != inf {
        m := try FmtFloatLen(w, abs, wide, spec)
        try FmtZeros(w, width - n - m)
    }
    try FmtFloatBits(w, abs, wide, spec)
}

// FmtFloatLen is the byte count FmtFloatBits writes for b and spec.
FmtFloatLen(w: mut Writer, b: u64, wide: bool, spec: u32): WriteError!int {
    var n := 0
    var cw := CountWriter(w, mut n)
    try FmtFloatBits(mut cw, b, wide, spec)
    return n
}

// FmtMeasure is the byte count f writes for the value at p and spec.
FmtMeasure(w: mut Writer, f: fn(*u8, u32, mut Writer): WriteError!void, p: *u8, spec: u32): WriteError!int {
    var n := 0
    var cw := CountWriter(w, mut n)
    try f(p, spec, mut cw)
    return n
}

// A counting Writer adds the length of each write to the int its ctx
// points at and keeps no byte: the width measure. It is built as
// Text.Writer is, not through WriterOver, so the evaluator runs it.
const CountWriteTable = WriterTable(write: CountWrite, flush: CountFlush)

// CountWriter is a counting Writer over n, carrying w's Io.
CountWriter(w: mut Writer, n: mut int): Writer from n {
    unsafe "a pointer cast; CountWrite dereferences it while n's loan, which the Writer holds, keeps it alive" {
        ctx := raw.PtrCast[int, u8](raw.OfMut(n))
        return Writer(io: w.io, table: &CountWriteTable, ctx: ctx, buf: ctx, used: 0, cap: 0)
    }
}

CountWrite(w: mut Writer, b: []u8): WriteError!void {
    unsafe "a counting writer's ctx is the int it was made from, which its loan keeps alive (CountWriter)" {
        p := raw.PtrCast[u8, int](w.ctx)
        raw.Write(p, raw.Read(p) + b.Len())
    }
}

CountFlush(w: mut Writer): WriteError!void {
}

/// Writes x in lowercase hexadecimal, with no prefix and no leading zeros.
/// O(1): at most 16 digits.
export FmtHex(w: mut Writer, x: u64): WriteError!void {
    var buf: [16]u8 = [0; 16]
    var i := 16
    var v := x
    // A table load per digit, as FmtIntSpec's radix loop.
    digits := HexLower.AsBytes()
    for {
        i -= 1
        buf[i] = digits[Trunc[int](v & 15)]
        v = v >> 4
        if v == 0 {
            break
        }
    }
    try w.Bytes(buf[i..16])
}

// The radix digits by value, for FmtIntSpec and FmtHex.
const HexLower = "0123456789abcdef"
const HexUpper = "0123456789ABCDEF"

/// Writes a Print call's output: each literal of pieces, then its hole
/// through the argument's function. Stops at the first WriteError.
/// O(output length) plus the Format calls.
export FmtRun(w: mut Writer, pieces: []FmtPiece, args: []FmtArg): WriteError!void {
    for p in pieces {
        try w.Str(p.lit)
        if p.arg >= 0 {
            a := args[p.arg]
            f := a.f
            try f(a.p, p.spec, w)
        }
    }
}

/// FmtRun for a Print call where some hole has a width; the compiler picks
/// it, so a program that pads nothing does not link it. Such a hole is
/// formatted once to measure it, then padded with its fill to its width at
/// its alignment around a second formatting, so a padded value is formatted
/// twice. A value at or past the width is written as it is. A number with
/// the zero flag pads itself (digits with zeros, nan and inf with spaces)
/// and is formatted once. O(output length) plus the Format calls.
export FmtPadRun(w: mut Writer, pieces: []FmtPiece, args: []FmtArg): WriteError!void {
    for p in pieces {
        try w.Str(p.lit)
        if p.arg >= 0 {
            a := args[p.arg]
            f := a.f
            width := Trunc[int]((p.spec >> 16) & 255)
            if width == 0 || p.spec & 32 != 0 {
                try f(a.p, p.spec, w)
                continue
            }
            n := try FmtMeasure(w, f, a.p, p.spec)
            pad := width - n
            align := (p.spec >> 6) & 3
            fill := Trunc[u8](p.spec >> 24)
            var before := 0
            if align == 1 {
                before = pad
            } else if align == 2 {
                before = pad / 2
            }
            try FmtFill(w, fill, before)
            try f(a.p, p.spec, w)
            try FmtFill(w, fill, pad - before)
        }
    }
}

// FmtFill writes n copies of the byte c, nothing for n <= 0.
FmtFill(w: mut Writer, c: u8, n: int): WriteError!void {
    if n <= 0 {
        return
    }
    var buf: [32]u8 = [0; 32]
    for i in 0..IntMin(n, 32) {
        buf[i] = c
    }
    var k := n
    for k > 0 {
        m := IntMin(k, 32)
        try w.Bytes(buf[0..m])
        k -= m
    }
}

// DigitPairs holds "00" to "99": FmtU64 takes two digits per division.
const DigitPairs = "00010203040506070809101112131415161718192021222324252627282930313233343536373839404142434445464748495051525354555657585960616263646566676869707172737475767778798081828384858687888990919293949596979899"

/// Writes x in decimal, after a "-" when neg. O(1): at most 21 bytes, built
/// on the stack two digits at a time.
export FmtU64(w: mut Writer, x: u64, neg: bool): WriteError!void {
    var buf: [21]u8 = [0; 21]
    var i := 21
    var v := x
    d := DigitPairs.AsBytes()
    for v >= 100 {
        q := v / 100
        // v % 100, not v - q * 100: the same value, known to be under 100,
        // which bounds r and d[r] (LLVM derives both from one division).
        r := Trunc[int](v % 100) * 2
        i -= 2
        buf[i] = d[r]
        buf[i + 1] = d[r + 1]
        v = q
    }
    if v >= 10 {
        r := Trunc[int](v) * 2
        i -= 2
        buf[i] = d[r]
        buf[i + 1] = d[r + 1]
    } else {
        i -= 1
        buf[i] = Trunc[u8](v + 0x30)
    }
    if neg {
        i -= 1
        buf[i] = 0x2D
    }
    try w.Bytes(buf[i..21])
}

/// Writes x in decimal, the minimum i64 included. O(1).
export FmtI64(w: mut Writer, x: i64): WriteError!void {
    if x < 0 {
        try FmtU64(w, ~Trunc[u64](x) + 1, true)
        return
    }
    try FmtU64(w, Trunc[u64](x), false)
}

const ZeroDigits = "0000000000000000000000000000000000000000000000000000000000000000"

// FmtZeros writes n zero digits, at most 64 per call.
FmtZeros(w: mut Writer, n: int): WriteError!void {
    var k := n
    for k > 0 {
        c := IntMin(k, 64)
        try w.Str(ZeroDigits[0..c])
        k -= c
    }
}

IntMin(a: int, b: int): int {
    if a < b {
        return a
    }
    return b
}

IntMax(a: int, b: int): int {
    if a > b {
        return a
    }
    return b
}

// BitLen64 is Go's bits.Len64.
BitLen64(x: u64): int {
    return 64 - raw.Clz64(x)
}

// B2 is 1 for true, 0 for false.
B2(b: bool): u64 {
    if b {
        return 1
    }
    return 0
}

// ---- Unrounded scaling, ported from Go 1.27's
// internal/strconv uscale.go: shared by the float formatting of
// core_fmtfloat.mk and the parsing of core_parse.mk, against Pow10Tab.

// FpLog10Pow2 is floor(x * log10(2)) for |x| <= 4000 (78913 / 2^18); >>
// on a negative int is arithmetic, so it floors.
FpLog10Pow2(x: int): int {
    return (x * 78913) >> 18
}

// FpLog2Pow10 is floor(x * log2(10)) for |x| <= 1000 (108853 / 2^15).
FpLog2Pow10(x: int): int {
    return (x * 108853) >> 15
}

// FpSkewed is floor(e * log10(2) - log10(4/3)), the decimal footprint of
// the asymmetric interval just above a power of two.
FpSkewed(e: int): int {
    return (e * 631305 - 261663) >> 21
}

const FpPow10: [20]u64 = [1, 10, 100, 1000, 10000, 100000, 1000000, 10000000, 100000000, 1000000000,
    10000000000, 100000000000, 1000000000000, 10000000000000, 100000000000000, 1000000000000000,
    10000000000000000, 100000000000000000, 1000000000000000000, 10000000000000000000]

// FpNumDigits is the number of decimal digits of d >= 1.
FpNumDigits(d: u64): int {
    nd := FpLog10Pow2(BitLen64(d))
    pow := &FpPow10
    if d >= pow[nd] {
        return nd + 1
    }
    return nd
}

// FpPutDigits writes d as exactly s.Len() decimal digits (d < 10^s.Len()),
// two per division by 100.
FpPutDigits(s: mut []u8, d: u64) {
    pairs := DigitPairs.AsBytes()
    var i := s.Len()
    var v := d
    for i >= 2 {
        q := v / 100
        r := Trunc[int](v % 100) * 2
        s[i - 2] = pairs[r]
        s[i - 1] = pairs[r + 1]
        v = q
        i -= 2
    }
    if i == 1 {
        s[0] = Trunc[u8](v) + 0x30
    }
}

// An unrounded value is a u64 holding floor(4x) with bit 0 sticky (set when
// x is not a multiple of 1/4): bit 1 is the half, so each rounding is one
// add and a shift (uscale.go's unrounded). Inputs stay below 2^62, so the
// adds cannot overflow.
FpFloor(u: u64): u64 {
    return u >> 2
}

FpRound(u: u64): u64 {
    return (u + 1 + ((u >> 2) & 1)) >> 2
}

FpCeil(u: u64): u64 {
    return (u + 3) >> 2
}

// FpDiv10 divides an unrounded value by 10, keeping the sticky bit.
FpDiv10(u: u64): u64 {
    return (u / 10) | (u & 1) | B2(u % 10 != 0)
}

// FpUnmin is the least unrounded value that rounds to x.
FpUnmin(x: u64): u64 {
    return (x << 2) - 2
}

// A 128-bit power of ten: hi * 2^64 - lo, normalized so hi's top bit is set.
struct Pow128 {
    hi: u64
    lo: u64
}

const Pow10Min = -348

// The scaling constants for an (e, p) pair: the unrounded x * 2^e * 10^p is
// the top bits of x * (hi * 2^64 - lo), shifted right by 64 + s.
struct FpScaler {
    hi: u64
    lo: u64
    s: int
}

// FpPrescale gives the constants for e, p; lp must be FpLog2Pow10(p), and
// Pow10Min <= p <= 347.
FpPrescale(e: int, p: int, lp: int): FpScaler {
    // By reference: naming the 696-entry const by value copies it whole at
    // compile time.
    tab := &Pow10Tab
    t := tab[p - Pow10Min]
    return FpScaler(hi: t.hi, lo: t.lo, s: -(e + lp + 3))
}

// FpUscale is the unrounded x * 2^e * 10^p for c = FpPrescale(e, p, ...),
// with x's top bit set and 0 <= c.s < 64. The second multiply is needed
// only when the first leaves no nonzero bit below the cut.
FpUscale(x: u64, c: FpScaler): u64 {
    r := raw.Mul128(x, c.hi)
    var hi := r.hi
    mid := r.lo
    s := c.s & 63
    if (hi >> s) << s != hi {
        return (hi >> s) | 1
    }
    r2 := raw.Mul128(x, c.lo)
    mid2 := r2.hi
    hi = hi -% B2(mid < mid2)
    return (hi >> s) | B2((mid -% mid2) > 1)
}

// FpDigits is d * 10^p.
struct FpDigits {
    d: u64
    p: int
}

// FpShortest is shortFloat: the shortest digits d * 10^p inside the
// rounding interval of m * 2^e (m left-justified), the correctly rounded
// digits when two candidates of that length fit.
FpShortest(m: u64, e: int, mant_bits: int, min_exp: int): FpDigits {
    var z := 63 - mant_bits
    var p := 0
    var lo: u64 = 0
    var hi: u64 = 0
    if m == 0x8000000000000000 && e > min_exp {
        p = -FpSkewed(e + z)
        lo = m - (Trunc[u64](1) << (z - 2))
        hi = m + (Trunc[u64](1) << (z - 1))
    } else {
        if e < min_exp {
            z = z + (min_exp - e)
        }
        p = -FpLog10Pow2(e + z)
        lo = m - (Trunc[u64](1) << (z - 1))
        hi = m + (Trunc[u64](1) << (z - 1))
    }
    odd := (m >> z) & 1
    pre := FpPrescale(e, p, FpLog2Pow10(p))
    dmin := FpCeil(FpUscale(lo, pre) +% odd)
    dmax := FpFloor(FpUscale(hi, pre) -% odd)
    d := dmax / 10
    if d * 10 >= dmin {
        return FpDigits(d: d, p: -(p - 1))
    }
    if dmin < dmax {
        return FpDigits(d: FpRound(FpUscale(m, pre)), p: -p)
    }
    return FpDigits(d: dmin, p: -p)
}

// FpFixedWidth is fixedWidthFloat for 'f': the about-n-digit form of
// m * 2^e (m left-justified, 1 <= n <= 18), cut to prec digits after the
// point and rounded half to even.
FpFixedWidth(m: u64, e: int, n: int, prec: int): FpDigits {
    var p := n - 1 - FpLog10Pow2(e + 63)
    pre := FpPrescale(e, p, FpLog2Pow10(p))
    var u := FpUscale(m, pre)
    pow := &FpPow10
    if u >= FpUnmin(pow[n]) {
        u = FpDiv10(u)
        p -= 1
    }
    for p > prec {
        u = FpDiv10(u)
        p -= 1
    }
    return FpDigits(d: FpRound(u), p: -p)
}

// BEGIN GENERATED Pow10Tab by TestTextTablesUpToDate (std/std-text_tables_test.go); do not edit
// 10^e for e in Pow10Min..=347, scaled into [2^127, 2^128), as
// hi * 2^64 - lo (Go 1.27 internal/strconv/pow10tab.go).
const Pow10Tab: [696]Pow128 = [
    Pow128(hi: 0xfa8fd5a0081c0289, lo: 0xe8cd3796329f1bac), Pow128(hi: 0x9c99e58405118196, lo: 0xf18042bddfa3714b),
    Pow128(hi: 0xc3c05ee50655e1fb, lo: 0xade0536d578c4d9e), Pow128(hi: 0xf4b0769e47eb5a79, lo: 0x19586848ad6f6106),
    Pow128(hi: 0x98ee4a22ecf3188c, lo: 0x6fd7412d6c659ca3), Pow128(hi: 0xbf29dcaba82fdeaf, lo: 0x8bcd1178c77f03cc),
    Pow128(hi: 0xeef453d6923bd65b, lo: 0xeec055d6f95ec4c0), Pow128(hi: 0x9558b4661b6565f9, lo: 0xb53835a65bdb3af8),
    Pow128(hi: 0xbaaee17fa23ebf77, lo: 0xa286430ff2d209b6), Pow128(hi: 0xe95a99df8ace6f54, lo: 0x0b27d3d3ef868c23),
    Pow128(hi: 0x91d8a02bb6c10595, lo: 0x86f8e46475b41796), Pow128(hi: 0xb64ec836a47146fa, lo: 0x68b71d7d93211d7b),
    Pow128(hi: 0xe3e27a444d8d98b8, lo: 0x02e4e4dcf7e964da), Pow128(hi: 0x8e6d8c6ab0787f73, lo: 0x01cf0f0a1af1df08),
    Pow128(hi: 0xb208ef855c969f50, lo: 0x4242d2cca1ae56ca), Pow128(hi: 0xde8b2b66b3bc4724, lo: 0x52d3877fca19ec7d),
    Pow128(hi: 0x8b16fb203055ac77, lo: 0xb3c434afde5033ce), Pow128(hi: 0xaddcb9e83c6b1794, lo: 0x20b541dbd5e440c2),
    Pow128(hi: 0xd953e8624b85dd79, lo: 0x28e29252cb5d50f2), Pow128(hi: 0x87d4713d6f33aa6c, lo: 0x798d9b73bf1a5297),
    Pow128(hi: 0xa9c98d8ccb009507, lo: 0x97f10250aee0e73d), Pow128(hi: 0xd43bf0effdc0ba49, lo: 0xfded42e4da99210d),
    Pow128(hi: 0x84a57695fe98746e, lo: 0xfeb449cf089fb4a8), Pow128(hi: 0xa5ced43b7e3e9189, lo: 0xbe615c42cac7a1d2),
    Pow128(hi: 0xcf42894a5dce35eb, lo: 0xadf9b3537d798a46), Pow128(hi: 0x818995ce7aa0e1b3, lo: 0x8cbc10142e6bf66c),
    Pow128(hi: 0xa1ebfb4219491a20, lo: 0xefeb14193a06f407), Pow128(hi: 0xca66fa129f9b60a7, lo: 0x2be5d91f8888b109),
    Pow128(hi: 0xfd00b897478238d1, lo: 0x76df4f676aaadd4b), Pow128(hi: 0x9e20735e8cb16383, lo: 0xaa4b91a0a2aaca4f),
    Pow128(hi: 0xc5a890362fddbc63, lo: 0x14de7608cb557ce2), Pow128(hi: 0xf712b443bbd52b7c, lo: 0x5a16138afe2adc1b),
    Pow128(hi: 0x9a6bb0aa55653b2e, lo: 0xb84dcc36dedac991), Pow128(hi: 0xc1069cd4eabe89f9, lo: 0x66613f4496917bf5),
    Pow128(hi: 0xf148440a256e2c77, lo: 0x3ff98f15bc35daf2), Pow128(hi: 0x96cd2a865764dbcb, lo: 0xc7fbf96d95a1a8d7),
    Pow128(hi: 0xbc807527ed3e12bd, lo: 0x39faf7c8fb0a130d), Pow128(hi: 0xeba09271e88d976c, lo: 0x0879b5bb39cc97d1),
    Pow128(hi: 0x93445b8731587ea4, lo: 0x854c1195041fdee2), Pow128(hi: 0xb8157268fdae9e4d, lo: 0xa69f15fa4527d69b),
    Pow128(hi: 0xe61acf033d1a45e0, lo: 0x9046db78d671cc42), Pow128(hi: 0x8fd0c16206306bac, lo: 0x5a2c492b86071fa9),
    Pow128(hi: 0xb3c4f1ba87bc8697, lo: 0x70b75b766788e793), Pow128(hi: 0xe0b62e2929aba83d, lo: 0xcce53254016b2178),
    Pow128(hi: 0x8c71dcd9ba0b4926, lo: 0x600f3f7480e2f4eb), Pow128(hi: 0xaf8e5410288e1b70, lo: 0xf8130f51a11bb226),
    Pow128(hi: 0xdb71e91432b1a24b, lo: 0x3617d32609629eaf), Pow128(hi: 0x892731ac9faf056f, lo: 0x41cee3f7c5dda32d),
    Pow128(hi: 0xab70fe17c79ac6cb, lo: 0x92429cf5b7550bf9), Pow128(hi: 0xd64d3d9db981787e, lo: 0xf6d34433252a4ef7),
    Pow128(hi: 0x85f0468293f0eb4f, lo: 0xda440a9ff73a715a), Pow128(hi: 0xa76c582338ed2622, lo: 0x50d50d47f5090db1),
    Pow128(hi: 0xd1476e2c07286fab, lo: 0xe50a5099f24b511e), Pow128(hi: 0x82cca4db847945cb, lo: 0xaf267260376f12b2),
    Pow128(hi: 0xa37fce126597973d, lo: 0x1af00ef8454ad75f), Pow128(hi: 0xcc5fc196fefd7d0d, lo: 0xe1ac12b6569d8d37),
    Pow128(hi: 0xff77b1fcbebcdc50, lo: 0xda171763ec44f085), Pow128(hi: 0x9faacf3df73609b2, lo: 0x884e6e9e73ab1653),
    Pow128(hi: 0xc795830d75038c1e, lo: 0x2a620a461095dbe8), Pow128(hi: 0xf97ae3d0d2446f26, lo: 0xb4fa8cd794bb52e2),
    Pow128(hi: 0x9becce62836ac578, lo: 0xb11c9806bcf513cd), Pow128(hi: 0xc2e801fb244576d6, lo: 0xdd63be086c3258c0),
    Pow128(hi: 0xf3a20279ed56d48b, lo: 0x94bcad8a873eeef0), Pow128(hi: 0x9845418c345644d7, lo: 0x7cf5ec7694875556),
    Pow128(hi: 0xbe5691ef416bd60d, lo: 0xdc33679439a92aac), Pow128(hi: 0xedec366b11c6cb90, lo: 0xd340417948137557),
    Pow128(hi: 0x94b3a202eb1c3f3a, lo: 0x840828ebcd0c2956), Pow128(hi: 0xb9e08a83a5e34f08, lo: 0x250a3326c04f33ac),
    Pow128(hi: 0xe858ad248f5c22ca, lo: 0x2e4cbff070630097), Pow128(hi: 0x91376c36d99995bf, lo: 0xdceff7f6463de05e),
    Pow128(hi: 0xb58547448ffffb2e, lo: 0x542bf5f3d7cd5875), Pow128(hi: 0xe2e69915b3fff9fa, lo: 0xe936f370cdc0ae93),
    Pow128(hi: 0x8dd01fad907ffc3c, lo: 0x51c2582680986d1c), Pow128(hi: 0xb1442798f49ffb4b, lo: 0x6632ee3020be8863),
    Pow128(hi: 0xdd95317f31c7fa1e, lo: 0xbfbfa9bc28ee2a7c), Pow128(hi: 0x8a7d3eef7f1cfc53, lo: 0xb7d7ca159994da8d),
    Pow128(hi: 0xad1c8eab5ee43b67, lo: 0x25cdbc9afffa1130), Pow128(hi: 0xd863b256369d4a41, lo: 0x6f412bc1bff8957d),
    Pow128(hi: 0x873e4f75e2224e69, lo: 0xa588bb5917fb5d6e), Pow128(hi: 0xa90de3535aaae203, lo: 0x8eeaea2f5dfa34c9),
    Pow128(hi: 0xd3515c2831559a84, lo: 0xf2a5a4bb3578c1fc), Pow128(hi: 0x8412d9991ed58092, lo: 0x17a786f5016b793d),
    Pow128(hi: 0xa5178fff668ae0b7, lo: 0x9d9168b241c6578d), Pow128(hi: 0xce5d73ff402d98e4, lo: 0x04f5c2ded237ed70),
    Pow128(hi: 0x80fa687f881c7f8f, lo: 0x831999cb4362f466), Pow128(hi: 0xa139029f6a239f73, lo: 0xe3e0003e143bb17f),
    Pow128(hi: 0xc987434744ac874f, lo: 0x5cd8004d994a9ddf), Pow128(hi: 0xfbe9141915d7a923, lo: 0xb40e0060ff9d4557),
    Pow128(hi: 0x9d71ac8fada6c9b6, lo: 0x9088c03c9fc24b56), Pow128(hi: 0xc4ce17b399107c23, lo: 0x34aaf04bc7b2de2c),
    Pow128(hi: 0xf6019da07f549b2c, lo: 0x81d5ac5eb99f95b7), Pow128(hi: 0x99c102844f94e0fc, lo: 0xd1258bbb3403bd92),
    Pow128(hi: 0xc0314325637a193a, lo: 0x056eeeaa0104acf7), Pow128(hi: 0xf03d93eebc589f89, lo: 0x86caaa548145d835),
    Pow128(hi: 0x96267c7535b763b6, lo: 0xb43eaa74d0cba721), Pow128(hi: 0xbbb01b9283253ca3, lo: 0x614e551204fe90e9),
    Pow128(hi: 0xea9c227723ee8bcc, lo: 0xb9a1ea56863e3523), Pow128(hi: 0x92a1958a76751760, lo: 0xf405327613e6e136),
    Pow128(hi: 0xb749faed14125d37, lo: 0x31067f1398e09984), Pow128(hi: 0xe51c79a85916f485, lo: 0x7d481ed87f18bfe5),
    Pow128(hi: 0x8f31cc0937ae58d3, lo: 0x2e4d13474f6f77ef), Pow128(hi: 0xb2fe3f0b8599ef08, lo: 0x79e05819234b55ea),
    Pow128(hi: 0xdfbdcece67006aca, lo: 0x98586e1f6c1e2b65), Pow128(hi: 0x8bd6a141006042be, lo: 0x1f3744d3a392db1f),
    Pow128(hi: 0xaecc49914078536e, lo: 0xa70516088c7791e7), Pow128(hi: 0xda7f5bf590966849, lo: 0x50c65b8aaf957661),
    Pow128(hi: 0x888f99797a5e012e, lo: 0x927bf936adbd69fc), Pow128(hi: 0xaab37fd7d8f58179, lo: 0x371af784592cc47c),
    Pow128(hi: 0xd5605fcdcf32e1d7, lo: 0x04e1b5656f77f59b), Pow128(hi: 0x855c3be0a17fcd27, lo: 0xa30d115f65aaf980),
    Pow128(hi: 0xa6b34ad8c9dfc070, lo: 0x0bd055b73f15b7e1), Pow128(hi: 0xd0601d8efc57b08c, lo: 0x0ec46b250edb25d9),
    Pow128(hi: 0x823c12795db6ce58, lo: 0x893ac2f72948f7a7), Pow128(hi: 0xa2cb1717b52481ee, lo: 0xab8973b4f39b3591),
    Pow128(hi: 0xcb7ddcdda26da269, lo: 0x566bd0a2308202f6), Pow128(hi: 0xfe5d54150b090b03, lo: 0x2c06c4cabca283b3),
    Pow128(hi: 0x9efa548d26e5a6e2, lo: 0x3b843afeb5e59250), Pow128(hi: 0xc6b8e9b0709f109b, lo: 0xca6549be635ef6e4),
    Pow128(hi: 0xf867241c8cc6d4c1, lo: 0x3cfe9c2dfc36b49d), Pow128(hi: 0x9b407691d7fc44f9, lo: 0x861f219cbda230e2),
    Pow128(hi: 0xc21094364dfb5637, lo: 0x67a6ea03ed0abd1b), Pow128(hi: 0xf294b943e17a2bc5, lo: 0xc190a484e84d6c62),
    Pow128(hi: 0x979cf3ca6cec5b5b, lo: 0x58fa66d3113063bd), Pow128(hi: 0xbd8430bd08277232, lo: 0xaf390087d57c7cac),
    Pow128(hi: 0xece53cec4a314ebe, lo: 0x5b0740a9cadb9bd7), Pow128(hi: 0x940f4613ae5ed137, lo: 0x78e4886a1ec94166),
    Pow128(hi: 0xb913179899f68585, lo: 0xd71daa84a67b91c0), Pow128(hi: 0xe757dd7ec07426e6, lo: 0xcce51525d01a7630),
    Pow128(hi: 0x9096ea6f38489850, lo: 0xc00f2d37a21089de), Pow128(hi: 0xb4bca50b065abe64, lo: 0xf012f8858a94ac56),
    Pow128(hi: 0xe1ebce4dc7f16dfc, lo: 0x2c17b6a6ed39d76b), Pow128(hi: 0x8d3360f09cf6e4be, lo: 0x9b8ed228544426a3),
    Pow128(hi: 0xb080392cc4349ded, lo: 0x427286b26955304c), Pow128(hi: 0xdca04777f541c568, lo: 0x130f285f03aa7c5f),
    Pow128(hi: 0x89e42caaf9491b61, lo: 0x0be9793b624a8dbb), Pow128(hi: 0xac5d37d5b79b623a, lo: 0xcee3d78a3add312a),
    Pow128(hi: 0xd77485cb25823ac8, lo: 0x829ccd6cc9947d74), Pow128(hi: 0x86a8d39ef77164bd, lo: 0x51a20063fdfcce68),
    Pow128(hi: 0xa8530886b54dbdec, lo: 0x260a807cfd7c0203), Pow128(hi: 0xd267caa862a12d67, lo: 0x2f8d209c3cdb0284),
    Pow128(hi: 0x8380dea93da4bc61, lo: 0xbdb83461a608e192), Pow128(hi: 0xa46116538d0deb79, lo: 0xad26417a0f8b19f7),
    Pow128(hi: 0xcd795be870516657, lo: 0x986fd1d8936de074), Pow128(hi: 0x806bd9714632dff7, lo: 0xff45e3275c24ac49),
    Pow128(hi: 0xa086cfcd97bf97f4, lo: 0x7f175bf1332dd75b), Pow128(hi: 0xc8a883c0fdaf7df1, lo: 0x9edd32ed7ff94d32),
    Pow128(hi: 0xfad2a4b13d1b5d6d, lo: 0x86947fa8dff7a07e), Pow128(hi: 0x9cc3a6eec6311a64, lo: 0x341ccfc98bfac44f),
    Pow128(hi: 0xc3f490aa77bd60fd, lo: 0x412403bbeef97563), Pow128(hi: 0xf4f1b4d515acb93c, lo: 0x116d04aaeab7d2bb),
    Pow128(hi: 0x991711052d8bf3c6, lo: 0x8ae422ead2b2e3b5), Pow128(hi: 0xbf5cd54678eef0b7, lo: 0x2d9d2ba5875f9ca2),
    Pow128(hi: 0xef340a98172aace5, lo: 0x7904768ee93783cb), Pow128(hi: 0x9580869f0e7aac0f, lo: 0x2ba2ca1951c2b25f),
    Pow128(hi: 0xbae0a846d2195713, lo: 0x768b7c9fa6335ef6), Pow128(hi: 0xe998d258869facd8, lo: 0xd42e5bc78fc036b4),
    Pow128(hi: 0x91ff83775423cc07, lo: 0x849cf95cb9d82230), Pow128(hi: 0xb67f6455292cbf09, lo: 0xe5c437b3e84e2abd),
    Pow128(hi: 0xe41f3d6a7377eecb, lo: 0xdf3545a0e261b56c), Pow128(hi: 0x8e938662882af53f, lo: 0xab814b848d7d1163),
    Pow128(hi: 0xb23867fb2a35b28e, lo: 0x16619e65b0dc55bc), Pow128(hi: 0xdec681f9f4c31f32, lo: 0x9bfa05ff1d136b2b),
    Pow128(hi: 0x8b3c113c38f9f37f, lo: 0x217c43bf722c22fb), Pow128(hi: 0xae0b158b4738705f, lo: 0x69db54af4eb72bba),
    Pow128(hi: 0xd98ddaee19068c77, lo: 0xc45229db2264f6a8), Pow128(hi: 0x87f8a8d4cfa417ca, lo: 0x1ab35a28f57f1a29),
    Pow128(hi: 0xa9f6d30a038d1dbd, lo: 0xa16030b332dee0b3), Pow128(hi: 0xd47487cc8470652c, lo: 0x89b83cdfff9698e0),
    Pow128(hi: 0x84c8d4dfd2c63f3c, lo: 0xd613260bffbe1f8c), Pow128(hi: 0xa5fb0a17c777cf0a, lo: 0x0b97ef8effada76f),
    Pow128(hi: 0xcf79cc9db955c2cd, lo: 0x8e7deb72bf99114b), Pow128(hi: 0x81ac1fe293d599c0, lo: 0x390eb327b7bfaacf),
    Pow128(hi: 0xa21727db38cb0030, lo: 0x47525ff1a5af9583), Pow128(hi: 0xca9cf1d206fdc03c, lo: 0x5926f7ee0f1b7ae3),
    Pow128(hi: 0xfd442e4688bd304b, lo: 0x6f70b5e992e2599c), Pow128(hi: 0x9e4a9cec15763e2f, lo: 0x65a671b1fbcd7801),
    Pow128(hi: 0xc5dd44271ad3cdbb, lo: 0xbf100e1e7ac0d602), Pow128(hi: 0xf7549530e188c129, lo: 0x2ed411a619710b83),
    Pow128(hi: 0x9a94dd3e8cf578ba, lo: 0x7d448b07cfe6a731), Pow128(hi: 0xc13a148e3032d6e8, lo: 0x1c95adc9c3e050fe),
    Pow128(hi: 0xf18899b1bc3f8ca2, lo: 0x23bb193c34d8653e), Pow128(hi: 0x96f5600f15a7b7e6, lo: 0xd654efc5a1073f46),
    Pow128(hi: 0xbcb2b812db11a5df, lo: 0x8bea2bb709490f18), Pow128(hi: 0xebdf661791d60f57, lo: 0xeee4b6a4cb9b52de),
    Pow128(hi: 0x936b9fcebb25c996, lo: 0x354ef226ff4113cb), Pow128(hi: 0xb84687c269ef3bfc, lo: 0xc2a2aeb0bf1158bd),
    Pow128(hi: 0xe65829b3046b0afb, lo: 0xf34b5a5ceed5aeed), Pow128(hi: 0x8ff71a0fe2c2e6dd, lo: 0xb80f187a15458d54),
    Pow128(hi: 0xb3f4e093db73a094, lo: 0xa612de989a96f0a9), Pow128(hi: 0xe0f218b8d25088b9, lo: 0xcf97963ec13cacd3),
    Pow128(hi: 0x8c974f7383725574, lo: 0xe1bebde738c5ec04), Pow128(hi: 0xafbd2350644eead0, lo: 0x1a2e6d6106f76705),
    Pow128(hi: 0xdbac6c247d62a584, lo: 0x20ba08b948b540c6), Pow128(hi: 0x894bc396ce5da773, lo: 0x94744573cd71487c),
    Pow128(hi: 0xab9eb47c81f51150, lo: 0xf99156d0c0cd9a9b), Pow128(hi: 0xd686619ba27255a3, lo: 0x37f5ac84f1010142),
    Pow128(hi: 0x8613fd0145877586, lo: 0x42f98bd316a0a0c9), Pow128(hi: 0xa798fc4196e952e8, lo: 0xd3b7eec7dc48c8fb),
    Pow128(hi: 0xd17f3b51fca3a7a1, lo: 0x08a5ea79d35afb3a), Pow128(hi: 0x82ef85133de648c5, lo: 0x6567b28c2418dd04),
    Pow128(hi: 0xa3ab66580d5fdaf6, lo: 0x3ec19f2f2d1f1445), Pow128(hi: 0xcc963fee10b7d1b4, lo: 0xce7206faf866d957),
    Pow128(hi: 0xffbbcfe994e5c620, lo: 0x020e88b9b6808fad), Pow128(hi: 0x9fd561f1fd0f9bd4, lo: 0x01491574121059cc),
    Pow128(hi: 0xc7caba6e7c5382c9, lo: 0x019b5ad11694703f), Pow128(hi: 0xf9bd690a1b68637c, lo: 0xc20231855c398c4f),
    Pow128(hi: 0x9c1661a651213e2e, lo: 0xf9415ef359a3f7b1), Pow128(hi: 0xc31bfa0fe5698db9, lo: 0xb791b6b0300cf59d),
    Pow128(hi: 0xf3e2f893dec3f127, lo: 0xa576245c3c103305), Pow128(hi: 0x986ddb5c6b3a76b8, lo: 0x0769d6b9a58a1fe3),
    Pow128(hi: 0xbe89523386091466, lo: 0x09444c680eeca7dc), Pow128(hi: 0xee2ba6c0678b5980, lo: 0x8b955f8212a7d1d3),
    Pow128(hi: 0x94db483840b717f0, lo: 0x573d5bb14ba8e323), Pow128(hi: 0xba121a4650e4ddec, lo: 0x6d0cb29d9e931bec),
    Pow128(hi: 0xe896a0d7e51e1567, lo: 0x884fdf450637e2e8), Pow128(hi: 0x915e2486ef32cd61, lo: 0xf531eb8b23e2edd1),
    Pow128(hi: 0xb5b5ada8aaff80b9, lo: 0xf27e666decdba945), Pow128(hi: 0xe3231912d5bf60e7, lo: 0xef1e000968129396),
    Pow128(hi: 0x8df5efabc5979c90, lo: 0x3572c005e10b9c3e), Pow128(hi: 0xb1736b96b6fd83b4, lo: 0x42cf7007594e834d),
    Pow128(hi: 0xddd0467c64bce4a1, lo: 0x53834c092fa22421), Pow128(hi: 0x8aa22c0dbef60ee5, lo: 0x94320f85bdc55694),
    Pow128(hi: 0xad4ab7112eb3929e, lo: 0x793e93672d36ac39), Pow128(hi: 0xd89d64d57a607745, lo: 0x178e3840f8845748),
    Pow128(hi: 0x87625f056c7c4a8c, lo: 0xeeb8e3289b52b68d), Pow128(hi: 0xa93af6c6c79b5d2e, lo: 0x2a671bf2c2276430),
    Pow128(hi: 0xd389b4787982347a, lo: 0xb500e2ef72b13d3c), Pow128(hi: 0x843610cb4bf160cc, lo: 0x31208dd5a7aec645),
    Pow128(hi: 0xa54394fe1eedb8ff, lo: 0x3d68b14b119a77d7), Pow128(hi: 0xce947a3da6a9273f, lo: 0x8cc2dd9dd60115cd),
    Pow128(hi: 0x811ccc668829b888, lo: 0xf7f9ca82a5c0ada0), Pow128(hi: 0xa163ff802a3426a9, lo: 0x35f83d234f30d908),
    Pow128(hi: 0xc9bcff6034c13053, lo: 0x03764c6c22fd0f4a), Pow128(hi: 0xfc2c3f3841f17c68, lo: 0x4453df872bbc531d),
    Pow128(hi: 0x9d9ba7832936edc1, lo: 0x2ab46bb47b55b3f2), Pow128(hi: 0xc5029163f384a932, lo: 0xf56186a19a2b20ee),
    Pow128(hi: 0xf64335bcf065d37e, lo: 0xb2b9e84a00b5e92a), Pow128(hi: 0x99ea0196163fa42f, lo: 0xafb4312e4071b1ba),
    Pow128(hi: 0xc06481fb9bcf8d3a, lo: 0x1ba13d79d08e1e29), Pow128(hi: 0xf07da27a82c37089, lo: 0xa2898cd844b1a5b3),
    Pow128(hi: 0x964e858c91ba2656, lo: 0xc595f8072aef0790), Pow128(hi: 0xbbe226efb628afeb, lo: 0x76fb7608f5aac974),
    Pow128(hi: 0xeadab0aba3b2dbe6, lo: 0xd4ba538b33157bd1), Pow128(hi: 0x92c8ae6b464fc970, lo: 0xc4f47436ffed6d62),
    Pow128(hi: 0xb77ada0617e3bbcc, lo: 0xf6319144bfe8c8bb), Pow128(hi: 0xe55990879ddcaabe, lo: 0x33bdf595efe2faea),
    Pow128(hi: 0x8f57fa54c2a9eab7, lo: 0x6056b97db5eddcd2), Pow128(hi: 0xb32df8e9f3546565, lo: 0xb86c67dd23695406),
    Pow128(hi: 0xdff9772470297ebe, lo: 0xa68781d46c43a908), Pow128(hi: 0x8bfbea76c619ef37, lo: 0xa814b124c3aa49a5),
    Pow128(hi: 0xaefae51477a06b04, lo: 0x1219dd6df494dc0e), Pow128(hi: 0xdab99e59958885c5, lo: 0x16a054c971ba1312),
    Pow128(hi: 0x88b402f7fd75539c, lo: 0xee2434fde7144beb), Pow128(hi: 0xaae103b5fcd2a882, lo: 0x29ad423d60d95ee6),
    Pow128(hi: 0xd59944a37c0752a3, lo: 0xb41892ccb90fb6a0), Pow128(hi: 0x857fcae62d8493a6, lo: 0x908f5bbff3a9d224),
    Pow128(hi: 0xa6dfbd9fb8e5b88f, lo: 0x34b332aff09446ad), Pow128(hi: 0xd097ad07a71f26b3, lo: 0x81dfff5becb95858),
    Pow128(hi: 0x825ecc24c8737830, lo: 0x712bff9973f3d737), Pow128(hi: 0xa2f67f2dfa90563c, lo: 0x8d76ff7fd0f0cd05),
    Pow128(hi: 0xcbb41ef979346bcb, lo: 0xb0d4bf5fc52d0046), Pow128(hi: 0xfea126b7d78186bd, lo: 0x1d09ef37b6784057),
    Pow128(hi: 0x9f24b832e6b0f437, lo: 0xf2263582d20b2836), Pow128(hi: 0xc6ede63fa05d3144, lo: 0x6eafc2e3868df244),
    Pow128(hi: 0xf8a95fcf88747d95, lo: 0x8a5bb39c68316ed5), Pow128(hi: 0x9b69dbe1b548ce7d, lo: 0x36795041c11ee545),
    Pow128(hi: 0xc24452da229b021c, lo: 0x0417a45231669e97), Pow128(hi: 0xf2d56790ab41c2a3, lo: 0x051d8d66bdc0463c),
    Pow128(hi: 0x97c560ba6b0919a6, lo: 0x2332786036982be5), Pow128(hi: 0xbdb6b8e905cb6010, lo: 0xabff1678443e36df),
    Pow128(hi: 0xed246723473e3814, lo: 0xd6fedc16554dc497), Pow128(hi: 0x9436c0760c86e30c, lo: 0x065f498df5509ade),
    Pow128(hi: 0xb94470938fa89bcf, lo: 0x07f71bf172a4c196), Pow128(hi: 0xe7958cb87392c2c3, lo: 0x49f4e2edcf4df1fb),
    Pow128(hi: 0x90bd77f3483bb9ba, lo: 0x4e390dd4a190b73d), Pow128(hi: 0xb4ecd5f01a4aa829, lo: 0xe1c75149c9f4e50c),
    Pow128(hi: 0xe2280b6c20dd5233, lo: 0xda39259c3c721e4f), Pow128(hi: 0x8d590723948a5360, lo: 0xa863b781a5c752f1),
    Pow128(hi: 0xb0af48ec79ace838, lo: 0xd27ca5620f3927ae), Pow128(hi: 0xdcdb1b2798182245, lo: 0x071bceba9307719a),
    Pow128(hi: 0x8a08f0f8bf0f156c, lo: 0xe47161349be4a700), Pow128(hi: 0xac8b2d36eed2dac6, lo: 0x1d8db981c2ddd0c0),
    Pow128(hi: 0xd7adf884aa879178, lo: 0xa4f127e2339544f0), Pow128(hi: 0x86ccbb52ea94baeb, lo: 0x6716b8ed603d4b16),
    Pow128(hi: 0xa87fea27a539e9a6, lo: 0xc0dc6728b84c9ddb), Pow128(hi: 0xd29fe4b18e88640f, lo: 0x711380f2e65fc552),
    Pow128(hi: 0x83a3eeeef9153e8a, lo: 0xe6ac3097cffbdb53), Pow128(hi: 0xa48ceaaab75a8e2c, lo: 0xa0573cbdc3fad228),
    Pow128(hi: 0xcdb02555653131b7, lo: 0xc86d0bed34f986b2), Pow128(hi: 0x808e17555f3ebf12, lo: 0x1d442774411bf42f),
    Pow128(hi: 0xa0b19d2ab70e6ed7, lo: 0xa49531515162f13b), Pow128(hi: 0xc8de047564d20a8c, lo: 0x0dba7da5a5bbad8a),
    Pow128(hi: 0xfb158592be068d2f, lo: 0x11291d0f0f2a98ed), Pow128(hi: 0x9ced737bb6c4183e, lo: 0xaab9b229697a9f94),
    Pow128(hi: 0xc428d05aa4751e4d, lo: 0x55681eb3c3d94779), Pow128(hi: 0xf53304714d9265e0, lo: 0x2ac22660b4cf9957),
    Pow128(hi: 0x993fe2c6d07b7fac, lo: 0x1ab957fc7101bfd6), Pow128(hi: 0xbf8fdb78849a5f97, lo: 0x2167adfb8d422fcc),
    Pow128(hi: 0xef73d256a5c0f77d, lo: 0x69c1997a7092bbbf), Pow128(hi: 0x95a8637627989aae, lo: 0x2218ffec865bb557),
    Pow128(hi: 0xbb127c53b17ec15a, lo: 0xaa9f3fe7a7f2a2ad), Pow128(hi: 0xe9d71b689dde71b0, lo: 0x55470fe191ef4b59),
    Pow128(hi: 0x9226712162ab070e, lo: 0x354c69ecfb358f17), Pow128(hi: 0xb6b00d69bb55c8d2, lo: 0xc29f84683a02f2dd),
    Pow128(hi: 0xe45c10c42a2b3b06, lo: 0x734765824883af95), Pow128(hi: 0x8eb98a7a9a5b04e4, lo: 0x880c9f716d524dbd),
    Pow128(hi: 0xb267ed1940f1c61d, lo: 0xaa0fc74dc8a6e12c), Pow128(hi: 0xdf01e85f912e37a4, lo: 0x9493b9213ad09977),
    Pow128(hi: 0x8b61313bbabce2c7, lo: 0xdcdc53b4c4c25fea), Pow128(hi: 0xae397d8aa96c1b78, lo: 0x541368a1f5f2f7e5),
    Pow128(hi: 0xd9c7dced53c72256, lo: 0x691842ca736fb5de), Pow128(hi: 0x881cea14545c7576, lo: 0x81af29be8825d1ab),
    Pow128(hi: 0xaa242499697392d3, lo: 0x221af42e2a2f4616), Pow128(hi: 0xd4ad2dbfc3d07788, lo: 0x6aa1b139b4bb179b),
    Pow128(hi: 0x84ec3c97da624ab5, lo: 0x42a50ec410f4eec1), Pow128(hi: 0xa6274bbdd0fadd62, lo: 0x134e527515322a71),
    Pow128(hi: 0xcfb11ead453994bb, lo: 0x9821e7125a7eb50d), Pow128(hi: 0x81ceb32c4b43fcf5, lo: 0x7f15306b788f3128),
    Pow128(hi: 0xa2425ff75e14fc32, lo: 0x5eda7c8656b2fd72), Pow128(hi: 0xcad2f7f5359a3b3f, lo: 0xf6911ba7ec5fbccf),
    Pow128(hi: 0xfd87b5f28300ca0e, lo: 0x74356291e777ac03), Pow128(hi: 0x9e74d1b791e07e49, lo: 0x88a15d9b30aacb82),
    Pow128(hi: 0xc612062576589ddb, lo: 0x6ac9b501fcd57e62), Pow128(hi: 0xf79687aed3eec552, lo: 0xc57c22427c0addfb),
    Pow128(hi: 0x9abe14cd44753b53, lo: 0x3b6d95698d86cabd), Pow128(hi: 0xc16d9a0095928a28, lo: 0x8a48fac3f0e87d6c),
    Pow128(hi: 0xf1c90080baf72cb2, lo: 0xacdb3974ed229cc7), Pow128(hi: 0x971da05074da7bef, lo: 0x2c0903e91435a1fc),
    Pow128(hi: 0xbce5086492111aeb, lo: 0x770b44e359430a7b), Pow128(hi: 0xec1e4a7db69561a6, lo: 0xd4ce161c2f93cd1a),
    Pow128(hi: 0x9392ee8e921d5d08, lo: 0xc500cdd19dbc6030), Pow128(hi: 0xb877aa3236a4b44a, lo: 0xf6410146052b783d),
    Pow128(hi: 0xe69594bec44de15c, lo: 0xb3d141978676564c), Pow128(hi: 0x901d7cf73ab0acda, lo: 0xf062c8feb409f5ef),
    Pow128(hi: 0xb424dc35095cd810, lo: 0xac7b7b3e610c736b), Pow128(hi: 0xe12e13424bb40e14, lo: 0xd79a5a0df94f9046),
    Pow128(hi: 0x8cbccc096f5088cc, lo: 0x06c07848bbd1ba2c), Pow128(hi: 0xafebff0bcb24aaff, lo: 0x0870965aeac628b7),
    Pow128(hi: 0xdbe6fecebdedd5bf, lo: 0x4a8cbbf1a577b2e4), Pow128(hi: 0x89705f4136b4a598, lo: 0xce97f577076acfcf),
    Pow128(hi: 0xabcc77118461cefd, lo: 0x023df2d4c94583c2), Pow128(hi: 0xd6bf94d5e57a42bd, lo: 0xc2cd6f89fb96e4b3),
    Pow128(hi: 0x8637bd05af6c69b6, lo: 0x59c065b63d3e4ef0), Pow128(hi: 0xa7c5ac471b478424, lo: 0xf0307f23cc8de2ac),
    Pow128(hi: 0xd1b71758e219652c, lo: 0x2c3c9eecbfb15b57), Pow128(hi: 0x83126e978d4fdf3c, lo: 0x9ba5e353f7ced916),
    Pow128(hi: 0xa3d70a3d70a3d70b, lo: 0xc28f5c28f5c28f5c), Pow128(hi: 0xcccccccccccccccd, lo: 0x3333333333333333),
    Pow128(hi: 0x8000000000000000, lo: 0x0000000000000000), Pow128(hi: 0xa000000000000000, lo: 0x0000000000000000),
    Pow128(hi: 0xc800000000000000, lo: 0x0000000000000000), Pow128(hi: 0xfa00000000000000, lo: 0x0000000000000000),
    Pow128(hi: 0x9c40000000000000, lo: 0x0000000000000000), Pow128(hi: 0xc350000000000000, lo: 0x0000000000000000),
    Pow128(hi: 0xf424000000000000, lo: 0x0000000000000000), Pow128(hi: 0x9896800000000000, lo: 0x0000000000000000),
    Pow128(hi: 0xbebc200000000000, lo: 0x0000000000000000), Pow128(hi: 0xee6b280000000000, lo: 0x0000000000000000),
    Pow128(hi: 0x9502f90000000000, lo: 0x0000000000000000), Pow128(hi: 0xba43b74000000000, lo: 0x0000000000000000),
    Pow128(hi: 0xe8d4a51000000000, lo: 0x0000000000000000), Pow128(hi: 0x9184e72a00000000, lo: 0x0000000000000000),
    Pow128(hi: 0xb5e620f480000000, lo: 0x0000000000000000), Pow128(hi: 0xe35fa931a0000000, lo: 0x0000000000000000),
    Pow128(hi: 0x8e1bc9bf04000000, lo: 0x0000000000000000), Pow128(hi: 0xb1a2bc2ec5000000, lo: 0x0000000000000000),
    Pow128(hi: 0xde0b6b3a76400000, lo: 0x0000000000000000), Pow128(hi: 0x8ac7230489e80000, lo: 0x0000000000000000),
    Pow128(hi: 0xad78ebc5ac620000, lo: 0x0000000000000000), Pow128(hi: 0xd8d726b7177a8000, lo: 0x0000000000000000),
    Pow128(hi: 0x878678326eac9000, lo: 0x0000000000000000), Pow128(hi: 0xa968163f0a57b400, lo: 0x0000000000000000),
    Pow128(hi: 0xd3c21bcecceda100, lo: 0x0000000000000000), Pow128(hi: 0x84595161401484a0, lo: 0x0000000000000000),
    Pow128(hi: 0xa56fa5b99019a5c8, lo: 0x0000000000000000), Pow128(hi: 0xcecb8f27f4200f3a, lo: 0x0000000000000000),
    Pow128(hi: 0x813f3978f8940985, lo: 0xc000000000000000), Pow128(hi: 0xa18f07d736b90be6, lo: 0xb000000000000000),
    Pow128(hi: 0xc9f2c9cd04674edf, lo: 0x5c00000000000000), Pow128(hi: 0xfc6f7c4045812297, lo: 0xb300000000000000),
    Pow128(hi: 0x9dc5ada82b70b59e, lo: 0x0fe0000000000000), Pow128(hi: 0xc5371912364ce306, lo: 0x93d8000000000000),
    Pow128(hi: 0xf684df56c3e01bc7, lo: 0x38ce000000000000), Pow128(hi: 0x9a130b963a6c115d, lo: 0xc380c00000000000),
    Pow128(hi: 0xc097ce7bc90715b4, lo: 0xb460f00000000000), Pow128(hi: 0xf0bdc21abb48db21, lo: 0xe1792c0000000000),
    Pow128(hi: 0x96769950b50d88f5, lo: 0xecebbb8000000000), Pow128(hi: 0xbc143fa4e250eb32, lo: 0xe826aa6000000000),
    Pow128(hi: 0xeb194f8e1ae525fe, lo: 0xa23054f800000000), Pow128(hi: 0x92efd1b8d0cf37bf, lo: 0xa55e351b00000000),
    Pow128(hi: 0xb7abc627050305ae, lo: 0x0eb5c261c0000000), Pow128(hi: 0xe596b7b0c643c71a, lo: 0x926332fa30000000),
    Pow128(hi: 0x8f7e32ce7bea5c70, lo: 0x1b7dffdc5e000000), Pow128(hi: 0xb35dbf821ae4f38c, lo: 0x225d7fd375800000),
    Pow128(hi: 0xe0352f62a19e306f, lo: 0x2af4dfc852e00000), Pow128(hi: 0x8c213d9da502de46, lo: 0xbad90bdd33cc0000),
    Pow128(hi: 0xaf298d050e4395d7, lo: 0x698f4ed480bf0000), Pow128(hi: 0xdaf3f04651d47b4d, lo: 0xc3f32289a0eec000),
    Pow128(hi: 0x88d8762bf324cd10, lo: 0x5a77f59604953800), Pow128(hi: 0xab0e93b6efee0054, lo: 0x7115f2fb85ba8600),
    Pow128(hi: 0xd5d238a4abe98069, lo: 0x8d5b6fba67292780), Pow128(hi: 0x85a36366eb71f042, lo: 0xb85925d48079b8b0),
    Pow128(hi: 0xa70c3c40a64e6c52, lo: 0x666f6f49a09826dc), Pow128(hi: 0xd0cf4b50cfe20766, lo: 0x000b4b1c08be3093),
    Pow128(hi: 0x82818f1281ed44a0, lo: 0x40070ef18576de5b), Pow128(hi: 0xa321f2d7226895c8, lo: 0x5008d2ade6d495f2),
    Pow128(hi: 0xcbea6f8ceb02bb3a, lo: 0x640b07596089bb6f), Pow128(hi: 0xfee50b7025c36a09, lo: 0xfd0dc92fb8ac2a4b),
    Pow128(hi: 0x9f4f2726179a2246, lo: 0xfe289dbdd36b9a6f), Pow128(hi: 0xc722f0ef9d80aad7, lo: 0xbdb2c52d4846810a),
    Pow128(hi: 0xf8ebad2b84e0d58c, lo: 0x2d1f76789a58214d), Pow128(hi: 0x9b934c3b330c8578, lo: 0x9c33aa0b607714d0),
    Pow128(hi: 0xc2781f49ffcfa6d6, lo: 0xc340948e3894da04), Pow128(hi: 0xf316271c7fc3908b, lo: 0x7410b9b1c6ba1085),
    Pow128(hi: 0x97edd871cfda3a57, lo: 0x688a740f1c344a53), Pow128(hi: 0xbde94e8e43d0c8ed, lo: 0xc2ad1112e3415ce8),
    Pow128(hi: 0xed63a231d4c4fb28, lo: 0xb35855579c11b422), Pow128(hi: 0x945e455f24fb1cf9, lo: 0x70173556c18b1095),
    Pow128(hi: 0xb975d6b6ee39e437, lo: 0x4c1d02ac71edd4bb), Pow128(hi: 0xe7d34c64a9c85d45, lo: 0x9f2443578e6949e9),
    Pow128(hi: 0x90e40fbeea1d3a4b, lo: 0x4376aa16b901ce32), Pow128(hi: 0xb51d13aea4a488de, lo: 0x9454549c674241be),
    Pow128(hi: 0xe264589a4dcdab15, lo: 0x396969c38112d22e), Pow128(hi: 0x8d7eb76070a08aed, lo: 0x03e1e21a30abc35d),
    Pow128(hi: 0xb0de65388cc8ada9, lo: 0xc4da5aa0bcd6b434), Pow128(hi: 0xdd15fe86affad913, lo: 0xb610f148ec0c6141),
    Pow128(hi: 0x8a2dbf142dfcc7ac, lo: 0x91ca96cd9387bcc8), Pow128(hi: 0xacb92ed9397bf997, lo: 0xb63d3c80f869abfb),
    Pow128(hi: 0xd7e77a8f87daf7fc, lo: 0x23cc8ba1368416f9), Pow128(hi: 0x86f0ac99b4e8dafe, lo: 0x965fd744c2128e5c),
    Pow128(hi: 0xa8acd7c0222311bd, lo: 0x3bf7cd15f29731f3), Pow128(hi: 0xd2d80db02aabd62c, lo: 0x0af5c05b6f3cfe6f),
    Pow128(hi: 0x83c7088e1aab65dc, lo: 0x86d9983925861f05), Pow128(hi: 0xa4b8cab1a1563f53, lo: 0xa88ffe476ee7a6c7),
    Pow128(hi: 0xcde6fd5e09abcf27, lo: 0x12b3fdd94aa19079), Pow128(hi: 0x80b05e5ac60b6179, lo: 0xabb07ea7cea4fa4b),
    Pow128(hi: 0xa0dc75f1778e39d7, lo: 0x969c9e51c24e38de), Pow128(hi: 0xc913936dd571c84d, lo: 0xfc43c5e632e1c716),
    Pow128(hi: 0xfb5878494ace3a60, lo: 0xfb54b75fbf9a38dc), Pow128(hi: 0x9d174b2dcec0e47c, lo: 0x9d14f29bd7c06389),
    Pow128(hi: 0xc45d1df942711d9b, lo: 0xc45a2f42cdb07c6b), Pow128(hi: 0xf5746577930d6501, lo: 0x3570bb13811c9b86),
    Pow128(hi: 0x9968bf6abbe85f21, lo: 0x816674ec30b1e134), Pow128(hi: 0xbfc2ef456ae276e9, lo: 0x61c012273cde5981),
    Pow128(hi: 0xefb3ab16c59b14a3, lo: 0x3a3016b10c15efe1), Pow128(hi: 0x95d04aee3b80ece6, lo: 0x445e0e2ea78db5ed),
    Pow128(hi: 0xbb445da9ca612820, lo: 0xd57591ba51712368), Pow128(hi: 0xea1575143cf97227, lo: 0x0ad2f628e5cd6c42),
    Pow128(hi: 0x924d692ca61be759, lo: 0xa6c3d9d98fa063a9), Pow128(hi: 0xb6e0c377cfa2e12f, lo: 0x9074d04ff3887c93),
    Pow128(hi: 0xe498f455c38b997b, lo: 0xf4920463f06a9bb8), Pow128(hi: 0x8edf98b59a373fed, lo: 0xb8db42be7642a153),
    Pow128(hi: 0xb2977ee300c50fe8, lo: 0xa712136e13d349a8), Pow128(hi: 0xdf3d5e9bc0f653e2, lo: 0xd0d6984998c81c12),
    Pow128(hi: 0x8b865b215899f46d, lo: 0x42861f2dff7d118b), Pow128(hi: 0xae67f1e9aec07188, lo: 0x1327a6f97f5c55ee),
    Pow128(hi: 0xda01ee641a708dea, lo: 0x17f190b7df336b6a), Pow128(hi: 0x884134fe908658b3, lo: 0xcef6fa72eb802322),
    Pow128(hi: 0xaa51823e34a7eedf, lo: 0x42b4b90fa6602bea), Pow128(hi: 0xd4e5e2cdc1d1ea97, lo: 0x9361e7538ff836e5),
    Pow128(hi: 0x850fadc09923329f, lo: 0xfc1d309439fb224f), Pow128(hi: 0xa6539930bf6bff46, lo: 0x7b247cb94879eae3),
    Pow128(hi: 0xcfe87f7cef46ff17, lo: 0x19ed9be79a98659c), Pow128(hi: 0x81f14fae158c5f6f, lo: 0xb0348170c09f3f81),
    Pow128(hi: 0xa26da3999aef774a, lo: 0x1c41a1ccf0c70f62), Pow128(hi: 0xcb090c8001ab551d, lo: 0xa3520a402cf8d33a),
    Pow128(hi: 0xfdcb4fa002162a64, lo: 0x8c268cd038370809), Pow128(hi: 0x9e9f11c4014dda7f, lo: 0xd798180223226505),
    Pow128(hi: 0xc646d63501a1511e, lo: 0x4d7e1e02abeafe47), Pow128(hi: 0xf7d88bc24209a566, lo: 0xe0dda58356e5bdd9),
    Pow128(hi: 0x9ae7575969460760, lo: 0xcc8a8772164f96a7), Pow128(hi: 0xc1a12d2fc3978938, lo: 0xffad294e9be37c51),
    Pow128(hi: 0xf209787bb47d6b85, lo: 0x3f9873a242dc5b65), Pow128(hi: 0x9745eb4d50ce6333, lo: 0x07bf484569c9b91f),
    Pow128(hi: 0xbd176620a501fc00, lo: 0x49af1a56c43c2767), Pow128(hi: 0xec5d3fa8ce427b00, lo: 0x5c1ae0ec754b3141),
    Pow128(hi: 0x93ba47c980e98ce0, lo: 0x3990cc93c94efec8), Pow128(hi: 0xb8a8d9bbe123f018, lo: 0x47f4ffb8bba2be7b),
    Pow128(hi: 0xe6d3102ad96cec1e, lo: 0x59f23fa6ea8b6e1a), Pow128(hi: 0x9043ea1ac7e41393, lo: 0x783767c8529724d0),
    Pow128(hi: 0xb454e4a179dd1878, lo: 0xd64541ba673cee04), Pow128(hi: 0xe16a1dc9d8545e95, lo: 0x0bd69229010c2985),
    Pow128(hi: 0x8ce2529e2734bb1e, lo: 0xe7661b59a0a799f3), Pow128(hi: 0xb01ae745b101e9e5, lo: 0xa13fa23008d18070),
    Pow128(hi: 0xdc21a1171d42645e, lo: 0x898f8abc0b05e08c), Pow128(hi: 0x899504ae72497ebb, lo: 0x95f9b6b586e3ac57),
    Pow128(hi: 0xabfa45da0edbde6a, lo: 0xfb782462e89c976d), Pow128(hi: 0xd6f8d7509292d604, lo: 0xba562d7ba2c3bd49),
    Pow128(hi: 0x865b86925b9bc5c3, lo: 0xf475dc6d45ba564d), Pow128(hi: 0xa7f26836f282b733, lo: 0x719353889728ebe1),
    Pow128(hi: 0xd1ef0244af236500, lo: 0xcdf8286abcf326d9), Pow128(hi: 0x8335616aed761f20, lo: 0x80bb1942b617f847),
    Pow128(hi: 0xa402b9c5a8d3a6e8, lo: 0xa0e9df93639df659), Pow128(hi: 0xcd036837130890a2, lo: 0xc92457783c8573f0),
    Pow128(hi: 0x802221226be55a65, lo: 0x3db6b6ab25d36876), Pow128(hi: 0xa02aa96b06deb0fe, lo: 0x0d246455ef484293),
    Pow128(hi: 0xc83553c5c8965d3e, lo: 0x906d7d6b6b1a5338), Pow128(hi: 0xfa42a8b73abbf48d, lo: 0x3488dcc645e0e806),
    Pow128(hi: 0x9c69a97284b578d8, lo: 0x00d589fbebac9104), Pow128(hi: 0xc38413cf25e2d70e, lo: 0x010aec7ae697b545),
    Pow128(hi: 0xf46518c2ef5b8cd2, lo: 0x814da799a03da296), Pow128(hi: 0x98bf2f79d5993803, lo: 0x10d088c00426859e),
    Pow128(hi: 0xbeeefb584aff8604, lo: 0x5504aaf005302705), Pow128(hi: 0xeeaaba2e5dbf6785, lo: 0x6a45d5ac067c30c7),
    Pow128(hi: 0x952ab45cfa97a0b3, lo: 0x226ba58b840d9e7c), Pow128(hi: 0xba756174393d88e0, lo: 0x6b068eee6511061b),
    Pow128(hi: 0xe912b9d1478ceb18, lo: 0x85c832a9fe5547a2), Pow128(hi: 0x91abb422ccb812ef, lo: 0x539d1faa3ef54cc5),
    Pow128(hi: 0xb616a12b7fe617ab, lo: 0xa8846794ceb29ff6), Pow128(hi: 0xe39c49765fdf9d95, lo: 0x12a5817a025f47f4),
    Pow128(hi: 0x8e41ade9fbebc27e, lo: 0xeba770ec417b8cf8), Pow128(hi: 0xb1d219647ae6b31d, lo: 0xa6914d2751da7037),
    Pow128(hi: 0xde469fbd99a05fe4, lo: 0x9035a07126510c44), Pow128(hi: 0x8aec23d680043bef, lo: 0xda218446b7f2a7ab),
    Pow128(hi: 0xada72ccc20054aea, lo: 0x50a9e55865ef5195), Pow128(hi: 0xd910f7ff28069da5, lo: 0xe4d45eae7f6b25fb),
    Pow128(hi: 0x87aa9aff79042287, lo: 0x6f04bb2d0fa2f7bd), Pow128(hi: 0xa99541bf57452b29, lo: 0xcac5e9f8538bb5ac),
    Pow128(hi: 0xd3fa922f2d1675f3, lo: 0xbd776476686ea317), Pow128(hi: 0x847c9b5d7c2e09b8, lo: 0x966a9eca014525ee),
    Pow128(hi: 0xa59bc234db398c26, lo: 0xbc05467c81966f6a), Pow128(hi: 0xcf02b2c21207ef2f, lo: 0x6b06981ba1fc0b44),
    Pow128(hi: 0x8161afb94b44f57e, lo: 0xe2e41f11453d870a), Pow128(hi: 0xa1ba1ba79e1632dd, lo: 0x9b9d26d5968ce8cd),
    Pow128(hi: 0xca28a291859bbf94, lo: 0x8284708afc302301), Pow128(hi: 0xfcb2cb35e702af79, lo: 0xa3258cadbb3c2bc1),
    Pow128(hi: 0x9defbf01b061adac, lo: 0xc5f777ec95059b58), Pow128(hi: 0xc56baec21c7a1917, lo: 0xf77555e7ba47022f),
    Pow128(hi: 0xf6c69a72a3989f5c, lo: 0x7552ab61a8d8c2ba), Pow128(hi: 0x9a3c2087a63f639a, lo: 0xc953ab1d098779b4),
    Pow128(hi: 0xc0cb28a98fcf3c80, lo: 0x7ba895e44be95822), Pow128(hi: 0xf0fdf2d3f3c30ba0, lo: 0x9a92bb5d5ee3ae2a),
    Pow128(hi: 0x969eb7c47859e744, lo: 0x609bb51a5b4e4cda), Pow128(hi: 0xbc4665b596706115, lo: 0x78c2a260f221e011),
    Pow128(hi: 0xeb57ff22fc0c795a, lo: 0x56f34af92eaa5815), Pow128(hi: 0x9316ff75dd87cbd9, lo: 0xf6580edbbd2a770d),
    Pow128(hi: 0xb7dcbf5354e9becf, lo: 0xf3ee1292ac7514d0), Pow128(hi: 0xe5d3ef282a242e82, lo: 0x70e9973757925a05),
    Pow128(hi: 0x8fa475791a569d11, lo: 0x0691fe8296bb7843), Pow128(hi: 0xb38d92d760ec4456, lo: 0xc8367e233c6a5653),
    Pow128(hi: 0xe070f78d3927556b, lo: 0x7a441dac0b84ebe8), Pow128(hi: 0x8c469ab843b89563, lo: 0x6c6a928b87331371),
    Pow128(hi: 0xaf58416654a6babc, lo: 0xc785372e68ffd84d), Pow128(hi: 0xdb2e51bfe9d0696b, lo: 0xf96684fa033fce61),
    Pow128(hi: 0x88fcf317f22241e3, lo: 0xbbe0131c4207e0fc), Pow128(hi: 0xab3c2fddeeaad25b, lo: 0x2ad817e35289d93c),
    Pow128(hi: 0xd60b3bd56a5586f2, lo: 0x758e1ddc272c4f8b), Pow128(hi: 0x85c7056562757457, lo: 0x0978d2a9987bb1b6),
    Pow128(hi: 0xa738c6bebb12d16d, lo: 0x4bd70753fe9a9e24), Pow128(hi: 0xd106f86e69d785c8, lo: 0x1eccc928fe4145ad),
    Pow128(hi: 0x82a45b450226b39d, lo: 0x133ffdb99ee8cb8c), Pow128(hi: 0xa34d721642b06085, lo: 0xd80ffd2806a2fe6f),
    Pow128(hi: 0xcc20ce9bd35c78a6, lo: 0xce13fc72084bbe0b), Pow128(hi: 0xff290242c83396cf, lo: 0x8198fb8e8a5ead8e),
    Pow128(hi: 0x9f79a169bd203e42, lo: 0xf0ff9d39167b2c79), Pow128(hi: 0xc75809c42c684dd2, lo: 0xad3f84875c19f797),
    Pow128(hi: 0xf92e0c3537826146, lo: 0x588f65a93320757d), Pow128(hi: 0x9bbcc7a142b17ccc, lo: 0x77599f89bff4496e),
    Pow128(hi: 0xc2abf989935ddbff, lo: 0x9530076c2ff15bca), Pow128(hi: 0xf356f7ebf83552ff, lo: 0xfa7c09473bedb2bc),
    Pow128(hi: 0x98165af37b2153df, lo: 0x3c8d85cc85748fb5), Pow128(hi: 0xbe1bf1b059e9a8d7, lo: 0x8bb0e73fa6d1b3a3),
    Pow128(hi: 0xeda2ee1c7064130d, lo: 0xee9d210f9086208c), Pow128(hi: 0x9485d4d1c63e8be8, lo: 0x752234a9ba53d457),
    Pow128(hi: 0xb9a74a0637ce2ee2, lo: 0x926ac1d428e8c96d), Pow128(hi: 0xe8111c87c5c1ba9a, lo: 0x370572493322fbc8),
    Pow128(hi: 0x910ab1d4db9914a1, lo: 0xe263676dbff5dd5d), Pow128(hi: 0xb54d5e4a127f59c9, lo: 0xdafc41492ff354b4),
    Pow128(hi: 0xe2a0b5dc971f303b, lo: 0xd1bb519b7bf029e2), Pow128(hi: 0x8da471a9de737e25, lo: 0xa31513012d761a2d),
    Pow128(hi: 0xb10d8e1456105dae, lo: 0x8bda57c178d3a0b8), Pow128(hi: 0xdd50f1996b947519, lo: 0x2ed0edb1d70888e6),
    Pow128(hi: 0x8a5296ffe33cc930, lo: 0x7d42948f26655590), Pow128(hi: 0xace73cbfdc0bfb7c, lo: 0x9c9339b2effeaaf4),
    Pow128(hi: 0xd8210befd30efa5b, lo: 0xc3b8081fabfe55b1), Pow128(hi: 0x8714a775e3e95c79, lo: 0x9a530513cb7ef58e),
    Pow128(hi: 0xa8d9d1535ce3b397, lo: 0x80e7c658be5eb2f2), Pow128(hi: 0xd31045a8341ca07d, lo: 0xe121b7eeedf65faf),
    Pow128(hi: 0x83ea2b892091e44e, lo: 0x6cb512f554b9fbcd), Pow128(hi: 0xa4e4b66b68b65d61, lo: 0x07e257b2a9e87ac0),
    Pow128(hi: 0xce1de40642e3f4ba, lo: 0xc9daed9f54629971), Pow128(hi: 0x80d2ae83e9ce78f4, lo: 0x3e28d48394bd9fe6),
    Pow128(hi: 0xa1075a24e4421731, lo: 0x4db309a479ed07e0), Pow128(hi: 0xc94930ae1d529cfd, lo: 0x211fcc0d986849d8),
    Pow128(hi: 0xfb9b7cd9a4a7443d, lo: 0xe967bf10fe825c4e), Pow128(hi: 0x9d412e0806e88aa6, lo: 0x71e0d76a9f1179b1),
    Pow128(hi: 0xc491798a08a2ad4f, lo: 0x0e590d4546d5d81d), Pow128(hi: 0xf5b5d7ec8acb58a3, lo: 0x51ef5096988b4e24),
    Pow128(hi: 0x9991a6f3d6bf1766, lo: 0x5335925e1f5710d6), Pow128(hi: 0xbff610b0cc6edd40, lo: 0xe802f6f5a72cd50c),
    Pow128(hi: 0xeff394dcff8a948f, lo: 0x2203b4b310f80a4f), Pow128(hi: 0x95f83d0a1fb69cda, lo: 0xb54250efea9b0671),
    Pow128(hi: 0xbb764c4ca7a44410, lo: 0x6292e52be541c80e), Pow128(hi: 0xea53df5fd18d5514, lo: 0x7b379e76de923a12),
    Pow128(hi: 0x92746b9be2f8552d, lo: 0xcd02c30a4b1b644b), Pow128(hi: 0xb7118682dbb66a78, lo: 0xc04373ccdde23d5e),
    Pow128(hi: 0xe4d5e82392a40516, lo: 0xf05450c0155accb5), Pow128(hi: 0x8f05b1163ba6832e, lo: 0xd634b2780d58bff1),
    Pow128(hi: 0xb2c71d5bca9023f9, lo: 0x8bc1df1610aeefed), Pow128(hi: 0xdf78e4b2bd342cf7, lo: 0x6eb256db94daabe9),
    Pow128(hi: 0x8bab8eefb6409c1b, lo: 0xe52f76493d08ab71), Pow128(hi: 0xae9672aba3d0c321, lo: 0x5e7b53db8c4ad64e),
    Pow128(hi: 0xda3c0f568cc4f3e9, lo: 0x361a28d26f5d8be1), Pow128(hi: 0x8865899617fb1872, lo: 0x81d05983859a776d),
    Pow128(hi: 0xaa7eebfb9df9de8e, lo: 0x22446fe467011548), Pow128(hi: 0xd51ea6fa85785632, lo: 0xaad58bdd80c15a9a),
    Pow128(hi: 0x8533285c936b35df, lo: 0x2ac5776a7078d8a0), Pow128(hi: 0xa67ff273b8460357, lo: 0x7576d5450c970ec8),
    Pow128(hi: 0xd01fef10a657842d, lo: 0xd2d48a964fbcd27a), Pow128(hi: 0x8213f56a67f6b29c, lo: 0x63c4d69df1d6038c),
    Pow128(hi: 0xa298f2c501f45f43, lo: 0x7cb60c456e4b8470), Pow128(hi: 0xcb3f2f7642717714, lo: 0xdbe38f56c9de658c),
    Pow128(hi: 0xfe0efb53d30dd4d8, lo: 0x12dc732c7c55feef), Pow128(hi: 0x9ec95d1463e8a507, lo: 0x0bc9c7fbcdb5bf55),
    Pow128(hi: 0xc67bb4597ce2ce49, lo: 0x4ebc39fac1232f2a), Pow128(hi: 0xf81aa16fdc1b81db, lo: 0x226b4879716bfaf5),
    Pow128(hi: 0x9b10a4e5e9913129, lo: 0x35830d4be6e37cd9), Pow128(hi: 0xc1d4ce1f63f57d73, lo: 0x02e3d09ee09c5c0f),
    Pow128(hi: 0xf24a01a73cf2dcd0, lo: 0x439cc4c698c37313), Pow128(hi: 0x976e41088617ca02, lo: 0x2a41fafc1f7a27ec),
    Pow128(hi: 0xbd49d14aa79dbc83, lo: 0xb4d279bb2758b1e7), Pow128(hi: 0xec9c459d51852ba3, lo: 0x22071829f12ede61),
    Pow128(hi: 0x93e1ab8252f33b46, lo: 0x35446f1a36bd4afc), Pow128(hi: 0xb8da1662e7b00a18, lo: 0xc2958ae0c46c9dbc),
    Pow128(hi: 0xe7109bfba19c0c9e, lo: 0xf33aed98f587c52b), Pow128(hi: 0x906a617d450187e3, lo: 0xd804d47f9974db3a),
    Pow128(hi: 0xb484f9dc9641e9db, lo: 0x4e06099f7fd21209), Pow128(hi: 0xe1a63853bbd26452, lo: 0xa1878c075fc6968c),
    Pow128(hi: 0x8d07e33455637eb3, lo: 0x24f4b7849bdc1e17), Pow128(hi: 0xb049dc016abc5e60, lo: 0x6e31e565c2d3259d),
    Pow128(hi: 0xdc5c5301c56b75f8, lo: 0x89be5ebf3387ef04), Pow128(hi: 0x89b9b3e11b6329bb, lo: 0x5616fb378034f562),
    Pow128(hi: 0xac2820d9623bf42a, lo: 0xab9cba05604232bb), Pow128(hi: 0xd732290fbacaf134, lo: 0x5683e886b852bf6a),
    Pow128(hi: 0x867f59a9d4bed6c1, lo: 0xb61271543333b7a2), Pow128(hi: 0xa81f301449ee8c71, lo: 0xa3970da94000a58b),
    Pow128(hi: 0xd226fc195c6a2f8d, lo: 0x8c7cd1139000ceee), Pow128(hi: 0x83585d8fd9c25db8, lo: 0x37ce02ac3a008154),
    Pow128(hi: 0xa42e74f3d032f526, lo: 0x45c183574880a1aa), Pow128(hi: 0xcd3a1230c43fb270, lo: 0xd731e42d1aa0ca14),
    Pow128(hi: 0x80444b5e7aa7cf86, lo: 0x867f2e9c30a47e4c), Pow128(hi: 0xa0555e361951c367, lo: 0x281efa433ccd9de0),
    Pow128(hi: 0xc86ab5c39fa63441, lo: 0x7226b8d40c010558), Pow128(hi: 0xfa856334878fc151, lo: 0x4eb067090f0146ae),
    Pow128(hi: 0x9c935e00d4b9d8d3, lo: 0x912e4065a960cc2c), Pow128(hi: 0xc3b8358109e84f08, lo: 0xf579d07f13b8ff37),
    Pow128(hi: 0xf4a642e14c6262c9, lo: 0x32d8449ed8a73f05), Pow128(hi: 0x98e7e9cccfbd7dbe, lo: 0x7fc72ae347688763),
    Pow128(hi: 0xbf21e44003acdd2d, lo: 0x1fb8f59c1942a93c), Pow128(hi: 0xeeea5d5004981479, lo: 0xe7a733031f93538b),
    Pow128(hi: 0x95527a5202df0ccc, lo: 0xf0c87fe1f3bc1437), Pow128(hi: 0xbaa718e68396cffe, lo: 0x2cfa9fda70ab1945),
    Pow128(hi: 0xe950df20247c83fe, lo: 0xb83947d10cd5df96), Pow128(hi: 0x91d28b7416cdd27f, lo: 0xb323cce2a805abbe),
    Pow128(hi: 0xb6472e511c81471e, lo: 0x1fecc01b520716ad), Pow128(hi: 0xe3d8f9e563a198e6, lo: 0xa7e7f0222688dc59),
    Pow128(hi: 0x8e679c2f5e44ff90, lo: 0xa8f0f615581589b7), Pow128(hi: 0xb201833b35d63f74, lo: 0xd32d339aae1aec25),
    Pow128(hi: 0xde81e40a034bcf50, lo: 0x07f8808159a1a72e), Pow128(hi: 0x8b112e86420f6192, lo: 0x04fb5050d805087d),
    Pow128(hi: 0xadd57a27d29339f7, lo: 0x863a24650e064a9c), Pow128(hi: 0xd94ad8b1c7380875, lo: 0xe7c8ad7e5187dd43),
    Pow128(hi: 0x87cec76f1c830549, lo: 0x70dd6c6ef2f4ea4a), Pow128(hi: 0xa9c2794ae3a3c69b, lo: 0x4d14c78aafb224dd),
    Pow128(hi: 0xd433179d9c8cb842, lo: 0xa059f96d5b9eae14), Pow128(hi: 0x849feec281d7f329, lo: 0x24383be459432ccc),
    Pow128(hi: 0xa5c7ea73224deff4, lo: 0xed464add6f93f7ff), Pow128(hi: 0xcf39e50feae16bf0, lo: 0x2897dd94cb78f5ff),
    Pow128(hi: 0x81842f29f2cce376, lo: 0x195eea7cff2b99bf), Pow128(hi: 0xa1e53af46f801c54, lo: 0x9fb6a51c3ef6802f),
    Pow128(hi: 0xca5e89b18b602369, lo: 0xc7a44e634eb4203b), Pow128(hi: 0xfcf62c1dee382c43, lo: 0xb98d61fc2261284a),
    Pow128(hi: 0x9e19db92b4e31baa, lo: 0x93f85d3d957cb92e), Pow128(hi: 0xc5a05277621be294, lo: 0x38f6748cfadbe77a),
    Pow128(hi: 0xf70867153aa2db39, lo: 0x473411b03992e158), Pow128(hi: 0x9a65406d44a5c904, lo: 0x8c808b0e23fbccd7),
    Pow128(hi: 0xc0fe908895cf3b45, lo: 0xafa0add1acfac00d), Pow128(hi: 0xf13e34aabb430a16, lo: 0x9b88d94618397010),
    Pow128(hi: 0x96c6e0eab509e64e, lo: 0xa13587cbcf23e60a), Pow128(hi: 0xbc789925624c5fe1, lo: 0x4982e9bec2ecdf8d),
    Pow128(hi: 0xeb96bf6ebadf77d9, lo: 0x1be3a42e73a81770), Pow128(hi: 0x933e37a534cbaae8, lo: 0x716e469d08490ea6),
    Pow128(hi: 0xb80dc58e81fe95a2, lo: 0x8dc9d8444a5b524f), Pow128(hi: 0xe61136f2227e3b0a, lo: 0x313c4e555cf226e3),
    Pow128(hi: 0x8fcac257558ee4e7, lo: 0xdec5b0f55a17584e), Pow128(hi: 0xb3bd72ed2af29e20, lo: 0x56771d32b09d2e62),
    Pow128(hi: 0xe0accfa875af45a8, lo: 0x6c14e47f5cc479fa), Pow128(hi: 0x8c6c01c9498d8b89, lo: 0x438d0ecf99facc3c),
    Pow128(hi: 0xaf87023b9bf0ee6b, lo: 0x1470528380797f4b), Pow128(hi: 0xdb68c2ca82ed2a06, lo: 0x598c67246097df1e),
    Pow128(hi: 0x892179be91d43a44, lo: 0x77f7c076bc5eeb73), Pow128(hi: 0xab69d82e364948d5, lo: 0x95f5b0946b76a64f),
    Pow128(hi: 0xd6444e39c3db9b0a, lo: 0x7b731cb986544fe3), Pow128(hi: 0x85eab0e41a6940e6, lo: 0x0d27f1f3f3f4b1ee),
    Pow128(hi: 0xa7655d1d21039120, lo: 0x9071ee70f0f1de6a), Pow128(hi: 0xd13eb46469447568, lo: 0xb48e6a0d2d2e5604),
]
// END GENERATED Pow10Tab
