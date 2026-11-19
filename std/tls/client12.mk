// Why: many servers still speak only TLS 1.2; a client that must reach
// them can turn it on with Config.min_version = Tls12.
//
// What: ClientFlights12, the TLS 1.2 client handshake, with
// CheckServerHello12, Prf12 (the RFC 5246 PRF), VerifySignature12, Keys12,
// Suite12 and the TLS 1.2 ClientHello extensions (WriteHello12Exts).
//
// How: with Config.min_version = Tls12 the ClientHello offers TLS 1.2
// beside TLS 1.3 (supported_versions, the ECDHE suites,
// extended_master_secret, ec_point_formats and an empty
// renegotiation_info). A TLS 1.2
// ServerHello gets RFC 5246 7.3's full handshake with RFC 8422's ECDHE over
// X25519 or P-256: the server's Certificate checked by std.x509, its
// ServerKeyExchange signature over both randoms and the curve parameters,
// an empty Certificate for a CertificateRequest, ClientKeyExchange, the
// extended master secret (RFC 7627, required: a server without it is
// refused with handshake_failure), the key block, and ChangeCipherSpec and
// Finished both ways, compared in constant time. A ServerHello whose random
// ends in RFC 8446 4.1.3's downgrade sentinel is refused. Suites:
// ECDHE-ECDSA and ECDHE-RSA with AES-128-GCM, AES-256-GCM (RFC 5289) and
// ChaCha20-Poly1305 (RFC 7905). Not built: resumption, session tickets,
// renegotiation (a HelloRequest is refused, record12.mk), compression,
// client certificates.
//
// Bounds: Hs's bounds (64 KiB a message, 256 KiB a Certificate, 16
// certificates); every secret (the ephemeral key, the pre-master secret,
// the master secret, the key block) is wiped on every path, and no
// function here recurses.

import std.crypto
import std.net
import std.time
import std.x509

// Suite12 is a TLS 1.2 ECDHE suite: its AEAD with its PRF hash (the TLS
// 1.3 Suite of the same AEAD, whose hash is the suite's PRF hash), and
// whether the server signs with ECDSA or Ed25519 (true) or RSA (false).
struct Suite12 {
    aead: Suite
    ecdsa: bool
}

// Suite12Of is the TLS 1.2 suite of a code point, none for one std.tls
// does not build.
Suite12Of(code: u16): ?Suite12 {
    return match code {
        // TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
        0xc02b { some(Suite12(aead: Suite.Aes128Gcm, ecdsa: true)) }
        // TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
        0xc02f { some(Suite12(aead: Suite.Aes128Gcm, ecdsa: false)) }
        // TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384
        0xc02c { some(Suite12(aead: Suite.Aes256Gcm, ecdsa: true)) }
        // TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384
        0xc030 { some(Suite12(aead: Suite.Aes256Gcm, ecdsa: false)) }
        // TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256
        0xcca9 { some(Suite12(aead: Suite.Chacha20Poly1305, ecdsa: true)) }
        // TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256
        0xcca8 { some(Suite12(aead: Suite.Chacha20Poly1305, ecdsa: false)) }
        _ { none }
    }
}

// ClientSuites12 are the TLS 1.2 suites offered after the TLS 1.3 ones.
const ClientSuites12: [6]u16 = [0xc02b, 0xc02f, 0xc02c, 0xc030, 0xcca9, 0xcca8]

// Downgrade is RFC 8446 4.1.3's sentinel without its last byte, 1 (TLS
// 1.2) or 0 (older).
const Downgrade: [7]u8 = [0x44, 0x4f, 0x57, 0x4e, 0x47, 0x52, 0x44]

// Offers12 reports whether the ClientHello m offers a TLS 1.2 suite, which
// it does only when Config.min_version is Tls12.
Offers12(m: &Hello): bool {
    for x in m.suites {
        if Suite12Of(x).IsSome() {
            return true
        }
    }
    return false
}

// WithSuites12 is suites followed by the TLS 1.2 suites, on a.
WithSuites12(a: &Arena, suites: []u16): OutOfMemory![]u16 from a {
    out := try a.Slice[u16](suites.Len() + ClientSuites12.Len())
    (mut out[0..suites.Len()]).CopyFrom(suites)
    (mut out[suites.Len()..]).CopyFrom(ClientSuites12[..])
    return out[..]
}

