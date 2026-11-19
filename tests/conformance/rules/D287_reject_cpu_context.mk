// verdict: reject E1101
// rule: D-287
// note: a context with fewer CPU features than the callee's clause (sse4.2 below avx2, the hosted baseline below avx512bw) calls it only in unsafe
import std.simd

Double8(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] cpu "avx2" {
    return v + v
}

Wide(v: simd.Vec[u8, 64]): simd.Vec[u8, 64] cpu "avx512bw" {
    return v +% v
}

Narrow(v: simd.Vec[f32, 8]): simd.Vec[f32, 8] cpu "sse4.2" {
    return Double8(v) // <- E1101
}

Hosted(v: simd.Vec[u8, 64]): simd.Vec[u8, 64] {
    return Wide(v) // <- E1101
}

Upward(v: simd.Vec[u8, 64]): simd.Vec[u8, 64] cpu "avx2" {
    return Wide(v) // <- E1101
}
