// verdict: reject E1104
// note: no Mask comes out of asm, which could leave a lane neither all zeros nor all ones
import k
import std.simd

All(): simd.Mask[u8, 16] {
    return k.Ones()
}
