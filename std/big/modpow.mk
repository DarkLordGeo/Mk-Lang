// Why: modular exponentiation is the core of RSA and Diffie-Hellman, and is
// thousands of multiplications per call, so each must avoid a division.
//
// What: Int.ModPow, with Montgomery multiplication (MontMul, MontInv,
// RSquared), the window loop (PowRun, WindowBits) and the even-modulus split
// (PowOdd, PowLow).
//
// How: left-to-right sliding-window exponentiation (HAC 14.85) over the
// exponent's bits. For an odd modulus its products are in Montgomery form
// (Montgomery 1985; the CIOS loop of Koc, Acar and Kaliski 1996 with its two
// products fused into one pass, their FIOS form, as Go's montgomery and
// GMP's redc_1). An even modulus m1 2^k takes one power modulo the odd m1
// and one modulo 2^k (the low halves of products) and joins them (Garner),
// as Go's expNNMontgomeryEven. The window width follows the exponent's
// length (OpenSSL's BN_window_bits_for_exponent_size). Not constant time:
// the sequence of squarings and multiplications follows the exponent's bits.

import std.raw

// WindowBits is the sliding window's width for an exponent of bits bits.
WindowBits(bits: int): int {
    if bits > 671 {
        return 6
    }
    if bits > 239 {
        return 5
    }
    if bits > 79 {
        return 4
    }
    if bits > 23 {
        return 3
    }
    return 1
}

// ExpBit is bit i of the magnitude e.
ExpBit(e: []u64, i: int): u64 {
    return (e[i >> 6] >> (i & 63)) & 1
}

// CmpN orders x and y of equal length as numbers, zero top words included.
CmpN(x: []u64, y: []u64): int {
    var i := x.Len() - 1
    for i >= 0 {
        if x[i] != y[i] {
            return if x[i] < y[i] { -1 } else { 1 }
        }
        i -= 1
    }
    return 0
}

// MontInv is -m0^-1 mod 2^64 for odd m0, by Newton's iteration, each step
// doubling the correct low bits from the 3 of m0 itself (Dusse and Kaliski).
MontInv(m0: u64): u64 {
    var inv := m0
    for _ in 0..5 {
        inv = inv *% (2 -% m0 *% inv)
    }
    return 0 -% inv
}

// MontMul sets out = x y R^-1 mod m, R = 2^(64 n), for x, y < m of n words
// each and odd m of n words, with t of 2n words as scratch and k0 =
// MontInv(m[0]). out may not overlap x or y.
MontMul(out: mut []u64, x: []u64, y: []u64, m: []u64, k0: u64, t: mut []u64) {
    n := m.Len()
    xs := x[..n]
    ms := m[..n]
    Clear(t)
    var c: u64 = 0
    for i in 0..n {
        // t += x y[i] + m q in one pass, q chosen so the low word becomes
        // zero: two carry chains, each as in AddMulVVW, interleaved.
        yi := y[i]
        q := (t[i] +% xs[0] *% yi) *% k0
        ts := mut t[i..i + n]
        var ca: u64 = 0
        var cb: u64 = 0
        for j in 0..n {
            pa := raw.Mul128(xs[j], yi)
            pb := raw.Mul128(ms[j], q)
            t1 := ts[j] +% pa.lo
            ha := pa.hi +% Bit1(t1 < pa.lo)
            s1 := t1 +% ca
            ca = ha +% Bit1(s1 < ca)
            t2 := s1 +% pb.lo
            hb := pb.hi +% Bit1(t2 < pb.lo)
            s2 := t2 +% cb
            cb = hb +% Bit1(s2 < cb)
            ts[j] = s2
        }
        cx := c +% ca
        cy := cx +% cb
        t[n + i] = cy
        c = Bit1(cx < ca || cy < cb)
    }
    // The sum is below 2m (each input below m), so one subtraction of m
    // brings it below m.
    if c != 0 || CmpN(t[n..2 * n], m) >= 0 {
        _ = SubVV(out, t[n..2 * n], m)
    } else {
        Copy(out, t[n..2 * n])
    }
}

// Pow is the state of one exponentiation: the modulus, the scratch, and the
// product that replaces acc by acc * y mod m. odd: Montgomery products
// modulo the odd m. Otherwise products modulo 2^(64 n - s), n = m.Len(),
// s the zero bits mask leaves on top (m holds no value then).
struct Pow {
    m: []u64
    k0: u64
    odd: bool
    mask: u64
    t: mut []u64
    u: mut []u64
}

