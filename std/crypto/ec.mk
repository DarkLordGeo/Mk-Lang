// Why: TLS 1.3 and most key exchanges use P-256 ECDH, and its secrets need
// careful generation and constant-time use.
//
// What: Curve, EcSecret with Generate, FromBytes, Public and Wipe, and
// EcdhShared.
//
// How: the elliptic-curve keys: a P-256 secret (EcSecret), its
// generation by rejection sampling from the OS source, its public point,
// and ECDH (SEC 1 3.3.1, the shared secret the x coordinate of d Q, as
// TLS 1.3 and Go's crypto/ecdh use it). P-384 is verification only
// (ecdsa.mk), so a P-384 secret or exchange fails with BadKey.
// Points cross the API in SEC 1 2.3.3's uncompressed form, 0x04 || x || y;
// a compressed point, the identity's encoding and a point off the curve
// are refused. The group arithmetic is ec_p256.mk's, generated from one
// template with ec_p384.mk.
//
// Constant time: the secret scalar enters only ec_p256.mk's fixed-window
// multiplication (masked table reads, complete formulas, no branch on
// data), Fermat inversion and the scalar field's word operations. What
// branches is public: lengths, the curve, the validity of a peer's point,
// and whether a scalar read from bytes or the OS is in [1, n - 1] (an
// out-of-range secret is refused or redrawn, as Go does).

/// Curve names an elliptic curve: P256 (NIST P-256: secrets, ECDH, signing
/// and verification) or P384 (NIST P-384: verification only).
export open enum Curve {
    P256
    P384
}

/// EcSecret is an elliptic-curve secret key: a scalar d in [1, n - 1] with
/// its curve. Only P-256 secrets exist; asking for a P-384 one is BadKey.
/// It owns no memory: a copy is a copy of the secret, and Wipe clears only
/// the copy it is called on.
export struct EcSecret {
    curve: Curve
    d: [32]u8
}

// EcP256Valid reports whether b (32 big-endian bytes) is a scalar in
// [1, n - 1].
EcP256Valid(b: []u8): bool {
    d := P256nDecode(b) else {
        return false
    }
    return P256nIsZero(&d) == 0
}

/// Returns a new secret for curve from Random: 32 bytes are drawn until
/// they are a scalar in [1, n - 1] (FIPS 186-5 A.4.2's rejection sampling;
/// a draw is refused with probability below 2^-32). Fails with Canceled as
/// Io.Random does, and with BadKey for P384. A cancelled call keeps no
/// state, so it may be called again. One system call per draw; allocates
/// nothing.
export EcSecret.Generate(io: Io, curve: Curve): Canceled|Error!EcSecret {
    match curve {
        .P256 {}
        .P384 {
            fail BadKey
        }
    }
    var b: [32]u8 = [0; 32]
    for true {
        try Random(io, mut b[..])
        if EcP256Valid(b[..]) {
            break
        }
    }
    s := EcSecret(curve: Curve.P256, d: b)
    Wipe(mut b[..])
    return s
}

/// Returns the secret whose scalar is b, big-endian (32 bytes for P-256;
/// SEC 1 2.3.6). Fails with BadLength when b is not 32 bytes, and with
/// BadKey when the scalar is zero or not below the order n, or for P384. b
/// is copied. O(1); allocates nothing.
export EcSecret.FromBytes(curve: Curve, b: []u8): Error!EcSecret {
    match curve {
        .P256 {}
        .P384 {
            fail BadKey
        }
    }
    if b.Len() != 32 {
        fail BadLength
    }
    if !EcP256Valid(b) {
        fail BadKey
    }
    var d: [32]u8 = [0; 32]
    (mut d[..]).CopyFrom(b)
    return EcSecret(curve: Curve.P256, d: d)
}

/// Writes the public key d G, an uncompressed SEC 1 point (65 bytes for
/// P-256), to the front of out and returns its length. Fails with
/// BadLength, writing nothing, when out is shorter. Constant time in the
/// secret: one scalar multiplication and one inversion; allocates nothing.
export EcSecret.Public(&self, out: mut []u8): Error!int {
    if out.Len() < 65 {
        fail BadLength
    }
    p := P256PtBaseMul(self.d[..])
    if !P256PtBytes(&p, mut out[0..65]) {
        // d is in [1, n - 1] by construction, so d G is never the identity.
        fail BadKey
    }
    return 65
}

/// Sets the secret's scalar to zero with Wipe, so it does not stay in
/// memory; the secret is then unusable (BadKey). Cannot fail. O(1);
/// allocates nothing.
export EcSecret.Wipe(mut self) {
    Wipe(mut self.d[..])
}

/// Writes the ECDH shared secret of secret and the peer's public key (an
/// uncompressed SEC 1 point) to out: the x coordinate of d Q, 32 bytes for
/// P-256 (SEC 1 3.3.1). Pass it through HkdfExtract before using it as a
/// key. Fails with BadKey when curve or the secret's curve is not P-256,
/// when peer is not an uncompressed point on the curve, or when the secret
/// was wiped; with BadLength when out is not 32 bytes. Writes nothing on
/// failure. Constant time in the secret: one scalar multiplication and one
/// inversion; allocates nothing.
export EcdhShared(curve: Curve, secret: &EcSecret, peer: []u8, out: mut []u8): Error!void {
    match curve {
        .P256 {}
        .P384 {
            fail BadKey
        }
    }
    match secret.curve {
        .P256 {}
        .P384 {
            fail BadKey
        }
    }
    if out.Len() != 32 {
        fail BadLength
    }
    q := P256PtFromBytes(peer) else {
        fail BadKey
    }
    if !EcP256Valid(secret.d[..]) {
        fail BadKey
    }
    p := P256PtMul(&q, secret.d[..])
    var x: [32]u8 = [0; 32]
    if !P256PtXBytes(&p, mut x[..]) {
        // A point of prime order times a scalar in [1, n - 1] is never the
        // identity.
        fail BadKey
    }
    (mut out[..]).CopyFrom(x[..])
    Wipe(mut x[..])
}
