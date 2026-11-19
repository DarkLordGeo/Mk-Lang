// Why: SHA-384 is the hash of TLS suites and signatures at the 192-bit
// security level, such as P-384 ECDSA.
//
// What: Sha384 (one-shot) and Sha384State (incremental), SHA-384 of FIPS
// 180-4 (sections 5.3.4 and 6.5).
//
// How: SHA-512's computation from its own initial hash value; the digest is
// the first 48 bytes of SHA-512's output. The state wraps a Sha512State, so
// the compression is SHA-512's one loop. No allocation; the state is plain.

/// Sha384State is a SHA-384 hash in progress. It is not zero-valid: a
/// zero-filled state is not the start of a hash, so
/// Arena.Slice[Sha384State] is E0701. Make one with New.
export struct Sha384State {
    s: Sha512State
}

/// Returns the state of the empty message. O(1); allocates nothing.
export Sha384State.New(): Sha384State {
    return Sha384State(s: Sha512State(h: [0xcbbb9d5dc1059ed8, 0x629a292a367cd507, 0x9159015a3070dd17, 0x152fecd8f70e5939, 0x67332667ffc00b31, 0x8eb44a8768581511, 0xdb0c2e0d64f98fa7, 0x47b5481dbefa4fa4], buf: [0; 128], n: 0, total: 0))
}

/// Absorbs data. Bytes in any split gives the same digest as Sha384 of the
/// concatenation. Whole blocks are hashed straight from data, without a
/// copy. O(data.Len()); allocates nothing.
export Sha384State.Bytes(mut self, data: []u8) {
    self.s.Bytes(data)
}

/// Returns the 48-byte digest of everything absorbed. It pads a copy of the
/// state, so the state can keep absorbing. One or two block compressions;
/// allocates nothing.
export Sha384State.Sum(self): [48]u8 {
    d := self.s.Sum()
    var out: [48]u8 = [0; 48]
    _ = CopyBytes(mut out[..], d[0..48])
    return out
}

/// Returns the SHA-384 digest of data, SHA-512 truncated to 48 bytes from
/// its own starting values. O(data.Len()); allocates nothing.
export Sha384(data: []u8): [48]u8 {
    var s := Sha384State.New()
    s.Bytes(data)
    return s.Sum()
}
