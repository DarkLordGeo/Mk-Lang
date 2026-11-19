// Why: the field files translated from fiat-crypto call Go's math/bits
// add and subtract with carry, which Mk has to supply.
//
// What: FiatAdd64, FiatSub64 and FiatSubBorrow.
//
// How: the word operations fiat-crypto's generated code takes from Go's
// math/bits, for the translated field files (fiat_p256.mk, fiat_p256n.mk,
// fiat_p384.mk, fiat_p384n.mk): an add and a subtract with a carry in and
// out, through raw.AddCarry64 and raw.SubBorrow64, which lower to
// LLVM's overflow intrinsics: no compare and no branch in any build, so
// constant time, and a chain of them is one adc or sbb chain on x86_64.
// math/bits' branch-free formula gives the same values for a carry in of
// 0 or 1, but LLVM does not turn it into adc: in x86_64 release
// disassembly a P-256 field product is about three times as many
// instructions with it.
// One place keeps the formula: FiatSubBorrow, for a borrow a branch reads.

import std.raw

// FiatAdd64 is x + y + c (c is 0 or 1) and its carry out (math/bits.Add64).
FiatAdd64(x: u64, y: u64, c: u64): (s: u64, c: u64) {
    r := raw.AddCarry64(x, y, c)
    return (s: r.sum, c: r.carry)
}

// FiatSub64 is x - y - b (b is 0 or 1) and its borrow out (math/bits.Sub64).
FiatSub64(x: u64, y: u64, b: u64): (d: u64, b: u64) {
    r := raw.SubBorrow64(x, y, b)
    return (d: r.diff, b: r.borrow)
}

// FiatSubBorrow is FiatSub64's borrow alone, by math/bits.Sub64's formula,
// for a chain whose last borrow decides a branch: ec_*.mk's Below, the
// range check of a scalar or a coordinate, whose answer is public. LLVM
// reads a chain of raw.SubBorrow64's flags as a comparison of the two
// numbers and splits the branch on it into one jump per limb, the first on
// the top limb of a secret scalar (EcSecret.FromBytes, EcdhShared,
// EcdsaSign); the formula's bit is arithmetic, and the branch stays one
// test of the final borrow.
FiatSubBorrow(x: u64, y: u64, b: u64): u64 {
    d := x -% y -% b
    return ((~x & y) | (~(x ^ y) & d)) >> 63
}
