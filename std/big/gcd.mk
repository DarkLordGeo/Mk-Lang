// Why: greatest common divisors and modular inverses (RSA keys, rationals)
// cost O(n^2) word operations at best, and plain Euclid, also O(n^2), runs
// 5 to 12 times slower than Lehmer's method (measured below).
//
// What: Int.Gcd and Int.ModInverse, over Lehmer, LehmerSim and the cosequence
// helpers LinSum and LinDiff.
//
// How: Lehmer's algorithm (Lehmer 1938; TAOCP vol. 2, 4.5.2, Algorithm L), as
// Go's lehmerGCD: Euclid's steps are run on the top word of the two
// remainders while Collins' condition proves them right (Jebelean 1995), and
// their 2-by-2 cosequence matrix is then applied to the whole remainders in
// one pass, so each pass over n words retires about a word of quotients.
// When no step can be proved, one full Euclid step with Algorithm D's
// remainder is taken (TAOCP 4.5.2, Algorithm A). The inverse keeps only the
// cofactor of x, whose signs alternate, so only magnitudes are stored and
// every cofactor update is a sum (TAOCP 4.5.2, Algorithm X; GMP's mpn_gcdext
// keeps the same invariant). Both run in O(n^2) word operations for n-word
// operands (measured: Euclid alone was 5 to 12 times slower than Go's Lehmer
// at 4 to 400 words). Not constant time.

import std.raw

// Cos is the cosequence of a run of Lehmer's single-word steps: the new
// remainders are a' = |u0| a - |v0| b and b' = |v1| b - |u1| a when the
// step count is even, and a' = |v0| b - |u0| a and b' = |u1| a - |v1| b
// when it is odd; v0 is zero when no step was proved.
struct Cos {
    u0: u64
    u1: u64
    v0: u64
    v1: u64
    even: bool
}

// LehmerSim runs Euclid's algorithm on the top 64 bits of a and the bits of
// b at the same positions, a >= b, b at least 2 words, while Collins'
// condition proves each quotient equals the full remainders' (Go's
// lehmerSimulate). The cosequence words cannot wrap: they are bounded by
// the 64-bit inputs (Jebelean 1995, 4.2).
LehmerSim(a: []u64, b: []u64): Cos {
    n := a.Len()
    m := b.Len()
    h := raw.Clz64(a[n - 1])
    var a1 := a[n - 1]
    var a2: u64 = 0
    if h == 0 {
        if n == m {
            a2 = b[n - 1]
        }
    } else {
        a1 = (a[n - 1] << h) | (a[n - 2] >> (64 - h))
        if n == m {
            a2 = (b[n - 1] << h) | (b[n - 2] >> (64 - h))
        } else if n == m + 1 {
            a2 = b[n - 2] >> (64 - h)
        }
    }
    var even := false
    var u0: u64 = 0
    var u1: u64 = 1
    var u2: u64 = 0
    var v0: u64 = 0
    var v1: u64 = 0
    var v2: u64 = 1
    for a2 >= v2 && a1 -% a2 >= v1 +% v2 {
        q := a1 / a2
        r := a1 -% q *% a2
        a1 = a2
        a2 = r
        u3 := u1 +% q *% u2
        u0 = u1
        u1 = u2
        u2 = u3
        v3 := v1 +% q *% v2
        v0 = v1
        v1 = v2
        v2 = v3
        even = !even
    }
    return Cos(u0: u0, u1: u1, v0: v0, v1: v1, even: even)
}

// LinDiff sets z = p x - q y over z.Len() words, the result known to lie in
// 0 to 2^(64 z.Len()) - 1; x and y have at least z.Len() words.
LinDiff(z: mut []u64, x: []u64, p: u64, y: []u64, q: u64) {
    n := z.Len()
    _ = MulAddVWW(z, x[..n], p, 0)
    _ = SubMulVVW(z, y[..n], q)
}

// LinSum sets z = p x + q y, z.Len() = n + 2 for x and y of n words.
LinSum(z: mut []u64, x: []u64, p: u64, y: []u64, q: u64) {
    n := x.Len()
    z[n] = MulAddVWW(mut z[..n], x, p, 0)
    z[n + 1] = 0
    c := AddMulVVW(mut z[..n], y, q)
    _ = AddW(mut z[n..], c)
}

