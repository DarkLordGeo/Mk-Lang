// A SIMD program and its scalar twin, whose outputs
// are equal. Over 100,003 pseudo-random bytes (a length that leaves a tail
// for every width), each kernel runs once with std.simd vectors and once lane by lane:
// a byte count (Vec[u8, 16] compare and CountSet), a wrapping i32 sum (Vec[i32, 8] +%),
// a u8 saturating add's checksum (AddSat), the f32 maximum (exact in any order), and a
// Select of the larger of two byte streams. Each line prints the vector result, the scalar
// result and whether they are equal; the last line says whether all were.
import std.simd

const Size: int = 100_003

Fill(xs: mut []u8, seed: u32) {
    var s := seed
    for i in 0..xs.Len() {
        s = s *% 1_664_525 +% 1_013_904_223
        xs[i] = Trunc[u8](s >> 24)
    }
}

CountVec(xs: []u8, b: u8): int {
    var n := 0
    var i := 0
    needle := simd.Vec[u8, 16].Splat(b)
    for i + 16 <= xs.Len() {
        n += (simd.Vec[u8, 16].FromSlice(xs, i) == needle).CountSet()
        i += 16
    }
    for i < xs.Len() {
        if xs[i] == b { n += 1 }
        i += 1
    }
    return n
}

CountScalar(xs: []u8, b: u8): int {
    var n := 0
    for x in xs {
        if x == b { n += 1 }
    }
    return n
}

SumVec(xs: []i32): i32 {
    var acc := simd.Vec[i32, 8].Zero()
    var i := 0
    for i + 8 <= xs.Len() {
        acc = acc +% simd.Vec[i32, 8].FromSlice(xs, i)
        i += 8
    }
    var t := acc.ReduceSumWrap()
    for i < xs.Len() {
        t = t +% xs[i]
        i += 1
    }
    return t
}

SumScalar(xs: []i32): i32 {
    var t: i32 = 0
    for x in xs {
        t = t +% x
    }
    return t
}

SatVec(a: []u8, b: []u8, out: mut []u8) {
    var i := 0
    for i + 32 <= a.Len() {
        v := simd.Vec[u8, 32].FromSlice(a, i).AddSat(simd.Vec[u8, 32].FromSlice(b, i))
        v.CopyTo(out, i)
        i += 32
    }
    for i < a.Len() {
        out[i] = SatScalar1(a[i], b[i])
        i += 1
    }
}

SatScalar1(x: u8, y: u8): u8 {
    s := int(x) + int(y)
    if s > 255 {
        return 255
    }
    return Trunc[u8](s)
}

SatScalar(a: []u8, b: []u8, out: mut []u8) {
    for i in 0..a.Len() {
        out[i] = SatScalar1(a[i], b[i])
    }
}

MaxVec(xs: []f32): f32 {
    var m := simd.Vec[f32, 4].Splat(xs[0])
    var i := 0
    for i + 4 <= xs.Len() {
        m = m.Max(simd.Vec[f32, 4].FromSlice(xs, i))
        i += 4
    }
    var r := m.ReduceMax()
    for i < xs.Len() {
        if xs[i] > r { r = xs[i] }
        i += 1
    }
    return r
}

MaxScalar(xs: []f32): f32 {
    var r := xs[0]
    for x in xs {
        if x > r { r = x }
    }
    return r
}

LargerVec(a: []u8, b: []u8, out: mut []u8) {
    var i := 0
    for i + 16 <= a.Len() {
        x := simd.Vec[u8, 16].FromSlice(a, i)
        y := simd.Vec[u8, 16].FromSlice(b, i)
        (x > y).Select(x, y).CopyTo(out, i)
        i += 16
    }
    for i < a.Len() {
        out[i] = Larger1(a[i], b[i])
        i += 1
    }
}

Larger1(x: u8, y: u8): u8 {
    if x > y {
        return x
    }
    return y
}

LargerScalar(a: []u8, b: []u8, out: mut []u8) {
    for i in 0..a.Len() {
        out[i] = Larger1(a[i], b[i])
    }
}

// Digest is FNV-1a over the bytes, so two outputs compare by one number.
Digest(xs: []u8): u32 {
    var h: u32 = 2_166_136_261
    for x in xs {
        h = (h ^ u32(x)) *% 16_777_619
    }
    return h
}

Line(w: mut Writer, name: str, v: i64, s: i64): WriteError!bool {
    same := v == s
    try Print(w, "{name} {v} {s} {same}\n", (name, v, s, same))
    return same
}

export Main(sys: &System): WriteError|OutOfMemory!void {
    a := try sys.arena.Slice[u8](Size)
    b := try sys.arena.Slice[u8](Size)
    Fill(a, 1)
    Fill(b, 2)
    ints := try sys.arena.Slice[i32](Size)
    floats := try sys.arena.Slice[f32](Size)
    for i in 0..Size {
        ints[i] = i32(a[i]) *% 16_777_259 +% i32(b[i])
        floats[i] = f32(a[i]) * 0.5 - f32(b[i]) * 0.25
    }
    o1 := try sys.arena.Slice[u8](Size)
    o2 := try sys.arena.Slice[u8](Size)
    var out := sys.Stdout()
    var all := true
    all = try Line(mut out, "count", i64(CountVec(a[..], 7)), i64(CountScalar(a[..], 7))) && all
    all = try Line(mut out, "sum", i64(SumVec(ints[..])), i64(SumScalar(ints[..]))) && all
    SatVec(a[..], b[..], o1)
    SatScalar(a[..], b[..], o2)
    all = try Line(mut out, "addsat", i64(Digest(o1[..])), i64(Digest(o2[..]))) && all
    mv := MaxVec(floats[..])
    ms := MaxScalar(floats[..])
    all = try Line(mut out, "max4x", i64(Cast[i64](mv * 4.0)), i64(Cast[i64](ms * 4.0))) && all
    LargerVec(a[..], b[..], o1)
    LargerScalar(a[..], b[..], o2)
    all = try Line(mut out, "select", i64(Digest(o1[..])), i64(Digest(o2[..]))) && all
    try Print(mut out, "equal {all}\n", (all))
}
