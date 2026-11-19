// Why: the handshake messages of RFC 8446 section 4 must be parsed without
// trusting the peer and written back byte for byte.
//
// What: one parsing function per message structure (ClientHello,
// ServerHello, Certificate, CertificateRequest, CertificateVerify,
// Finished, NewSessionTicket, ...) and the writers both roles send with.
//
// How: each parsed message is a view of its input that keeps every field.
// A message is parsed whole from its body (the four-byte header already
// split off by ParseHandshake); every vector's bounds are the RFC's, and a
// leftover byte is Protocol. Extensions stay as their encoded block, read by
// ExtIter (ext.mk). Nothing here allocates or recurses.

// The handshake message types (RFC 8446 4).
const HsClientHello: u8 = 1
const HsServerHello: u8 = 2
const HsNewTicket: u8 = 4
const HsEndOfEarlyData: u8 = 5
const HsEncryptedExtensions: u8 = 8
const HsCertificate: u8 = 11
const HsCertificateRequest: u8 = 13
const HsCertificateVerify: u8 = 15
const HsFinished: u8 = 20
const HsKeyUpdate: u8 = 24
const HsMessageHash: u8 = 254

// The ServerHello.random of a HelloRetryRequest (RFC 8446 4.1.3): the
// SHA-256 of "HelloRetryRequest".
const HrrRandom: [32]u8 = [0xcf, 0x21, 0xad, 0x74, 0xe5, 0x9a, 0x61, 0x11, 0xbe, 0x1d, 0x8c, 0x02, 0x1e, 0x65, 0xb8, 0x91,
    0xc2, 0xa2, 0x11, 0x16, 0x7a, 0xbb, 0x8c, 0x5e, 0x07, 0x9e, 0x09, 0xe2, 0xc8, 0xa8, 0x33, 0x9c]

// Handshake is one handshake message (RFC 8446 4): its type and body.
struct Handshake {
    ty: u8
    body: []u8
}

// ParseHandshake reads one whole message from the front of b (RFC 8446
// 4: msg_type, a 24-bit length, the body) and returns it with how many
// bytes it took; none when b does not hold a whole message yet. A length
// past max is Protocol.
ParseHandshake(b: []u8, max: int): Protocol!?(m: Handshake, n: int) from b {
    if b.Len() < 4 {
        return none
    }
    n := (int(b[1]) << 16) | (int(b[2]) << 8) | int(b[3])
    if n > max {
        fail Protocol
    }
    if b.Len() < 4 + n {
        return none
    }
    return some((m: Handshake(ty: b[0], body: b[4..4 + n]), n: 4 + n))
}

// OpenMessage writes a handshake header of type ty with its length to be
// patched by Out.Close(at, 3).
OpenMessage(w: mut Out, ty: u8): int {
    w.U8(ty)
    return w.Open(3)
}

// ClientHello is RFC 8446 4.1.2's ClientHello.
struct ClientHello {
    version: u16
    random: []u8
    legacy_id: []u8
    suites: []u8
    compression: []u8
    exts: []u8
}

// ParseClientHello reads a ClientHello body (RFC 8446 4.1.2):
// legacy_version, a 32-byte random, legacy_legacy_id <0..32>,
// cipher_suites <2..2^16-2> (an even length), legacy_compression_methods
// <1..2^8-1>, and extensions <8..2^16-1>, which TLS 1.3 requires.
ParseClientHello(body: []u8): Protocol!ClientHello from body {
    var c := Cursor.New(body)
    version := try c.U16()
    random := try c.Take(32)
    legacy_id := try c.Vec8(0, 32)
    suites := try c.Vec16(2, 65534)
    if suites.Len() % 2 != 0 {
        fail Protocol
    }
    compression := try c.Vec8(1, 255)
    exts := try c.Vec16(8, 65535)
    try c.Done()
    try ExtCheck(exts)
    return ClientHello(version: version, random: random, legacy_id: legacy_id, suites: suites, compression: compression, exts: exts)
}

// WriteClientHello writes m as a whole handshake message.
WriteClientHello(w: mut Out, m: &ClientHello) {
    at := OpenMessage(w, HsClientHello)
    w.U16(m.version)
    w.Bytes(m.random)
    w.Vec8(m.legacy_id)
    w.Vec16(m.suites)
    w.Vec8(m.compression)
    w.Vec16(m.exts)
    w.Close(at, 3)
}

// ServerHello is RFC 8446 4.1.3's ServerHello, a HelloRetryRequest when
// its random is HrrRandom. has_exts is false for a TLS 1.2 or older
// ServerHello that ends after the compression method (RFC 5246 7.4.1.3).
struct ServerHello {
    version: u16
    random: []u8
    legacy_id: []u8
    suite: u16
    compression: u8
    has_exts: bool
    exts: []u8
}

