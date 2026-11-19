// Why: certificates and TLS handshakes sign with ECDSA, and a signer must
// never leak its nonce.
//
// What: SigFormat, EcdsaSign and EcdsaVerify, with the RFC 6979 nonce
// generator Drbg and the signature codecs SigEncode and SigDecode (r || s
// or DER).
//
// How: FIPS 186-5 section 6 over P-256 (signing and verification) and P-384
// (verification only), with deterministic nonces by RFC 6979
// section 3.2 (HMAC_DRBG keyed by the secret and the digest), so signing
// needs no random source and two signatures of one digest are equal, as
// Go's crypto/ecdsa signs with a nil random source. A signature is r || s
// (Fixed, each the curve's field length, IEEE P1363) or the DER SEQUENCE
// of two INTEGERs of RFC 3279 2.2.3 (Der), read through std.encoding's DER
// reader, which refuses BER's other forms.
//
// RFC 6979's HMAC hash is chosen by the digest's length, since the API
// takes a digest and not its hash: SHA-384 for 48 bytes, SHA-512 for 64,
// SHA-256 otherwise; so a SHA-256, SHA-384 or SHA-512 digest gets the
// nonce RFC 6979 and Go give it.
//
// Constant time in the secret and the nonce: the DRBG is HMAC, the nonce
// multiplies the base point in ec_p256.mk's fixed window, its inverse is
// Fermat's, and s = k^-1 (e + r d) is word arithmetic. What branches is
// public (lengths, the format, r and s being zero) or the rejection of a
// nonce outside [1, n - 1], which happens with probability below 2^-32
// and reveals nothing of the nonce used. Verification handles public data
// only and is variable time.

import std.encoding

/// SigFormat is the encoding of an ECDSA signature: Der, the DER SEQUENCE
/// of two INTEGERs that X.509 and TLS use, or Fixed, r then s, each as long
/// as the curve's field (IEEE P1363).
export enum SigFormat {
    /// The DER SEQUENCE of the two INTEGERs r and s, as X.509 and TLS use.
    Der
    /// r then s, each as long as the curve's field (IEEE P1363).
    Fixed
}

// Drbg is RFC 6979 3.2's HMAC_DRBG state: K and V, HashLen(h) bytes each.
struct Drbg {
    h: HashKind
    k: [64]u8
    v: [64]u8
}

// DrbgMac writes HMAC_key(a || b || c || d) under h to the front of out.
DrbgMac(h: HashKind, key: []u8, a: []u8, b: []u8, c: []u8, d: []u8, out: mut []u8) {
    match h {
        .Sha256 {
            k := Hmac256Key.New(key)
            var s := k.inner
            s.Bytes(a)
            s.Bytes(b)
            s.Bytes(c)
            s.Bytes(d)
            t := k.Finish(&s)
            (mut out[0..32]).CopyFrom(t[..])
        }
        .Sha384 {
            k := Hmac384Key.New(key)
            var s := k.inner
            s.Bytes(a)
            s.Bytes(b)
            s.Bytes(c)
            s.Bytes(d)
            t := k.Finish(&s)
            (mut out[0..48]).CopyFrom(t[..])
        }
        .Sha512 {
            k := Hmac512Key.New(key)
            var s := k.inner
            s.Bytes(a)
            s.Bytes(b)
            s.Bytes(c)
            s.Bytes(d)
            t := k.Finish(&s)
            (mut out[0..64]).CopyFrom(t[..])
        }
    }
}

// Drbg.New is RFC 6979 3.2 steps b to g: V = 0x01..., K = 0x00..., then
// two rounds of K = HMAC_K(V || i || x || m), V = HMAC_K(V), with x the
// secret's and m the digest's int2octets.
Drbg.New(h: HashKind, x: []u8, m: []u8): Drbg {
    n := HashLen(h)
    var g := Drbg(h: h, k: [0; 64], v: [1; 64])
    var t: [64]u8 = [0; 64]
    var sep: [1]u8 = [0]
    for _ in 0..2 {
        DrbgMac(h, g.k[0..n], g.v[0..n], sep[..], x, m, mut t[..])
        (mut g.k[0..n]).CopyFrom(t[0..n])
        DrbgMac(h, g.k[0..n], g.v[0..n], x[0..0], x[0..0], x[0..0], mut t[..])
        (mut g.v[0..n]).CopyFrom(t[0..n])
        sep[0] = 1
    }
    Wipe(mut t[..])
    return g
}

