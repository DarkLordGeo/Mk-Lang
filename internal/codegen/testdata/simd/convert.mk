// Differential quick set: conversions and the memory intrinsics. Main prints
// Kernel() at compile time and at run time.
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

Fold8(h: u64, v: simd.Vec[u8, 8]): u64 {
    a := v.ToArray()
    var r := h
    for j in 0..8 {
        r = (r ^ u64(a[j])) *% 0x100000001b3
    }
    return r
}

Fold4(h: u64, v: simd.Vec[u8, 4]): u64 {
    a := v.ToArray()
    var r := h
    for j in 0..4 {
        r = (r ^ u64(a[j])) *% 0x100000001b3
    }
    return r
}

Conv(h: u64, s: u64): u64 {
    a := Bytes16(s)
    b := Bytes16(s +% 9)
    var r := Fold16(h, a.WidenLo[u16, 8]().Reinterpret[u8, 16]())
    r = Fold16(r, a.WidenHi[u16, 8]().Reinterpret[u8, 16]())
    sa := a.Reinterpret[i8, 16]()
    r = Fold16(r, sa.WidenLo[i16, 8]().Reinterpret[u8, 16]())
    r = Fold16(r, sa.MulWidenHi[i16, 8](b.Reinterpret[i8, 16]()).Reinterpret[u8, 16]())
    r = Fold16(r, a.MulWidenLo[u16, 8](b).Reinterpret[u8, 16]())
    w := a.Reinterpret[i16, 8]()
    r = Fold16(r, w.NarrowSat[u8, 16](w >> 3))
    r = Fold16(r, w.NarrowSat[i8, 16](w >> 6).Reinterpret[u8, 16]())
    i := a.Reinterpret[i32, 4]()
    r = Fold4(r, i.Trunc[u8]())
    r = Fold4(r, i.CastSat[i8]().Reinterpret[u8, 4]())
    r = Fold16(r, i.Cast[f32]().Reinterpret[u8, 16]())
    r = Fold4(r, (i >> 24).Cast[i8]().Reinterpret[u8, 4]())
    f := i.Cast[f32]() / simd.Vec[f32, 4].Splat(1000.0)
    r = Fold16(r, f.CastSat[i32]().Reinterpret[u8, 16]())
    r = Fold4(r, f.CastSat[u8]())
    r = Fold16(r, f.WidenLo[f64, 2]().Reinterpret[u8, 16]())
    r = Fold8(r, (f / simd.Vec[f32, 4].Splat(1000000.0)).Cast[i16]().Reinterpret[u8, 8]())
    d := f.WidenHi[f64, 2]()
    r = Fold8(r, d.Cast[f32]().Reinterpret[u8, 8]())
    // CastSat from signed lanes to wider unsigned (and signed) lanes;
    // about half the lanes are negative and clamp to 0.
    s8 := a.Reinterpret[i8, 16]()
    r = Fold32(r, s8.CastSat[u16]().Reinterpret[u8, 32]())
    r = Fold16(r, simd.Vec[i8, 8].Splat(-5).CastSat[u16]().Reinterpret[u8, 16]())
    r = Fold16(r, simd.Vec[i8, 2].Splat(-3).CastSat[u64]().Reinterpret[u8, 16]())
    r = Fold16(r, simd.Vec[i32, 2].Splat(-7).CastSat[u64]().Reinterpret[u8, 16]())
    r = Fold32(r, i.CastSat[u64]().Reinterpret[u8, 32]())
    r = Fold32(r, s8.WidenLo[i16, 8]().CastSat[u32]().Reinterpret[u8, 32]())
    r = Fold16(r, simd.Vec[i16, 8].Splat(-300).CastSat[i64]().Trunc[u16]().Reinterpret[u8, 16]())
    return r
}

Mem(h: u64, s: u64): u64 {
    var buf: [40]u8 = [0; 40]
    var t := s
    for j in 0..40 {
        t = Mix(t)
        buf[j] = Trunc[u8](t)
    }
    xs := buf[..]
    var r := Fold16(h, simd.Vec[u8, 16].FromSlice(xs, 3))
    r = Fold16(r, simd.Vec[u8, 16].FromSliceOr(xs, 30, simd.Vec[u8, 16].Splat(7)))
    r = Fold16(r, simd.Vec[u8, 16].FromSliceOr(xs, 40, simd.Vec[u8, 16].Splat(9)))
    r = Fold16(r, simd.Vec[u8, 16].Gather(xs, Bytes16(s) & simd.Vec[u8, 16].Splat(31)))
    words := [10, 20, 30, 40, 50, 60, 70, 80]
    idx := Bytes16(s).Reinterpret[i32, 4]() & simd.Vec[i32, 4].Splat(7)
    g := simd.Vec[int, 4].Gather(words[..], idx)
    r = Fold32(r, g.Reinterpret[u8, 32]())
    var out: [40]u8 = [0; 40]
    a := Bytes16(s +% 1)
    a.CopyTo(mut out[..], 5)
    a.CopyToMasked(mut out[..], 24, a > simd.Vec[u8, 16].Splat(128))
    for j in 0..40 {
        r = (r ^ u64(out[j])) *% 0x100000001b3
    }
    return r
}

// LanesOf is simd.Lanes[T]() of an instance's T: a call that
// eval and codegen answer per instance, where Natural's is check's constant.
LanesOf[T: simd.Lane](): int {
    return simd.Lanes[T]()
}

Natural(h: u64): u64 {
    v := simd.Vec[f32, simd.Lanes[f32]()].Iota()
    w: simd.Vec[f32, 4] = v
    a := w.ToArray()
    var r := h
    for j in 0..4 {
        r = (r ^ Cast[u64](a[j])) *% 0x100000001b3
    }
    r = (r ^ Cast[u64](simd.Lanes[u8]() * 100 + simd.Lanes[f64]())) *% 0x100000001b3
    r = (r ^ Cast[u64](LanesOf[i16]() * 10 + LanesOf[u64]())) *% 0x100000001b3
    return r
}

Both(h: u64, s: u64): u64 {
    return Natural(Mem(Conv(h, s), s))
}

Kernel(): u64 {
    var h: u64 = 1469598103934665603
    for s in 0..24 {
        h = Both(h, Cast[u64](s))
    }
    return h
}

const C: u64 = Kernel()

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    r := Kernel()
    try Print(mut out, "{c} {r}\n", (c: C, r))
}
