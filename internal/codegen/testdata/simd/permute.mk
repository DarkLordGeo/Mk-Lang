// Differential quick set: permutations, construction, lanes and Lookup16.
// Main prints Kernel() at compile time and at run time.
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

Perm(h: u64, s: u64): u64 {
    a := Bytes16(s)
    b := Bytes16(s +% 5)
    var r := Fold16(h, a.Shuffle(b, [0, 17, 2, 19, 31, 30, 5, 5, 8, 24, 10, 11, 12, 13, 14, 15]))
    r = Fold16(r, a.Reverse())
    r = Fold16(r, a.Broadcast(3))
    r = Fold16(r, a.Rotate(5))
    r = Fold16(r, a.Slide(b, 4))
    r = Fold16(r, a.Slide(b, 0))
    r = Fold16(r, a.Slide(b, 16))
    r = Fold16(r, a.InterleaveLo(b))
    r = Fold16(r, a.InterleaveHi(b))
    r = Fold16(r, a.DeinterleaveEven(b))
    r = Fold16(r, a.DeinterleaveOdd(b))
    r = Fold16(r, a.With(7, 99))
    r = (r ^ u64(a.Lane(9))) *% 31
    r = Fold16(r, simd.Vec[u8, 16].Iota() +% simd.Vec[u8, 16].Zero())
    r = Fold16(r, simd.Lookup16(a & simd.Vec[u8, 16].Splat(31), b))
    w := simd.Vec[u8, 32].Iota() *% simd.Vec[u8, 32].Splat(Trunc[u8](s +% 3))
    r = Fold32(r, simd.Lookup16(w, a))
    f := Bytes16(s).Reinterpret[i32, 4]()
    r = Fold16(r, f.Rotate(1).InterleaveLo(f).Reinterpret[u8, 16]())
    d := Bytes16(s).Reinterpret[u64, 2]()
    r = Fold16(r, d.Reverse().Slide(d, 1).Reinterpret[u8, 16]())
    return r
}

Kernel(): u64 {
    var h: u64 = 1469598103934665603
    for s in 0..24 {
        h = Perm(h, Cast[u64](s))
    }
    return h
}

const C: u64 = Kernel()

export Main(sys: &System): WriteError!void {
    var out := sys.Stdout()
    r := Kernel()
    try Print(mut out, "{c} {r}\n", (c: C, r))
}