// WriteHello12Exts writes the ClientHello extensions TLS 1.2 needs:
// extended_master_secret (RFC 7627 5.1), ec_point_formats with the
// uncompressed format only (RFC 8422 5.1.2) and an empty
// renegotiation_info (RFC 5746 3.4).
WriteHello12Exts(w: mut Out) {
    w.U16(ExtExtendedMasterSecret)
    w.U16(0)
    w.U16(ExtPointFormats)
    w.U16(2)
    w.U8(1)
    w.U8(0)
    w.U16(ExtRenegotiationInfo)
    w.U16(1)
    w.U8(0)
}

// Is12 reports whether the ServerHello sh chose TLS 1.2: legacy_version
// 0x0303 and no supported_versions (RFC 8446 4.2.1).
Is12(sh: &ServerHello): Protocol!bool {
    if sh.version != WireTls12 {
        return false
    }
    return (try FindExt(sh.exts, ExtSupportedVersions)).IsNone()
}

// HasU8 reports whether list holds v.
HasU8(list: []u8, v: u8): bool {
    for x in list {
        if x == v {
            return true
        }
    }
    return false
}

// CheckServerHello12 checks a TLS 1.2 ServerHello against the ClientHello
// m (RFC 5246 7.4.1.3): no compression, an offered TLS 1.2 suite, no
// downgrade sentinel, and only extensions the client asked for:
// extended_master_secret (required), an empty renegotiation_info (RFC 5746
// 3.4), ec_point_formats with the uncompressed format (RFC 8422 5.2),
// server_name's empty acknowledgement, and ALPN with one offered protocol
// (RFC 7301 3.1), copied to chosen. Returns the suite.
CheckServerHello12(alert: mut u8, sh: &ServerHello, m: &Hello, chosen: mut Chosen): Error!Suite12 {
    if sh.compression != 0 {
        try Bad(alert, AlertIllegalParameter)
    }
    var offered := false
    for x in m.suites {
        if x == sh.suite {
            offered = true
        }
    }
    s := Suite12Of(sh.suite) else {
        try Bad(alert, AlertIllegalParameter)
        fail Protocol
    }
    if !offered {
        try Bad(alert, AlertIllegalParameter)
    }
    if crypto.Equal(sh.random[24..31], Downgrade[..]) && sh.random[31] <= 1 {
        try Bad(alert, AlertIllegalParameter)
    }
    var ems := false
    var it := ExtIter.New(sh.exts)
    for x := try it.Next() {
        if x.ty == ExtExtendedMasterSecret {
            try ParseEms(x.data)
            ems = true
        } else if x.ty == ExtRenegotiationInfo {
            rc := try ParseRenegotiationInfo(x.data)
            if rc.Len() != 0 {
                try Bad(alert, AlertHandshakeFailure)
            }
        } else if x.ty == ExtPointFormats {
            formats := try ParsePointFormats(x.data)
            if !HasU8(formats, 0) {
                try Bad(alert, AlertIllegalParameter)
            }
        } else if x.ty == ExtServerName && m.sni.Len() > 0 && x.data.Len() == 0 {
            continue
        } else if x.ty == ExtAlpn && m.alpn.Len() > 0 {
            p := try ChosenAlpn12(alert, x.data, m)
            (mut chosen.alpn[0..p.Len()]).CopyFrom(p)
            chosen.n = p.Len()
        } else {
            try Bad(alert, AlertUnsupportedExtension)
        }
    }
    if !ems {
        *alert = AlertHandshakeFailure
        fail Unsupported
    }
    return s
}

// ChosenAlpn12 reads the ServerHello's ALPN data (RFC 7301 3.1): one
// protocol, one the client offered.
ChosenAlpn12(alert: mut u8, data: []u8, m: &Hello): Protocol![]u8 from data {
    list := try ParseAlpn(data)
    var names := AlpnNames(c: Cursor.New(list))
    p := names.Next() else {
        try Bad(alert, AlertDecodeError)
        fail Protocol
    }
    if names.Next().IsSome() {
        try Bad(alert, AlertIllegalParameter)
    }
    for o in m.alpn {
        if crypto.Equal(o.AsBytes(), p) {
            return p
        }
    }
    try Bad(alert, AlertIllegalParameter)
    fail Protocol
}

