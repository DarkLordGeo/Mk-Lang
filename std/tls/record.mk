// Why: record protection is the part of TLS that every byte passes
// through; it is kept apart from the connection logic so it can be tested
// on byte slices alone.
//
// What: the record header, Aead (Seal and Open with the suite's AEAD), the
// per-record nonce, the inner plaintext's content type and padding, and
// the alert codes.
//
// How: RFC 8446 section 5 (5.1 to 5.4). The nonce is the IV XOR the 64-bit
// sequence number, and the XOR touches every byte, so its timing does not
// depend on secrets. The connection's reads and writes are conn.mk's.

import std.crypto

// The record content types (RFC 8446 5.1).
const TypeChangeCipherSpec: u8 = 20
const TypeAlert: u8 = 21
const TypeHandshake: u8 = 22
const TypeAppData: u8 = 23

// The alert descriptions std.tls sends (RFC 8446 6).
const AlertCloseNotify: u8 = 0
const AlertUnexpectedMessage: u8 = 10
const AlertBadRecordMac: u8 = 20
const AlertRecordOverflow: u8 = 22
const AlertHandshakeFailure: u8 = 40
const AlertBadCertificate: u8 = 42
const AlertUnsupportedCertificate: u8 = 43
const AlertCertificateExpired: u8 = 45
const AlertIllegalParameter: u8 = 47
const AlertUnknownCa: u8 = 48
const AlertDecodeError: u8 = 50
const AlertDecryptError: u8 = 51
const AlertProtocolVersion: u8 = 70
const AlertInternalError: u8 = 80
const AlertMissingExtension: u8 = 109
const AlertUnsupportedExtension: u8 = 110
const AlertNoApplicationProtocol: u8 = 120

// AlertNoRenegotiation is RFC 5246 7.2.2's no_renegotiation, always at
// the warning level: the TLS 1.2 client's answer to a HelloRequest.
const AlertNoRenegotiation: u8 = 100

// MaxPlain is the most content one record carries (RFC 8446 5.1, 2^14).
const MaxPlain: int = 16384

// MaxSealed is the most a protected record's body holds (RFC 8446 5.2,
// 2^14 + 256).
const MaxSealed: int = 16640

// RecordMax is a whole record's largest size: the header and MaxSealed.
const RecordMax: int = 16645

// Aead protects one direction of a connection: the traffic key's AEAD, its
// IV and the sequence number of the next record. Off before the keys
// exist, when records are plaintext. v12 marks a TLS 1.2 connection's
// protection (record12.mk): its records have no inner type, and its key
// never moves forward.
struct Aead {
    on: bool
    v12: bool
    suite: Suite
    gcm: ?crypto.AesGcm
    key: [32]u8
    iv: [12]u8
    seq: u64
}

// Aead.Off is the protection of a direction with no keys yet.
Aead.Off(): Aead {
    return Aead(on: false, v12: false, suite: Suite.Aes128Gcm, gcm: none, key: [0; 32], iv: [0; 12], seq: 0)
}

// Aead.New is the protection under the traffic key k, from sequence
// number 0.
Aead.New(k: &TrafficKey): Aead {
    n := SuiteKeyLen(k.suite)
    gcm: ?crypto.AesGcm = match k.suite {
        // A 16- or 32-byte key is what AesGcm.New takes, so it cannot fail.
        .Aes128Gcm | .Aes256Gcm { some(crypto.AesGcm.New(k.key[0..n]).Must()) }
        .Chacha20Poly1305 { none }
    }
    return Aead(on: true, v12: false, suite: k.suite, gcm: gcm, key: k.key, iv: k.iv, seq: 0)
}

// Aead.Wipe clears the key, its schedule and the IV, and turns the
// protection off.
Aead.Wipe(mut self) {
    if g := mut self.gcm {
        g.Wipe()
    }
    crypto.Wipe(mut self.key[..])
    crypto.Wipe(mut self.iv[..])
    self.on = false
}

// Aead.Limit is how many records one key of the suite seals before the
// key is moved forward (RFC 8446 5.5): for AES-GCM the RFC allows 2^24.5
// full records per key; std.tls stops at 2^23, rustls's margin. For
// ChaCha20-Poly1305 the sequence number would wrap first, so its limit is
// the last sequence number, as it is for a TLS 1.2 key, which never moves
// forward.
Aead.Limit(&self): u64 {
    if self.v12 {
        return 0xffff_ffff_ffff_ffff
    }
    return match self.suite {
        .Aes128Gcm | .Aes256Gcm { 1 << 23 }
        .Chacha20Poly1305 { 0xffff_ffff_ffff_ffff }
    }
}

