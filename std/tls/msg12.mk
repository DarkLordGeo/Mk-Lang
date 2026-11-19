// Why: the TLS 1.2 client needs the messages and ServerHello extensions
// that TLS 1.3 does not have.
//
// What: parsers and writers for ServerKeyExchange, CertificateRequest12,
// Certificate12, ServerHelloDone, ClientKeyExchange, HelloRequest, and the
// extended_master_secret, ec_point_formats and renegotiation_info
// extensions.
//
// How: RFC 5246 7.4 with RFC 8422's ECDHE messages, RFC 7627 and RFC 5746;
// each writer lets the codec tests write every message back byte for byte.
// Every read goes through Cursor, so no input panics; nothing here
// allocates or recurses.

// The TLS 1.2 handshake types (RFC 5246 7.4).
const HsHelloRequest: u8 = 0
const HsServerKeyExchange: u8 = 12
const HsServerHelloDone: u8 = 14
const HsClientKeyExchange: u8 = 16

// ExtPointFormats is RFC 8422 5.1.2's ec_point_formats.
const ExtPointFormats: u16 = 11

// CurveNamed is RFC 8422 5.4's ECCurveType named_curve, the only one.
const CurveNamed: u8 = 3

// Certs12 reads a TLS 1.2 certificate_list's entries in order.
struct Certs12 {
    c: Cursor
}

// ParseCertificate12 reads a TLS 1.2 Certificate body (RFC 5246 7.4.2):
// certificate_list <0..2^24-1> of ASN.1Cert <1..2^24-1>; returns the list,
// whose entries Certs12 gives out.
ParseCertificate12(body: []u8): Protocol![]u8 from body {
    var c := Cursor.New(body)
    list := try c.Vec24(0, 16777215)
    try c.Done()
    var it := Certs12.New(list)
    for e := try it.Next() {
        _ = e
    }
    return list
}

// Certs12.New is the entries of the certificate_list list.
Certs12.New(list: []u8): Certs12 {
    return Certs12(c: Cursor.New(list))
}

// Certs12.Next is the next ASN.1Cert <1..2^24-1>; none at the end.
Certs12.Next(mut self): Protocol!?[]u8 from self.c.b {
    if self.c.Left() == 0 {
        return none
    }
    return some(try self.c.Vec24(1, 16777215))
}

// WriteCertificate12 writes a TLS 1.2 Certificate whose certificate_list
// is list (entries already encoded; empty for the client's answer to a
// CertificateRequest).
WriteCertificate12(w: mut Out, list: []u8) {
    at := OpenMessage(w, HsCertificate)
    w.Vec24(list)
    w.Close(at, 3)
}

// ServerKeyExchange is RFC 8422 5.4's ServerKeyExchange for ECDHE: the
// named curve, the server's public point, the params' bytes (what the
// signature covers after both randoms), and the signature with its
// scheme (RFC 5246 7.4.1.4.1's SignatureAndHashAlgorithm).
struct ServerKeyExchange {
    curve: u16
    point: []u8
    params: []u8
    scheme: u16
    signature: []u8
}

// ParseServerKeyExchange reads a ServerKeyExchange body (RFC 8422 5.4,
// RFC 5246 7.4.3): ServerECDHParams (curve_type, which must be
// named_curve, the NamedCurve and ECPoint point <1..2^8-1>), then a
// digitally-signed struct: the scheme and signature <0..2^16-1>.
ParseServerKeyExchange(body: []u8): Protocol!ServerKeyExchange from body {
    var c := Cursor.New(body)
    ty := try c.U8()
    if ty != CurveNamed {
        fail Protocol
    }
    curve := try c.U16()
    point := try c.Vec8(1, 255)
    params := body[0..c.at]
    scheme := try c.U16()
    signature := try c.Vec16(0, 65535)
    try c.Done()
    return ServerKeyExchange(curve: curve, point: point, params: params, scheme: scheme, signature: signature)
}

