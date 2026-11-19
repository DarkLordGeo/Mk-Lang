// Why: much of the web's PKI still signs with RSA, so a TLS client must
// verify RSA signatures, though it never needs to make one.
//
// What: RsaPublic.New, RsaVerifyPkcs1 and RsaVerifyPss, over the
// fixed-size arithmetic RsaMont and RsaPublicOp.
//
// How: RFC 8017: RSASSA-PKCS1-v1_5 (section 8.2.2) and RSASSA-PSS (section
// 8.1.2, EMSA-PSS-VERIFY 9.1.2, MGF1 B.2.1), with SHA-256, SHA-384 or SHA-512
// and, for PSS, a salt as long as the digest. Verification only: no signing,
// decryption or key generation, so every value here is public and the
// arithmetic may be variable time.
//
// The arithmetic is fixed-capacity: a modulus of at most 8192 bits is 128
// 64-bit limbs, every number a [128]u64 on the stack, nothing allocated
// and no std.big. s^e mod n is left-to-right binary exponentiation by
// Montgomery multiplication (CIOS, Koc, Acar and Kaliski 1996), with R^2
// mod n made per call by Go's crypto/internal/fips140/bigmod chain, as Go
// makes it per verification (R mod n by doublings, then doublings while
// the exponent is at most l / 4 and square-and-double over the rest of
// log2 R's bits). The words' carries come from comparisons, which a
// release build turns into add-with-carry and a dev build may branch on:
// every value here is public.
// PKCS#1 v1.5 is checked by building the expected encoding and comparing
// it whole, as RFC 8017 8.2.2 step 3 says and Go does, so no parser of
// the padding exists to get wrong.

import std.hash
import std.raw

/// RsaPublic is an RSA public key for signature verification: a view of the
/// modulus's big-endian bytes (leading zeros dropped) and the public
/// exponent. std.crypto verifies RSA signatures; it does not make them,
/// encrypt, decrypt or generate RSA keys.
export view struct RsaPublic {
    n: []u8
    e: u64
}

// RsaLimbs is the most limbs a modulus has: 8192 bits.
const RsaLimbs: int = 128

/// Returns the key (modulus, exponent), modulus big-endian with any leading
/// zero bytes, as a view of modulus. Fails with BadKey unless the modulus
/// is odd and 2048 to 8192 bits long and the exponent odd and from 3 to
/// 2^32 - 1. O(n); allocates nothing.
export RsaPublic.New(modulus: []u8, exponent: u64): Error!RsaPublic from modulus {
    var i := 0
    for i < modulus.Len() && modulus[i] == 0 {
        i += 1
    }
    n := modulus[i..modulus.Len()]
    if n.Len() == 0 {
        fail BadKey
    }
    bits := RsaBits(n)
    if bits < 2048 || bits > 8192 || n[n.Len() - 1] & 1 == 0 {
        fail BadKey
    }
    if exponent & 1 == 0 || exponent < 3 || exponent > 0xFFFFFFFF {
        fail BadKey
    }
    return RsaPublic(n: n, e: exponent)
}

// RsaBits is the modulus's length in bits.
RsaBits(n: []u8): int {
    var top := n[0]
    var bits := 8 * (n.Len() - 1)
    for top != 0 {
        bits += 1
        top = top >> 1
    }
    return bits
}

// RsaFromBytes reads the big-endian b into the first l limbs of x.
RsaFromBytes(b: []u8, x: mut [128]u64, l: int) {
    for i in 0..l {
        x[i] = 0
    }
    k := b.Len()
    for j in 0..k {
        x[j / 8] = x[j / 8] | (u64(b[k - 1 - j]) << Cast[u64](8 * (j % 8)))
    }
}

// RsaToBytes writes the first l limbs of x big-endian into out (all of
// it; x is below 2^(8 out.Len())).
RsaToBytes(x: &[128]u64, out: mut []u8) {
    k := out.Len()
    for j in 0..k {
        out[k - 1 - j] = Trunc[u8](x[j / 8] >> Cast[u64](8 * (j % 8)))
    }
}