// PowMul sets out = x * y mod m (times R^-1 when the modulus is odd), x and
// y below m, n words each; out may not overlap them.
PowMul(p: mut Pow, out: mut []u64, x: []u64, y: []u64) {
    if p.odd {
        MontMul(out, x, y, p.m, p.k0, mut p.t[..])
        return
    }
    // Modulo a power of two: the low words of the product, masked.
    n := p.m.Len()
    MulNat(mut p.t[..2 * n], x, y, mut p.u[..])
    Copy(out, p.t[..n])
    out[n - 1] = out[n - 1] & p.mask
}

// PowRun is base^e by the sliding window over p's products, base below m
// in n words (in Montgomery form when p is odd), e of bits bits, bits > 0;
// the result is n words in a.
PowRun(a: &Arena, p: mut Pow, base: []u64, e: []u64, bits: int): OutOfMemory![]u64 from a {
    n := base.Len()
    w := WindowBits(bits)
    size := 1 << (w - 1)
    // table[i] holds base^(2i + 1), n words each.
    table := try a.Slice[u64](size * n)
    Copy(mut table[..n], base)
    if size > 1 {
        sq := try a.Slice[u64](n)
        PowMul(p, mut sq[..], base, base)
        for i in 1..size {
            (left: prev, right: cur) := table.SplitAt(i * n)
            PowMul(p, mut cur[..n], prev[(i - 1) * n..], sq[..])
        }
    }
    var acc := try a.Slice[u64](n)
    var tmp := try a.Slice[u64](n)
    var started := false
    var i := bits - 1
    for i >= 0 {
        if ExpBit(e, i) == 0 {
            if started {
                PowMul(p, mut tmp[..], acc[..], acc[..])
                Swap(mut acc, mut tmp)
            }
            i -= 1
            continue
        }
        // The window [j, i]: at most w bits, ending in a one.
        var j := if i - w + 1 > 0 { i - w + 1 } else { 0 }
        for ExpBit(e, j) == 0 {
            j += 1
        }
        var val := 0
        for k in 0..i - j + 1 {
            val = (val << 1) | Cast[int](ExpBit(e, i - k))
        }
        entry := table[(val >> 1) * n..(val >> 1) * n + n]
        if started {
            for _ in 0..i - j + 1 {
                PowMul(p, mut tmp[..], acc[..], acc[..])
                Swap(mut acc, mut tmp)
            }
            PowMul(p, mut tmp[..], acc[..], entry)
            Swap(mut acc, mut tmp)
        } else {
            Copy(mut acc[..], entry)
            started = true
        }
        i = j - 1
    }
    return acc[..]
}

// PowOdd is base^e mod m for odd m of n words, base below m in n words, e
// of bits bits, bits > 0; n words in a.
PowOdd(a: &Arena, base: []u64, e: []u64, bits: int, m: []u64): OutOfMemory![]u64 from a {
    n := m.Len()
    var p := Pow(m: m, k0: MontInv(m[0]), odd: true, mask: 0, t: try a.Slice[u64](2 * n), u: try a.Slice[u64](0))
    // Into Montgomery form: base R mod m, as base * R^2 mod m times R^-1.
    rr := try RSquared(a, m)
    mb := try a.Slice[u64](n)
    MontMul(mut mb[..], base, rr, m, p.k0, mut p.t[..])
    acc := try PowRun(a, mut p, mb[..], e, bits)
    // Out of Montgomery form: acc * 1 * R^-1.
    one := try a.Slice[u64](n)
    one[0] = 1
    out := try a.Slice[u64](n)
    MontMul(mut out[..], acc, one[..], m, p.k0, mut p.t[..])
    return out[..]
}

// PowLow is x^e mod 2^k, k > 0, for e of bits bits, bits > 0; ceil(k / 64)
// words in a.
PowLow(a: &Arena, x: []u64, e: []u64, bits: int, k: int): OutOfMemory![]u64 from a {
    n := (k + 63) / 64
    mask: u64 = if k % 64 == 0 { 0xFFFFFFFFFFFFFFFF } else { (1 << (k % 64)) - 1 }
    base := try a.Slice[u64](n)
    m := if x.Len() < n { x.Len() } else { n }
    Copy(mut base[..m], x[..m])
    base[n - 1] = base[n - 1] & mask
    var p := Pow(m: base[..], k0: 0, odd: false, mask: mask,
        t: try a.Slice[u64](2 * n), u: try a.Slice[u64](MulScratch(n, n)))
    return try PowRun(a, mut p, base[..], e, bits)
}

