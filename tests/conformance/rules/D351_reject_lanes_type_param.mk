// verdict: reject E0703
// rule: D-351
// note: simd.Lanes[T]() is a const argument only for a concrete T; of a type parameter it is a run-time value
import std.simd

Splat[T: simd.Lane](x: T): simd.Vec[T, simd.Lanes[T]()] { // <- E0703
    return simd.Vec[T, simd.Lanes[T]()].Splat(x) // <- E0703
}
