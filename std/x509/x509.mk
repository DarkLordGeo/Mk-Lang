// Why: std.tls and programs need X.509 certificates by RFC 5280, parsed
// without trusting the input; std.x509 is marked Experimental.
//
// What: Certificate (NotBefore, NotAfter, Der), Span, the errors Malformed,
// UnknownAuthority, Expired, NameMismatch, BadSignature, BadUsage,
// TooComplex and Unsupported, and their union Error.
//
// How: a Certificate is a set of views of its DER: Parse records where each
// part is, and the extensions a check needs are kept as DER and walked when
// used, so a certificate owns nothing and is plain, Send and Share. More
// below.

// Every element is read through std.encoding's DER reader, one at a time,
// with no recursion, so parsing holds O(1) stack and memory whatever the
// input.

import std.time

/// The input is not a certificate std.x509 reads. offset is the byte of
/// the input, the DER or the PEM text, where the first fault is.
export error Malformed(offset: int)

/// No path from the chain leads to a certificate of the pool.
export error UnknownAuthority

/// A certificate of the path is not valid at the time given.
export error Expired

/// The host is not one of the leaf's subjectAltName names.
export error NameMismatch

/// A signature of the path does not verify under its issuer's key.
export error BadSignature

/// A certificate of the path may not be used so: an issuer that is not a
/// CA, lacks keyCertSign, or is followed by more intermediates than its
/// path length allows; a name outside an issuer's name constraints; a leaf
/// whose key usage lacks digitalSignature; or an extended key usage
/// without serverAuth.
export error BadUsage

/// Verify gave up: 100 signature checks, or 2^20 units of name-constraint
/// work, did not settle the chain, or the chain handed to the platform's
/// check is longer than 16 certificates.
export error TooComplex

/// The path needs what std.x509 does not support: a signature by SHA-1 or
/// MD5 (refused), a key or signature algorithm std.crypto lacks, an
/// unhandled critical extension, or a platform verifier the target lacks.
/// On Windows it is also the platform's refusal of a weak signature or
/// key.
export error Unsupported

/// Every failure of std.x509. The set is open, so a match on it outside
/// std keeps a `_` arm.
export open type Error = Malformed | UnknownAuthority | Expired | NameMismatch | BadSignature | BadUsage | TooComplex | Unsupported | Canceled | OutOfMemory

/// One parsed X.509 certificate: data is its DER, and the rest records
/// where each part of data is and what the extensions say. A plain set of
/// views of its DER, Send and Share; only Parse and ParsePem make one.
export struct Certificate {
    /// The certificate's DER, as Der returns it.
    export data: []u8
    // The parts of data: the TBSCertificate element (what the signature
    // covers), the signature's bytes, the issuer and subject Name elements
    // (compared byte for byte), the SubjectPublicKeyInfo element (parsed
    // when a signature by this certificate is checked), the content of
    // the subjectAltName's GeneralNames and of the name constraints'
    // subtrees.
    tbs_span: Span
    sig_span: Span
    issuer_span: Span
    subject_span: Span
    spki_span: Span
    san_span: Span
    permitted_span: Span
    excluded_span: Span
    // sig_alg is the signature algorithm (SigAlg's codes).
    sig_alg: u8
    valid_from: time.Time
    valid_to: time.Time
    // flags are the Flag bits; path_len the basic constraints' length, -1
    // when absent; key_usage the KeyUsage bits, bit 0 digitalSignature.
    flags: u32
    path_len: int
    key_usage: u32
}

// Span is data[at..end].
struct Span {
    at: int
    end: int
}

// NoSpan is an absent part.
const NoSpan: Span = Span(at: 0, end: 0)

// Tbs is the TBSCertificate element.
Certificate.Tbs(&self): []u8 from self.data {
    return self.data[self.tbs_span.at..self.tbs_span.end]
}

// Signature is the signature's bytes.
Certificate.Signature(&self): []u8 from self.data {
    return self.data[self.sig_span.at..self.sig_span.end]
}

// IssuerDer is the issuer's Name element.
Certificate.IssuerDer(&self): []u8 from self.data {
    return self.data[self.issuer_span.at..self.issuer_span.end]
}

// SubjectDer is the subject's Name element.
Certificate.SubjectDer(&self): []u8 from self.data {
    return self.data[self.subject_span.at..self.subject_span.end]
}

// Spki is the SubjectPublicKeyInfo element.
Certificate.Spki(&self): []u8 from self.data {
    return self.data[self.spki_span.at..self.spki_span.end]
}

// San is the subjectAltName's GeneralNames content, empty when absent.
Certificate.San(&self): []u8 from self.data {
    return self.data[self.san_span.at..self.san_span.end]
}

// Permitted is the permitted subtrees' content, empty when absent.
Certificate.Permitted(&self): []u8 from self.data {
    return self.data[self.permitted_span.at..self.permitted_span.end]
}

// Excluded is the excluded subtrees' content, empty when absent.
Certificate.Excluded(&self): []u8 from self.data {
    return self.data[self.excluded_span.at..self.excluded_span.end]
}

// The Flag bits of Certificate.flags.
const FlagBasic: u32 = 1
const FlagCa: u32 = 2
const FlagKeyUsage: u32 = 4
const FlagEku: u32 = 8
const FlagEkuServer: u32 = 16
const FlagEkuAny: u32 = 32
const FlagCritical: u32 = 64
const FlagSan: u32 = 128
const FlagConstraints: u32 = 256

// KeyUsage bits by RFC 5280 4.2.1.3's numbering.
const UsageDigitalSignature: u32 = 1
const UsageKeyCertSign: u32 = 32

/// The start of the validity period. A time outside Time's range
/// saturates to that end of the range. O(1); allocates nothing.
export Certificate.NotBefore(&self): time.Time {
    return self.valid_from
}

/// The end of the validity period. RFC 5280's no-expiry value
/// (99991231235959Z) and any time past Time's range saturate to the end
/// of the range. O(1); allocates nothing.
export Certificate.NotAfter(&self): time.Time {
    return self.valid_to
}

/// The certificate's DER encoding. O(1); allocates nothing.
export Certificate.Der(&self): []u8 from self {
    return self.data
}

// Has is whether the certificate's flags hold f.
Certificate.Has(&self, f: u32): bool {
    return (self.flags & f) != 0
}

// Same is whether a and b hold the same bytes (public data).
Same(a: []u8, b: []u8): bool {
    if a.Len() != b.Len() {
        return false
    }
    for i in 0..a.Len() {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}
