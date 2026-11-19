// validutf8, Mk, SIMD table: the kernel of README.md with std.simd's Vec, at
// the build's fixed CPU target and no run-time dispatch
//: Keiser and Lemire's lookup validator, the body of std.simd's
// ValidUtf8N (std/simd/utf8.mk) copied without its Cpu() test, four vectors
// a step with an all-ASCII test, then one vector a step, then one
// overlapping vector for the tail. This package is the v3 variants', L = 32
// (Vec[u8, 32]: one ymm register under --cpu x86-64-v3); ../mkfixv2 is the
// v2 variants', the same text with L = 16 (one xmm), since Mk has no
// compile-time fact of the build's CPU (Target.vector_bits never follows
// --cpu) and C, Rust and Zig choose the width from the target at compile
// time. The rest is the row's original Mk program. The buffer is an arena
// slice (labelled alignment exception, README.md).
// Usage: validutf8 <size> <reps> <seed>. Prints the checksum.

import std.simd

const L: int = 32

type V = simd.Vec[u8, L]

// The error bits of the three tables (simdjson's names).
const TooShort: u8 = 1
const TooLong: u8 = 2
const Overlong3: u8 = 4
const TooLarge: u8 = 8
const Surrogate: u8 = 16
const Overlong2: u8 = 32
const TooLarge1000: u8 = 64
const Overlong4: u8 = 64
const TwoConts: u8 = 128
const Carry: u8 = 131

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

// The 16 encodings of ../README.md, four bytes each; entry k is 1 + k / 4
// bytes long.
const Enc: [64]u8 = [
    0x61, 0, 0, 0, 0x20, 0, 0, 0, 0x30, 0, 0, 0, 0x7E, 0, 0, 0,
    0xC2, 0x80, 0, 0, 0xDF, 0xBF, 0, 0, 0xC3, 0xA9, 0, 0, 0xD0, 0x96, 0, 0,
    0xE0, 0xA0, 0x80, 0, 0xED, 0x9F, 0xBF, 0, 0xEE, 0x80, 0x80, 0, 0xE4, 0xB8, 0x96, 0,
    0xF0, 0x90, 0x80, 0x80, 0xF4, 0x8F, 0xBF, 0xBF, 0xF0, 0x9F, 0x98, 0x80, 0xF3, 0xA0, 0x80, 0x80,
]

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    var blen := n
    if blen > 65536 {
        blen = 65536
    }
    blk := try sys.arena.Slice[u8](blen)
    var p := 0
    for {
        st = st +% 0x9e3779b97f4a7c15
        k := Cast[int](Mix(st) & 15)
        l := 1 + k / 4
        if p + l > blen {
            break
        }
        for j in 0..l {
            blk[p + j] = Enc[k * 4 + j]
        }
        p += l
    }
    (mut blk[p..blen]).Fill(0x61)
    buf := try sys.arena.Slice[u8](n)
    var i := 0
    for i + blen <= n {
        (mut buf[i..i + blen]).CopyFrom(blk[0..blen])
        i += blen
    }
    (mut buf[i..n]).Fill(0x61)
    var span: u64 = 64
    if n < 64 {
        span = Cast[u64](n)
    }
    var total: u64 = 0
    for _ in 0..reps {
        st = st +% 0x9e3779b97f4a7c15
        z := Mix(st)
        pos := n - 1 - Cast[int]((z >> 1) % span)
        old := buf[pos]
        if z & 1 == 1 {
            buf[pos] = Trunc[u8](z >> 56)
        }
        var ok: u64 = 0
        if ValidUtf8(buf[0..n]) {
            ok = 1
        }
        total = total *% 31 +% ok
        buf[pos] = old
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}

