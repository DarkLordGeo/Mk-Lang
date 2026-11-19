// Why: math.Fma and FmaF32 lower to vfmadd under a cpu clause that names fma
// (internal/codegen/hwmath.go); the hardware result must match the software
// form bit for bit.
// What: one test comparing both forms over 160 input triples.
// How: the evaluator computes the software form for the same inputs in a
// const; the native run needs a CPU with fma (Cpu()). The inputs are edge
// values and bit patterns from a fixed linear congruential sequence
// (std/math/tests_hwmath.mk's). Run by `mk test --std=std.mem`;
// it lives in std.mem, which may hold the unsafe block Cpu()'s dispatch
// needs and import std.math and std.simd.

import std.math
import std.simd

// FmaInput is input i of 160.
FmaInput(i: int): f64 {
    edges: [16]u64 = [0x0, 0x8000000000000000, 0x3ff0000000000000, 0xbff0000000000000, 0x7ff0000000000000,
        0xfff0000000000000, 0x7ff8000000000000, 0x0000000000000001, 0x7fefffffffffffff, 0x3cb0000000000000,
        0x3ff0000000000001, 0xbfefffffffffffff, 0x4340000000000000, 0x0010000000000000, 0x3fe0000000000000,
        0xc000000000000000]
    if i < 16 {
        return f64.FromBits(edges[i])
    }
    var s: u64 = 0x9e3779b97f4a7c15
    for _ in 0..i {
        s = s *% 6364136223846793005 +% 1442695040888963407
    }
    e := (s >> 32) % 200 + 923
    return f64.FromBits(s & 0x800fffffffffffff | e << 52)
}

// HighF32 is an f32 from x's high 32 bits: a pattern of the same sign
// and a nearby exponent range.
HighF32(x: f64): f32 {
    return f32.FromBits(Trunc[u32](x.Bits() >> 32))
}

// FmaResults is Fma and FmaF32 over 160 input triples, as bits.
FmaResults(): [320]u64 {
    var out: [320]u64 = [0; 320]
    for i in 0..160 {
        x := FmaInput(i)
        y := FmaInput((i * 7 + 3) % 160)
        z := FmaInput((i * 13 + 5) % 160)
        out[2 * i] = math.Fma(x, y, z).Bits()
        out[2 * i + 1] = u64(math.FmaF32(HighF32(x), HighF32(y), HighF32(z)).Bits())
    }
    return out
}

// FmaResultsHw is FmaResults compiled with fma: each Fma call is vfmadd.
FmaResultsHw(): [320]u64 cpu "fma" {
    var out: [320]u64 = [0; 320]
    for i in 0..160 {
        x := FmaInput(i)
        y := FmaInput((i * 7 + 3) % 160)
        z := FmaInput((i * 13 + 5) % 160)
        out[2 * i] = math.Fma(x, y, z).Bits()
        out[2 * i + 1] = u64(math.FmaF32(HighF32(x), HighF32(y), HighF32(z)).Bits())
    }
    return out
}

const SwFmas: [320]u64 = FmaResults()

test "hardware fused multiply-add is bit-identical to the software form" {
    sw := FmaResults()
    for i in 0..320 {
        Assert(sw[i] == SwFmas[i])
    }
    if simd.Cpu().Has("fma") {
        var hw: [320]u64 = [0; 320]
        unsafe "Cpu() reports fma, which FmaResultsHw's clause names" {
            hw = FmaResultsHw()
        }
        for i in 0..320 {
            Assert(hw[i] == SwFmas[i])
        }
    }
}