// RsaBit is 1 when b and 0 otherwise (public data only: a dev build may
// branch on it).
RsaBit(b: bool): u64 {
    return if b { 1 } else { 0 }
}

// RsaGeq reports whether x (with top word hi above its l limbs) >= n.
RsaGeq(x: &[128]u64, hi: u64, n: &[128]u64, l: int): bool {
    if hi != 0 {
        return true
    }
    xs := x[..l]
    ns := n[..l]
    var i := l - 1
    for i >= 0 {
        if xs[i] != ns[i] {
            return xs[i] > ns[i]
        }
        i -= 1
    }
    return true
}

// RsaSubN sets x = x - n over l limbs (the borrow out drops the top
// word).
RsaSubN(x: mut [128]u64, n: &[128]u64, l: int) {
    xs := mut x[..l]
    ns := n[..l]
    var b: u64 = 0
    for i in 0..l {
        a := xs[i]
        d := a -% ns[i]
        xs[i] = d -% b
        b = RsaBit(a < ns[i]) | RsaBit(d < b)
    }
}

// RsaMont is out = a b R^-1 mod n over l limbs (CIOS), for a, b < n;
// n0 = -n^-1 mod 2^64. Each inner step adds the product's low word before
// the carry, so the chain from one word's carry to the next is one add
// and one add-with-carry (std.big's AddMulVVW form).
RsaMont(a: &[128]u64, b: &[128]u64, n: &[128]u64, n0: u64, l: int, out: mut [128]u64) {
    var t: [130]u64 = [0; 130]
    av := a[..l]
    ns := n[..l]
    ts := mut t[..l + 2]
    for i in 0..l {
        bi := b[i]
        var c: u64 = 0
        for j in 0..l {
            p := raw.Mul128(av[j], bi)
            x := ts[j] +% p.lo
            hi := p.hi +% RsaBit(x < p.lo)
            s := x +% c
            ts[j] = s
            c = hi +% RsaBit(s < c)
        }
        s1 := ts[l] +% c
        ts[l + 1] = RsaBit(s1 < c)
        ts[l] = s1
        m := ts[0] *% n0
        p0 := raw.Mul128(m, ns[0])
        x0 := ts[0] +% p0.lo
        c = p0.hi +% RsaBit(x0 < p0.lo)
        for j in 1..l {
            p := raw.Mul128(m, ns[j])
            x := ts[j] +% p.lo
            hi := p.hi +% RsaBit(x < p.lo)
            s := x +% c
            ts[j - 1] = s
            c = hi +% RsaBit(s < c)
        }
        s2 := ts[l] +% c
        ts[l - 1] = s2
        ts[l] = ts[l + 1] +% RsaBit(s2 < c)
    }
    (mut out[..l]).CopyFrom(ts[..l])
    if RsaGeq(&*out, ts[l], n, l) {
        RsaSubN(out, n, l)
    }
}

// RsaDouble sets x = 2 x mod n over l limbs, for x < n.
RsaDouble(x: mut [128]u64, n: &[128]u64, l: int) {
    xs := mut x[..l]
    var c: u64 = 0
    for i in 0..l {
        w := xs[i]
        xs[i] = (w << 1) | c
        c = w >> 63
    }
    if RsaGeq(&*x, c, n, l) {
        RsaSubN(x, n, l)
    }
}

