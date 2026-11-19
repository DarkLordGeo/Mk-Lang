// Why: SHA-1 is broken for collision resistance but still required by Git
// object names, WebSocket handshakes and older protocols.
//
// What: Sha1 (one-shot) and Sha1State (incremental), SHA-1 of FIPS 180-4
// (sections 5.1.1 and 6.1).
//
// How: 64-byte blocks, the message schedule expanded per block, 80 rounds
// unrolled by five with the working variables renamed (the round macros of
// OpenSSL's portable sha_local.h). Every word operation is mod 2^32 (+%).
// No allocation; the state is plain. Sha1Blocks first offers its whole
// blocks to std.simd.kernel's Sha1Blocks (the SHA extensions, x86_64 sha or
// aarch64 sha2), which compresses them when the running CPU has them and
// else reports false having written nothing; the portable loop then runs,
// and it is the reference. Both give the same bits. The offer sits in
// Sha1Blocks, so the binary holds one copy of the kernel's guard, as
// Sha256Compress does for SHA-256.

import std.simd.kernel

/// Sha1State is a SHA-1 hash in progress. It is not zero-valid: a
/// zero-filled state is not the start of a hash, so Arena.Slice[Sha1State]
/// is E0701. Make one with New. See Sha1 for where SHA-1 may be used.
export struct Sha1State {
    h: [5]u32
    buf: [64]u8
    n: int
    total: u64
    invariant n >= 0 && n < 64
}

/// Returns the state of the empty message. O(1); allocates nothing.
export Sha1State.New(): Sha1State {
    return Sha1State(h: [0x67452301, 0xefcdab89, 0x98badcfe, 0x10325476, 0xc3d2e1f0], buf: [0; 64], n: 0, total: 0)
}

// Sha1Ch is one round with FIPS 180-4's Ch (rounds 0 to 19), written for
// the renaming schedule: e and b are updated in place, and the caller
// rotates the names (the next round's a is this round's e, its c this
// round's b), so five calls in a row move no register. kw is K + W[t].
// The new a term is added last, so one rotate and one add are on the
// chain from round to round.
Sha1Ch(a: u32, b: mut u32, c: u32, d: u32, e: mut u32, kw: u32) {
    *e = *e +% kw +% (d ^ (*b & (c ^ d))) +% RotR32(a, 27)
    *b = RotR32(*b, 2)
}

// Sha1Parity is Sha1Ch's round with Parity (rounds 20 to 39 and 60 to 79).
Sha1Parity(a: u32, b: mut u32, c: u32, d: u32, e: mut u32, kw: u32) {
    *e = *e +% kw +% (*b ^ c ^ d) +% RotR32(a, 27)
    *b = RotR32(*b, 2)
}

// Sha1Maj is Sha1Ch's round with Maj (rounds 40 to 59): b & c and
// d & (b ^ c) share no bit, so their sum is Maj and both join e's adds.
Sha1Maj(a: u32, b: mut u32, c: u32, d: u32, e: mut u32, kw: u32) {
    *e = *e +% kw +% (*b & c) +% (d & (*b ^ c)) +% RotR32(a, 27)
    *b = RotR32(*b, 2)
}

