// verdict: reject E0706
// note: std.simd's NarrowSat inside `if U is u8` in a generic over a bare lane type parameter: the narrowed U passes check's lane rule, and the clause 2 * SizeOf[U]() == SizeOf[T]() reads the caller's own type parameter, so the caller restates it
import std.simd

N[U: simd.Lane](a: simd.Vec[u32, 4], b: simd.Vec[u32, 4]): int {
    if U is u8 {
        r := a.NarrowSat[U, 8](b) // <- E0706
        return Cast[int](r.Lane(0))
    }
    return -1
}

export Main(sys: &System): int {
    n := Cast[u32](sys.args.Len())
    return N[u8](simd.Vec[u32, 4].Splat(300 + n), simd.Vec[u32, 4].Splat(7))
}
