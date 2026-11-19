// verdict: reject E0706
// note: std.simd's WidenLo in a generic over bare lane type parameters, 8 lanes to 2: its clause (M * 2 == N && M >= 2 && SizeOf[U]() == 2 * SizeOf[T]()) reads the caller's own type parameters, so the caller restates it
import std.simd

W[T: simd.Lane, U: simd.Lane](v: simd.Vec[T, 8]): simd.Vec[U, 2] {
    return v.WidenLo[U, 2]() // <- E0706
}

export Main(sys: &System): int {
    w := W[u8, u16](simd.Vec[u8, 8].Splat(Cast[u8](sys.args.Len())))
    return Cast[int](w.Lane(0))
}
