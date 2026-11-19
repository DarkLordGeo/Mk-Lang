// Differential quick set: float lanes, no NaN made (NaN payloads are
// unspecified; eval's tests pin NaN). Main
// prints Kernel() at compile time and at run time.
import std.simd

Mix(x: u64): u64 {
    var z := x +% 0x9e3779b97f4a7c15
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Bytes16(seed: u64): simd.Vec[u8, 16] {
    var a: [16]u8 = [0; 16]
    var s := seed
    for j in 0..16 {
        s = Mix(s)
        a[j] = Trunc[u8](s)
    }
    return simd.Vec[u8, 16].FromArray(a)
}

Fold16(h: u64, v: simd.Vec[u8, 16]): u64 {
    a := v.ToArray()
    var r := h
    for j in 0..16 {
        r = (r ^ u64(a[j])) *% 0x100000001b3
    }
    return r
}

F4(h: u64, v: simd.Vec[f32, 4]): u64 {
    return Fold16(h, v.Reinterpret[u8, 16]())
}

D2(h: u64, v: simd.Vec[f64, 2]): u64 {
    return Fold16(h, v.Reinterpret[u8, 16]())
}

F32s(h: u64, s: u64): u64 {
    i := Bytes16(s).Reinterpret[i32, 4]() >> 8
    j := Bytes16(s +% 3).Reinterpret[i32, 4]() >> 12
    a := i.Cast[f32]() / simd.Vec[f32, 4].Splat(7.0)
    b := j.Cast[f32]() * simd.Vec[f32, 4].Splat(0.375)
    d := b.Abs() + simd.Vec[f32, 4].Splat(1.0)
    var r := F4(h, a + b)
    r = F4(r, a - b)
    r = F4(r, a * b)
    r = F4(r, a / d)
    r = F4(r, -a)
    r = F4(r, a.Abs().Sqrt())
    r = F4(r, a.Floor())
    r = F4(r, a.Ceil())
    r = F4(r, a.RoundEven())
    r = F4(r, a.RoundToZero())
    r = F4(r, a.Min(b))
    r = F4(r, a.Max(b))
    r = F4(r, (a < b).Select(a, b))
    r = (r ^ (a < b).Bits() ^ (a <= b).Bits() ^ (a == a).Bits() ^ (a != b).Bits() ^ (a >= b).Bits()) *% 31
    r = F4(r, simd.Vec[f32, 4].Splat(a.ReduceSum()))
    r = F4(r, simd.Vec[f32, 4].Splat(a.ReduceMin() + b.ReduceMax()))
    w := simd.Vec[f32, 8].FromArray([1.5, -2.25, 1e30, -1e-30, 0.1, 3.0, 2.0, 1e8])
    x := w * w + w
    r = Fold16(r, simd.Vec[f32, 4].Splat(x.ReduceSum()).Reinterpret[u8, 16]())
    return r
}

F64s(h: u64, s: u64): u64 {
    i := Bytes16(s).Reinterpret[i64, 2]() >> 20
    a := i.Cast[f64]() / simd.Vec[f64, 2].Splat(3.0)
    b := a * simd.Vec[f64, 2].Splat(-0.5) + simd.Vec[f64, 2].Splat(0.25)
    var r := D2(h, a + b)
    r = D2(r, a * b - a)
    r = D2(r, a / (b.Abs() + simd.Vec[f64, 2].Splat(2.0)))
    r = D2(r, a.Abs().Sqrt())
    r = D2(r, a.Floor() + a.Ceil() + a.RoundEven() + a.RoundToZero())
    r = D2(r, a.Min(b) - a.Max(b))
    r = (r ^ (a > b).Bits()) *% 31
    r = D2(r, simd.Vec[f64, 2].Splat(a.ReduceSum() + a.ReduceMax()))
    q := simd.Vec[f64, 4].FromArray([1e300, 1e-300, -3.5, 0.1])
    t := q * q
    r = D2(r, simd.Vec[f64, 2].Splat(t.ReduceSum() + t.ReduceMin()))
    return r
}

Kernel(): u64 {
    var h: u64 = 1469598103934665603
    for s in 0..24 {
        x := Cast[u64](s)
        h = F32s(h, x)
        h = F64s(h, x)
    }
    return h
}

const C: u64 = Kernel()

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    r := Kernel()
    try Print(mut out, "{c} {r}\n", (c: C, r))
}
