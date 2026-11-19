// Why: Rng's sequence for a seed is a promise across targets and releases.
// What: test blocks pinning SplitMix64 seeding, the first outputs, Below,
// Float, Fill, RngFromBytes and FromIo, at compile time and at run time.
// How: run by `mk test --std=std.math`; the full sequences are checked
// against a Go model of xoshiro256** in std/std-missing_rng_test.go.

// Vigna's splitmix64.c from seed 0: the first output is 0xE220A8397B1DCDAF.
test "Rng seeds through SplitMix64" {
    var x: u64 = 0
    Assert(SplitMix(mut x) == 0xE220A8397B1DCDAF)
    Assert(SplitMix(mut x) == 0x6E789E6AA1B965F4)
    r := Rng.New(0)
    Assert(r.s0 == 0xE220A8397B1DCDAF)
    Assert(r.s1 == 0x6E789E6AA1B965F4)
}

// xoshiro256** from the state 1, 2, 3, 4: 11520, 0, 1509978240,
// 1215971899390074240 (the reference implementation's first outputs).
test "Rng xoshiro256** matches the reference" {
    var r := Rng(s0: 1, s1: 2, s2: 3, s3: 4)
    Assert(r.U64() == 11520)
    Assert(r.U64() == 0)
    Assert(r.U64() == 1509978240)
    Assert(r.U64() == 1215971899390074240)
}

test "Rng Below stays below n and covers it" {
    var r := Rng.New(42)
    var seen: [7]bool = [false; 7]
    for _ in 0..1000 {
        x := r.Below(7)
        Assert(x < 7)
        seen[Cast[int](x)] = true
    }
    for i in 0..7 {
        Assert(seen[i])
    }
    for _ in 0..100 {
        Assert(r.Below(1) == 0)
    }
    big: u64 = 0x8000000000000001
    for _ in 0..100 {
        Assert(r.Below(big) < big)
    }
    all_ones: u64 = 0xFFFF_FFFF_FFFF_FFFF
    for _ in 0..100 {
        Assert(r.Below(all_ones) < all_ones)
    }
}

test "Rng Float is in [0, 1)" {
    var r := Rng.New(7)
    for _ in 0..10000 {
        f := r.Float()
        Assert(f >= 0.0 && f < 1.0)
    }
    // The all-zero state draws 0 for ever, the bottom of the range.
    var s := Rng(s0: 0, s1: 0, s2: 0, s3: 0)
    Assert(s.Float() == 0.0)
}

test "Rng Fill is U64 in little-endian order, tail included" {
    var a := Rng.New(9)
    var b := Rng.New(9)
    var buf: [13]u8 = [0; 13]
    a.Fill(mut buf[..])
    x := b.U64()
    y := b.U64()
    for i in 0..8 {
        Assert(buf[i] == Trunc[u8](x >> (8 * i)))
    }
    for i in 0..5 {
        Assert(buf[8 + i] == Trunc[u8](y >> (8 * i)))
    }
    // An empty buffer draws nothing.
    var c := Rng.New(9)
    var empty: [0]u8 = []
    c.Fill(mut empty[..])
    Assert(c.U64() == Rng.New(9).U64())
}

test "RngFromBytes reads little-endian words, and 32 zero bytes seed as New(0)" {
    var z: [32]u8 = [0; 32]
    r := RngFromBytes(&z)
    q := Rng.New(0)
    Assert(r.s0 == q.s0 && r.s1 == q.s1 && r.s2 == q.s2 && r.s3 == q.s3)
    var o: [32]u8 = [0; 32]
    o[0] = 1
    o[31] = 0x80
    p := RngFromBytes(&o)
    Assert(p.s0 == 1 && p.s1 == 0 && p.s2 == 0 && p.s3 == 0x8000000000000000)
}

test "Rng FromIo gives a working generator" (sys: &System) {
    var r := Rng.FromIo(sys.io) else {
        Assert(false)
        return
    }
    Assert(r.Below(10) < 10)
}

// Compile-time evaluation gives the same sequence.
const FirstOfSeed5 = Rng.New(5).U64()

test "Rng.U64 gives the same first draw at compile time and run time" {
    var r := Rng.New(5)
    Assert(r.U64() == FirstOfSeed5)
}

// The evaluator against compiled code for every Rng operation,
// not only U64: Below (raw.Mul128 and the `(0 -% n) % n` rejection band:
// n = 2^63 + 1 rejects about half the draws), Float (Cast[f64] and
// f64.FromBits) and Fill (the little-endian stores and a 5-byte tail).
struct RngDraws {
    u: [16]u64
    b: [5]u64
    f: [8]f64
    fill: [21]u8
}

RngDrawsOf(seed: u64): RngDraws {
    var r := Rng.New(seed)
    var d := RngDraws(u: [0; 16], b: [0; 5], f: [0.0; 8], fill: [0; 21])
    for i in 0..16 {
        d.u[i] = r.U64()
    }
    ns: [5]u64 = [1, 7, 1000, 0x8000_0000_0000_0001, 0xFFFF_FFFF_FFFF_FFFF]
    for i in 0..5 {
        d.b[i] = r.Below(ns[i])
    }
    // Many draws at the worst n, so the rejection loop runs at compile time.
    for _ in 0..64 {
        d.b[3] = d.b[3] ^ r.Below(0x8000_0000_0000_0001)
    }
    for i in 0..8 {
        d.f[i] = r.Float()
    }
    r.Fill(mut d.fill[..])
    return d
}

const Draws5 = RngDrawsOf(5)
const Draws0 = RngDrawsOf(0)

RngDrawsEqual(x: &RngDraws, y: &RngDraws): bool {
    for i in 0..16 {
        if x.u[i] != y.u[i] {
            return false
        }
    }
    for i in 0..5 {
        if x.b[i] != y.b[i] {
            return false
        }
    }
    for i in 0..8 {
        if x.f[i] != y.f[i] {
            return false
        }
    }
    for i in 0..21 {
        if x.fill[i] != y.fill[i] {
            return false
        }
    }
    return true
}

test "Rng gives the same U64, Below, Float and Fill draws at compile time and run time" {
    var seed: u64 = 5
    r5 := RngDrawsOf(seed)
    Assert(RngDrawsEqual(&r5, &Draws5))
    seed = 0
    r0 := RngDrawsOf(seed)
    Assert(RngDrawsEqual(&r0, &Draws0))
    Assert(!RngDrawsEqual(&r0, &r5))
    // Below(1) is always 0; Float is in [0, 1).
    Assert(Draws5.b[0] == 0 && Draws5.b[1] < 7 && Draws5.b[2] < 1000)
    for i in 0..8 {
        Assert(Draws5.f[i] >= 0.0 && Draws5.f[i] < 1.0)
    }
}

// FromIo is a cancellation point: Canceled in a cancelled
// scope, and nothing is drawn.
RngFromIoCanceled(io: Io): bool {
    _ = Rng.FromIo(io) else {
        return true
    }
    return false
}

test "Rng.FromIo in a cancelled scope is Canceled" (sys: &System) {
    var c := false
    var nc := true
    scope s := sys.io.Scope() {
        s.Cancel()
        c = RngFromIoCanceled(sys.io)
        nc = RngFromIoCanceled(sys.io.NoCancel())
    }
    Assert(c && !nc)
}
