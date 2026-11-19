// Why: tests, simulations and shuffles need a fast generator whose
// sequence for a seed is the same on every target, at compile time and in
// every release.
//
// What: Rng (New, U64, Below, Float, Fill), RngFromBytes and SplitMix.
// Rng.FromIo is in io_rng.mk, since it needs Io.
//
// How: xoshiro256** (Blackman and Vigna, 2018, and their
// xoshiro256starstar.c) seeded through SplitMix64 (Steele, Lea and Flood
// 2014). Plain state, no Io, nothing allocates. Rng is not a cryptographic
// generator: whoever sees 4 outputs knows the state.

import std.raw

/// A pseudo-random generator, xoshiro256**: one seed gives the same sequence on
/// every target. It is not for secrets; use crypto.Random for those. Plain, 32
/// bytes: four state words, never all zero.
export struct Rng {
    s0: u64
    s1: u64
    s2: u64
    s3: u64
}

// SplitMix64 step: advances x by the golden gamma and returns the mixed
// value (Vigna's splitmix64.c, the seeder xoshiro's authors recommend).
SplitMix(x: mut u64): u64 {
    *x = *x +% 0x9E3779B97F4A7C15
    var z := *x
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

/// A generator whose state is four successive SplitMix64 outputs from seed.
/// Every seed is valid, 0 included: the four outputs are never all zero. O(1);
/// allocates nothing.
export Rng.New(seed: u64): Rng {
    var x := seed
    a := SplitMix(mut x)
    b := SplitMix(mut x)
    c := SplitMix(mut x)
    d := SplitMix(mut x)
    return Rng(s0: a, s1: b, s2: c, s3: d)
}

/// The next 64 random bits. The sequence repeats after 2^256 - 1 draws. O(1);
/// allocates nothing.
export Rng.U64(mut self): u64 {
    r := raw.RotL64(self.s1 *% 5, 7) *% 9
    t := self.s1 << 17
    self.s2 = self.s2 ^ self.s0
    self.s3 = self.s3 ^ self.s1
    self.s1 = self.s1 ^ self.s2
    self.s0 = self.s0 ^ self.s3
    self.s2 = self.s2 ^ t
    self.s3 = raw.RotL64(self.s3, 45)
    return r
}

/// A uniform integer from 0 to n - 1, with no bias, by Lemire's method: one
/// multiplication per draw, and a division only in the rare case that the draw
/// lands near the biased band. Fewer than 2 draws are expected for every n, and
/// 1 + n / 2^64 on average. n == 0 panics with code 8. It allocates nothing.
export Rng.Below(mut self, n: u64): u64 {
    if n == 0 {
        raw.Fail(8, "Rng.Below(0)")
    }
    var m := raw.Mul128(self.U64(), n)
    if m.lo < n {
        // 2^64 mod n, as (2^64 - n) mod n.
        t := (0 -% n) % n
        for m.lo < t {
            m = raw.Mul128(self.U64(), n)
        }
    }
    return m.hi
}

/// A uniform f64 in [0, 1): the top 53 bits of U64 times 2^-53, so every result
/// is a multiple of 2^-53 and 1 is never returned. O(1); allocates nothing.
export Rng.Float(mut self): f64 {
    // 2^-53 is 0x3CA0000000000000.
    return Cast[f64](self.U64() >> 11) * f64.FromBits(0x3CA0000000000000)
}

/// Fills buf with random bytes: U64 outputs in little-endian order, 8 bytes
/// each, and for a tail of 1 to 7 bytes the low bytes of one more output. The
/// bytes are the same on every target. O(n); allocates nothing.
export Rng.Fill(mut self, buf: mut []u8) {
    n := buf.Len()
    var i := 0
    for i + 8 <= n {
        x := self.U64()
        buf[i] = Trunc[u8](x)
        buf[i + 1] = Trunc[u8](x >> 8)
        buf[i + 2] = Trunc[u8](x >> 16)
        buf[i + 3] = Trunc[u8](x >> 24)
        buf[i + 4] = Trunc[u8](x >> 32)
        buf[i + 5] = Trunc[u8](x >> 40)
        buf[i + 6] = Trunc[u8](x >> 48)
        buf[i + 7] = Trunc[u8](x >> 56)
        i += 8
    }
    if i < n {
        var x := self.U64()
        for i < n {
            buf[i] = Trunc[u8](x)
            x = x >> 8
            i += 1
        }
    }
}

// RngFromBytes is the state of the 32 bytes b (little-endian words), or
// New(0)'s when they are all zero (rand_xoshiro's seed_from rule): FromIo's
// half that is layer 0.
RngFromBytes(b: &[32]u8): Rng {
    a := Le64(b, 0)
    c := Le64(b, 8)
    d := Le64(b, 16)
    e := Le64(b, 24)
    if (a | c | d | e) == 0 {
        return Rng.New(0)
    }
    return Rng(s0: a, s1: c, s2: d, s3: e)
}

Le64(b: &[32]u8, at: int): u64 {
    var x: u64 = 0
    var k := 7
    for k >= 0 {
        x = (x << 8) | u64(b[at + k])
        k -= 1
    }
    return x
}
