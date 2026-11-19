// verdict: ok
// rule: D-262
// note: float lanes take + - * / and unary -, integer lanes the wrapping and bitwise operators and shifts by a scalar, comparisons give a Mask that takes & | ^ ! and selects
import std.simd

Floats(a: simd.Vec[f32, 4], b: simd.Vec[f32, 4]): simd.Vec[f32, 4] {
    c := (a + b) * a - b / a
    return -c
}

Ints(a: simd.Vec[i32, 8], b: simd.Vec[i32, 8]): simd.Vec[i32, 8] {
    c := (a +% b) *% a -% b
    d := (c & a) | (b ^ ~a)
    return (d << 3) >> 1
}

Clamp(a: simd.Vec[u8, 16], lo: u8, hi: u8): simd.Vec[u8, 16] {
    l := simd.Vec[u8, 16].Splat(lo)
    h := simd.Vec[u8, 16].Splat(hi)
    m := (a < l) | (a > h)
    inside := !m & (a != l)
    _ = inside.CountSet()
    return m.Select(l, a).Min(h)
}

Total(xs: []i64): i64 {
    var s := simd.Vec[i64, 2].Zero()
    var i := 0
    for i + 2 <= xs.Len() {
        s = s +% simd.Vec[i64, 2].FromSlice(xs, i)
        i += 2
    }
    var t := s.ReduceSumWrap()
    for i < xs.Len() {
        t += xs[i]
        i += 1
    }
    return t
}

Lanes(): int {
    v := simd.Vec[i32, 4].FromArray([1, 2, 3, 4])
    w := v.Shuffle(v, [3, 2, 1, 0]).With(0, 9)
    return int(w.Lane(0)) + int(v.ReduceMax()) + simd.Lanes[u8]()
}
