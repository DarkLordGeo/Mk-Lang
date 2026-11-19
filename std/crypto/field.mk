// Why: X25519 and Ed25519 both compute in GF(2^255 - 19), and every
// operation must take the same time whatever the values.
//
// What: Fe with FeAdd, FeSub, FeMul, FeSquare, FeInvert, FeSqrtRatio,
// FeBytes, FeFromBytes and FeSelect.
//
// How: GF(2^255 - 19), the field of X25519 and Ed25519 (RFC 7748 section 4.1,
// RFC 8032 section 5.1), in radix 2^51 with five u64 limbs, after Go's
// crypto/internal/fips140/edwards25519/field (fe.go, fe_generic.go), which is
// fiat-crypto's representation; Zig's std/crypto/25519/field.zig is the same.
// Every function here takes time independent of the limbs' values: no branch
// and no index depends on them, only +%, -%, *%, shifts, masks, raw.Mul128
// (one mulq) and raw.AddCarry64 (an add and an adc). The bounds below are
// what makes every product fit 128 bits and every sum fit its word; they are
// Go's and are checked by TestSTDCRYPTO_FieldAndX25519MatchModels against
// math/big, and the column form against the split form by tests_field.mk's
// blocks, at the bounds.
//
// Representation: a value is l0 + l1 2^51 + l2 2^102 + l3 2^153 + l4 2^204.
// Every function returns limbs below 2^51 + 2^18 ("lightly reduced") and
// accepts limbs below 2^54; only FeBytes gives the canonical value.

import std.raw

// Mask51 is 2^51 - 1.
const Mask51: u64 = 0x7FFFFFFFFFFFF

// Fe is a field element, five limbs of 51 bits (see the file comment).
struct Fe {
    l0: u64
    l1: u64
    l2: u64
    l3: u64
    l4: u64
}

const FeZero: Fe = Fe(l0: 0, l1: 0, l2: 0, l3: 0, l4: 0)
const FeOne: Fe = Fe(l0: 1, l1: 0, l2: 0, l3: 0, l4: 0)

// Col is one column of a product, a sum of 128-bit products as one 128-bit
// value hi 2^64 + lo (Go's fe_generic.go uint128), each product added by
// raw.AddCarry64: an add and an adc on x86_64, no branch in any build.
//
// Why a column fits, and FeWideCarry's shift of hi by 13 loses nothing:
// with every limb of the inputs below L, a column is at most the sum of
// its products' factors times L^2. Those sums are, for columns 0 to 4,
// 77, 59, 41, 23 and 5 in FeMul (1 + 4 * 19, 2 + 3 * 19, 3 + 2 * 19,
// 4 + 19, 5: a factor 19 on each product past 2^255, which is 19 mod p)
// and the same in FeSquare (1 + 38 + 38, 2 + 38 + 19, 2 + 1 + 38,
// 2 + 2 + 19, 2 + 2 + 1: the symmetric products counted once, doubled).
// FeMul and FeSquare accept limbs below 2^54, so the widest column is
// below 77 * 2^108 < 2^114.27 and hi below 2^50.27 < 2^51; then hi << 13
// keeps every bit of hi, and (hi << 13) | (lo >> 51) is the column's value
// divided by 2^51, rounded down, as the split form's hi + lo / 2^51 was,
// with the same low 51 bits: the results are the same bits. 2^54 leaves
// room: a column stays below 2^115 for limbs up to 2^54.3. The factors
// fit their words too: 38 * 2^54 < 2^59.3, a product below 2^113.3.
// What callers pass is below 2^51 + 2^18: every function here returns
// FeCarry's limbs (below 2^51 + 2^17.3), FeFromBytes masks to 51 bits,
// FeSelect returns one of its inputs' limbs, and the constants (tables.mk's
// D, D2, SqrtM1) and comb tables (BaseComb, BaseNaf) are below 2^51.
// FeAdd and FeSub carry, so no caller adds limbs without a reduction; a sum
// of up to seven lightly reduced elements, 7 (2^51 + 2^18) < 2^54, would
// still be a valid input.
struct Col {
    lo: u64
    hi: u64
}

// MulW is the 128-bit product a * b (poly1305.mk).
MulW(a: u64, b: u64): (hi: u64, lo: u64) {
    return raw.Mul128(a, b)
}

// MulC is the column holding a * b.
MulC(a: u64, b: u64): Col {
    p := raw.Mul128(a, b)
    return Col(lo: p.lo, hi: p.hi)
}

