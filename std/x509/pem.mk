// Why: certificates usually come as PEM text, a chain or a bundle in one
// file.
//
// What: ParsePem, the CERTIFICATE blocks of RFC 7468 text.
//
// How: each block is decoded by std.encoding's PEM reader in a sub-arena
// freed at once, and its DER copied into a and parsed, so the certificates
// hold a's loan alone and memory is the DER's plus one block's base64.
// Blocks with other labels (a key beside its chain) are skipped.

import std.encoding

const PemBegin: str = "-----BEGIN "
const PemEnd: str = "-----END "

/// The certificates of every CERTIFICATE block of pem, in order. Text
/// outside the blocks, and blocks with other labels, are skipped. O(pem);
/// allocates the list and each certificate's DER in a, and one block's
/// decoding at a time.
///
/// Fails with Malformed when a block is not RFC 7468's or its DER is not a
/// certificate. The offset is the block's begin line, or the fault inside
/// the base64 where std.encoding names one. Fails with OutOfMemory too.
export ParsePem(a: &Arena, pem: str): Error!List[Certificate] from a {
    var out := List[Certificate].New(a)
    try PemCertificates(a, pem, true, mut out)
    return out
}

// PemCertificates appends the certificates of text's CERTIFICATE blocks
// to out. Strict, a block that does not read fails the call; otherwise it
// is skipped, as the system's bundle is read.
PemCertificates(a: &Arena into out, text: str, strict: bool, out: mut List[Certificate]): Malformed|OutOfMemory!void {
    var at := 0
    for at < text.Len() {
        b := text[at..].Find(PemBegin) else {
            return
        }
        start := at + b
        e := text[start..].Find(PemEnd) else {
            if strict {
                fail Malformed(offset: start)
            }
            return
        }
        var stop := start + e
        for stop < text.Len() && text.Byte(stop) != 0x0A {
            stop += 1
        }
        at = stop
        var scratch := try a.Sub()
        defer scratch.Free()
        blocks := encoding.ParsePem(&scratch, text[start..stop]) else err {
            match err {
                OutOfMemory {
                    fail OutOfMemory
                }
                _ {}
            }
            if strict {
                fail Malformed(offset: start + PemWhere(err))
            }
            continue
        }
        if blocks.Len() != 1 || blocks[0].label != "CERTIFICATE" {
            continue
        }
        der := blocks[0].der
        copy := try a.Slice[u8](der.Len())
        copy.CopyFrom(der)
        cert := ParseDer(copy[..]) else {
            if strict {
                fail Malformed(offset: start)
            }
            continue
        }
        try out.Push(cert)
    }
}

// PemWhere is the offset a PEM error names, 0 when it names none.
PemWhere(e: encoding.Error | OutOfMemory): int {
    return match e {
        encoding.BadInput(offset) { offset }
        _ { 0 }
    }
}
