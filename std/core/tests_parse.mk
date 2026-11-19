// Why: number parsing must round-trip and refuse what is not a number.
// What: test blocks for str.ParseInt, ParseFloat and the Decimal fallback.
// How: run by mk test --std=std.core with contracts on and the counting heap;
// seeded values (ParseMix) check the round trip.

import std.raw

// ParseIntIs reports whether s parses to want.
ParseIntIs(s: str, want: i64): bool {
    v := s.ParseInt() else {
        return false
    }
    return v == want
}

ParseIntFails(s: str): bool {
    _ = s.ParseInt() else {
        return true
    }
    return false
}

ParseFloatBits(s: str, want: u64): bool {
    v := s.ParseFloat() else {
        return false
    }
    return raw.F64Bits(v) == want
}

ParseFloatFails(s: str): bool {
    _ = s.ParseFloat() else {
        return true
    }
    return false
}

// ParseMix is splitmix64's output function.
ParseMix(s: u64): u64 {
    var z := s
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

test "ParseInt limits and signs" {
    Assert(ParseIntIs("0", 0))
    Assert(ParseIntIs("-0", 0))
    Assert(ParseIntIs("+17", 17))
    Assert(ParseIntIs("-17", -17))
    Assert(ParseIntIs("007", 7))
    Assert(ParseIntIs("9223372036854775807", 9223372036854775807))
    Assert(ParseIntIs("-9223372036854775808", -9223372036854775807 - 1))
    Assert(ParseIntIs("000000000000000000000000000042", 42))
    Assert(ParseIntFails("9223372036854775808"))
    Assert(ParseIntFails("-9223372036854775809"))
    Assert(ParseIntFails("99999999999999999999"))
    Assert(ParseIntFails(""))
    Assert(ParseIntFails("+"))
    Assert(ParseIntFails("-"))
    Assert(ParseIntFails(" 1"))
    Assert(ParseIntFails("1 "))
    Assert(ParseIntFails("1_000"))
    Assert(ParseIntFails("0x10"))
    Assert(ParseIntFails("--1"))
    Assert(ParseIntFails("1.0"))
}

test "ParseFloat inf, nan, signs and the grammar" {
    Assert(ParseFloatBits("inf", 0x7FF0000000000000))
    Assert(ParseFloatBits("+Infinity", 0x7FF0000000000000))
    Assert(ParseFloatBits("-INF", 0xFFF0000000000000))
    Assert(ParseFloatBits("NaN", 0x7FF8000000000000))
    Assert(ParseFloatBits("nan", 0x7FF8000000000000))
    Assert(ParseFloatBits("-0", 0x8000000000000000))
    Assert(ParseFloatBits("-1e-400", 0x8000000000000000))
    Assert(ParseFloatBits("5e-324", 1))
    Assert(ParseFloatBits("1.7976931348623157e308", 0x7FEFFFFFFFFFFFFF))
    Assert(ParseFloatBits(".5", 0x3FE0000000000000))
    Assert(ParseFloatBits("5.", 0x4014000000000000))
    Assert(ParseFloatFails("1e400"))
    Assert(ParseFloatFails("-nan"))
    Assert(ParseFloatFails("0x1p3"))
    Assert(ParseFloatFails("1_0"))
    Assert(ParseFloatFails(" 1"))
    Assert(ParseFloatFails("."))
    Assert(ParseFloatFails("1e"))
    Assert(ParseFloatFails("infinit"))
    Assert(ParseFloatFails(""))
}

test "ParseFloat round-trips formatted output" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var s: u64 = 7
    var checked := 0
    for i in 0..20_000 {
        s = s +% 0x9E3779B97F4A7C15
        b := ParseMix(s)
        x := raw.F64From(b)
        if x != x {
            continue
        }
        if i % 2 == 0 {
            t := try ar.Fmt("{x}", (x))
            Assert(ParseFloatBits(t, b))
        } else {
            // The 17-digit fixed form is exact enough to round-trip only
            // for magnitudes it can hold; the shortest form always is.
            y := raw.F64From((b & 0x000FFFFFFFFFFFFF) | 0x3FF0000000000000)
            t := try ar.Fmt("{y:.17}", (y))
            Assert(ParseFloatBits(t, raw.F64Bits(y)))
        }
        checked += 1
        if i % 1000 == 0 {
            ar.Reset()
        }
    }
    Assert(checked > 19_000)
}

test "ParseInt round-trips formatted output" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var s: u64 = 11
    for i in 0..20_000 {
        s = s +% 0x9E3779B97F4A7C15
        v := Cast[i64](ParseMix(s) >> Cast[u64](i % 63 + 1))
        w := -v
        t := try ar.Fmt("{v} {w}", (v, w))
        sp := t.Find(" ").Must()
        Assert(ParseIntIs(t[0..sp], v))
        Assert(ParseIntIs(t[sp + 1..], w))
        if i % 1000 == 0 {
            ar.Reset()
        }
    }
}

// The Decimal places the point by every significant
// digit, not the 800 it keeps, and an exponent offsets any number of zeros.
// Go 1.27's strconv gives 9.007199254740992e-102 and 0 for the first two.
test "ParseFloat is correctly rounded on very long text" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var a := Text.New(&ar)
    try a.Push("9007199254740993")
    for _ in 0..900 {
        try a.Push("0")
    }
    try a.Push("1e-901")
    Assert(ParseFloatBits(a.Str(), 0x4340000000000001))
    var b := Text.New(&ar)
    try b.Push("0.")
    for _ in 0..100_000 {
        try b.Push("0")
    }
    try b.Push("1e100005")
    Assert(ParseFloatBits(b.Str(), 0x40C3880000000000))
    var c := Text.New(&ar)
    try c.Push("-1")
    for _ in 0..100_000 {
        try c.Push("0")
    }
    try c.Push("e-100000")
    Assert(ParseFloatBits(c.Str(), 0xBFF0000000000000))
    Assert(ParseFloatFails("1e99999999999999999999"))
    Assert(ParseFloatBits("-1e-99999999999999999999", 0x8000000000000000))
}