// RsaPublicOp writes sig^e mod n, k bytes big-endian, to out (k bytes)
// and reports true, or reports false when sig is not k bytes or not below
// n (RFC 8017 5.2.2's RSAVP1 and 8.2.2 step 1).
RsaPublicOp(key: &RsaPublic, sig: []u8, out: mut []u8): bool {
    k := key.n.Len()
    if sig.Len() != k {
        return false
    }
    l := (k + 7) / 8
    var n: [128]u64 = [0; 128]
    RsaFromBytes(key.n, mut n, l)
    var s: [128]u64 = [0; 128]
    RsaFromBytes(sig, mut s, l)
    if RsaGeq(&s, 0, &n, l) {
        return false
    }
    // n0 = -n^-1 mod 2^64 by Newton's iteration (each step doubles the
    // correct low bits; n is odd).
    var inv: u64 = 1
    for _ in 0..6 {
        inv = inv *% (2 -% n[0] *% inv)
    }
    n0 := 0 -% inv
    // rr = R^2 mod n, R = 2^(64 l), as bigmod's rr: R mod n by doublings
    // from the top bit of n; then, in the Montgomery domain, where
    // doubling adds one to the exponent of 2 and squaring doubles it, the
    // top bits of log2 R by doublings while that exponent is at most l / 4
    // (cheaper than a product there, bigmod's measured threshold), and
    // the rest by square-and-double.
    bits := RsaBits(key.n)
    logr := 64 * l
    var rr: [128]u64 = [0; 128]
    rr[(bits - 1) / 64] = u64(1) << Cast[u64]((bits - 1) % 64)
    for _ in bits - 1..logr {
        RsaDouble(mut rr, &n, l)
    }
    var i := 63
    for logr >> i <= l / 4 {
        i -= 1
    }
    for _ in 0..logr >> i {
        RsaDouble(mut rr, &n, l)
    }
    var tmp: [128]u64 = [0; 128]
    for i > 0 {
        RsaMont(&rr, &rr, &n, n0, l, mut tmp)
        rr = tmp
        i -= 1
        if (logr >> i) & 1 == 1 {
            RsaDouble(mut rr, &n, l)
        }
    }
    // x = s R mod n; acc = x^e by the exponent's bits from the top.
    var x: [128]u64 = [0; 128]
    RsaMont(&s, &rr, &n, n0, l, mut x)
    var acc := x
    var top := 63
    for (key.e >> Cast[u64](top)) & 1 == 0 {
        top -= 1
    }
    var b := top - 1
    for b >= 0 {
        RsaMont(&acc, &acc, &n, n0, l, mut tmp)
        acc = tmp
        if (key.e >> Cast[u64](b)) & 1 == 1 {
            RsaMont(&acc, &x, &n, n0, l, mut tmp)
            acc = tmp
        }
        b -= 1
    }
    var one: [128]u64 = [0; 128]
    one[0] = 1
    RsaMont(&acc, &one, &n, n0, l, mut tmp)
    RsaToBytes(&tmp, mut out[..])
    return true
}

// RsaPrefix is the DER DigestInfo before a digest of h (RFC 8017 9.2
// note 1).
RsaPrefix(h: HashKind): [19]u8 {
    return match h {
        .Sha256 { [0x30, 0x31, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x01, 0x05, 0x00, 0x04, 0x20] }
        .Sha384 { [0x30, 0x41, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x02, 0x05, 0x00, 0x04, 0x30] }
        .Sha512 { [0x30, 0x51, 0x30, 0x0d, 0x06, 0x09, 0x60, 0x86, 0x48, 0x01, 0x65, 0x03, 0x04, 0x02, 0x03, 0x05, 0x00, 0x04, 0x40] }
    }
}

/// Reports whether sig is an RSASSA-PKCS1-v1_5 signature (RFC 8017 8.2.2)
/// of digest, made with hash h, under public. sig^e mod n must equal the
/// expected encoding (0x00 0x01 0xFF... 0x00, then the DigestInfo of h and
/// digest) byte for byte; no padding parser runs. False, never an error,
/// when digest is not h's length or sig is not the modulus's length or not
/// below it. Variable time, since every input is public. O(k^2 log e) word
/// operations for a k-byte modulus; allocates nothing.
export RsaVerifyPkcs1(public: &RsaPublic, h: HashKind, digest: []u8, sig: []u8): bool {
    hl := HashLen(h)
    if digest.Len() != hl {
        return false
    }
    k := public.n.Len()
    var em: [1024]u8 = [0; 1024]
    if !RsaPublicOp(public, sig, mut em[0..k]) {
        return false
    }
    var want: [1024]u8 = [0; 1024]
    t := 19 + hl
    want[1] = 1
    for i in 2..k - t - 1 {
        want[i] = 0xFF
    }
    p := RsaPrefix(h)
    (mut want[k - t..k - hl]).CopyFrom(p[..])
    (mut want[k - hl..k]).CopyFrom(digest)
    return Equal(em[0..k], want[0..k])
}

