// Source: WidenLo in a generic over the lane types, whose lane-class rule (integers of one signedness, or f32 to f64) check does not judge for a type parameter, called with f32 to u64 lanes (types.PanicContract, every profile; without the check, an LLVM fpext ICE). W restates the size clause, which reads its own type parameters; the lane-class rule stays the run-time backstop: every instance is judged in full at the call.
import std.simd

W[T: simd.Lane, U: simd.Lane](v: simd.Vec[T, 4]): simd.Vec[U, 2]
    requires 2 * 2 == 4 && 2 >= 2 && SizeOf[U]() == 2 * SizeOf[T]()
{
    return v.WidenLo[U, 2]()
}

export Main(sys: &System): int {
    w := W[f32, u64](simd.Vec[f32, 4].Splat(Cast[f32](sys.args.Len())))
    return Cast[int](w.Lane(0))
}