// Prf12 writes TLS 1.2's PRF(secret, label, seed) to out (RFC 5246 5)
// under h: P_hash(secret, label || seed), with A(0) = label || seed, A(i)
// = HMAC(secret, A(i - 1)), and the output the blocks HMAC(secret, A(i) ||
// label || seed) cut to out.Len(). label is at most 22 bytes (this
// module's constants), seed at most 64. Every intermediate is wiped.
Prf12(h: crypto.HashKind, secret: []u8, label: str, seed: []u8, out: mut []u8) {
    hl := HashLen(h)
    ll := label.Len()
    ls := ll + seed.Len()
    // buf[48 - hl..48] holds A(i), buf[48..48 + ls] label || seed.
    var buf: [134]u8 = [0; 134]
    (mut buf[48..48 + ll]).CopyFrom(label.AsBytes())
    (mut buf[48 + ll..48 + ls]).CopyFrom(seed)
    var a: [48]u8 = [0; 48]
    Mac(h, secret, buf[48..48 + ls], mut a[..])
    var block: [48]u8 = [0; 48]
    var next: [48]u8 = [0; 48]
    var i := 0
    for i < out.Len() {
        (mut buf[48 - hl..48]).CopyFrom(a[0..hl])
        Mac(h, secret, buf[48 - hl..48 + ls], mut block[..])
        k := if out.Len() - i < hl { out.Len() - i } else { hl }
        (mut out[i..i + k]).CopyFrom(block[0..k])
        i += k
        if i < out.Len() {
            Mac(h, secret, a[0..hl], mut next[..])
            (mut a[..]).CopyFrom(next[..])
        }
    }
    crypto.Wipe(mut buf[..])
    crypto.Wipe(mut a[..])
    crypto.Wipe(mut block[..])
    crypto.Wipe(mut next[..])
}

// Keys12 holds a TLS 1.2 handshake's secrets: the pre-master secret, the
// master secret and the key block (at most 2 x (32 + 12) bytes).
struct Keys12 {
    pre: [32]u8
    master: [48]u8
    block: [88]u8
}

// Keys12.Wipe clears every secret.
Keys12.Wipe(mut self) {
    crypto.Wipe(mut self.pre[..])
    crypto.Wipe(mut self.master[..])
    crypto.Wipe(mut self.block[..])
}

// VerifySignature12 checks the ServerKeyExchange's signature over content
// with the leaf's key (RFC 5246 7.4.1.4.1, RFC 8422 5.4): the key fits the
// suite (ECDSA or Ed25519 for an ECDSA suite, RSA for an RSA suite, else
// unsupported_certificate), and the scheme is one the client offered and
// fits the key: in TLS 1.2 an ECDSA scheme names the hash only, not the
// curve, and RSA may sign with PKCS#1 v1.5 or PSS. A mismatch is
// illegal_parameter, a signature that does not verify decrypt_error.
VerifySignature12(alert: mut u8, key: &crypto.PublicKey, ecdsa: bool, scheme: u16, sig: []u8, content: []u8): Protocol!void {
    var offered := false
    for x in ClientSchemes {
        if x == scheme {
            offered = true
        }
    }
    if !offered {
        try Bad(alert, AlertIllegalParameter)
    }
    var d: [64]u8 = [0; 64]
    ok := match key {
        .Ec(curve, point) {
            if !ecdsa {
                try Bad(alert, AlertUnsupportedCertificate)
            }
            if scheme != SchemeEcdsaP256 && scheme != SchemeEcdsaP384 {
                try Bad(alert, AlertIllegalParameter)
            }
            h := if scheme == SchemeEcdsaP256 { crypto.HashKind.Sha256 } else { crypto.HashKind.Sha384 }
            n := Digest(h, content, mut d[..])
            crypto.EcdsaVerify(*point, *curve, d[0..n], crypto.SigFormat.Der, sig)
        }
        .Rsa(key: rsa) {
            if ecdsa {
                try Bad(alert, AlertUnsupportedCertificate)
            }
            pss := scheme == SchemePssSha256 || scheme == SchemePssSha384 || scheme == SchemePssSha512
            h := if scheme == SchemePssSha256 || scheme == SchemePkcs1Sha256 {
                crypto.HashKind.Sha256
            } else if scheme == SchemePssSha384 || scheme == SchemePkcs1Sha384 {
                crypto.HashKind.Sha384
            } else if scheme == SchemePssSha512 || scheme == SchemePkcs1Sha512 {
                crypto.HashKind.Sha512
            } else {
                try Bad(alert, AlertIllegalParameter)
                crypto.HashKind.Sha256
            }
            n := Digest(h, content, mut d[..])
            if pss {
                crypto.RsaVerifyPss(rsa, h, d[0..n], sig)
            } else {
                crypto.RsaVerifyPkcs1(rsa, h, d[0..n], sig)
            }
        }
        .Ed25519(key: pk) {
            if !ecdsa {
                try Bad(alert, AlertUnsupportedCertificate)
            }
            if scheme != SchemeEd25519 {
                try Bad(alert, AlertIllegalParameter)
            }
            if sig.Len() != 64 {
                try Bad(alert, AlertDecryptError)
            }
            var s: [64]u8 = [0; 64]
            (mut s[..]).CopyFrom(sig)
            crypto.Ed25519.Verify(pk, content, &s)
        }
    }
    if !ok {
        try Bad(alert, AlertDecryptError)
    }
}

