// Why: HMAC is the keyed hash under HKDF, RFC 6979 nonces and many
// protocols.
//
// What: Hmac256, Hmac384 and Hmac512, and the keyed states Hmac256Key,
// Hmac384Key and Hmac512Key.
//
// How: RFC 2104 and FIPS 198-1 over std.hash's SHA-256, SHA-384 and SHA-512
// states (the one SHA-2 of std). Constant time: the key only enters XORs
// with the pads and the compression function, which has no branch or
// index on its data; the key's length (public) chooses whether it is
// hashed first.
//
// Hmac384 and Hmac512 go through a keyed state (RFC 2104 section 4's
// precomputation: the inner and outer states after their pad blocks),
// which HKDF's expansion reuses for every output block, so a block costs
// the message's compressions and one outer compression, not two more for
// the pads. Hmac256 is a one-shot form, kept as it is: its
// jumps are read by the constant-time disassembly test.

import std.hash

/// Returns HMAC-SHA256 of data under key (RFC 2104): SHA256((K ^ opad) ||
/// SHA256((K ^ ipad) || data)), where K is key zero-padded to 64 bytes, or
/// SHA256(key) zero-padded when key is longer than 64 bytes. Any lengths,
/// the empty key included. Constant time in key and data: only their
/// lengths show. Compare a received tag with Equal. Cannot fail. O(key +
/// data); it allocates nothing (two 64-byte pad blocks on the stack).
export Hmac256(key: []u8, data: []u8): [32]u8 {
    var k: [64]u8 = [0; 64]
    if key.Len() > 64 {
        d := hash.Sha256(key)
        (mut k[0..32]).CopyFrom(d[..])
    } else {
        (mut k[0..key.Len()]).CopyFrom(key)
    }
    var pad: [64]u8 = [0; 64]
    for i in 0..64 {
        pad[i] = k[i] ^ 0x36
    }
    var inner := hash.Sha256State.New()
    inner.Bytes(pad[..])
    inner.Bytes(data)
    ih := inner.Sum()
    for i in 0..64 {
        pad[i] = k[i] ^ 0x5c
    }
    var outer := hash.Sha256State.New()
    outer.Bytes(pad[..])
    outer.Bytes(ih[..])
    return outer.Sum()
}

/// Returns HMAC-SHA384 of data under key (RFC 2104 over SHA-384's 128-byte
/// block): K is key zero-padded to 128 bytes, or SHA384(key) zero-padded
/// when key is longer. Any lengths, the empty key included. Constant time
/// in key and data. Cannot fail. O(key + data); allocates nothing.
export Hmac384(key: []u8, data: []u8): [48]u8 {
    k := Hmac384Key.New(key)
    var s := k.inner
    s.Bytes(data)
    return k.Finish(&s)
}

/// Returns HMAC-SHA512 of data under key (RFC 2104 over SHA-512's 128-byte
/// block): K is key zero-padded to 128 bytes, or SHA512(key) zero-padded
/// when key is longer. Any lengths, the empty key included. Constant time
/// in key and data. Cannot fail. O(key + data); allocates nothing.
export Hmac512(key: []u8, data: []u8): [64]u8 {
    k := Hmac512Key.New(key)
    var s := k.inner
    s.Bytes(data)
    return k.Finish(&s)
}

// HmacPad is the block-sized key K (key, zero-padded to b bytes, which the
// caller makes at most 128) XOR x in the first b bytes.
HmacPad(key: []u8, b: int, x: u8): [128]u8 {
    var p: [128]u8 = [0; 128]
    (mut p[0..key.Len()]).CopyFrom(key)
    for i in 0..b {
        p[i] = p[i] ^ x
    }
    return p
}

// Hmac256Key is HMAC-SHA256 under one key: the inner and outer states
// after the ipad and opad blocks.
struct Hmac256Key {
    inner: hash.Sha256State
    outer: hash.Sha256State
}

// Hmac256Key.New keys the states with key, hashed first when longer than
// SHA-256's 64-byte block.
Hmac256Key.New(key: []u8): Hmac256Key {
    var short: [32]u8 = [0; 32]
    var k := key
    if key.Len() > 64 {
        short = hash.Sha256(key)
        k = short[..]
    }
    var inner := hash.Sha256State.New()
    ip := HmacPad(k, 64, 0x36)
    inner.Bytes(ip[0..64])
    var outer := hash.Sha256State.New()
    op := HmacPad(k, 64, 0x5c)
    outer.Bytes(op[0..64])
    return Hmac256Key(inner: inner, outer: outer)
}

// Hmac256Key.Finish is the tag of the message absorbed into s, a copy of
// inner.
Hmac256Key.Finish(&self, s: &hash.Sha256State): [32]u8 {
    ih := s.Sum()
    var o := self.outer
    o.Bytes(ih[..])
    return o.Sum()
}

// Hmac384Key is HMAC-SHA384 under one key, as Hmac256Key.
struct Hmac384Key {
    inner: hash.Sha384State
    outer: hash.Sha384State
}

// Hmac384Key.New keys the states with key, hashed first when longer than
// the 128-byte block.
Hmac384Key.New(key: []u8): Hmac384Key {
    var short: [48]u8 = [0; 48]
    var k := key
    if key.Len() > 128 {
        short = hash.Sha384(key)
        k = short[..]
    }
    var inner := hash.Sha384State.New()
    ip := HmacPad(k, 128, 0x36)
    inner.Bytes(ip[..])
    var outer := hash.Sha384State.New()
    op := HmacPad(k, 128, 0x5c)
    outer.Bytes(op[..])
    return Hmac384Key(inner: inner, outer: outer)
}

// Hmac384Key.Finish is the tag of the message absorbed into s, a copy of
// inner.
Hmac384Key.Finish(&self, s: &hash.Sha384State): [48]u8 {
    ih := s.Sum()
    var o := self.outer
    o.Bytes(ih[..])
    return o.Sum()
}

// Hmac512Key is HMAC-SHA512 under one key, as Hmac256Key.
struct Hmac512Key {
    inner: hash.Sha512State
    outer: hash.Sha512State
}

// Hmac512Key.New keys the states with key, hashed first when longer than
// the 128-byte block.
Hmac512Key.New(key: []u8): Hmac512Key {
    var short: [64]u8 = [0; 64]
    var k := key
    if key.Len() > 128 {
        short = hash.Sha512(key)
        k = short[..]
    }
    var inner := hash.Sha512State.New()
    ip := HmacPad(k, 128, 0x36)
    inner.Bytes(ip[..])
    var outer := hash.Sha512State.New()
    op := HmacPad(k, 128, 0x5c)
    outer.Bytes(op[..])
    return Hmac512Key(inner: inner, outer: outer)
}

// Hmac512Key.Finish is the tag of the message absorbed into s, a copy of
// inner.
Hmac512Key.Finish(&self, s: &hash.Sha512State): [64]u8 {
    ih := s.Sum()
    var o := self.outer
    o.Bytes(ih[..])
    return o.Sum()
}
