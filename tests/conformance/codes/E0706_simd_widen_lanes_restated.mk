// verdict: reject E0706
// note: std.simd's WidenLo in a generic over bare lane type parameters, 2 lanes to 1: its clause (M * 2 == N && M >= 2 && SizeOf[U]() == 2 * SizeOf[T]()) reads the caller's own type parameters, so the caller is asked to restate it, though its M >= 2 part is false for every U
import std.simd

W[T: simd.Lane, U: simd.Lane](v: simd.Vec[T, 2]): simd.Vec[U, 1] {
    return v.WidenLo[U, 1]() // <- E0706
}

export Main(sys: &System): int {
    w := W[u8, u16](simd.Vec[u8, 2].Splat(Cast[u8](sys.args.Len())))
    _ = w
    return 0
}
