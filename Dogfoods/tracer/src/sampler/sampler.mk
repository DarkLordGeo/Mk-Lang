// Why: A render must give the same bits on any number of carriers, on any
// target, in any profile.
//
// What: Sampler, with ForPixel, Next, Stratum, Disk and Sphere.
//
// How: Every random number is a function of the scene seed, the pixel and the
// sample index, hashed with SplitMix64's finalizer, so no state passes between
// pixels.

import std.math

/// The random stream of one sample of one pixel.
export struct Sampler {
    state: u64
    inc: u64
    index: u32
    scramble: u32
}

// SplitMix64's finalizer (Steele, Lea and Flood, "Fast Splittable Pseudorandom
// Number Generators", OOPSLA 2014): a bijection on u64 with full avalanche.
Mix64(var x: u64): u64 {
    x = (x ^ (x >> 30)) *% 0xBF58476D1CE4E5B9
    x = (x ^ (x >> 27)) *% 0x94D049BB133111EB
    return x ^ (x >> 31)
}

/// Hashes the seed, a pixel and a sample index into an independent stream.
/// O(1); allocates nothing.
export Sampler.ForPixel(seed: u64, pixel: int, sample: int): Sampler {
    key := Mix64(seed ^ math.RotateLeft64(Cast[u64](pixel), 17))
    salt := Mix64(key ^ 0x9E3779B97F4A7C15)
    return Sampler(state: Mix64(key +% Cast[u64](sample)), inc: salt | 1, index: Cast[u32](sample), scramble: Trunc[u32](salt >> 32))
}

/// The next 32 random bits: PCG-XSH-RR (O'Neill, "PCG: A Family of Simple Fast
/// Space-Efficient Statistically Good Algorithms", 2014). O(1).
export Sampler.Bits(mut self): u32 {
    old := self.state
    self.state = old *% 6364136223846793005 +% self.inc
    shifted := Trunc[u32](((old >> 18) ^ old) >> 27)
    rot := Cast[int](old >> 59)
    return math.RotateRight32(shifted, rot)
}

/// A uniform number in [0, 1) with 32 random bits. O(1).
export Sampler.Next(mut self): f64 {
    return math.LdExp(f64(self.Bits()), -32)
}

/// The sample's position in the scrambled van der Corput sequence, which
/// stratifies the first dimension of the samples of one pixel. O(1).
export Sampler.Stratum(&self): f64 {
    return math.LdExp(f64(math.ReverseBits32(self.index) ^ self.scramble), -32)
}

/// A point on the unit disk, by Shirley and Chiu's concentric map ("A
/// Low-Distortion Map Between Disk and Square", JGT 1997). O(1).
export Sampler.Disk(mut self): (x: f64, y: f64) {
    a := 2.0 * self.Next() - 1.0
    b := 2.0 * self.Next() - 1.0
    if a == 0.0 && b == 0.0 {
        return (x: 0.0, y: 0.0)
    }
    if math.Abs(a) > math.Abs(b) {
        phi := math.Pi / 4.0 * (b / a)
        return (x: a * math.Cos(phi), y: a * math.Sin(phi))
    }
    phi := math.Pi / 2.0 - math.Pi / 4.0 * (a / b)
    return (x: b * math.Cos(phi), y: b * math.Sin(phi))
}

/// A direction on the unit sphere, uniform by area. O(1).
export Sampler.Sphere(mut self): (x: f64, y: f64, z: f64) {
    z := 1.0 - 2.0 * self.Next()
    r := math.Sqrt(math.Max(0.0, 1.0 - z * z))
    phi := 2.0 * math.Pi * self.Next()
    return (x: r * math.Cos(phi), y: r * math.Sin(phi), z: z)
}

test "a pixel's stream does not depend on what ran before" {
    var a := Sampler.ForPixel(7, 1234, 3)
    var b := Sampler.ForPixel(7, 99, 0)
    _ = b.Bits()
    var c := Sampler.ForPixel(7, 1234, 3)
    Assert(a.Bits() == c.Bits())
}

test "Next stays in [0, 1)" {
    var s := Sampler.ForPixel(1, 2, 3)
    for _ in 0..1000 {
        x := s.Next()
        Assert(x >= 0.0 && x < 1.0)
    }
}

test "the strata of the first eight samples are in distinct eighths" {
    var seen: [8]bool = [false; 8]
    for i in 0..8 {
        s := Sampler.ForPixel(5, 0, i)
        k := Cast[int](math.Floor(s.Stratum() * 8.0))
        seen[k] = true
    }
    for b in seen {
        Assert(b)
    }
}
