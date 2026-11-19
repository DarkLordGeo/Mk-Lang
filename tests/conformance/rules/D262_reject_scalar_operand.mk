// verdict: reject E0301
// rule: D-262
// note: a scalar beside a vector is E0301, fixed by Splat; a Mask is no bool, as a condition or an operand of &&
import std.simd

Scale(v: simd.Vec[f32, 4]): simd.Vec[f32, 4] {
    return v * 2.0 // <- E0301
}

Has(v: simd.Vec[u8, 16], b: simd.Vec[u8, 16]): bool {
    if v == b { // <- E0301
        return true
    }
    return false
}