// AddMulC is v + a * b, the low words added by raw.AddCarry64 and its
// carry into the high words' sum; Col's comment bounds the column, so the
// high word never wraps.
AddMulC(v: Col, a: u64, b: u64): Col {
    p := raw.Mul128(a, b)
    r := raw.AddCarry64(v.lo, p.lo, 0)
    return Col(lo: r.sum, hi: v.hi +% p.hi +% r.carry)
}

// FeCarry reduces limbs below 2^64 to limbs below 2^51 + 2^18: each limb's
// bits above 51 move up one limb, and the top limb's come back times 19
// (2^255 = 19 mod p).
FeCarry(a: Fe): Fe {
    c0 := a.l0 >> 51
    c1 := a.l1 >> 51
    c2 := a.l2 >> 51
    c3 := a.l3 >> 51
    c4 := a.l4 >> 51
    return Fe(l0: (a.l0 & Mask51) +% c4 *% 19, l1: (a.l1 & Mask51) +% c0, l2: (a.l2 & Mask51) +% c1,
        l3: (a.l3 & Mask51) +% c2, l4: (a.l4 & Mask51) +% c3)
}

FeAdd(a: Fe, b: Fe): Fe {
    return FeCarry(Fe(l0: a.l0 +% b.l0, l1: a.l1 +% b.l1, l2: a.l2 +% b.l2, l3: a.l3 +% b.l3, l4: a.l4 +% b.l4))
}

// FeSub is a - b, computed as a + 2p - b so no limb goes below zero (b's
// limbs are below 2^51 + 2^18, 2p's are 2^52 - 38 and 2^52 - 2).
FeSub(a: Fe, b: Fe): Fe {
    return FeCarry(Fe(l0: (a.l0 +% 0xFFFFFFFFFFFDA) -% b.l0, l1: (a.l1 +% 0xFFFFFFFFFFFFE) -% b.l1,
        l2: (a.l2 +% 0xFFFFFFFFFFFFE) -% b.l2, l3: (a.l3 +% 0xFFFFFFFFFFFFE) -% b.l3,
        l4: (a.l4 +% 0xFFFFFFFFFFFFE) -% b.l4))
}

FeNeg(a: Fe): Fe {
    return FeSub(FeZero, a)
}

// FeMul is a * b, for limbs below 2^54 (Col's comment bounds the columns).
FeMul(a: Fe, b: Fe): Fe {
    a1 := a.l1 *% 19
    a2 := a.l2 *% 19
    a3 := a.l3 *% 19
    a4 := a.l4 *% 19
    r0 := AddMulC(AddMulC(AddMulC(AddMulC(MulC(a.l0, b.l0), a1, b.l4), a2, b.l3), a3, b.l2), a4, b.l1)
    r1 := AddMulC(AddMulC(AddMulC(AddMulC(MulC(a.l0, b.l1), a.l1, b.l0), a2, b.l4), a3, b.l3), a4, b.l2)
    r2 := AddMulC(AddMulC(AddMulC(AddMulC(MulC(a.l0, b.l2), a.l1, b.l1), a.l2, b.l0), a3, b.l4), a4, b.l3)
    r3 := AddMulC(AddMulC(AddMulC(AddMulC(MulC(a.l0, b.l3), a.l1, b.l2), a.l2, b.l1), a.l3, b.l0), a4, b.l4)
    r4 := AddMulC(AddMulC(AddMulC(AddMulC(MulC(a.l0, b.l4), a.l1, b.l3), a.l2, b.l2), a.l3, b.l1), a.l4, b.l0)
    return FeWideCarry(r0, r1, r2, r3, r4)
}

// FeWideCarry folds five columns back to limbs: column i is c_i 2^51 +
// (lo_i mod 2^51) with c_i = hi_i 2^13 + lo_i / 2^51 (hi_i << 13 has its
// low 13 bits zero and lo_i >> 51 is below 2^13, so the or is that sum;
// Col's comment says why the shift drops nothing); c_i moves up one limb,
// the top one back times 19 (2^255 = 19 mod p), then one FeCarry. Column 4
// has no factor 19 in its products (below 5 * 2^108), so c4 is below
// 2^59.4 and c4 * 19 below 2^63.6.
FeWideCarry(r0: Col, r1: Col, r2: Col, r3: Col, r4: Col): Fe {
    c0 := (r0.hi << 13) | (r0.lo >> 51)
    c1 := (r1.hi << 13) | (r1.lo >> 51)
    c2 := (r2.hi << 13) | (r2.lo >> 51)
    c3 := (r3.hi << 13) | (r3.lo >> 51)
    c4 := (r4.hi << 13) | (r4.lo >> 51)
    return FeCarry(Fe(l0: (r0.lo & Mask51) +% c4 *% 19, l1: (r1.lo & Mask51) +% c0, l2: (r2.lo & Mask51) +% c1,
        l3: (r3.lo & Mask51) +% c2, l4: (r4.lo & Mask51) +% c3))
}

