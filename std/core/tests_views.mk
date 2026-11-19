// Why: the place primitives and slice views hand out disjoint exclusive
// views the loan rules cannot prove disjoint, so the library must be right.
// What: test blocks for Swap, Replace, Slice.SplitAt, GetPair, Chunks,
// ChunksMut and the float bit casts (f64.Bits, FromBits and f32's).
// How: edges (empty, one element, the last piece shorter, equal and
// out-of-range indices), linear values moved without a copy, and bit casts at
// every class of binary64 and binary32 value, NaN payloads and signed zeros
// included. Helper names start with Tv: every file of std/core is one module.

import std.raw

linear struct TvTok {
    n: int
}

TvClose(t: TvTok): int {
    TvTok(n) := t
    return n
}

test "Swap exchanges two plain places and two linear ones" (sys: &System) {
    var a := 1
    var b := 2
    Swap(mut a, mut b)
    Assert(a == 2 && b == 1)
    var s := TvTok(n: 3)
    var t := TvTok(n: 4)
    Swap(mut s, mut t)
    x := TvClose(s)
    y := TvClose(t)
    Assert(x == 4 && y == 3)
    var xs := [10, 20, 30]
    if p := xs.GetPair(0, 2) {
        (a: pa, b: pb) := p
        Swap(pa, pb)
    }
    Assert(xs[0] == 30 && xs[2] == 10)
}

test "Replace returns the old value and leaves the new one" (sys: &System) {
    var a := 5
    Assert(Replace(mut a, 6) == 5 && a == 6)
    var t := TvTok(n: 1)
    old := Replace(mut t, TvTok(n: 2))
    x := TvClose(old)
    y := TvClose(t)
    Assert(x == 1 && y == 2)
}

test "SplitAt at every index of a slice, the empty one included" (sys: &System) {
    var xs := [0, 1, 2, 3, 4, 5, 6]
    for i in 0..8 {
        (left, right) := xs.SplitAt(i)
        Assert(left.Len() == i && right.Len() == 7 - i)
        for j in 0..left.Len() {
            Assert(left[j] == j)
        }
        for j in 0..right.Len() {
            Assert(right[j] == i + j)
        }
    }
    var e: [0]int = []
    (left: l, right: r) := e.SplitAt(0)
    Assert(l.Len() == 0 && r.Len() == 0)
}

test "SplitAt halves are written independently" (sys: &System) {
    var xs := [1, 1, 1, 1]
    (left, right) := xs.SplitAt(1)
    left[0] = 7
    right[2] = 9
    Assert(xs[0] == 7 && xs[1] == 1 && xs[3] == 9)
}

test "SplitAt past the end panics [exit 101: Slice.SplitAt: index out of range]" (sys: &System) {
    var xs := [1, 2]
    (left, right) := xs.SplitAt(3)
    _ = left
    _ = right
}

test "SplitAt below 0 panics [exit 101: Slice.SplitAt: index out of range]" (sys: &System) {
    var xs := [1, 2]
    (left, right) := xs.SplitAt(-1)
    _ = left
    _ = right
}

test "GetPair is none on equal and out-of-range indices, never a panic" (sys: &System) {
    var xs := [1, 2, 3]
    Assert(xs.GetPair(1, 1).IsNone())
    Assert(xs.GetPair(-1, 0).IsNone())
    Assert(xs.GetPair(0, 3).IsNone())
    Assert(xs.GetPair(3, 0).IsNone())
    Assert(xs.GetPair(0x7FFFFFFFFFFFFFFF, 0).IsNone())
    Assert(xs.GetPair(-0x7FFFFFFFFFFFFFFF - 1, 1).IsNone())
    var e: [0]int = []
    Assert(e.GetPair(0, 1).IsNone())
    for i in 0..3 {
        for j in 0..3 {
            if i != j {
                p := xs.GetPair(i, j).Must()
                Assert(*p.a == i + 1 && *p.b == j + 1)
            }
        }
    }
}

test "GetPair on a List gives both elements mut" (sys: &System) {
    var xs := List[int].New(sys.arena)
    for i in 0..10 {
        try xs.Push(i)
    }
    p := xs.GetPair(9, 0).Must()
    *p.a += 100
    *p.b += 200
    Assert(xs[9] == 109 && xs[0] == 200)
}