// Drbg.Next fills out (at most HashLen bytes: P-256's 32) with step h.2's
// V = HMAC_K(V), then moves the state on as step h.3 does, K = HMAC_K(V ||
// 0x00), V = HMAC_K(V), so a refused candidate is followed by a new one.
Drbg.Next(mut self, out: mut []u8) {
    n := HashLen(self.h)
    var t: [64]u8 = [0; 64]
    var zero: [1]u8 = [0]
    DrbgMac(self.h, self.k[0..n], self.v[0..n], zero[0..0], zero[0..0], zero[0..0], mut t[..])
    (mut self.v[0..n]).CopyFrom(t[0..n])
    (mut out[..]).CopyFrom(t[0..out.Len()])
    DrbgMac(self.h, self.k[0..n], self.v[0..n], zero[..], zero[0..0], zero[0..0], mut t[..])
    (mut self.k[0..n]).CopyFrom(t[0..n])
    DrbgMac(self.h, self.k[0..n], self.v[0..n], zero[0..0], zero[0..0], zero[0..0], mut t[..])
    (mut self.v[0..n]).CopyFrom(t[0..n])
    Wipe(mut t[..])
}

// NonceHash is the HMAC hash of RFC 6979 for a digest of this length.
NonceHash(n: int): HashKind {
    if n == 48 {
        return HashKind.Sha384
    }
    if n == 64 {
        return HashKind.Sha512
    }
    return HashKind.Sha256
}

// DerIntLen is the content length of the DER INTEGER of the unsigned
// big-endian v: v without its leading zero bytes (one byte for zero), plus
// a zero byte when the top bit is set.
DerIntLen(v: []u8): int {
    var i := 0
    for i < v.Len() - 1 && v[i] == 0 {
        i += 1
    }
    var n := v.Len() - i
    if v[i] >= 0x80 {
        n += 1
    }
    return n
}

// PutDerInt writes the DER INTEGER of v at out[at..] and returns the
// offset after it.
PutDerInt(v: []u8, out: mut []u8, at: int): int {
    var i := 0
    for i < v.Len() - 1 && v[i] == 0 {
        i += 1
    }
    n := DerIntLen(v)
    out[at] = 2
    out[at + 1] = Trunc[u8](n)
    var o := at + 2
    if v[i] >= 0x80 {
        out[o] = 0
        o += 1
    }
    (mut out[o..o + v.Len() - i]).CopyFrom(v[i..v.Len()])
    return o + v.Len() - i
}

// SigEncode writes r and s (equal lengths, big-endian) in format at the
// front of out and returns the count, or fails with BadLength, writing
// nothing, when out is shorter. A P-256 or P-384 DER signature is at most
// 104 bytes, so its lengths take the short form.
SigEncode(format: SigFormat, r: []u8, s: []u8, out: mut []u8): Error!int {
    return match format {
        .Fixed {
            n := r.Len() + s.Len()
            if out.Len() < n {
                fail BadLength
            }
            (mut out[0..r.Len()]).CopyFrom(r)
            (mut out[r.Len()..n]).CopyFrom(s)
            n
        }
        .Der {
            body := 4 + DerIntLen(r) + DerIntLen(s)
            if out.Len() < body + 2 {
                fail BadLength
            }
            out[0] = 0x30
            out[1] = Trunc[u8](body)
            at := PutDerInt(r, mut out[..], 2)
            PutDerInt(s, mut out[..], at)
        }
    }
}

// SigInt copies the DER INTEGER v right-aligned into out (zeroed first)
// and reports true, or reports false when v is not a non-negative integer
// that fits out.
SigInt(v: &encoding.DerValue, out: mut []u8): bool {
    b := v.Integer() else {
        return false
    }
    if b[0] >= 0x80 {
        return false
    }
    var m := b
    if b.Len() > 1 && b[0] == 0 {
        m = b[1..b.Len()]
    }
    if m.Len() > out.Len() {
        return false
    }
    for i in 0..out.Len() {
        out[i] = 0
    }
    (mut out[out.Len() - m.Len()..out.Len()]).CopyFrom(m)
    return true
}