// Hs.Next12 reads TLS 1.2 records until buf starts with one whole
// handshake message and returns its type and length with its header:
// plaintext before the server's ChangeCipherSpec, opened by Aead.Open12
// after it. An alert ends the handshake with Alert; a ChangeCipherSpec
// (Hs.Ccs12 reads the one expected), application data or an empty
// handshake record is unexpected_message.
Hs.Next12(mut self, rd: mut ReadHalf, conn: &net.Conn, io: Io): Error!(ty: u8, n: int) {
    for {
        if self.have >= 4 {
            len := (int(self.buf[1]) << 16) | (int(self.buf[2]) << 8) | int(self.buf[3])
            max := if self.buf[0] == HsCertificate { MaxCertificate } else { MaxMessage }
            if len > max {
                try Bad(mut self.alert, AlertDecodeError)
            }
            if self.have >= 4 + len {
                return (ty: self.buf[0], n: 4 + len)
            }
        }
        n := try rd.Record(conn, io, mut self.alert)
        at := rd.start
        ty := rd.buf[at]
        if ty != TypeHandshake && ty != TypeAlert {
            try Bad(mut self.alert, AlertUnexpectedMessage)
        }
        var k := n
        if rd.aead.on {
            var head: [5]u8 = [0; 5]
            (mut head[..]).CopyFrom(rd.buf[at..at + 5])
            k = try rd.aead.Open12(head[..], rd.buf[at + 5..at + 5 + n], mut rd.plain[..], mut self.alert)
        } else {
            (mut rd.plain[0..n]).CopyFrom(rd.buf[at + 5..at + 5 + n])
        }
        rd.Consume(5 + n)
        if ty == TypeAlert {
            fail Alert(code: try AlertOf(rd.plain[0..k], mut self.alert))
        }
        if k == 0 {
            try Bad(mut self.alert, AlertUnexpectedMessage)
        }
        try self.Append(rd.plain[0..k])
    }
}

// Hs.Expect12 is Next12 for a message that must be of type ty; returns its
// length.
Hs.Expect12(mut self, rd: mut ReadHalf, conn: &net.Conn, io: Io, ty: u8): Error!int {
    m := try self.Next12(rd, conn, io)
    if m.ty != ty {
        try Bad(mut self.alert, AlertUnexpectedMessage)
    }
    return m.n
}

// Hs.Ccs12 reads the server's ChangeCipherSpec (RFC 5246 7.1): one record
// of one byte 1, at a message boundary; a plaintext alert ends the
// handshake with Alert, anything else is unexpected_message.
Hs.Ccs12(mut self, rd: mut ReadHalf, conn: &net.Conn, io: Io): Error!void {
    if self.have != 0 {
        try Bad(mut self.alert, AlertUnexpectedMessage)
    }
    n := try rd.Record(conn, io, mut self.alert)
    at := rd.start
    ty := rd.buf[at]
    if ty == TypeAlert {
        code := try AlertOf(rd.buf[at + 5..at + 5 + n], mut self.alert)
        rd.Consume(5 + n)
        fail Alert(code: code)
    }
    if ty != TypeChangeCipherSpec || n != 1 || rd.buf[at + 5] != 1 {
        try Bad(mut self.alert, AlertUnexpectedMessage)
    }
    rd.Consume(6)
}