// ParseServerHello reads a ServerHello body (RFC 8446 4.1.3):
// legacy_version, a 32-byte random, legacy_legacy_id_echo <0..32>, the
// cipher suite, legacy_compression_method, and extensions <0..2^16-1>,
// which may be absent from an older version's ServerHello so that the
// client can refuse its version with protocol_version; TLS 1.3's own
// bound of 6 is the client's check (it needs supported_versions).
ParseServerHello(body: []u8): Protocol!ServerHello from body {
    var c := Cursor.New(body)
    version := try c.U16()
    random := try c.Take(32)
    legacy_id := try c.Vec8(0, 32)
    suite := try c.U16()
    compression := try c.U8()
    has_exts := c.Left() > 0
    var exts := body[0..0]
    if has_exts {
        exts = try c.Vec16(0, 65535)
    }
    try c.Done()
    try ExtCheck(exts)
    return ServerHello(version: version, random: random, legacy_id: legacy_id, suite: suite, compression: compression,
        has_exts: has_exts, exts: exts)
}

// ServerHello.IsRetry reports whether the message is a
// HelloRetryRequest.
ServerHello.IsRetry(&self): bool {
    for i in 0..32 {
        if self.random[i] != HrrRandom[i] {
            return false
        }
    }
    return true
}

// WriteServerHello writes m as a whole handshake message.
WriteServerHello(w: mut Out, m: &ServerHello) {
    at := OpenMessage(w, HsServerHello)
    w.U16(m.version)
    w.Bytes(m.random)
    w.Vec8(m.legacy_id)
    w.U16(m.suite)
    w.U8(m.compression)
    if m.has_exts {
        w.Vec16(m.exts)
    }
    w.Close(at, 3)
}

// ParseEncryptedExtensions reads an EncryptedExtensions body (RFC 8446
// 4.3.1): extensions <0..2^16-1>.
ParseEncryptedExtensions(body: []u8): Protocol![]u8 from body {
    var c := Cursor.New(body)
    exts := try c.Vec16(0, 65535)
    try c.Done()
    try ExtCheck(exts)
    return exts
}

// WriteEncryptedExtensions writes the message carrying exts.
WriteEncryptedExtensions(w: mut Out, exts: []u8) {
    at := OpenMessage(w, HsEncryptedExtensions)
    w.Vec16(exts)
    w.Close(at, 3)
}

// CertificateRequest is RFC 8446 4.3.2's CertificateRequest.
struct CertificateRequest {
    context: []u8
    exts: []u8
}

// ParseCertificateRequest reads a CertificateRequest body (RFC 8446
// 4.3.2): certificate_request_context <0..2^8-1> and extensions
// <2..2^16-1>.
ParseCertificateRequest(body: []u8): Protocol!CertificateRequest from body {
    var c := Cursor.New(body)
    context := try c.Vec8(0, 255)
    exts := try c.Vec16(2, 65535)
    try c.Done()
    try ExtCheck(exts)
    return CertificateRequest(context: context, exts: exts)
}

// WriteCertificateRequest writes m as a whole handshake message.
WriteCertificateRequest(w: mut Out, m: &CertificateRequest) {
    at := OpenMessage(w, HsCertificateRequest)
    w.Vec8(m.context)
    w.Vec16(m.exts)
    w.Close(at, 3)
}

// Certificate is RFC 8446 4.4.2's Certificate: the request context and
// the encoded certificate_list, read by CertEntries.
struct Certificate {
    context: []u8
    list: []u8
}

// ParseCertificate reads a Certificate body (RFC 8446 4.4.2):
// certificate_request_context <0..2^8-1> and certificate_list
// <0..2^24-1>, each CertificateEntry checked: cert_data <1..2^24-1> (an
// X.509 certificate) and extensions <0..2^16-1>.
ParseCertificate(body: []u8): Protocol!Certificate from body {
    var c := Cursor.New(body)
    context := try c.Vec8(0, 255)
    list := try c.Vec24(0, 0xFFFFFF)
    try c.Done()
    var e := CertEntries.New(list)
    for x := try e.Next() {
        try ExtCheck(x.exts)
    }
    return Certificate(context: context, list: list)
}

// CertEntries reads a certificate_list's entries in order.
struct CertEntries {
    c: Cursor
}

// CertEntries.New is the entries of list.
CertEntries.New(list: []u8): CertEntries {
    return CertEntries(c: Cursor.New(list))
}

