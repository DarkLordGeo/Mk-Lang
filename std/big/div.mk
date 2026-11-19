// Why: division is the costliest basic operation of big integers and the
// base of Mod, Text, Gcd and ModPow.
//
// What: the division kernels: DivWide, Reciprocal, DivRec, DivVW, ModVW,
// DivKnuth, DivNat and QuoRemInto, behind Int.DivMod and Int.Mod.
//
// How: truncated division, and the Euclidean remainder. A word divisor is a
// loop of 2-by-1 divisions by a precomputed reciprocal (Moller and Granlund,
// "Improved division by invariant integers", IEEE Trans. Computers 60(2),
// 2011; Go's math/big divWW); a longer one is Knuth's Algorithm D (TAOCP
// vol. 2, 4.3.1) with the 3-by-2 refinement of the quotient digit, as GMP's
// mpn_sbpi1_div_qr and Go's divBasic do. Not constant time.

import std.raw

// DivWide is the quotient and remainder of (hi * 2^64 + lo) / y, hi < y,
// by two 64-by-64 divisions on 32-bit halves (Hacker's Delight 9-4, divlu;
// Go's math/bits.Div64). Used only to make a reciprocal and for one-word
// operands: the reciprocal path, DivRec, is the hot one.
DivWide(hi: u64, lo: u64, y: u64): (q: u64, r: u64) {
    if hi == 0 {
        return (q: lo / y, r: lo % y)
    }
    s := raw.Clz64(y)
    d := y << s
    yn1 := d >> 32
    yn0 := d & 0xFFFFFFFF
    un32 := if s == 0 { hi } else { (hi << s) | (lo >> (64 - s)) }
    un10 := lo << s
    un1 := un10 >> 32
    un0 := un10 & 0xFFFFFFFF
    var q1 := un32 / yn1
    var rhat := un32 -% q1 *% yn1
    for q1 >= 0x100000000 || q1 *% yn0 > (rhat << 32) +% un1 {
        q1 -= 1
        rhat += yn1
        if rhat >= 0x100000000 {
            break
        }
    }
    un21 := (un32 << 32) +% un1 -% q1 *% d
    var q0 := un21 / yn1
    rhat = un21 -% q0 *% yn1
    for q0 >= 0x100000000 || q0 *% yn0 > (rhat << 32) +% un0 {
        q0 -= 1
        rhat += yn1
        if rhat >= 0x100000000 {
            break
        }
    }
    return (q: (q1 << 32) +% q0, r: ((un21 << 32) +% un0 -% q0 *% d) >> s)
}

// Reciprocal is floor((2^128 - 1) / d) - 2^64 for d with its top bit set,
// the m of Moller and Granlund's 2-by-1 division.
Reciprocal(d: u64): u64 {
    return DivWide(~d, 0xFFFFFFFFFFFFFFFF, d).q
}

// DivRec is (hi * 2^64 + lo) / d and its remainder, for d with its top bit
// set, hi < d and m = Reciprocal(d): the quotient estimate is the high word
// of m * hi + (hi * 2^64 + lo), at most two below the quotient (Go's
// divWW).
DivRec(hi: u64, lo: u64, d: u64, m: u64): (q: u64, r: u64) {
    t := raw.Mul128(m, hi)
    s := t.lo +% lo
    var q := t.hi +% hi +% Bit1(s < lo)
    p := raw.Mul128(d, q)
    var r := lo -% p.lo
    r1 := hi -% p.hi -% Bit1(lo < p.lo)
    if r1 != 0 {
        q += 1
        r = r -% d
    }
    if r >= d {
        q += 1
        r = r -% d
    }
    return (q, r)
}

// DivVW sets z = x / y over z.Len() words (z may be shorter than x when x's
// top words are below y) and returns the remainder; y is not zero.
DivVW(z: mut []u64, x: []u64, y: u64): u64 {
    n := x.Len()
    s := raw.Clz64(y)
    d := y << s
    m := Reciprocal(d)
    var r: u64 = 0
    var i := n - 1
    if s == 0 {
        for i >= 0 {
            qr := DivRec(r, x[i], d, m)
            if i < z.Len() {
                z[i] = qr.q
            }
            r = qr.r
            i -= 1
        }
        return r
    }
    // Divide x << s by d, shifting x's words in as they are read.
    r = x[n - 1] >> (64 - s)
    for i >= 0 {
        lo := if i > 0 { (x[i] << s) | (x[i - 1] >> (64 - s)) } else { x[i] << s }
        qr := DivRec(r, lo, d, m)
        if i < z.Len() {
            z[i] = qr.q
        }
        r = qr.r
        i -= 1
    }
    return r >> s
}

// ModVW is x mod y, y not zero, with no quotient stored.
ModVW(x: []u64, y: u64): u64 {
    n := x.Len()
    s := raw.Clz64(y)
    d := y << s
    m := Reciprocal(d)
    var r: u64 = 0
    var i := n - 1
    if s == 0 {
        for i >= 0 {
            r = DivRec(r, x[i], d, m).r
            i -= 1
        }
        return r
    }
    r = x[n - 1] >> (64 - s)
    for i >= 0 {
        lo := if i > 0 { (x[i] << s) | (x[i - 1] >> (64 - s)) } else { x[i] << s }
        r = DivRec(r, lo, d, m).r
        i -= 1
    }
    return r >> s
}