// Lehmer is gcd(x, y) for normalized x >= y, x not zero, and with ext the
// magnitude t of the cofactor of x's remainder sequence start, in the
// sense t x' = g (mod x) for x' = y's initial value, and whether t is
// negative. Its buffers are in a.
Lehmer(a: &Arena, x: []u64, y: []u64, ext: bool): OutOfMemory!(g: []u64, t: []u64, neg: bool) from a {
    n := x.Len()
    var ra := try a.Slice[u64](n + 1)
    var rb := try a.Slice[u64](n + 1)
    var rc := try a.Slice[u64](n + 1)
    var rd := try a.Slice[u64](n + 1)
    q := try a.Slice[u64](n + 1)
    s := try a.Slice[u64](n + 1)
    tn := if ext { n + 2 } else { 0 }
    var t0 := try a.Slice[u64](tn)
    var t1 := try a.Slice[u64](tn)
    var t2 := try a.Slice[u64](tn)
    var t3 := try a.Slice[u64](tn)
    prod := try a.Slice[u64](tn)
    Copy(mut ra[..], x)
    Copy(mut rb[..], y)
    var la := n
    var lb := y.Len()
    var lt0 := 0
    var lt1 := 0
    if ext {
        // t0 = 0 for x, t1 = 1 for y; t0 holds the sign "negative" so the
        // signs alternate from the start.
        t1[0] = 1
        lt1 = 1
    }
    var neg0 := true
    for lb > 0 {
        if lb > 1 {
            c := LehmerSim(ra[..la], rb[..lb])
            if c.v0 != 0 {
                // Both new remainders fit in la words and are below a.
                if c.even {
                    LinDiff(mut rc[..la], ra[..], c.u0, rb[..], c.v0)
                    LinDiff(mut rd[..la], rb[..], c.v1, ra[..], c.u1)
                } else {
                    LinDiff(mut rc[..la], rb[..], c.v0, ra[..], c.u0)
                    LinDiff(mut rd[..la], ra[..], c.u1, rb[..], c.v1)
                }
                Swap(mut ra, mut rc)
                Swap(mut rb, mut rd)
                la = Norm(ra[..la]).Len()
                lb = Norm(rb[..la]).Len()
                if ext {
                    // The cofactors have opposite signs, as do u0 and v0, so
                    // each new magnitude is a sum.
                    lt := if lt0 > lt1 { lt0 } else { lt1 }
                    LinSum(mut t2[..lt + 2], t0[..lt], c.u0, t1[..lt], c.v0)
                    LinSum(mut t3[..lt + 2], t0[..lt], c.u1, t1[..lt], c.v1)
                    Swap(mut t0, mut t2)
                    Swap(mut t1, mut t3)
                    lt0 = Norm(t0[..lt + 2]).Len()
                    lt1 = Norm(t1[..lt + 2]).Len()
                    if !c.even {
                        neg0 = !neg0
                    }
                }
                continue
            }
        } else if la == 1 {
            break
        }
        // One full Euclid step: (a, b) = (b, a mod b), t2 = t0 + q t1.
        (lq, lr) := QuoRemInto(mut q[..], mut rc[..], ra[..la], rb[..lb], mut s[..])
        Clear(mut rc[lr..lb])
        Swap(mut ra, mut rb)
        Swap(mut rb, mut rc)
        la = lb
        lb = lr
        if ext {
            Clear(mut t2[..])
            Copy(mut t2[..], t0[..lt0])
            if lq > 0 && lt1 > 0 {
                Clear(mut prod[..])
                MulBasic(mut prod[..lq + lt1], q[..lq], t1[..lt1])
                _ = AddTo(mut t2[..], Norm(prod[..lq + lt1]))
            }
            Swap(mut t0, mut t1)
            Swap(mut t1, mut t2)
            lt0 = lt1
            lt1 = Norm(t1[..]).Len()
            neg0 = !neg0
        }
    }
    if lb == 1 {
        // a and b are single words: Euclid on words, the cofactor of a as
        // ua |t0| + va |t1|.
        var aw := ra[0]
        var bw := rb[0]
        var ua: u64 = 1
        var ub: u64 = 0
        var va: u64 = 0
        var vb: u64 = 1
        var steps := 0
        for bw != 0 {
            qw := aw / bw
            r := aw % bw
            aw = bw
            bw = r
            nu := ua +% qw *% ub
            ua = ub
            ub = nu
            nv := va +% qw *% vb
            va = vb
            vb = nv
            steps += 1
        }
        ra[0] = aw
        if ext {
            lt := if lt0 > lt1 { lt0 } else { lt1 }
            LinSum(mut t2[..lt + 2], t0[..lt], ua, t1[..lt], va)
            Swap(mut t0, mut t2)
            lt0 = Norm(t0[..lt + 2]).Len()
            if steps % 2 == 1 {
                neg0 = !neg0
            }
        }
    }
    return (g: ra[..la], t: t0[..lt0], neg: neg0)
}

