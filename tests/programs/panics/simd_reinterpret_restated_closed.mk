// Source: Reinterpret in a generic over its result lane that restates the size clause with SizeOf of a closed type (`SizeOf[u8]()`), which check demands and then does not judge, called with 4 bytes to 32: the entry check panics in dev and test builds (types.PanicContract), and in release, which has no entry check, codegen's size guard panics at the call instead of a bitcast ICE (the run-time backstop).
import std.simd

R[U: simd.Lane](v: simd.Vec[u8, 4]): simd.Vec[U, 4]
    requires 4 * SizeOf[U]() == 4 * SizeOf[u8]() && 4 >= 2 && 4 <= 64
{
    return v.Reinterpret[U, 4]()
}

export Main(sys: &System): int {
    w := R[u64](simd.Vec[u8, 4].Splat(Cast[u8](sys.args.Len())))
    return Cast[int](w.Lane(0))
}
