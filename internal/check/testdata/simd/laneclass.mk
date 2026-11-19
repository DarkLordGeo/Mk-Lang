import std.simd

L(a: simd.Vec[i32, 4], f: simd.Vec[f32, 4], u: simd.Vec[u16, 8], q: simd.Vec[i64, 2]): int {
    x := f.AddSat(f)
    y := a.Sqrt()
    z := u.AbsWrap()
    w := q.ReduceSumWide()
    v := a.WidenLo[u64, 2]()
    t := a.Trunc[i64]()
    ok1 := a.AddSat(a)
    ok2 := f.Sqrt()
    ok3 := u.ReduceSumWide()
    ok4 := f.WidenLo[f64, 2]()
    ok5 := a.Trunc[i8]()
    return 0
}

G[T: simd.Lane](v: simd.Vec[T, 4]): T {
    return v.ReduceSum()
}

H[T: simd.Lane](v: simd.Vec[T, 4]): simd.Vec[T, 4] {
    if T is f32 {
        return v.Sqrt()
    }
    return v
}

export Main(sys: &System) {
}
