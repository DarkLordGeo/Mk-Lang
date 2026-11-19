// verdict: reject E0706
// note: std.simd's Reinterpret in a generic over a bare lane type parameter: its size clause (M * SizeOf[U]() == N * SizeOf[T]()) reads the caller's own type parameters, so the caller restates it
import std.simd

R[T: simd.Lane, U: simd.Lane](v: simd.Vec[T, 4]): simd.Vec[U, 4] {
    return v.Reinterpret[U, 4]() // <- E0706
}

export Main(sys: &System): int {
    w := R[u8, u64](simd.Vec[u8, 4].Splat(Cast[u8](sys.args.Len())))
    return Cast[int](w.Lane(0))
}
