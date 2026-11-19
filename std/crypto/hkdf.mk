// Why: protocols derive many keys from one shared secret; HKDF is the
// standard extract-then-expand way to do it.
//
// What: Prk with FromBytes and Wipe, HkdfExtract and HkdfExpand.
//
// How: RFC 5869 over HMAC-SHA256, -SHA384 and -SHA512 (hmac.mk): Extract is
// one HMAC with the salt as the key (an empty salt is HashLen zero bytes,
// which keys HMAC exactly as the empty key does), and Expand is the
// counter chain T(i) = HMAC(PRK, T(i - 1) || info || i) over one keyed
// state, so each block costs its own compressions only. Constant time in
// the key material: HMAC's; the lengths of salt, info and out are public.

/// Prk is HKDF's pseudorandom key: as many bytes as the hash's output (32,
/// 48 or 64), with the hash that made them. HkdfExtract and Prk.FromBytes
/// make one; HkdfExpand turns it into keys. After Wipe, HkdfExpand fails
/// with BadKey.
export struct Prk {
    kind: HashKind
    key: [64]u8
    live: bool
}

/// Returns HKDF-Extract (RFC 5869) of ikm with salt under the hash h:
/// HMAC(salt, ikm), the pseudorandom key that HkdfExpand stretches. Any
/// lengths; an empty salt is the same as a salt of zero bytes as long as
/// h's output. Constant time in ikm and salt. Cannot fail. O(salt + ikm);
/// allocates nothing.
export HkdfExtract(salt: []u8, ikm: []u8, h: HashKind): Prk {
    var key: [64]u8 = [0; 64]
    match h {
        .Sha256 {
            t := Hmac256(salt, ikm)
            (mut key[0..32]).CopyFrom(t[..])
        }
        .Sha384 {
            t := Hmac384(salt, ikm)
            (mut key[0..48]).CopyFrom(t[..])
        }
        .Sha512 {
            t := Hmac512(salt, ikm)
            (mut key[..]).CopyFrom(t[..])
        }
    }
    return Prk(kind: h, key: key, live: true)
}

/// Makes a Prk from key, a pseudorandom key that HkdfExtract did not make,
/// such as a TLS 1.3 secret that an earlier HkdfExpand wrote. Fails with
/// BadLength unless key is as long as h's output (32, 48 or 64 bytes). key
/// is copied, so it may be wiped after. O(1); allocates nothing.
export Prk.FromBytes(h: HashKind, key: []u8): Error!Prk {
    if key.Len() != HashLen(h) {
        fail BadLength
    }
    var k: [64]u8 = [0; 64]
    (mut k[0..key.Len()]).CopyFrom(key)
    return Prk(kind: h, key: k, live: true)
}

/// Sets the key's bytes to zero with Wipe and marks the Prk wiped:
/// HkdfExpand then fails with BadKey rather than expand under an all-zero
/// key. Cannot fail. O(1); allocates nothing.
export Prk.Wipe(mut self) {
    Wipe(mut self.key[..])
    self.live = false
}

/// Fills out with HKDF-Expand (RFC 5869) of prk and info: the first
/// out.Len() bytes of T(1) || T(2) || ..., where T(i) = HMAC(prk, T(i - 1)
/// || info || i). Use a different info for each key you derive from one
/// prk. Fails with BadKey when prk was wiped, and with BadLength when out
/// is longer than 255 times the hash's output (8,160 bytes for SHA-256),
/// writing nothing either way. Constant time in prk. O(out + info)
/// compressions per output block; allocates nothing.
export HkdfExpand(prk: &Prk, info: []u8, out: mut []u8): Error!void {
    if !prk.live {
        fail BadKey
    }
    if out.Len() > 255 * HashLen(prk.kind) {
        fail BadLength
    }
    match prk.kind {
        .Sha256 { Expand256(&Hmac256Key.New(prk.key[0..32]), info, out) }
        .Sha384 { Expand384(&Hmac384Key.New(prk.key[0..48]), info, out) }
        .Sha512 { Expand512(&Hmac512Key.New(prk.key[..]), info, out) }
    }
}

// Expand256 is HKDF-Expand under the keyed state k; out is at most 255
// blocks.
Expand256(k: &Hmac256Key, info: []u8, out: mut []u8) {
    var t: [32]u8 = [0; 32]
    var tl := 0
    var c: [1]u8 = [1]
    var i := 0
    for i < out.Len() {
        var s := k.inner
        s.Bytes(t[0..tl])
        s.Bytes(info)
        s.Bytes(c[..])
        t = k.Finish(&s)
        tl = 32
        m := if out.Len() - i < 32 { out.Len() - i } else { 32 }
        (mut out[i..i + m]).CopyFrom(t[0..m])
        i += m
        c[0] = c[0] +% 1
    }
}

// Expand384 is Expand256 over HMAC-SHA384.
Expand384(k: &Hmac384Key, info: []u8, out: mut []u8) {
    var t: [48]u8 = [0; 48]
    var tl := 0
    var c: [1]u8 = [1]
    var i := 0
    for i < out.Len() {
        var s := k.inner
        s.Bytes(t[0..tl])
        s.Bytes(info)
        s.Bytes(c[..])
        t = k.Finish(&s)
        tl = 48
        m := if out.Len() - i < 48 { out.Len() - i } else { 48 }
        (mut out[i..i + m]).CopyFrom(t[0..m])
        i += m
        c[0] = c[0] +% 1
    }
}

// Expand512 is Expand256 over HMAC-SHA512.
Expand512(k: &Hmac512Key, info: []u8, out: mut []u8) {
    var t: [64]u8 = [0; 64]
    var tl := 0
    var c: [1]u8 = [1]
    var i := 0
    for i < out.Len() {
        var s := k.inner
        s.Bytes(t[0..tl])
        s.Bytes(info)
        s.Bytes(c[..])
        t = k.Finish(&s)
        tl = 64
        m := if out.Len() - i < 64 { out.Len() - i } else { 64 }
        (mut out[i..i + m]).CopyFrom(t[0..m])
        i += m
        c[0] = c[0] +% 1
    }
}