// Sha1Blocks compresses every whole 64-byte block of data into h, in order;
// data.Len() % 64 trailing bytes are ignored. O(data.Len()). The schedule W
// is expanded per block into a local array first, then each twenty rounds
// run as four groups of five renamed rounds; unless the kernel took the
// blocks.
Sha1Blocks(h: mut [5]u32, data: []u8) {
    if kernel.Sha1Blocks(h, data) {
        return
    }
    n := data.Len()
    var i := 0
    var w: [80]u32 = [0; 80]
    for n - i >= 64 {
        blk := data[i..n][0..64]
        for t in 0..16 {
            w[t] = Be32(blk, 4 * t)
        }
        for t in 16..80 {
            x := w[t - 3] ^ w[t - 8] ^ w[t - 14] ^ w[t - 16]
            w[t] = RotR32(x, 31)
        }
        var a := h[0]
        var b := h[1]
        var c := h[2]
        var d := h[3]
        var e := h[4]
        for q in 0..4 {
            t := 5 * q
            Sha1Ch(a, mut b, c, d, mut e, 0x5a827999 +% w[t])
            Sha1Ch(e, mut a, b, c, mut d, 0x5a827999 +% w[t + 1])
            Sha1Ch(d, mut e, a, b, mut c, 0x5a827999 +% w[t + 2])
            Sha1Ch(c, mut d, e, a, mut b, 0x5a827999 +% w[t + 3])
            Sha1Ch(b, mut c, d, e, mut a, 0x5a827999 +% w[t + 4])
        }
        for q in 4..8 {
            t := 5 * q
            Sha1Parity(a, mut b, c, d, mut e, 0x6ed9eba1 +% w[t])
            Sha1Parity(e, mut a, b, c, mut d, 0x6ed9eba1 +% w[t + 1])
            Sha1Parity(d, mut e, a, b, mut c, 0x6ed9eba1 +% w[t + 2])
            Sha1Parity(c, mut d, e, a, mut b, 0x6ed9eba1 +% w[t + 3])
            Sha1Parity(b, mut c, d, e, mut a, 0x6ed9eba1 +% w[t + 4])
        }
        for q in 8..12 {
            t := 5 * q
            Sha1Maj(a, mut b, c, d, mut e, 0x8f1bbcdc +% w[t])
            Sha1Maj(e, mut a, b, c, mut d, 0x8f1bbcdc +% w[t + 1])
            Sha1Maj(d, mut e, a, b, mut c, 0x8f1bbcdc +% w[t + 2])
            Sha1Maj(c, mut d, e, a, mut b, 0x8f1bbcdc +% w[t + 3])
            Sha1Maj(b, mut c, d, e, mut a, 0x8f1bbcdc +% w[t + 4])
        }
        for q in 12..16 {
            t := 5 * q
            Sha1Parity(a, mut b, c, d, mut e, 0xca62c1d6 +% w[t])
            Sha1Parity(e, mut a, b, c, mut d, 0xca62c1d6 +% w[t + 1])
            Sha1Parity(d, mut e, a, b, mut c, 0xca62c1d6 +% w[t + 2])
            Sha1Parity(c, mut d, e, a, mut b, 0xca62c1d6 +% w[t + 3])
            Sha1Parity(b, mut c, d, e, mut a, 0xca62c1d6 +% w[t + 4])
        }
        h[0] = h[0] +% a
        h[1] = h[1] +% b
        h[2] = h[2] +% c
        h[3] = h[3] +% d
        h[4] = h[4] +% e
        i += 64
    }
}

/// Absorbs data. Bytes in any split gives the same digest as Sha1 of the
/// concatenation. Whole blocks are hashed straight from data, without a
/// copy. O(data.Len()); allocates nothing.
export Sha1State.Bytes(mut self, data: []u8) {
    n := data.Len()
    self.total = self.total +% Trunc[u64](n)
    var i := 0
    if self.n > 0 {
        i = CopyBytes(mut self.buf[self.n..64], data)
        self.n += i
        if self.n < 64 {
            return
        }
        Sha1Blocks(mut self.h, self.buf[..])
        self.n = 0
    }
    whole := (n - i) - (n - i) % 64
    if whole > 0 {
        Sha1Blocks(mut self.h, data[i..i + whole])
        i += whole
    }
    if i < n {
        self.n = CopyBytes(mut self.buf[..], data[i..n])
    }
}

/// Returns the digest of everything absorbed. It pads a copy of the state,
/// so the state can keep absorbing. One or two block compressions;
/// allocates nothing.
export Sha1State.Sum(self): [20]u8 {
    var h := self.h
    var tail: [128]u8 = [0; 128]
    _ = CopyBytes(mut tail[..], self.buf[0..self.n])
    tail[self.n] = 0x80
    len := if self.n >= 56 { 128 } else { 64 }
    PutBe64(mut tail[..], len - 8, self.total << 3)
    Sha1Blocks(mut h, tail[0..len])
    var out: [20]u8 = [0; 20]
    for k in 0..5 {
        PutBe32(mut out[..], 4 * k, h[k])
    }
    return out
}

/// Returns the SHA-1 digest of data. SHA-1 is broken for collisions: a
/// chosen-prefix collision is practical. Use it only where a format
/// requires it (the WebSocket handshake, Git object names, legacy
/// identifiers), never for signatures or for integrity against an attacker.
/// O(data.Len()); allocates nothing.
export Sha1(data: []u8): [20]u8 {
    var s := Sha1State.New()
    s.Bytes(data)
    return s.Sum()
}