// FeSquare is a * a with the symmetric products counted once, doubled.
FeSquare(a: Fe): Fe {
    d0 := a.l0 *% 2
    d1 := a.l1 *% 2
    a1_38 := a.l1 *% 38
    a2_38 := a.l2 *% 38
    a3_38 := a.l3 *% 38
    a3_19 := a.l3 *% 19
    a4_19 := a.l4 *% 19
    r0 := AddMulC(AddMulC(MulC(a.l0, a.l0), a1_38, a.l4), a2_38, a.l3)
    r1 := AddMulC(AddMulC(MulC(d0, a.l1), a2_38, a.l4), a3_19, a.l3)
    r2 := AddMulC(AddMulC(MulC(d0, a.l2), a.l1, a.l1), a3_38, a.l4)
    r3 := AddMulC(AddMulC(MulC(d0, a.l3), d1, a.l2), a4_19, a.l4)
    r4 := AddMulC(AddMulC(MulC(d0, a.l4), d1, a.l3), a.l2, a.l2)
    return FeWideCarry(r0, r1, r2, r3, r4)
}

// FeSquareN is a squared n times; n is a constant at every call.
FeSquareN(a: Fe, n: int): Fe {
    var t := a
    for _ in 0..n {
        t = FeSquare(t)
    }
    return t
}

// FeMulSmall is a * k for k below 2^32 (121665 in X25519): each column is
// one product, below 2^(54 + 32) for limbs below 2^54.
FeMulSmall(a: Fe, k: u64): Fe {
    return FeWideCarry(MulC(a.l0, k), MulC(a.l1, k), MulC(a.l2, k), MulC(a.l3, k), MulC(a.l4, k))
}

// FeSelect is a when m is all ones and b when m is zero; m is a mask, so
// no branch.
FeSelect(a: Fe, b: Fe, m: u64): Fe {
    return Fe(l0: b.l0 ^ ((a.l0 ^ b.l0) & m), l1: b.l1 ^ ((a.l1 ^ b.l1) & m), l2: b.l2 ^ ((a.l2 ^ b.l2) & m),
        l3: b.l3 ^ ((a.l3 ^ b.l3) & m), l4: b.l4 ^ ((a.l4 ^ b.l4) & m))
}

// FeInvert is a^(p - 2) = 1 / a (0 for a = 0), by ref10's addition chain
// of 254 squarings and 11 multiplications (Go's Element.Invert).
FeInvert(a: Fe): Fe {
    z2 := FeSquare(a)                         // 2
    z9 := FeMul(FeSquareN(z2, 2), a)          // 9
    z11 := FeMul(z9, z2)                      // 11
    z5 := FeMul(FeSquare(z11), z9)            // 2^5 - 1
    z10 := FeMul(FeSquareN(z5, 5), z5)        // 2^10 - 1
    z20 := FeMul(FeSquareN(z10, 10), z10)     // 2^20 - 1
    z40 := FeMul(FeSquareN(z20, 20), z20)     // 2^40 - 1
    z50 := FeMul(FeSquareN(z40, 10), z10)     // 2^50 - 1
    z100 := FeMul(FeSquareN(z50, 50), z50)    // 2^100 - 1
    z200 := FeMul(FeSquareN(z100, 100), z100) // 2^200 - 1
    z250 := FeMul(FeSquareN(z200, 50), z50)   // 2^250 - 1
    return FeMul(FeSquareN(z250, 5), z11)     // 2^255 - 21 = p - 2
}

// FePow22523 is a^((p - 5) / 8) = a^(2^252 - 3), the exponent of the
// square root in FeSqrtRatio (Go's Element.Pow22523).
FePow22523(a: Fe): Fe {
    z2 := FeSquare(a)
    z9 := FeMul(FeSquareN(z2, 2), a)
    z11 := FeMul(z9, z2)
    z5 := FeMul(FeSquare(z11), z9) // 2^5 - 1
    z10 := FeMul(FeSquareN(z5, 5), z5)
    z20 := FeMul(FeSquareN(z10, 10), z10)
    z40 := FeMul(FeSquareN(z20, 20), z20)
    z50 := FeMul(FeSquareN(z40, 10), z10)
    z100 := FeMul(FeSquareN(z50, 50), z50)
    z200 := FeMul(FeSquareN(z100, 100), z100)
    z250 := FeMul(FeSquareN(z200, 50), z50) // 2^250 - 1
    return FeMul(FeSquareN(z250, 2), a)     // 2^252 - 3
}

