// Source: NarrowSat inside `if U is u8` in a generic over its result lane that restates the clause with SizeOf of a closed type (`SizeOf[u32]()`), which check demands and then does not judge, called for u32 to u8, a 4x narrowing: the entry check panics in dev and test builds (types.PanicContract), and in release, which has no entry check, codegen's NarrowSat guard panics at the call instead of a silent result (the run-time backstop).
import std.simd

N[U: simd.Lane](a: simd.Vec[u32, 4], b: simd.Vec[u32, 4]): int
    requires 8 == 2 * 4 && 8 <= 64 && 2 * SizeOf[U]() == SizeOf[u32]()
{
    if U is u8 {
        r := a.NarrowSat[U, 8](b)
        return Cast[int](r.Lane(0))
    }
    return -1
}

export Main(sys: &System): int {
    n := Cast[u32](sys.args.Len())
    return N[u8](simd.Vec[u32, 4].Splat(300 + n), simd.Vec[u32, 4].Splat(7))
}
