// Why: the field code's word carries go through raw.AddCarry64 and
// raw.SubBorrow64, and must give math/bits' branch-free formula's words.
// What: test blocks for fiat.mk's FiatAdd64, FiatSub64 and FiatSubBorrow and
// poly1305.mk's Add64 and Sub64 ("mk test --std=std.crypto -run Carries").
// How: boundary words with each carry in and seeded random words, against Go's
// math/bits.Add64 and Sub64 (the oracle of fiat-crypto's Go output), natively
// in dev and release. The evaluator runs TxCaFiat in
// std/std-crypto_carry_test.go's program, not in a const here: a const of a
// tests_ file is evaluated in every build that imports std.crypto.

// TxCaOldAdd is math/bits.Add64's formula: x + y + c and its carry out.
TxCaOldAdd(x: u64, y: u64, c: u64): (s: u64, c: u64) {
    s := x +% y +% c
    return (s: s, c: ((x & y) | ((x | y) & ~s)) >> 63)
}

// TxCaOldSub is math/bits.Sub64's formula: x - y - b and its borrow out.
TxCaOldSub(x: u64, y: u64, b: u64): (d: u64, b: u64) {
    d := x -% y -% b
    return (d: d, b: ((~x & y) | (~(x ^ y) & d)) >> 63)
}

// TxCaEdges are the words where a carry or a borrow turns: 0, 1, 2, 2^63
// and its neighbours, 2^64 - 2 and 2^64 - 1.
const TxCaEdges: [8]u64 = [0, 1, 2, 0x7FFFFFFFFFFFFFFF, 0x8000000000000000, 0x8000000000000001, 0xFFFFFFFFFFFFFFFE,
    0xFFFFFFFFFFFFFFFF]

// TxCaMix is splitmix64's output function (Steele, Lea and Flood, 2014)
// of s: a fixed sequence of words, so a failure repeats.
TxCaMix(s: u64): u64 {
    var z := s +% 0x9E3779B97F4A7C15
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

// TxCaFiatOne reports whether FiatAdd64 and FiatSub64, and Poly1305's
// Add64 and Sub64, give the formula's sum, difference, carry and borrow for
// x, y and a carry in c, and FiatSubBorrow FiatSub64's borrow.
TxCaFiatOne(x: u64, y: u64, c: u64): bool {
    a := FiatAdd64(x, y, c)
    oa := TxCaOldAdd(x, y, c)
    s := FiatSub64(x, y, c)
    os := TxCaOldSub(x, y, c)
    pa := Add64(x, y, c)
    ps := Sub64(x, y, c)
    return a.s == oa.s && a.c == oa.c && s.d == os.d && s.b == os.b && FiatSubBorrow(x, y, c) == s.b && pa.s == oa.s &&
        pa.c == oa.c && ps.d == os.d && ps.b == os.b
}

// TxCaFiat is TxCaFiatOne over every pair of edge words with a carry in
// of 0 and of 1, then 2,000 seeded random triples, and four values known
// by hand.
TxCaFiat(): bool {
    for i in 0..8 {
        for j in 0..8 {
            for c in 0..2 {
                if !TxCaFiatOne(TxCaEdges[i], TxCaEdges[j], Cast[u64](c)) {
                    return false
                }
            }
        }
    }
    for k in 0..2000 {
        n := Cast[u64](k) *% 3
        if !TxCaFiatOne(TxCaMix(n), TxCaMix(n +% 1), TxCaMix(n +% 2) & 1) {
            return false
        }
    }
    a := FiatAdd64(0xFFFFFFFFFFFFFFFF, 0, 1)
    b := FiatAdd64(0xFFFFFFFFFFFFFFFF, 0xFFFFFFFFFFFFFFFF, 1)
    s := FiatSub64(0, 0, 1)
    d := FiatSub64(5, 3, 1)
    return a.s == 0 && a.c == 1 && b.s == 0xFFFFFFFFFFFFFFFF && b.c == 1 && s.d == 0xFFFFFFFFFFFFFFFF && s.b == 1 &&
        d.d == 1 && d.b == 0
}

test "Carries: FiatAdd64, FiatSub64, FiatSubBorrow and Poly1305's Add64 and Sub64 match math/bits' formula on edge and random words" {
    Assert(TxCaFiat())
}
