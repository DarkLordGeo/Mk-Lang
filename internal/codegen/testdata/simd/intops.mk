// Differential quick set: integer lanes. Main prints Kernel() evaluated at
// compile time (the const C) and at run time; TestDifferentialNative wants
// the two equal at dev and release.
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

Fold32(h: u64, v: simd.Vec[u8, 32]): u64 {
    a := v.ToArray()
    var r := h
    for j in 0..32 {
        r = (r ^ u64(a[j])) *% 0x100000001b3
    }
    return r
}

U8s(h: u64, s: u64): u64 {
    a := Bytes16(s)
    b := Bytes16(s +% 1)
    var r := Fold16(h, a +% b)
    r = Fold16(r, a -% b)
    r = Fold16(r, a *% b)
    r = Fold16(r, (a & b) | (a ^ ~b))
    r = Fold16(r, a.AddSat(b))
    r = Fold16(r, a.SubSat(b))
    r = Fold16(r, a.Min(b))
    r = Fold16(r, a.Max(b))
    r = Fold16(r, a.MulHigh(b))
    r = Fold16(r, a << 3)
    r = Fold16(r, a >> 5)
    r = Fold16(r, a.ShlEach(b & simd.Vec[u8, 16].Splat(7)))
    r = Fold16(r, a.ShrEach(b & simd.Vec[u8, 16].Splat(7)))
    r = Fold16(r, a.DivTrunc(b | simd.Vec[u8, 16].Splat(1)))
    r = (r ^ (a < b).Bits()) *% 31
    r = (r ^ (a == a).Bits() ^ (a != b).Bits() ^ (a >= b).Bits()) *% 31
    r = (r ^ u64(a.ReduceSumWrap()) ^ Cast[u64](a.ReduceSumWide())) *% 31
    r = (r ^ u64(a.ReduceMin()) ^ u64(a.ReduceMax())) *% 31
    r = (r ^ u64(a.ReduceAnd()) ^ u64(a.ReduceOr()) ^ u64(a.ReduceXor())) *% 31
    m := a < b
    r = (r ^ Cast[u64]((m & !(a == b)).CountSet())) *% 31
    if f := m.FirstSet() {
        r = (r ^ Cast[u64](f)) *% 31
    }
    r = Fold16(r, m.Select(a, b))
    return r
}

I16s(h: u64, s: u64): u64 {
    a := Bytes16(s).Reinterpret[i16, 8]()
    b := Bytes16(s +% 7).Reinterpret[i16, 8]()
    var r := Fold16(h, (a +% b).Reinterpret[u8, 16]())
    r = Fold16(r, (a *% b).Reinterpret[u8, 16]())
    r = Fold16(r, a.AddSat(b).Reinterpret[u8, 16]())
    r = Fold16(r, a.SubSat(b).Reinterpret[u8, 16]())
    r = Fold16(r, a.Min(b).Reinterpret[u8, 16]())
    r = Fold16(r, a.Max(b).Reinterpret[u8, 16]())
    r = Fold16(r, a.MulHigh(b).Reinterpret[u8, 16]())
    r = Fold16(r, a.AbsWrap().Reinterpret[u8, 16]())
    r = Fold16(r, (a >> 4).Reinterpret[u8, 16]())
    r = Fold16(r, a.DivTrunc(b | simd.Vec[i16, 8].Splat(1)).Reinterpret[u8, 16]())
    r = (r ^ (a < b).Bits() ^ (a <= b).Bits() ^ (a > b).Bits()) *% 31
    r = (r ^ Cast[u64](a.ReduceSumWide() +% 100000)) *% 31
    r = Fold16(r, Splat16(a.ReduceMin()))
    return r
}

Splat16(x: i16): simd.Vec[u8, 16] {
    return simd.Vec[i16, 8].Splat(x).Reinterpret[u8, 16]()
}

I32s(h: u64, s: u64): u64 {
    a := Bytes16(s).Reinterpret[i32, 4]()
    b := Bytes16(s +% 11).Reinterpret[i32, 4]()
    var r := Fold16(h, (a -% b).Reinterpret[u8, 16]())
    r = Fold16(r, a.MulHigh(b).Reinterpret[u8, 16]())
    r = Fold16(r, a.AbsWrap().Reinterpret[u8, 16]())
    r = Fold16(r, a.ShrEach(b & simd.Vec[i32, 4].Splat(31)).Reinterpret[u8, 16]())
    r = (r ^ (a < b).Bits() ^ Cast[u64](a.ReduceSumWide() +% 10000000000)) *% 31
    w := simd.Vec[u32, 8].FromArray([1, 2, 3, 4, 0xffffffff, 0x80000000, 7, 9])
    r = Fold32(r, (w *% w).Reinterpret[u8, 32]())
    r = (r ^ (w > simd.Vec[u32, 8].Splat(5)).Bits()) *% 31
    return r
}

I64s(h: u64, s: u64): u64 {
    a := Bytes16(s).Reinterpret[i64, 2]()
    b := Bytes16(s +% 13).Reinterpret[u64, 2]()
    var r := Fold16(h, (a +% a).Reinterpret[u8, 16]())
    r = Fold16(r, a.AddSat(a).Reinterpret[u8, 16]())
    r = Fold16(r, b.SubSat(b >> 1).Reinterpret[u8, 16]())
    r = Fold16(r, a.Min(a.AbsWrap()).Reinterpret[u8, 16]())
    r = (r ^ (b < b.Reverse()).Bits() ^ b.ReduceMax() ^ Cast[u64](a.Lane(1) & 0xffff)) *% 31
    return r
}

Kernel(): u64 {
    var h: u64 = 1469598103934665603
    for s in 0..24 {
        x := Cast[u64](s)
        h = U8s(h, x)
        h = I16s(h, x)
        h = I32s(h, x)
        h = I64s(h, x)
    }
    return h
}

const C: u64 = Kernel()

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    r := Kernel()
    try Print(mut out, "{c} {r}\n", (c: C, r))
}