// RsaHash writes h(a || b || c) to the front of out.
RsaHash(h: HashKind, a: []u8, b: []u8, c: []u8, out: mut []u8) {
    match h {
        .Sha256 {
            var s := hash.Sha256State.New()
            s.Bytes(a)
            s.Bytes(b)
            s.Bytes(c)
            d := s.Sum()
            (mut out[0..32]).CopyFrom(d[..])
        }
        .Sha384 {
            var s := hash.Sha384State.New()
            s.Bytes(a)
            s.Bytes(b)
            s.Bytes(c)
            d := s.Sum()
            (mut out[0..48]).CopyFrom(d[..])
        }
        .Sha512 {
            var s := hash.Sha512State.New()
            s.Bytes(a)
            s.Bytes(b)
            s.Bytes(c)
            d := s.Sum()
            (mut out[0..64]).CopyFrom(d[..])
        }
    }
}

// RsaMgf1Xor XORs MGF1(seed) under h (RFC 8017 B.2.1) into db.
RsaMgf1Xor(h: HashKind, seed: []u8, db: mut []u8) {
    hl := HashLen(h)
    var c: [4]u8 = [0; 4]
    var t: [64]u8 = [0; 64]
    var done := 0
    var counter: u32 = 0
    for done < db.Len() {
        PutBe32(mut c[..], 0, counter)
        RsaHash(h, seed, c[..], c[0..0], mut t[..])
        var j := 0
        for j < hl && done < db.Len() {
            db[done] = db[done] ^ t[j]
            j += 1
            done += 1
        }
        counter += 1
    }
}

/// Reports whether sig is an RSASSA-PSS signature (RFC 8017 8.1.2) of
/// digest under public, with hash h for the digest and MGF1, and a salt
/// exactly h's length. False, never an error, when digest is not h's
/// length, sig is not the modulus's length or not below it, or any check of
/// EMSA-PSS-VERIFY fails. Variable time, since every input is public. O(k^2
/// log e) word operations; allocates nothing.
export RsaVerifyPss(public: &RsaPublic, h: HashKind, digest: []u8, sig: []u8): bool {
    hl := HashLen(h)
    if digest.Len() != hl {
        return false
    }
    k := public.n.Len()
    var m: [1024]u8 = [0; 1024]
    if !RsaPublicOp(public, sig, mut m[0..k]) {
        return false
    }
    // emBits = modBits - 1; when it is a multiple of 8 the encoded message
    // is one byte shorter than k and the first byte of m must be zero.
    embits := RsaBits(public.n) - 1
    emlen := (embits + 7) / 8
    var at := 0
    if emlen < k {
        if m[0] != 0 {
            return false
        }
        at = 1
    }
    em := mut m[at..at + emlen]
    sl := hl
    if emlen < hl + sl + 2 || em[emlen - 1] != 0xBC {
        return false
    }
    dbl := emlen - hl - 1
    zbits := 8 * emlen - embits
    if zbits > 0 && (em[0] >> Cast[u8](8 - zbits)) != 0 {
        return false
    }
    var hh: [64]u8 = [0; 64]
    (mut hh[0..hl]).CopyFrom(em[dbl..dbl + hl])
    RsaMgf1Xor(h, hh[0..hl], mut em[0..dbl])
    em[0] = em[0] & Trunc[u8](0xFF >> zbits)
    for i in 0..dbl - sl - 1 {
        if em[i] != 0 {
            return false
        }
    }
    if em[dbl - sl - 1] != 1 {
        return false
    }
    var zeros: [8]u8 = [0; 8]
    var hp: [64]u8 = [0; 64]
    RsaHash(h, zeros[..], digest, em[dbl - sl..dbl], mut hp[..])
    return Equal(hp[0..hl], hh[0..hl])
}
