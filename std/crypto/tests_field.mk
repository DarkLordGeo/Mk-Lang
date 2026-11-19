// Why: field.mk keeps each 128-bit column whole (Col, MulC, AddMulC,
// FeWideCarry), and FeMul, FeSquare and FeMulSmall must give the limbs of the
// form that keeps the column split at bit 51, on every input.
// What: test blocks ("mk test --std=std.crypto -run Columns") with the split
// form kept here as the oracle, compared limb for limb, not mod p.
// How: both forms take a column's value divided by 2^51 (rounded down, mod
// 2^64) and its low 51 bits, so they agree inside FeMul's bound (limbs below
// 2^54) and past it. The inputs: all five limbs at each boundary value (0, 1,
// 2^51 - 1, 2^51, 2^51 + 2^18, 2^52, 2^54 - 1, 2^63, 2^64 - 1) against each
// other, mixed across limbs, and seeded random limbs. The evaluator runs
// TxFeColumns in std/std-crypto_carry_test.go's program, not in a const here.

import std.raw

// TxFeCol is the split column: hi 2^51 + lo.
struct TxFeCol {
    lo: u64
    hi: u64
}

TxFeMulC(a: u64, b: u64): TxFeCol {
    p := raw.Mul128(a, b)
    return TxFeCol(lo: p.lo & Mask51, hi: (p.hi << 13) | (p.lo >> 51))
}

TxFeAddMulC(v: TxFeCol, a: u64, b: u64): TxFeCol {
    p := raw.Mul128(a, b)
    return TxFeCol(lo: v.lo +% (p.lo & Mask51), hi: v.hi +% ((p.hi << 13) | (p.lo >> 51)))
}

TxFeWide(r0: TxFeCol, r1: TxFeCol, r2: TxFeCol, r3: TxFeCol, r4: TxFeCol): Fe {
    c0 := r0.hi +% (r0.lo >> 51)
    c1 := r1.hi +% (r1.lo >> 51)
    c2 := r2.hi +% (r2.lo >> 51)
    c3 := r3.hi +% (r3.lo >> 51)
    c4 := r4.hi +% (r4.lo >> 51)
    return FeCarry(Fe(l0: (r0.lo & Mask51) +% c4 *% 19, l1: (r1.lo & Mask51) +% c0, l2: (r2.lo & Mask51) +% c1,
        l3: (r3.lo & Mask51) +% c2, l4: (r4.lo & Mask51) +% c3))
}

TxFeMul(a: Fe, b: Fe): Fe {
    a1 := a.l1 *% 19
    a2 := a.l2 *% 19
    a3 := a.l3 *% 19
    a4 := a.l4 *% 19
    r0 := TxFeAddMulC(TxFeAddMulC(TxFeAddMulC(TxFeAddMulC(TxFeMulC(a.l0, b.l0), a1, b.l4), a2, b.l3), a3, b.l2), a4, b.l1)
    r1 := TxFeAddMulC(TxFeAddMulC(TxFeAddMulC(TxFeAddMulC(TxFeMulC(a.l0, b.l1), a.l1, b.l0), a2, b.l4), a3, b.l3), a4, b.l2)
    r2 := TxFeAddMulC(TxFeAddMulC(TxFeAddMulC(TxFeAddMulC(TxFeMulC(a.l0, b.l2), a.l1, b.l1), a.l2, b.l0), a3, b.l4), a4, b.l3)
    r3 := TxFeAddMulC(TxFeAddMulC(TxFeAddMulC(TxFeAddMulC(TxFeMulC(a.l0, b.l3), a.l1, b.l2), a.l2, b.l1), a.l3, b.l0), a4, b.l4)
    r4 := TxFeAddMulC(TxFeAddMulC(TxFeAddMulC(TxFeAddMulC(TxFeMulC(a.l0, b.l4), a.l1, b.l3), a.l2, b.l2), a.l3, b.l1), a.l4, b.l0)
    return TxFeWide(r0, r1, r2, r3, r4)
}

