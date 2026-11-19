// spectralnorm, Mk: the reference algorithm of ../README.md (the Benchmarks
// Game's spectral-norm, scalar and single-threaded). A(i, j) computed from
// integers on every use, []f64 vectors indexed v[j] (bounds checked), the
// integer + and * checked. No FMA (Mk never contracts).
// Usage: spectralnorm <n>. Prints trunc(norm * 1e9).

import std.math

A(i: int, j: int): f64 {
    return 1.0 / Cast[f64]((i + j) * (i + j + 1) / 2 + i + 1)
}

MulAv(v: []f64, out: mut []f64) {
    n := v.Len()
    for i in 0..n {
        var s := 0.0
        for j in 0..n {
            s = s + A(i, j) * v[j]
        }
        out[i] = s
    }
}

MulAtv(v: []f64, out: mut []f64) {
    n := v.Len()
    for i in 0..n {
        var s := 0.0
        for j in 0..n {
            s = s + A(j, i) * v[j]
        }
        out[i] = s
    }
}

MulAtAv(v: []f64, out: mut []f64, tmp: mut []f64) {
    MulAv(v, tmp)
    MulAtv(tmp[..], out)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    u := try sys.arena.Slice[f64](n)
    v := try sys.arena.Slice[f64](n)
    tmp := try sys.arena.Slice[f64](n)
    for i in 0..n {
        u[i] = 1.0
    }
    for _ in 0..10 {
        MulAtAv(u[..], v, tmp)
        MulAtAv(v[..], u, tmp)
    }
    var vbv := 0.0
    var vv := 0.0
    for i in 0..n {
        vbv = vbv + u[i] * v[i]
        vv = vv + v[i] * v[i]
    }
    norm := Cast[i64](math.Sqrt(vbv / vv) * 1e9)
    var out := sys.Stdout()
    try Print(mut out, "{norm}\n", (norm))
}