// Aead.Worn reports that one record is left under this key: the one that
// carries the KeyUpdate moving it forward.
Aead.Worn(&self): bool {
    return self.on && self.seq >= self.Limit() - 1
}

// Aead.Nonce is the nonce of the next record (RFC 8446 5.3): the IV with
// the sequence number, big-endian and left-padded to 12 bytes, XORed in.
Aead.Nonce(&self): [12]u8 {
    var n := self.iv
    for i in 0..8 {
        n[4 + i] = n[4 + i] ^ Trunc[u8](self.seq >> Trunc[u64](56 - 8 * i))
    }
    return n
}

// RecordHeader writes a record header (RFC 8446 5.1): the type, the
// legacy version 0x0303, and the length.
RecordHeader(ty: u8, len: int, out: mut []u8) {
    out[0] = ty
    out[1] = 3
    out[2] = 3
    out[3] = Trunc[u8](len >> 8)
    out[4] = Trunc[u8](len)
}

// SealedLen is the size of the protected record that carries n bytes of
// content: the header, the content, its type byte and the 16-byte tag.
SealedLen(n: int): int {
    return 5 + n + 1 + 16
}

// Aead.Seal writes the protected record of content data of type ty
// (RFC 8446 5.2) to out[0..SealedLen(data.Len())] and returns that
// length: the header as additional data, the TLSInnerPlaintext data || ty
// (no padding) sealed under the next nonce, the sequence number then
// advanced. data is at most MaxPlain bytes; inner is scratch of at least
// data.Len() + 1 bytes, out of at least SealedLen(data.Len()).
Aead.Seal(mut self, ty: u8, data: []u8, inner: mut []u8, out: mut []u8): int {
    n := data.Len()
    (mut inner[0..n]).CopyFrom(data)
    inner[n] = ty
    total := SealedLen(n)
    RecordHeader(TypeAppData, total - 5, mut out[0..5])
    var head: [5]u8 = [0; 5]
    (mut head[..]).CopyFrom(out[0..5])
    nonce := self.Nonce()
    // The lengths are within both AEADs' limits and out is long enough,
    // and a live key was made by New: neither BadLength nor BadKey.
    if g := &self.gcm {
        _ = g.Seal(&nonce, head[..], inner[0..n + 1], mut out[5..total]).Must()
    } else {
        crypto.ChaCha20Poly1305.Seal(&self.key, &nonce, head[..], inner[0..n + 1], mut out[5..total]).Must()
    }
    self.seq += 1
    return total
}

// Aead.Open opens the protected record whose header is head (5 bytes) and
// whose body is body (RFC 8446 5.2), writing its content to out and
// returning the inner content type and the content's length, the zero
// padding removed (5.4). Fails with Protocol, alert set: bad_record_mac
// when the tag does not verify or the body is shorter than a tag,
// record_overflow when the body or the inner plaintext is too long, and
// unexpected_message when the inner plaintext is all zeros. out is at
// least body.Len() - 16 bytes. Advances the sequence number on success.
Aead.Open(mut self, head: []u8, body: []u8, out: mut []u8, alert: mut u8): Protocol!(ty: u8, n: int) {
    if body.Len() > MaxSealed {
        *alert = AlertRecordOverflow
        fail Protocol
    }
    if body.Len() < 17 {
        *alert = AlertBadRecordMac
        fail Protocol
    }
    if self.seq == 0xffff_ffff_ffff_ffff {
        // The peer would have to wrap the sequence number (RFC 8446 5.3
        // forbids it): no record past the last one opens.
        *alert = AlertUnexpectedMessage
        fail Protocol
    }
    m := body.Len() - 16
    nonce := self.Nonce()
    if g := &self.gcm {
        _ = g.Open(&nonce, head, body, mut out[0..m]) else {
            *alert = AlertBadRecordMac
            fail Protocol
        }
    } else {
        crypto.ChaCha20Poly1305.Open(&self.key, &nonce, head, body, mut out[0..m]) else {
            *alert = AlertBadRecordMac
            fail Protocol
        }
    }
    self.seq += 1
    // The padding is public in length only to the peer; the scan from the
    // end is RFC 8446 5.4's, as every implementation does it.
    var i := m
    for i > 0 && out[i - 1] == 0 {
        i -= 1
    }
    if i == 0 {
        *alert = AlertUnexpectedMessage
        fail Protocol
    }
    if i - 1 > MaxPlain {
        *alert = AlertRecordOverflow
        fail Protocol
    }
    return (ty: out[i - 1], n: i - 1)
}