TxFeSquare(a: Fe): Fe {
    d0 := a.l0 *% 2
    d1 := a.l1 *% 2
    a1_38 := a.l1 *% 38
    a2_38 := a.l2 *% 38
    a3_38 := a.l3 *% 38
    a3_19 := a.l3 *% 19
    a4_19 := a.l4 *% 19
    r0 := TxFeAddMulC(TxFeAddMulC(TxFeMulC(a.l0, a.l0), a1_38, a.l4), a2_38, a.l3)
    r1 := TxFeAddMulC(TxFeAddMulC(TxFeMulC(d0, a.l1), a2_38, a.l4), a3_19, a.l3)
    r2 := TxFeAddMulC(TxFeAddMulC(TxFeMulC(d0, a.l2), a.l1, a.l1), a3_38, a.l4)
    r3 := TxFeAddMulC(TxFeAddMulC(TxFeMulC(d0, a.l3), d1, a.l2), a4_19, a.l4)
    r4 := TxFeAddMulC(TxFeAddMulC(TxFeMulC(d0, a.l4), d1, a.l3), a.l2, a.l2)
    return TxFeWide(r0, r1, r2, r3, r4)
}

TxFeMulSmall(a: Fe, k: u64): Fe {
    return TxFeWide(TxFeMulC(a.l0, k), TxFeMulC(a.l1, k), TxFeMulC(a.l2, k), TxFeMulC(a.l3, k), TxFeMulC(a.l4, k))
}

// TxFeSame reports whether a and b have the same limbs.
TxFeSame(a: Fe, b: Fe): bool {
    return a.l0 == b.l0 && a.l1 == b.l1 && a.l2 == b.l2 && a.l3 == b.l3 && a.l4 == b.l4
}

// TxFeOne reports whether the column form and the split form agree on
// a * b, a^2, a * 121665 and a * (2^32 - 1).
TxFeOne(a: Fe, b: Fe): bool {
    return TxFeSame(FeMul(a, b), TxFeMul(a, b)) && TxFeSame(FeSquare(a), TxFeSquare(a)) &&
        TxFeSame(FeMulSmall(a, 121665), TxFeMulSmall(a, 121665)) && TxFeSame(FeMulSmall(a, 0xFFFFFFFF), TxFeMulSmall(a, 0xFFFFFFFF))
}

// TxFeEdges are the boundary limbs: 0, 1, 2^51 - 1, 2^51, 2^51 + 2^18
// (above every function's output), 2^52, 2^54 - 1 (FeMul's bound), 2^63
// and 2^64 - 1.
const TxFeEdges: [9]u64 = [0, 1, 0x7FFFFFFFFFFFF, 0x8000000000000, 0x8000000040000, 0x10000000000000, 0x3FFFFFFFFFFFFF,
    0x8000000000000000, 0xFFFFFFFFFFFFFFFF]

// TxFeAt is the element whose limb k is TxFeEdges[(i + k s) mod 9].
TxFeAt(i: int, s: int): Fe {
    return Fe(l0: TxFeEdges[i % 9], l1: TxFeEdges[(i + s) % 9], l2: TxFeEdges[(i + 2 * s) % 9],
        l3: TxFeEdges[(i + 3 * s) % 9], l4: TxFeEdges[(i + 4 * s) % 9])
}

// TxFeRand is the element of seeded random limbs from n, each and-ed
// with m.
TxFeRand(n: u64, m: u64): Fe {
    return Fe(l0: TxCaMix(n) & m, l1: TxCaMix(n +% 1) & m, l2: TxCaMix(n +% 2) & m, l3: TxCaMix(n +% 3) & m,
        l4: TxCaMix(n +% 4) & m)
}

// TxFeColumns is TxFeOne over rows from to to - 1 of the 9 by 9 grid of
// boundary pairs (two pairs each) and n seeded pairs of each width; the
// native run takes every row and 2,000, the evaluator three rows a const.
TxFeColumns(from: int, to: int, n: int): bool {
    for i in from..to {
        for j in 0..9 {
            if !TxFeOne(TxFeAt(i, 0), TxFeAt(j, 0)) || !TxFeOne(TxFeAt(i, 1), TxFeAt(j, 2)) {
                return false
            }
        }
    }
    for k in 0..n {
        s := Cast[u64](k) *% 10
        if !TxFeOne(TxFeRand(s, 0xFFFFFFFFFFFFFFFF), TxFeRand(s +% 5, 0xFFFFFFFFFFFFFFFF)) ||
            !TxFeOne(TxFeRand(s, 0x3FFFFFFFFFFFFF), TxFeRand(s +% 5, 0x3FFFFFFFFFFFFF)) {
            return false
        }
    }
    return true
}

test "Columns: FeMul, FeSquare and FeMulSmall give the split form's limbs at the bounds and past them" {
    Assert(TxFeColumns(0, 9, 2000))
}