// Greater2 reports whether x1 * 2^64 + x0 > y1 * 2^64 + y0.
Greater2(x1: u64, x0: u64, y1: u64, y0: u64): bool {
    return x1 > y1 || (x1 == y1 && x0 > y0)
}

// DivKnuth divides u by v in place (Algorithm D): on entry u holds the
// dividend shifted left so that v's top word has its top bit set, with one
// extra top word, u.Len() = m + n + 1 for v.Len() = n >= 2; on return q's
// m + 1 words hold the quotient and u's low n words the remainder, still
// shifted. O(n * (m + 1)).
DivKnuth(q: mut []u64, u: mut []u64, v: []u64) {
    n := v.Len()
    m := u.Len() - n - 1
    vn1 := v[n - 1]
    vn2 := v[n - 2]
    rec := Reciprocal(vn1)
    var j := m
    for j >= 0 {
        // The 2-by-1 estimate of the quotient digit, refined by the third
        // word to at most one above the true digit (TAOCP 4.3.1, step D3).
        ujn := u[j + n]
        var qhat: u64 = 0xFFFFFFFFFFFFFFFF
        if ujn != vn1 {
            qr := DivRec(ujn, u[j + n - 1], vn1, rec)
            qhat = qr.q
            var rhat := qr.r
            ujn2 := u[j + n - 2]
            var p := raw.Mul128(qhat, vn2)
            for Greater2(p.hi, p.lo, rhat, ujn2) {
                qhat -= 1
                prev := rhat
                rhat = rhat +% vn1
                if rhat < prev {
                    break
                }
                p = raw.Mul128(qhat, vn2)
            }
        }
        // Subtract qhat * v from u[j..j + n + 1]; on a borrow qhat was one
        // too large, and v is added back (step D6, probability about 2^-64
        // on random input).
        b := SubMulVVW(mut u[j..j + n], v, qhat)
        top := u[j + n]
        u[j + n] = top -% b
        if top < b {
            c := AddTo(mut u[j..j + n], v)
            u[j + n] = u[j + n] +% c
            qhat -= 1
        }
        q[j] = qhat
        j -= 1
    }
}

// DivNat sets q = x / y and r = x mod y for normalized magnitudes x and y,
// y not zero, x.Len() >= y.Len(), q.Len() = x.Len() - y.Len() + 1 and
// r.Len() = y.Len(); scratch holds at least x.Len() + 1 words. O(y.Len() *
// q.Len()).
DivNat(q: mut []u64, r: mut []u64, x: []u64, y: []u64, scratch: mut []u64) {
    n := y.Len()
    if n == 1 {
        Clear(r)
        r[0] = DivVW(q, x, y[0])
        return
    }
    s := raw.Clz64(y[n - 1])
    u := mut scratch[..x.Len() + 1]
    // v, the shifted divisor, lives in r until the remainder replaces it.
    if s == 0 {
        Copy(mut u[..], x)
        u[x.Len()] = 0
        Copy(mut r[..], y)
    } else {
        u[x.Len()] = ShlVU(mut u[..x.Len()], x, s)
        _ = ShlVU(mut r[..], y, s)
    }
    DivKnuthR(q, u, r)
    if s == 0 {
        Copy(mut r[..], u[..n])
    } else {
        _ = ShrVU(mut r[..], u[..n], s, 0)
    }
}

// DivKnuthR is DivKnuth with the divisor in a mut slice, read as shared.
DivKnuthR(q: mut []u64, u: mut []u64, v: mut []u64) {
    DivKnuth(q, u, v[..])
}

// QuoRemInto divides the normalized x by the normalized, non-zero y into
// the buffers q (at least x.Len() - y.Len() + 1 words) and r (at least
// y.Len() words, or x.Len() when x < y), with s (x.Len() + 1 words) as
// scratch, and returns the normalized lengths of the quotient and the
// remainder.
QuoRemInto(q: mut []u64, r: mut []u64, x: []u64, y: []u64, s: mut []u64): (lq: int, lr: int) {
    if CmpVV(x, y) < 0 {
        Copy(r, x)
        return (lq: 0, lr: x.Len())
    }
    nq := x.Len() - y.Len() + 1
    if y.Len() == 1 {
        r[0] = DivVW(mut q[..nq], x, y[0])
        return (lq: Norm(q[..nq]).Len(), lr: Norm(r[..1]).Len())
    }
    DivNat(mut q[..nq], mut r[..y.Len()], x, y, mut s[..x.Len() + 1])
    return (lq: Norm(q[..nq]).Len(), lr: Norm(r[..y.Len()]).Len())
}

// DivInPlace sets z = z / y, y not zero, and returns the remainder.
DivInPlace(z: mut []u64, y: u64): u64 {
    n := z.Len()
    if n == 0 {
        return 0
    }
    s := raw.Clz64(y)
    d := y << s
    m := Reciprocal(d)
    var r: u64 = 0
    var i := n - 1
    if s == 0 {
        for i >= 0 {
            qr := DivRec(r, z[i], d, m)
            z[i] = qr.q
            r = qr.r
            i -= 1
        }
        return r
    }
    // Divide z << s by d; the word below z[i] is read before z[i - 1] is
    // overwritten, since the loop runs downward.
    r = z[n - 1] >> (64 - s)
    for i >= 0 {
        lo := if i > 0 { (z[i] << s) | (z[i - 1] >> (64 - s)) } else { z[i] << s }
        qr := DivRec(r, lo, d, m)
        z[i] = qr.q
        r = qr.r
        i -= 1
    }
    return r >> s
}
