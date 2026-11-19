// Source: MulWidenLo in a generic over its result lane, whose lane-class rule check skips for an open U, called with f32 lanes from u16, which without the check would be an invalid zext to float, an ICE (types.PanicContract, every profile). MW restates the size clause, which reads its own type parameter; the lane-class rule stays the run-time backstop: every instance is judged in full at the call.
import std.simd

MW[U: simd.Lane](a: simd.Vec[u16, 8]): simd.Vec[U, 4]
    requires 4 * 2 == 8 && 4 >= 2 && SizeOf[U]() == 2 * SizeOf[u16]()
{
    return a.MulWidenLo[U, 4](a)
}

export Main(sys: &System): int {
    w := MW[f32](simd.Vec[u16, 8].Splat(Cast[u16](sys.args.Len())))
    return Cast[int](w.Lane(0))
}
