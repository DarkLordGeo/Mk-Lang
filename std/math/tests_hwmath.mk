// Why: the hardware rounding instructions must give the same bits as the
// software forms.
// What: one test of Floor, Ceil, RoundToZero, Round, RoundEven and their
// F32 forms, run by `mk test --std=std.math`. Fma's comparison is
// std/mem/tests_fma.mk's (it needs Cpu() and an unsafe block), and its own
// paths are tests_fma.mk's.
// How: calls them natively from a function whose features hold sse4.1 (the
// hosted x86_64 baseline), on edge values and 240 bit patterns from a fixed
// linear congruential sequence, against the evaluator's results in a const.

const RoundN: int = 288

// RoundInput is input i: an edge value for i < 48, else a pattern from the
// sequence, half of them in the range where rounding happens.
RoundInput(i: int): u64 {
    edges: [48]u64 = [0x0, 0x8000000000000000, 0x3fe0000000000000, 0xbfe0000000000000, 0x3fdfffffffffffff,
        0xbfdfffffffffffff, 0x3fe0000000000001, 0x3ff0000000000000, 0xbff0000000000000, 0x3ff8000000000000,
        0xbff8000000000000, 0x4004000000000000, 0xc004000000000000, 0x400c000000000000, 0xc00c000000000000,
        0x4330000000000000, 0xc330000000000000, 0x432fffffffffffff, 0xc32fffffffffffff, 0x4340000000000000,
        0x7ff0000000000000, 0xfff0000000000000, 0x7ff8000000000000, 0xfff8000000000001, 0x7ff0000000000001,
        0x0000000000000001, 0x8000000000000001, 0x0010000000000000, 0x7fefffffffffffff, 0xffefffffffffffff,
        0x3fefffffffffffff, 0xbfefffffffffffff, 0x4000000000000001, 0xc000000000000001, 0x3fd0000000000000,
        0xbfd0000000000000, 0x4122000000000000, 0xc122000000000001, 0x3f50624dd2f1a9fc, 0x41dfffffffc00000,
        0xc1e0000000000000, 0x4415af1d78b58c40, 0x3fe8000000000000, 0xbfe8000000000000, 0x3fc999999999999a,
        0x4024000000000000, 0x4023ffffffffffff, 0xc024000000000001]
    if i < 48 {
        return edges[i]
    }
    var s: u64 = 0x9e3779b97f4a7c15
    for _ in 0..i {
        s = s *% 6364136223846793005 +% 1442695040888963407
    }
    if i % 2 == 0 {
        // An exponent from 2^-2 to 2^60, random sign and mantissa.
        e := (s >> 32) % 63 + 1021
        return s & 0x800fffffffffffff | e << 52
    }
    return s
}

// RoundResults is each input's five rounding results, as bits.
RoundResults(): [1440]u64 {
    var out: [1440]u64 = [0; 1440]
    for i in 0..RoundN {
        x := f64.FromBits(RoundInput(i))
        out[5 * i] = Floor(x).Bits()
        out[5 * i + 1] = Ceil(x).Bits()
        out[5 * i + 2] = RoundToZero(x).Bits()
        out[5 * i + 3] = Round(x).Bits()
        out[5 * i + 4] = RoundEven(x).Bits()
    }
    return out
}

// RoundResults32 is RoundResults over each input narrowed to f32.
RoundResults32(): [1440]u32 {
    var out: [1440]u32 = [0; 1440]
    for i in 0..RoundN {
        x := Narrow(f64.FromBits(RoundInput(i)))
        out[5 * i] = FloorF32(x).Bits()
        out[5 * i + 1] = CeilF32(x).Bits()
        out[5 * i + 2] = RoundToZeroF32(x).Bits()
        out[5 * i + 3] = RoundF32(x).Bits()
        out[5 * i + 4] = RoundEvenF32(x).Bits()
    }
    return out
}

const SwRounds: [1440]u64 = RoundResults()
const SwRounds32: [1440]u32 = RoundResults32()

test "hardware rounding is bit-identical to the software forms" {
    hw := RoundResults()
    hw32 := RoundResults32()
    for i in 0..1440 {
        Assert(hw[i] == SwRounds[i])
        Assert(hw32[i] == SwRounds32[i])
    }
}
