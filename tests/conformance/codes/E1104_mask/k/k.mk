import std.simd

export Ones(): simd.Mask[u8, 16] {
    var m := simd.Mask[u8, 16].FromBits(0)
    unsafe "pcmpeqb of a register with itself sets every lane" {
        asm("pcmpeqb {m}, {m}", m: inout(xmm_reg) m) // <- E1104
    }
    return m
}