// SigDecode reads sig in format into r and s (each the field length,
// big-endian) and reports whether it is a signature's well-formed
// encoding: Fixed is exactly r || s; Der is exactly one DER SEQUENCE of two
// non-negative INTEGERs that fit, with nothing after it.
SigDecode(format: SigFormat, sig: []u8, r: mut []u8, s: mut []u8): bool {
    match format {
        .Fixed {
            if sig.Len() != r.Len() + s.Len() {
                return false
            }
            (mut r[..]).CopyFrom(sig[0..r.Len()])
            (mut s[..]).CopyFrom(sig[r.Len()..sig.Len()])
            return true
        }
        .Der {
            t := encoding.DerRead(sig) else {
                return false
            }
            if t.rest.Len() != 0 || t.value.tag != 16 || t.value.class != 0 || !t.value.constructed {
                return false
            }
            var it := t.value.Children()
            first := it.Next() else {
                return false
            }
            rv := first else {
                return false
            }
            second := it.Next() else {
                return false
            }
            sv := second else {
                return false
            }
            third := it.Next() else {
                return false
            }
            if third.IsSome() {
                return false
            }
            return SigInt(&rv, mut r[..]) && SigInt(&sv, mut s[..])
        }
    }
}

/// Writes the ECDSA signature (FIPS 186-5) of digest under secret, in
/// format, to the front of out and returns its length: 64 bytes Fixed, at
/// most 72 Der. digest is the hash of the message, not the message: hash it
/// first, with hash.Sha256 for P-256.
///
/// The nonce is derived from the secret and the digest (RFC 6979), so
/// signing needs no random source and the same digest always gives the same
/// signature. RFC 6979's HMAC hash is SHA-384 for a 48-byte digest, SHA-512
/// for 64 bytes and SHA-256 otherwise; a digest longer than 32 bytes is cut
/// to its first 32. Fails with BadLength for an empty digest or an out
/// shorter than the signature, writing nothing, and with BadKey for a P-384
/// or wiped secret. Constant time in the secret and the nonce: one scalar
/// multiplication and two inversions; allocates nothing.
export EcdsaSign(secret: &EcSecret, digest: []u8, format: SigFormat, out: mut []u8): Error!int {
    match secret.curve {
        .P256 {}
        .P384 {
            fail BadKey
        }
    }
    if digest.Len() == 0 {
        fail BadLength
    }
    d := P256nDecode(secret.d[..]) else {
        fail BadKey
    }
    if P256nIsZero(&d) == 1 {
        fail BadKey
    }
    e := P256nDigest(digest)
    var m: [32]u8 = [0; 32]
    P256nEncode(&e, mut m[..])
    var g := Drbg.New(NonceHash(digest.Len()), secret.d[..], m[..])
    var kb: [32]u8 = [0; 32]
    var r: [4]u64 = [0; 4]
    var s: [4]u64 = [0; 4]
    for true {
        g.Next(mut kb[..])
        k := P256nDecode(kb[..]) else {
            continue
        }
        if P256nIsZero(&k) == 1 {
            continue
        }
        rp := P256PtBaseMul(kb[..])
        var x: [32]u8 = [0; 32]
        _ = P256PtXBytes(&rp, mut x[..])
        r = P256nReduce(x[..])
        ki := P256nInvert(&k)
        var t := P256nMul(&r, &d)
        t = P256nAdd(&t, &e)
        s = P256nMul(&ki, &t)
        if P256nIsZero(&r) == 0 && P256nIsZero(&s) == 0 {
            break
        }
    }
    Wipe(mut kb[..])
    Wipe(mut g.k[..])
    Wipe(mut g.v[..])
    var rb: [32]u8 = [0; 32]
    var sb: [32]u8 = [0; 32]
    P256nEncode(&r, mut rb[..])
    P256nEncode(&s, mut sb[..])
    return try SigEncode(format, rb[..], sb[..], mut out[..])
}

/// Reports whether sig, in format, is a valid ECDSA signature (FIPS 186-5)
/// of digest under public, an uncompressed SEC 1 point (65 bytes for P-256,
/// 97 for P-384) on curve. A digest longer than the curve's field is cut to
/// its leftmost bytes. False, never an error, for an empty digest, a point
/// off the curve or in another form, a signature not in format's exact
/// encoding, or r or s outside [1, n - 1]. Variable time, since every input
/// is public: one double multiplication and one inversion; allocates
/// nothing.
export EcdsaVerify(public: []u8, curve: Curve, digest: []u8, format: SigFormat, sig: []u8): bool {
    return match curve {
        .P256 {
            var r: [32]u8 = [0; 32]
            var s: [32]u8 = [0; 32]
            SigDecode(format, sig, mut r[..], mut s[..]) && P256EcdsaVerify(public, digest, r[..], s[..])
        }
        .P384 {
            var r: [48]u8 = [0; 48]
            var s: [48]u8 = [0; 48]
            SigDecode(format, sig, mut r[..], mut s[..]) && P384EcdsaVerify(public, digest, r[..], s[..])
        }
    }
}