// WriteServerKeyExchange writes m as a whole handshake message.
WriteServerKeyExchange(w: mut Out, m: &ServerKeyExchange) {
    at := OpenMessage(w, HsServerKeyExchange)
    w.U8(CurveNamed)
    w.U16(m.curve)
    w.Vec8(m.point)
    w.U16(m.scheme)
    w.Vec16(m.signature)
    w.Close(at, 3)
}

// CertificateRequest12 is RFC 5246 7.4.4's CertificateRequest.
struct CertificateRequest12 {
    types: []u8
    schemes: []u8
    authorities: []u8
}

// ParseCertificateRequest12 reads a TLS 1.2 CertificateRequest body (RFC
// 5246 7.4.4): certificate_types <1..2^8-1>,
// supported_signature_algorithms <2..2^16-2> (pairs of bytes) and
// certificate_authorities <0..2^16-1>.
ParseCertificateRequest12(body: []u8): Protocol!CertificateRequest12 from body {
    var c := Cursor.New(body)
    types := try c.Vec8(1, 255)
    schemes := try c.Vec16(2, 65534)
    if schemes.Len() % 2 != 0 {
        fail Protocol
    }
    authorities := try c.Vec16(0, 65535)
    try c.Done()
    return CertificateRequest12(types: types, schemes: schemes, authorities: authorities)
}

// WriteCertificateRequest12 writes m as a whole handshake message.
WriteCertificateRequest12(w: mut Out, m: &CertificateRequest12) {
    at := OpenMessage(w, HsCertificateRequest)
    w.Vec8(m.types)
    w.Vec16(m.schemes)
    w.Vec16(m.authorities)
    w.Close(at, 3)
}

// ParseServerHelloDone reads a ServerHelloDone body (RFC 5246 7.4.5),
// which is empty.
ParseServerHelloDone(body: []u8): Protocol!void {
    if body.Len() != 0 {
        fail Protocol
    }
}

// WriteServerHelloDone writes the ServerHelloDone message.
WriteServerHelloDone(w: mut Out) {
    at := OpenMessage(w, HsServerHelloDone)
    w.Close(at, 3)
}

// ParseClientKeyExchange reads an ECDHE ClientKeyExchange body (RFC 8422
// 5.7): ClientECDiffieHellmanPublic, ECPoint ecdh_Yc <1..2^8-1>.
ParseClientKeyExchange(body: []u8): Protocol![]u8 from body {
    var c := Cursor.New(body)
    point := try c.Vec8(1, 255)
    try c.Done()
    return point
}

// WriteClientKeyExchange writes the ClientKeyExchange carrying point.
WriteClientKeyExchange(w: mut Out, point: []u8) {
    at := OpenMessage(w, HsClientKeyExchange)
    w.Vec8(point)
    w.Close(at, 3)
}

// ParseHelloRequest reads a HelloRequest body (RFC 5246 7.4.1.1), which is
// empty. The client refuses it in any case (no renegotiation).
ParseHelloRequest(body: []u8): Protocol!void {
    if body.Len() != 0 {
        fail Protocol
    }
}

// ParsePointFormats reads ec_point_formats' data (RFC 8422 5.1.2):
// ec_point_format_list <1..2^8-1>.
ParsePointFormats(data: []u8): Protocol![]u8 from data {
    var c := Cursor.New(data)
    list := try c.Vec8(1, 255)
    try c.Done()
    return list
}

// ParseRenegotiationInfo reads renegotiation_info's data (RFC 5746 3.2):
// renegotiated_connection <0..255>.
ParseRenegotiationInfo(data: []u8): Protocol![]u8 from data {
    var c := Cursor.New(data)
    rc := try c.Vec8(0, 255)
    try c.Done()
    return rc
}

// ParseEms reads extended_master_secret's data (RFC 7627 5.1), which is
// empty.
ParseEms(data: []u8): Protocol!void {
    if data.Len() != 0 {
        fail Protocol
    }
}