// FeReduce is the canonical representative, below p: after a carry the
// value is below 2^255 + 2^18; it is at least p exactly when value + 19
// carries out of bit 255, and then 19 is added and bit 255 dropped.
FeReduce(a: Fe): Fe {
    v := FeCarry(a)
    var c := (v.l0 +% 19) >> 51
    c = (v.l1 +% c) >> 51
    c = (v.l2 +% c) >> 51
    c = (v.l3 +% c) >> 51
    c = (v.l4 +% c) >> 51
    l0 := v.l0 +% 19 *% c
    l1 := v.l1 +% (l0 >> 51)
    l2 := v.l2 +% (l1 >> 51)
    l3 := v.l3 +% (l2 >> 51)
    l4 := v.l4 +% (l3 >> 51)
    return Fe(l0: l0 & Mask51, l1: l1 & Mask51, l2: l2 & Mask51, l3: l3 & Mask51, l4: l4 & Mask51)
}

// FeBytes is the canonical 32-byte little-endian encoding (bit 255 zero).
FeBytes(a: Fe): [32]u8 {
    v := FeReduce(a)
    var out: [32]u8 = [0; 32]
    w0 := v.l0 | (v.l1 << 51)
    w1 := (v.l1 >> 13) | (v.l2 << 38)
    w2 := (v.l2 >> 26) | (v.l3 << 25)
    w3 := (v.l3 >> 39) | (v.l4 << 12)
    PutLe64(mut out[..], 0, w0)
    PutLe64(mut out[..], 8, w1)
    PutLe64(mut out[..], 16, w2)
    PutLe64(mut out[..], 24, w3)
    return out
}

// FeFromBytes reads 32 bytes little-endian, ignoring bit 255 (RFC 7748
// section 5). A value from p to 2^255 - 1 is accepted and reduced by the
// arithmetic (as RFC 7748 and Go's SetBytes).
FeFromBytes(b: &[32]u8): Fe {
    return Fe(l0: Le64(b[..], 0) & Mask51, l1: (Le64(b[..], 6) >> 3) & Mask51, l2: (Le64(b[..], 12) >> 6) & Mask51,
        l3: (Le64(b[..], 19) >> 1) & Mask51, l4: (Le64(b[..], 24) >> 12) & Mask51)
}

// FeIsNegative is the low bit of the canonical value (RFC 8032 section
// 5.1.2's sign of x), 0 or 1.
FeIsNegative(a: Fe): u64 {
    return FeReduce(a).l0 & 1
}

// FeIsZero is all ones when a = 0 mod p, else zero; no branch.
FeIsZero(a: Fe): u64 {
    v := FeReduce(a)
    x := v.l0 | v.l1 | v.l2 | v.l3 | v.l4
    return 0 -% (((x | (0 -% x)) >> 63) ^ 1)
}

// FeEq is all ones when a = b mod p, else zero.
FeEq(a: Fe, b: Fe): u64 {
    return FeIsZero(FeSub(a, b))
}

// FeAbs is a or -a, whichever is non-negative (low bit 0).
FeAbs(a: Fe): Fe {
    return FeSelect(FeNeg(a), a, 0 -% FeIsNegative(a))
}

// FeSqrtRatio is the non-negative r with v r^2 = u, and ok all ones, when
// u / v is a square; else ok is zero and r is unspecified (RFC 8032
// 5.1.3 step 3, with Go's constant-time selects): r = u v^3 (u v^7)^((p -
// 5) / 8), then times sqrt(-1) when v r^2 = -u.
FeSqrtRatio(u: Fe, v: Fe): (ok: u64, r: Fe) {
    v2 := FeSquare(v)
    uv3 := FeMul(u, FeMul(v2, v))
    uv7 := FeMul(uv3, FeSquare(v2))
    r := FeMul(uv3, FePow22523(uv7))
    check := FeMul(v, FeSquare(r))
    un := FeNeg(u)
    correct := FeEq(check, u)
    flipped := FeEq(check, un)
    flipped_i := FeEq(check, FeMul(un, SqrtM1))
    r2 := FeSelect(FeMul(r, SqrtM1), r, flipped | flipped_i)
    return (ok: correct | flipped, r: FeAbs(r2))
}