// ClientFlights12 is the client's side of RFC 5246 7.3's full handshake
// after the TLS 1.2 ServerHello hs.buf[0..n] that answers m, on the
// locked halves rd and wr, the server's chain checked for name; returns
// the chosen protocol. Both directions end on the TLS 1.2 record
// protection of the suite (Aead.New12).
ClientFlights12(h: &Heap, a: &Arena, io: Io, conn: &net.Conn, c: &Config, hs: mut Hs, m: &Hello, n: int, name: str, rd: mut ReadHalf, wr: mut WriteHalf): Error!Chosen {
    var chosen := Chosen(alpn: [0; 255], n: 0)
    var server_random: [32]u8 = [0; 32]
    sh := try ParseServerHello(hs.buf[4..n])
    s := try CheckServerHello12(mut hs.alert, &sh, m, mut chosen)
    (mut server_random[..]).CopyFrom(sh.random)
    kind := SuiteHash(s.aead)
    hl := HashLen(kind)
    hs.transcript.Choose(kind)
    hs.transcript.Add(hs.buf[0..n])
    hs.Drop(n)
    // Certificate (RFC 5246 7.4.2), checked as TLS 1.3's is.
    n1 := try hs.Expect12(rd, conn, io, HsCertificate)
    var chain := List[x509.Certificate].New(a)
    var entries := Certs12.New(try ParseCertificate12(hs.buf[4..n1]))
    for e := try entries.Next() {
        if chain.Len() == MaxChain {
            try Bad(mut hs.alert, AlertBadCertificate)
        }
        der := try a.Slice[u8](e.Len())
        (mut der[..]).CopyFrom(e)
        try chain.Push(try x509.Parse(a, der[..]))
    }
    if chain.Len() == 0 {
        try Bad(mut hs.alert, AlertDecodeError)
    }
    hs.transcript.Add(hs.buf[0..n1])
    hs.Drop(n1)
    certs := chain.Done()
    var pool := x509.Pool.New(certs[0..0])
    if r := c.roots {
        pool = *r
    } else {
        pool = try SystemPoolOf(h, a, io)
    }
    try x509.Verify(a, io, certs[..], &pool, time.Wall(io), name)
    leaf_key := certs[0].PublicKey() else {
        try Bad(mut hs.alert, AlertUnsupportedCertificate)
        fail Protocol
    }
    // ServerKeyExchange (RFC 8422 5.4): a curve the client offered, a
    // point of its size, signed over both randoms and the params.
    n2 := try hs.Expect12(rd, conn, io, HsServerKeyExchange)
    ske := try ParseServerKeyExchange(hs.buf[4..n2])
    group := ske.curve
    x25519 := group == GroupX25519 && ske.point.Len() == 32
    p256 := group == GroupP256 && ske.point.Len() == 65
    if !x25519 && !p256 {
        try Bad(mut hs.alert, AlertIllegalParameter)
    }
    var content: [140]u8 = [0; 140]
    (mut content[0..32]).CopyFrom(m.random[..])
    (mut content[32..64]).CopyFrom(server_random[..])
    pl := ske.params.Len()
    (mut content[64..64 + pl]).CopyFrom(ske.params)
    try VerifySignature12(mut hs.alert, &leaf_key, s.ecdsa, ske.scheme, ske.signature, content[0..64 + pl])
    var peer: [65]u8 = [0; 65]
    peer_len := ske.point.Len()
    (mut peer[0..peer_len]).CopyFrom(ske.point)
    hs.transcript.Add(hs.buf[0..n2])
    hs.Drop(n2)
    // An optional CertificateRequest (7.4.4), then ServerHelloDone (7.4.5),
    // the last message of the server's flight.
    var requested := false
    var mc := try hs.Next12(rd, conn, io)
    if mc.ty == HsCertificateRequest {
        _ = try ParseCertificateRequest12(hs.buf[4..mc.n])
        requested = true
        hs.transcript.Add(hs.buf[0..mc.n])
        hs.Drop(mc.n)
        mc = try hs.Next12(rd, conn, io)
    }
    if mc.ty != HsServerHelloDone {
        try Bad(mut hs.alert, AlertUnexpectedMessage)
    }
    try ParseServerHelloDone(hs.buf[4..mc.n])
    hs.transcript.Add(hs.buf[0..mc.n])
    hs.Drop(mc.n)
    if hs.have != 0 {
        try Bad(mut hs.alert, AlertUnexpectedMessage)
    }
    // The client's ephemeral key on the server's curve and the pre-master
    // secret (RFC 8422 5.10: X25519's output, refused when all zero, or
    // P-256's x coordinate).
    var keys := Keys12(pre: [0; 32], master: [0; 48], block: [0; 88])
    defer keys.Wipe()
    var shares := Shares(x25519: [0; 32], x25519_pub: [0; 32], p256: none, p256_pub: [0; 65])
    defer shares.Wipe()
    var mine_len := 32
    if x25519 {
        try crypto.Random(io, mut shares.x25519[..])
        shares.x25519_pub = crypto.X25519(&shares.x25519, &X25519Base)
    } else {
        sec := crypto.EcSecret.Generate(io, crypto.Curve.P256) else e {
            match e {
                Canceled {
                    fail Canceled
                }
                _ {
                    try Bad(mut hs.alert, AlertInternalError)
                    fail Protocol
                }
            }
        }
        // A P-256 secret's public point fits 65 bytes: Public cannot fail.
        _ = sec.Public(mut shares.p256_pub[..]).Must()
        shares.p256 = some(sec)
        mine_len = 65
    }
    try SharedSecret(mut hs.alert, &shares, group, peer[0..peer_len], mut keys.pre[..])
    // The client's flight: an empty Certificate when one was requested,
    // and ClientKeyExchange (RFC 8422 5.7).
    var flight: [96]u8 = [0; 96]
    var fw := Out.New(mut flight[..])
    if requested {
        WriteCertificate12(mut fw, b"")
    }
    if x25519 {
        WriteClientKeyExchange(mut fw, shares.x25519_pub[0..mine_len])
    } else {
        WriteClientKeyExchange(mut fw, shares.p256_pub[0..mine_len])
    }
    hs.transcript.Add(fw.b[0..fw.at])
    try wr.Send(conn, io, TypeHandshake, fw.b[0..fw.at])
    // The extended master secret over the session hash, the transcript
    // through ClientKeyExchange (RFC 7627 4), then the key block (RFC 5246
    // 6.3): client key, server key, client IV, server IV.
    var th: [48]u8 = [0; 48]
    hs.transcript.Sum(kind, mut th[..])
    Prf12(kind, keys.pre[..], "extended master secret", th[0..hl], mut keys.master[..])
    crypto.Wipe(mut keys.pre[..])
    var seed: [64]u8 = [0; 64]
    (mut seed[0..32]).CopyFrom(server_random[..])
    (mut seed[32..64]).CopyFrom(m.random[..])
    kl := SuiteKeyLen(s.aead)
    il := 12 - Explicit12(s.aead)
    Prf12(kind, keys.master[..], "key expansion", seed[..], mut keys.block[0..2 * (kl + il)])
    // ChangeCipherSpec, then the client's Finished under its new keys
    // (RFC 5246 7.4.9: 12 bytes of PRF(master, "client finished",
    // Hash(handshake messages))).
    try wr.Send(conn, io, TypeChangeCipherSpec, Ccs[..])
    wr.aead.Wipe()
    wr.aead = Aead.New12(s.aead, keys.block[0..kl], keys.block[2 * kl..2 * kl + il])
    hs.transcript.Sum(kind, mut th[..])
    var fin: [12]u8 = [0; 12]
    Prf12(kind, keys.master[..], "client finished", th[0..hl], mut fin[..])
    var fm: [16]u8 = [0; 16]
    var fw2 := Out.New(mut fm[..])
    WriteFinished(mut fw2, fin[..])
    hs.transcript.Add(fw2.b[0..fw2.at])
    try wr.Send(conn, io, TypeHandshake, fw2.b[0..fw2.at])
    // The server's ChangeCipherSpec and Finished, over the transcript
    // through the client's Finished.
    try hs.Ccs12(rd, conn, io)
    rd.aead.Wipe()
    rd.aead = Aead.New12(s.aead, keys.block[kl..2 * kl], keys.block[2 * kl + il..2 * kl + 2 * il])
    hs.transcript.Sum(kind, mut th[..])
    var want: [12]u8 = [0; 12]
    Prf12(kind, keys.master[..], "server finished", th[0..hl], mut want[..])
    n4 := try hs.Expect12(rd, conn, io, HsFinished)
    got := try ParseFinished(hs.buf[4..n4], 12)
    if !crypto.Equal(got, want[..]) {
        try Bad(mut hs.alert, AlertDecryptError)
    }
    hs.Drop(n4)
    if hs.have != 0 {
        try Bad(mut hs.alert, AlertUnexpectedMessage)
    }
    return chosen
}
