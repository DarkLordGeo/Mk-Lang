// verdict: ok
// rule: D-351
// note: simd.Lanes of a concrete type is a const argument, and of a type parameter a run-time value
import std.simd

Splat(x: f32): simd.Vec[f32, simd.Lanes[f32]()] {
    return simd.Vec[f32, simd.Lanes[f32]()].Splat(x)
}

Width[T: simd.Lane](): int {
    return simd.Lanes[T]()
}