// ValidUtf8 reports whether s is well-formed UTF-8 (Unicode section 3.9,
// table 3-7): std.simd's ValidUtf8N body at the fixed width L.
ValidUtf8(s: []u8): bool {
    n := s.Len()
    if n < L {
        return ValidUtf8Scalar(s)
    }
    t1 := simd.Vec[u8, 16].FromArray([TooLong, TooLong, TooLong, TooLong, TooLong, TooLong, TooLong, TooLong,
        TwoConts, TwoConts, TwoConts, TwoConts,
        TooShort | Overlong2, TooShort, TooShort | Overlong3 | Surrogate,
        TooShort | TooLarge | TooLarge1000 | Overlong4])
    t2 := simd.Vec[u8, 16].FromArray([Carry | Overlong3 | Overlong2 | Overlong4, Carry | Overlong2, Carry, Carry,
        Carry | TooLarge, Carry | TooLarge | TooLarge1000, Carry | TooLarge | TooLarge1000,
        Carry | TooLarge | TooLarge1000, Carry | TooLarge | TooLarge1000, Carry | TooLarge | TooLarge1000,
        Carry | TooLarge | TooLarge1000, Carry | TooLarge | TooLarge1000, Carry | TooLarge | TooLarge1000,
        Carry | TooLarge | TooLarge1000 | Surrogate, Carry | TooLarge | TooLarge1000,
        Carry | TooLarge | TooLarge1000])
    t3 := simd.Vec[u8, 16].FromArray([TooShort, TooShort, TooShort, TooShort, TooShort, TooShort, TooShort, TooShort,
        TooLong | Overlong2 | TwoConts | Overlong3 | TooLarge1000 | Overlong4,
        TooLong | Overlong2 | TwoConts | Overlong3 | TooLarge,
        TooLong | Overlong2 | TwoConts | Surrogate | TooLarge,
        TooLong | Overlong2 | TwoConts | Surrogate | TooLarge,
        TooShort, TooShort, TooShort, TooShort])
    high := V.Splat(0x80)
    zero := V.Zero()
    var err := zero
    var prev := zero
    var pending := zero // Utf8Incomplete of the last vector checked
    var i := 0
    for i + 4 * L <= n {
        w := s[i..i + 4 * L]
        v0 := V.FromSlice(w, 0)
        v1 := V.FromSlice(w, L)
        v2 := V.FromSlice(w, 2 * L)
        v3 := V.FromSlice(w, 3 * L)
        if ((v0 | v1 | v2 | v3) >= high).Any() {
            err = err | Utf8Check(v0, prev, t1, t2, t3) | Utf8Check(v1, v0, t1, t2, t3) |
                Utf8Check(v2, v1, t1, t2, t3) | Utf8Check(v3, v2, t1, t2, t3)
            pending = Utf8Incomplete(v3)
        } else {
            err = err | pending
            pending = zero
        }
        prev = v3
        i += 4 * L
    }
    for i + L <= n {
        v := V.FromSlice(s, i)
        err = err | Utf8Check(v, prev, t1, t2, t3)
        pending = Utf8Incomplete(v)
        prev = v
        i += L
    }
    if i < n {
        // The last L bytes, overlapping what was checked: each lane's
        // check reads only its own byte and the three before it, so
        // checking a byte twice gives the same answer, and the three
        // bytes before the window are put in prev's top lanes (built
        // in the low lanes and reversed: a const lane index L - 1 is a
        // mono ICE at present).
        p := n - L
        v := V.FromSlice(s, p)
        var w := zero
        if p >= 1 {
            w = w.With(0, s[p - 1])
        }
        if p >= 2 {
            w = w.With(1, s[p - 2])
        }
        if p >= 3 {
            w = w.With(2, s[p - 3])
        }
        err = err | Utf8Check(v, w.Reverse(), t1, t2, t3)
        pending = Utf8Incomplete(v)
    }
    return (err | pending).ReduceOr() == 0
}

// Utf8Check is the error vector of v given the vector before it: nonzero in
// a lane whose byte, with the bytes before it, breaks table 3-7.
Utf8Check(v: V, prev: V, t1: simd.Vec[u8, 16], t2: simd.Vec[u8, 16], t3: simd.Vec[u8, 16]): V {
    p1 := v.Slide(prev, 1)
    special := simd.Lookup16[L](p1 >> 4, t1) & simd.Lookup16[L](p1 & V.Splat(15), t2) & simd.Lookup16[L](v >> 4, t3)
    third := v.Slide(prev, 2).SubSat(V.Splat(0xE0 - 0x80))
    fourth := v.Slide(prev, 3).SubSat(V.Splat(0xF0 - 0x80))
    must := (third | fourth) & V.Splat(0x80)
    return must ^ special
}

// Utf8Incomplete is nonzero when v ends inside a sequence: a lead of four
// bytes in one of its last three lanes, of three in its last two, or of two
// in its last.
Utf8Incomplete(v: V): V {
    last := V.Zero().With(0, 0xC0 - 1).With(1, 0xE0 - 1).With(2, 0xF0 - 1).Reverse()
    max := (V.Iota().Reverse() >= V.Splat(3)).Select(V.Splat(255), last)
    return v.SubSat(max)
}

// ValidUtf8Scalar is table 3-7 one sequence at a time: the reference the
// vector form is tested against, and the form below L bytes.
ValidUtf8Scalar(s: []u8): bool {
    n := s.Len()
    var i := 0
    for i < n {
        k := Utf8SeqLen(s, i)
        if k == 0 {
            return false
        }
        i += k
    }
    return true
}

// Utf8SeqLen is the length, 1 to 4, of the well-formed sequence at s[i], or
// 0 when none starts there (table 3-7, with Go's utf8.Valid's per-lead
// ranges for the second byte); i < s.Len().
Utf8SeqLen(s: []u8, i: int): int {
    n := s.Len()
    c := s[i]
    if c < 0x80 {
        return 1
    }
    var need := 0
    var lo: u8 = 0x80
    var hi: u8 = 0xBF
    if c >= 0xC2 && c <= 0xDF {
        need = 1
    } else if c == 0xE0 {
        need = 2
        lo = 0xA0
    } else if c >= 0xE1 && c <= 0xEC || c == 0xEE || c == 0xEF {
        need = 2
    } else if c == 0xED {
        need = 2
        hi = 0x9F
    } else if c == 0xF0 {
        need = 3
        lo = 0x90
    } else if c >= 0xF1 && c <= 0xF3 {
        need = 3
    } else if c == 0xF4 {
        need = 3
        hi = 0x8F
    } else {
        return 0
    }
    if i + need >= n {
        return 0
    }
    d := s[i + 1]
    if d < lo || d > hi {
        return 0
    }
    for k in 2..need + 1 {
        e := s[i + k]
        if e < 0x80 || e > 0xBF {
            return 0
        }
    }
    return need + 1
}