/// gcd(|x|, |y|), never negative; Gcd of zero and zero is zero. Fails with
/// OutOfMemory when a cannot give the words.
///
/// For operands of n >= m words, in either order, reducing the longer
/// modulo the shorter costs O(n m) word operations when n > m + 1, or
/// O(n / m M(m) log m) from 60 words of the shorter, M being the product's
/// cost. The gcd loop then costs O(m^2). It allocates about 6m words in a
/// for the loop. The reduction allocates about 2n words in a up to n = 2m;
/// above that, m words in a and at most 3 (m + max(m, 256)) + 2 in a
/// sub-arena freed before the gcd loop starts.
export Int.Gcd(&self, a: &Arena, y: &Int): OutOfMemory!Int from a {
    if CmpVV(self.mag, y.mag) < 0 {
        return Make(try GcdMag(a, y.mag, self.mag), false)
    }
    if self.mag.Len() == 0 {
        return try Zero(a)
    }
    return Make(try GcdMag(a, self.mag, y.mag), false)
}

// GcdMag is gcd(x, y) for normalized x >= y, x not zero. When x is more
// than a word longer than y, Lehmer's simulation cannot start (its second
// top word is zero), so its first step would be one Algorithm D division
// of x by y at any size; x is reduced modulo y first instead, by RemMag
// (recursive division from DivRecWords words of y), and Lehmer's buffers
// then take y's length, not x's.
GcdMag(a: &Arena, x: []u64, y: []u64): OutOfMemory![]u64 from a {
    if y.Len() > 0 && x.Len() > y.Len() + 1 {
        r := try RemMag(a, x, y)
        (g, t: _, neg: _) := try Lehmer(a, y, r, false)
        return g
    }
    (g, t: _, neg: _) := try Lehmer(a, x, y, false)
    return g
}

/// The inverse of x modulo |m|, in 0 <= r < |m|, or none when x and m share
/// a factor. Panics when m is zero. Fails with OutOfMemory when a cannot
/// give the words.
///
/// It costs O(x.Len() * n + n^2) word operations for an n-word m, as x is
/// first reduced modulo m. It allocates about 13n words in a, however long
/// x is. Reducing an x of more than 2n words holds at most
/// 3 (n + max(n, 256)) + 2 words more, and a recursive division's own, in a
/// sub-arena freed before the gcd loop starts.
export Int.ModInverse(&self, a: &Arena, m: &Int): OutOfMemory!?Int from a {
    if m.mag.Len() == 0 {
        Panic("big.Int.ModInverse: zero modulus")
    }
    mm := m.mag
    n := mm.Len()
    if n == 1 && mm[0] == 1 {
        return some(try Zero(a))
    }
    // r1 = x mod |m|, made non-negative.
    xr := try RemMag(a, self.mag, mm)
    var r1 := xr
    if self.negative && xr.Len() > 0 {
        w := try a.Slice[u64](n)
        Copy(mut w[..], xr)
        v := try a.Slice[u64](n)
        _ = SubVV(mut v[..], mm, w[..])
        r1 = Norm(v[..])
    }
    // gcd(m, r1) = t r1 (mod m), t's magnitude and sign from Lehmer.
    (g, t, neg) := try Lehmer(a, mm, r1, true)
    if g.Len() != 1 || g[0] != 1 {
        return none
    }
    if neg && t.Len() > 0 {
        w := try a.Slice[u64](n)
        Copy(mut w[..], t)
        z := try a.Slice[u64](n)
        _ = SubVV(mut z[..], mm, w[..])
        return some(Make(z[..], false))
    }
    return some(Make(t, false))
}