test "Chunks covers every element once, the last piece shorter" (sys: &System) {
    var xs := List[int].New(sys.arena)
    for len in 0..20 {
        for n in 1..8 {
            var seen := 0
            var pieces := 0
            for c in xs.Chunks(n) {
                Assert(c.Len() >= 1 && c.Len() <= n)
                for j in 0..c.Len() {
                    Assert(c[j] == seen)
                    seen += 1
                }
                pieces += 1
            }
            Assert(seen == len && pieces == (len + n - 1) / n)
        }
        try xs.Push(len)
    }
}

test "ChunksMut hands out every element once, and pieces stay live together" (sys: &System) {
    var xs := List[int].New(sys.arena)
    for _ in 0..23 {
        try xs.Push(0)
    }
    var k := 0
    for c in xs.ChunksMut(5) {
        for j in 0..c.Len() {
            c[j] += k
        }
        k += 1
    }
    for i in 0..23 {
        Assert(xs[i] == i / 5)
    }
    var it := xs.ChunksMut(20)
    a := it.Next().Must()
    b := it.Next().Must()
    Assert(it.Next().IsNone() && a.Len() == 20 && b.Len() == 3)
    a[0] = -1
    b[0] = -2
    Assert(xs[0] == -1 && xs[20] == -2)
}

test "Chunks of 0 panics [exit 101: Slice.Chunks: the piece length must be above 0]" (sys: &System) {
    xs := [1, 2]
    var it := xs.Chunks(0)
    _ = it.Next()
}

test "ChunksMut of -1 panics [exit 101: Slice.ChunksMut: the piece length must be above 0]" (sys: &System) {
    var xs := [1, 2]
    var it := xs.ChunksMut(-1)
    _ = it.Next()
}

// TvF64 are binary64 encodings of every class: zeros of both signs, the
// smallest and largest subnormals, the smallest normal, one, the largest
// finite, infinities, a quiet NaN, a signalling NaN with payload 1, a
// negative NaN with a full payload.
const TvF64: [14]u64 = [0x0, 0x8000000000000000, 0x1, 0x000FFFFFFFFFFFFF, 0x0010000000000000, 0x3FF0000000000000, 0x7FEFFFFFFFFFFFFF, 0x7FF0000000000000, 0xFFF0000000000000, 0x7FF8000000000000, 0x7FF0000000000001, 0xFFFFFFFFFFFFFFFF, 0x7FF4000000000000, 0xBFF0000000000001]

const TvF32: [12]u32 = [0x0, 0x80000000, 0x1, 0x007FFFFF, 0x00800000, 0x3F800000, 0x7F7FFFFF, 0x7F800000, 0xFF800000, 0x7FC00000, 0x7F800001, 0xFFFFFFFF]

// TvRoundTrips counts the encodings that come back unchanged; a const, so
// the evaluator computes it.
TvRoundTrips(): int {
    var n := 0
    for b in TvF64 {
        if f64.FromBits(b).Bits() == b {
            n += 1
        }
    }
    for b in TvF32 {
        if f32.FromBits(b).Bits() == b {
            n += 1
        }
    }
    return n
}

const TvEvalRoundTrips = TvRoundTrips()

test "Bits and FromBits round-trip every class, NaN payloads included, at run time and at compile time" (sys: &System) {
    Assert(TvRoundTrips() == 26)
    Assert(TvEvalRoundTrips == 26)
    one := 1.0
    Assert(one.Bits() == 0x3FF0000000000000)
    Assert((-0.0).Bits() == 0x8000000000000000)
    Assert(f64.FromBits(0x4000000000000000) == 2.0)
    h: f32 = 1.5
    Assert(h.Bits() == 0x3FC00000)
    m: f32 = -2.0
    Assert(f32.FromBits(0xC0000000) == m)
    n := f64.FromBits(0x7FF0000000000001)
    Assert(n != n)
}

test "Bits agrees with the raw intrinsic over a walk of the encodings" (sys: &System) {
    var b: u64 = 0x9E3779B97F4A7C15
    for _ in 0..10_000 {
        b = b *% 6364136223846793005 +% 1442695040888963407
        x := f64.FromBits(b)
        Assert(x.Bits() == b && raw.F64Bits(x) == b)
        c := Trunc[u32](b >> 32)
        Assert(f32.FromBits(c).Bits() == c)
    }
}
