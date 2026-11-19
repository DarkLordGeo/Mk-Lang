// Source: Trunc in a generic over its result lane, which check's lane rule (integer lanes, U no wider than T) skips for an open U, called with f32 lanes: without the check the cast would reinterpret the bits (types.PanicContract, every profile).
import std.simd

Tr[U: simd.Lane](v: simd.Vec[u32, 4]): simd.Vec[U, 4] {
    return v.Trunc[U]()
}

export Main(sys: &System): int {
    w := Tr[f32](simd.Vec[u32, 4].Splat(Cast[u32](sys.args.Len())))
    return Cast[int](w.Lane(0))
}
