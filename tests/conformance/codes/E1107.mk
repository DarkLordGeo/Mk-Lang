// verdict: reject E1107
// note: a vector crosses no C signature, since its place there depends on the callee's features
import std.simd

export extern Total(v: simd.Vec[f32, 4]): f32 { // <- E1107
    return v.ReduceSum()
}