/// x^e mod |m|, in 0 <= r < |m| whatever x's sign. Panics when m is zero or
/// e is negative; for a negative power, take ModInverse first. Fails with
/// OutOfMemory when a cannot give the words. Not constant time: the
/// sequence of operations follows e's bits, so never pass a secret
/// exponent.
///
/// O(x.Len() * n + e.BitLen() * n^2) word operations for an n-word m: x is
/// first reduced modulo m, then it takes one Montgomery product per bit of
/// e and one per window. An even m = m1 2^k takes one such power modulo m1
/// and one modulo 2^k, and joins them by the Chinese remainder theorem. A
/// caller bounds x, e and m to bound the time.
///
/// However long x and e are, it allocates in a about 40n words for an odd
/// m (a window table of up to 32 values and the scratch) and about 100n for
/// an even one (both powers, a ModInverse modulo 2^k and the join's
/// products). Reducing an x of more than 2n words holds at most 3 (n +
/// max(n, 256)) + 2 words more, and a recursive division's own, in a
/// sub-arena freed before the power starts.
export Int.ModPow(&self, a: &Arena, e: &Int, m: &Int): OutOfMemory!Int from a {
    if m.mag.Len() == 0 {
        Panic("big.Int.ModPow: zero modulus")
    }
    if e.negative {
        Panic("big.Int.ModPow: negative exponent")
    }
    mm := m.mag
    n := mm.Len()
    if n == 1 && mm[0] == 1 {
        return try Zero(a)
    }
    // base = x mod m, Euclidean, in n words.
    base := try a.Slice[u64](n)
    r := try RemMag(a, self.mag, mm)
    if self.negative && r.Len() > 0 {
        wide := try a.Slice[u64](n)
        Copy(mut wide[..], r)
        _ = SubVV(mut base[..], mm, wide[..])
    } else {
        Copy(mut base[..], r)
    }
    bits := e.BitLen()
    if bits == 0 {
        one := try a.Slice[u64](1)
        one[0] = 1
        return Make(one[..], false)
    }
    if mm[0] & 1 == 1 {
        return Make(try PowOdd(a, base[..], e.mag, bits, mm), false)
    }
    // m = m1 2^k, m1 odd: r2 = x^e mod 2^k, r1 = x^e mod m1, and
    // r = r1 + m1 ((r2 - r1) m1^-1 mod 2^k), below m1 2^k (Garner; Go's
    // expNNMontgomeryEven).
    var z := 0
    for mm[z] == 0 {
        z += 1
    }
    k := 64 * z + raw.Ctz64(mm[z])
    r2 := Make(try PowLow(a, base[..], e.mag, bits, k), false)
    mabs := Make(mm, false)
    m1 := try mabs.Shr(a, k)
    if m1.mag.Len() == 1 && m1.mag[0] == 1 {
        return r2
    }
    b1 := try a.Slice[u64](m1.mag.Len())
    Copy(mut b1[..], try ModMag(a, base[..], m1.mag))
    r1 := Make(try PowOdd(a, b1[..], e.mag, bits, m1.mag), false)
    one := try FromI64(a, 1)
    p2 := try one.Shl(a, k)
    inv := (try m1.ModInverse(a, &p2)) else {
        Panic("big.Int.ModPow: an odd number has no inverse modulo a power of two")
    }
    d := try (try r2.Sub(a, &r1)).Mod(a, &p2)
    t := try (try d.Mul(a, &inv)).Mod(a, &p2)
    return try (try m1.Mul(a, &t)).Add(a, &r1)
}

// RSquared is R^2 mod m, R = 2^(64 n), n words, in a.
RSquared(a: &Arena, m: []u64): OutOfMemory![]u64 from a {
    n := m.Len()
    x := try a.Slice[u64](2 * n + 1)
    x[2 * n] = 1
    r := try ModMag(a, x[..], m)
    z := try a.Slice[u64](n)
    Copy(mut z[..], r)
    return z[..]
}