// CertEntries.Next is the next CertificateEntry (RFC 8446 4.4.2), none
// at the end.
CertEntries.Next(mut self): Protocol!?(cert: []u8, exts: []u8) from self.c.b {
    if self.c.Left() == 0 {
        return none
    }
    cert := try self.c.Vec24(1, 0xFFFFFF)
    exts := try self.c.Vec16(0, 65535)
    return some((cert, exts))
}

// WriteCertificateHead opens a Certificate message with context and its
// certificate_list; each entry is written by WriteCertEntry, then
// CloseCertificate patches both lengths.
WriteCertificateHead(w: mut Out, context: []u8): (msg: int, list: int) {
    msg := OpenMessage(w, HsCertificate)
    w.Vec8(context)
    list := w.Open(3)
    return (msg, list)
}

// WriteCertEntry writes one CertificateEntry: cert and its extensions.
WriteCertEntry(w: mut Out, cert: []u8, exts: []u8) {
    w.Vec24(cert)
    w.Vec16(exts)
}

// CloseCertificate patches the lengths WriteCertificateHead opened.
CloseCertificate(w: mut Out, at: (msg: int, list: int)) {
    w.Close(at.list, 3)
    w.Close(at.msg, 3)
}

// CertificateVerify is RFC 8446 4.4.3's CertificateVerify.
struct CertificateVerify {
    scheme: u16
    signature: []u8
}

// ParseCertificateVerify reads a CertificateVerify body (RFC 8446
// 4.4.3): the SignatureScheme and signature <0..2^16-1>.
ParseCertificateVerify(body: []u8): Protocol!CertificateVerify from body {
    var c := Cursor.New(body)
    scheme := try c.U16()
    signature := try c.Vec16(0, 65535)
    try c.Done()
    return CertificateVerify(scheme: scheme, signature: signature)
}

// WriteCertificateVerify writes m as a whole handshake message.
WriteCertificateVerify(w: mut Out, m: &CertificateVerify) {
    at := OpenMessage(w, HsCertificateVerify)
    w.U16(m.scheme)
    w.Vec16(m.signature)
    w.Close(at, 3)
}

// ParseFinished reads a Finished body (RFC 8446 4.4.4): verify_data of
// exactly the suite hash's length n.
ParseFinished(body: []u8, n: int): Protocol![]u8 from body {
    if body.Len() != n {
        fail Protocol
    }
    return body
}

// WriteFinished writes the Finished message carrying verify_data.
WriteFinished(w: mut Out, verify_data: []u8) {
    at := OpenMessage(w, HsFinished)
    w.Bytes(verify_data)
    w.Close(at, 3)
}

// NewTicket is RFC 8446 4.6.1's NewSessionTicket. std.tls builds
// no resumption, so a client reads and drops it.
struct NewTicket {
    lifetime: u32
    age_add: u32
    nonce: []u8
    ticket: []u8
    exts: []u8
}

// ParseNewTicket reads a NewSessionTicket body (RFC 8446 4.6.1):
// ticket_lifetime, ticket_age_add, ticket_nonce <0..255>, ticket
// <1..2^16-1> and extensions <0..2^16-2>.
ParseNewTicket(body: []u8): Protocol!NewTicket from body {
    var c := Cursor.New(body)
    lifetime := try c.U32()
    age_add := try c.U32()
    nonce := try c.Vec8(0, 255)
    ticket := try c.Vec16(1, 65535)
    exts := try c.Vec16(0, 65534)
    try c.Done()
    try ExtCheck(exts)
    return NewTicket(lifetime: lifetime, age_add: age_add, nonce: nonce, ticket: ticket, exts: exts)
}

// WriteNewTicket writes m as a whole handshake message.
WriteNewTicket(w: mut Out, m: &NewTicket) {
    at := OpenMessage(w, HsNewTicket)
    w.U32(m.lifetime)
    w.U32(m.age_add)
    w.Vec8(m.nonce)
    w.Vec16(m.ticket)
    w.Vec16(m.exts)
    w.Close(at, 3)
}

// ParseKeyUpdate reads a KeyUpdate body (RFC 8446 4.6.3): whether the
// peer asks for a KeyUpdate back (update_requested, 1) or not (0).
ParseKeyUpdate(body: []u8): Protocol!bool {
    if body.Len() != 1 || body[0] > 1 {
        fail Protocol
    }
    return body[0] == 1
}

// WriteKeyUpdate writes a KeyUpdate asking for one back when request is
// true.
WriteKeyUpdate(w: mut Out, request: bool) {
    at := OpenMessage(w, HsKeyUpdate)
    w.U8(if request { 1 } else { 0 })
    w.Close(at, 3)
}
