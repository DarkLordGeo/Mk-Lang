// Why: X25519 is the key exchange of TLS 1.3 and most modern protocols.
//
// What: X25519 and its constant-time swap FeCswap.
//
// How: RFC 7748 section 5, the Montgomery ladder written as the RFC's
// pseudo-code (a24 = 121665), over field.mk's GF(2^255 - 19).
// Constant time: the scalar is clamped, the ladder runs a fixed 255 steps
// from bit 254 down, each step does the same field operations whatever the
// bit, and the bit only enters FeCswap's mask; the final inversion is a
// fixed addition chain. No branch or index depends on the secret or the
// point.

// FeCswap swaps a and b when m is all ones and leaves them when m is
// zero, without a branch (RFC 7748's cswap).
FeCswap(a: mut Fe, b: mut Fe, m: u64) {
    x := FeSelect(*b, *a, m)
    y := FeSelect(*a, *b, m)
    *a = x
    *b = y
}

/// Returns the X25519 function of RFC 7748: the scalar secret times the
/// curve point point, as a 32-byte u-coordinate. With the base point (9
/// followed by 31 zero bytes) it gives secret's public key; with a peer's
/// public key it gives the shared secret. secret is clamped as the RFC says
/// (bits 0, 1, 2 and 255 cleared, bit 254 set); point is read little-endian
/// with bit 255 ignored, and a u at or above p is accepted and reduced.
///
/// A point of small order gives the all-zero result, which is returned as
/// it is: a protocol that must refuse it, as TLS 1.3 does, compares the
/// result with zero using Equal. Pass the shared secret through HkdfExtract
/// before using it as a key. Constant time in secret and point: a fixed 255
/// ladder steps and one inversion; allocates nothing.
export X25519(secret: &[32]u8, point: &[32]u8): [32]u8 {
    var k := *secret
    k[0] = k[0] & 248
    k[31] = (k[31] & 127) | 64
    x1 := FeFromBytes(point)
    var x2 := FeOne
    var z2 := FeZero
    var x3 := x1
    var z3 := FeOne
    var swap: u64 = 0
    var t := 254
    for t >= 0 {
        kt := (u64(k[t >> 3]) >> (t & 7)) & 1
        swap = swap ^ kt
        FeCswap(mut x2, mut x3, 0 -% swap)
        FeCswap(mut z2, mut z3, 0 -% swap)
        swap = kt
        a := FeAdd(x2, z2)
        aa := FeSquare(a)
        b := FeSub(x2, z2)
        bb := FeSquare(b)
        e := FeSub(aa, bb)
        c := FeAdd(x3, z3)
        d := FeSub(x3, z3)
        da := FeMul(d, a)
        cb := FeMul(c, b)
        x3 = FeSquare(FeAdd(da, cb))
        z3 = FeMul(x1, FeSquare(FeSub(da, cb)))
        x2 = FeMul(aa, bb)
        z2 = FeMul(e, FeAdd(aa, FeMulSmall(e, 121665)))
        t -= 1
    }
    FeCswap(mut x2, mut x3, 0 -% swap)
    FeCswap(mut z2, mut z3, 0 -% swap)
    return FeBytes(FeMul(x2, FeInvert(z2)))
}
