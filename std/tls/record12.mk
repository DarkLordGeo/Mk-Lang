// Why: a TLS 1.2 connection protects its records differently from TLS 1.3
// (explicit nonces, additional data, no inner content type).
//
// What: the TLS 1.2 seal and open of Aead, Ad12 (the additional data),
// Explicit12, SealedLen12, and ReadHalf.Take12.
//
// How: RFC 5246 6.2.3.3's AEAD records with RFC 5288's AES-GCM nonce (a
// 4-byte salt from the key block and an 8-byte explicit nonce carried in
// the record, the sequence number when sealing) and RFC 7905's
// ChaCha20-Poly1305 nonce (the IV XOR the sequence number). The additional
// data is the sequence number, type, version and length, 13 bytes.
// ReadHalf.Take12 refuses every handshake message after the handshake, a
// HelloRequest included (no renegotiation).

import std.crypto

// Aead.New12 is the TLS 1.2 protection of one direction with the AEAD of
// suite, its key (SuiteKeyLen bytes) and its IV from the key block (4
// bytes of salt for AES-GCM, 12 for ChaCha20-Poly1305), from sequence
// number 0. For AES-GCM the IV is held as salt || 8 zero bytes, so
// Nonce's IV XOR sequence number is RFC 5288's salt || explicit nonce.
Aead.New12(suite: Suite, key: []u8, iv: []u8): Aead {
    var k := TrafficKey(suite: suite, key: [0; 32], iv: [0; 12])
    (mut k.key[0..key.Len()]).CopyFrom(key)
    (mut k.iv[0..iv.Len()]).CopyFrom(iv)
    var x := Aead.New(&k)
    k.Wipe()
    x.v12 = true
    return x
}

// Explicit12 is the explicit nonce an AEAD record of the suite carries
// (RFC 5288 3: 8 bytes for AES-GCM; RFC 7905 2: none for ChaCha20-Poly1305).
Explicit12(s: Suite): int {
    return match s {
        .Chacha20Poly1305 { 0 }
        _ { 8 }
    }
}

// Ad12 writes RFC 5246 6.2.3.3's additional data to ad: seq_num (8 bytes),
// the record's type, version 0x0303 and the plaintext's length.
Ad12(seq: u64, ty: u8, n: int, ad: mut [13]u8) {
    for i in 0..8 {
        ad[i] = Trunc[u8](seq >> Trunc[u64](56 - 8 * i))
    }
    ad[8] = ty
    ad[9] = 3
    ad[10] = 3
    ad[11] = Trunc[u8](n >> 8)
    ad[12] = Trunc[u8](n)
}

// SealedLen12 is the size of the TLS 1.2 record that carries n bytes under
// suite s: the header, the explicit nonce, the content and the tag.
SealedLen12(s: Suite, n: int): int {
    return 5 + Explicit12(s) + n + 16
}

// Aead.Seal12 writes the TLS 1.2 record of data of type ty to
// out[0..SealedLen12(suite, data.Len())] and returns its length (RFC 5246
// 6.2.3.3): the header, for AES-GCM the sequence number as the explicit
// nonce, then data sealed under the nonce with the 13-byte additional
// data; the sequence number then advanced. data is at most MaxPlain
// bytes. The caller never seals record 2^64 - 1: WriteHalf.Renew refuses
// a TLS 1.2 key at its limit.
Aead.Seal12(mut self, ty: u8, data: []u8, out: mut []u8): int {
    n := data.Len()
    x := Explicit12(self.suite)
    total := SealedLen12(self.suite, n)
    RecordHeader(ty, total - 5, mut out[0..5])
    nonce := self.Nonce()
    if x > 0 {
        (mut out[5..13]).CopyFrom(nonce[4..12])
    }
    var ad: [13]u8 = [0; 13]
    Ad12(self.seq, ty, n, mut ad)
    // The lengths are within both AEADs' limits and out is long enough,
    // and a live key was made by New: neither BadLength nor BadKey.
    if g := &self.gcm {
        _ = g.Seal(&nonce, ad[..], data, mut out[5 + x..total]).Must()
    } else {
        crypto.ChaCha20Poly1305.Seal(&self.key, &nonce, ad[..], data, mut out[5 + x..total]).Must()
    }
    self.seq += 1
    return total
}

// Aead.Open12 opens the TLS 1.2 record whose header is head (5 bytes) and
// whose body is body (RFC 5246 6.2.3.3), writing its content to out and
// returning the content's length; the type is head[0]. For AES-GCM the
// nonce is the salt and the record's explicit nonce (RFC 5288 3). Fails
// with Protocol, alert set: bad_record_mac when the tag does not verify or
// the body is shorter than its nonce and tag, record_overflow when the
// body or the content is too long, unexpected_message past the last
// sequence number. out is at least body.Len() bytes. Advances the
// sequence number on success.
Aead.Open12(mut self, head: []u8, body: []u8, out: mut []u8, alert: mut u8): Protocol!int {
    x := Explicit12(self.suite)
    if body.Len() > MaxSealed {
        *alert = AlertRecordOverflow
        fail Protocol
    }
    if body.Len() < x + 16 {
        *alert = AlertBadRecordMac
        fail Protocol
    }
    if self.seq == 0xffff_ffff_ffff_ffff {
        *alert = AlertUnexpectedMessage
        fail Protocol
    }
    m := body.Len() - x - 16
    if m > MaxPlain {
        *alert = AlertRecordOverflow
        fail Protocol
    }
    var nonce := self.Nonce()
    if x > 0 {
        (mut nonce[4..12]).CopyFrom(body[0..8])
    }
    var ad: [13]u8 = [0; 13]
    Ad12(self.seq, head[0], m, mut ad)
    if g := &self.gcm {
        _ = g.Open(&nonce, ad[..], body[x..], mut out[0..m]) else {
            *alert = AlertBadRecordMac
            fail Protocol
        }
    } else {
        crypto.ChaCha20Poly1305.Open(&self.key, &nonce, ad[..], body[x..], mut out[0..m]) else {
            *alert = AlertBadRecordMac
            fail Protocol
        }
    }
    self.seq += 1
    return m
}

// ReadHalf.Take12 opens the TLS 1.2 record at the read-ahead's start,
// whose body is n bytes, and acts on it: application data is given out
// (plain[0..k]), close_notify ends the stream, a warning alert is dropped
// and counted as a record without data, a fatal alert is Alert, and any
// handshake message, a HelloRequest included, is refused with
// no_renegotiation (RFC 5746 4.2: a client that does not renegotiate
// answers so); a ChangeCipherSpec is unexpected_message.
ReadHalf.Take12(mut self, n: int, alert: mut u8): Error!void {
    at := self.start
    var head: [5]u8 = [0; 5]
    (mut head[..]).CopyFrom(self.buf[at..at + 5])
    k := try self.aead.Open12(head[..], self.buf[at + 5..at + 5 + n], mut self.plain[..], alert)
    self.Consume(5 + n)
    ty := head[0]
    if ty == TypeAppData {
        if k > 0 {
            self.useless = 0
        } else {
            try self.NoData(alert)
        }
        self.at = 0
        self.end = k
        return
    }
    if ty == TypeAlert {
        code := try AlertOf(self.plain[0..k], alert)
        if code == AlertCloseNotify {
            self.eof = true
            return
        }
        if self.plain[0] == 1 {
            try self.NoData(alert)
            return
        }
        fail Alert(code: code)
    }
    if ty == TypeHandshake {
        *alert = AlertNoRenegotiation
        fail Protocol
    }
    *alert = AlertUnexpectedMessage
    fail Protocol
}
