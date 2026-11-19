// Why: std.x509 does not give every field a certificate tool prints, so
// some are read from the DER directly.
//
// What: CertFields and ReadCert (serial, key bytes, basic constraints),
// RsaKey, Oid as text, and Signed.
//
// How: walks the X.690 encoding with std.encoding's DerRead and DerIter.

import std.big
import std.encoding

/// The DER ended where an element was expected.
export error Truncated

/// DerError is every way reading DER here can fail.
export type DerError = Truncated | encoding.Error | OutOfMemory

/// The fields of a certificate's to-be-signed part this module reads
/// (RFC 5280 4.1), as views of its DER.
export struct CertFields {
    /// The serial number's content: two's complement, big-endian
    export serial: []u8
    /// The SubjectPublicKeyInfo element, tag and length included
    export spki: []u8
    /// Whether basicConstraints marks a CA, none without the extension
    export ca: ?bool
    /// basicConstraints' pathLenConstraint, none when absent
    export path_len: ?int
}

/// ReadCert reads the serial, the key and the basic constraints of the
/// certificate der. Fails with the DER's first fault. O(der); allocates the
/// object identifiers' text in a.
export ReadCert(a: &Arena, der: []u8): DerError!CertFields from der {
    (value: cert, ..) := try encoding.DerRead(der)
    var top := cert.Children()
    tbs := try top.Next() else {
        fail Truncated
    }
    var data := tbs.content
    var index := 0
    var found := CertFields(serial: data[..0], spki: data[..0], ca: none, path_len: none)
    for data.Len() > 0 {
        (rest, value) := try encoding.DerRead(data)
        raw := data[..data.Len() - rest.Len()]
        data = rest
        if value.class == 2 && value.tag == 0 {
            // [0] version, before the serial
            continue
        }
        if value.class == 2 && value.tag == 3 {
            try Extensions(a, &value, mut found)
            continue
        }
        if index == 0 {
            found.serial = try value.Integer()
        } else if index == 5 {
            found.spki = raw
        }
        index += 1
    }
    if index < 6 {
        fail Truncated
    }
    return found
}

// [3] Extensions: SEQUENCE OF Extension { extnID, critical, extnValue }.
Extensions(a: &Arena, outer: &encoding.DerValue, found: mut CertFields): DerError!void {
    var wrap := outer.Children()
    list := try wrap.Next() else {
        fail Truncated
    }
    var exts := list.Children()
    for ext := try exts.Next() {
        var parts := ext.Children()
        id := try parts.Next() else {
            fail Truncated
        }
        if try Oid(a, id.content) != "2.5.29.19" {
            continue
        }
        var octets := try parts.Next() else {
            fail Truncated
        }
        if octets.tag == 1 {
            // critical
            octets = try parts.Next() else {
                fail Truncated
            }
        }
        // BasicConstraints ::= SEQUENCE { cA BOOLEAN DEFAULT FALSE, pathLenConstraint INTEGER OPTIONAL }
        (value: bc, ..) := try encoding.DerRead(octets.content)
        found.ca = some(false)
        var items := bc.Children()
        for item := try items.Next() {
            if item.tag == 1 {
                found.ca = some(try item.Boolean())
            } else if item.tag == 2 {
                n := try item.Integer()
                if n.Len() == 1 {
                    found.path_len = some(Cast[int](n[0]))
                }
            }
        }
    }
}

/// Oid is the dotted text of an OBJECT IDENTIFIER's content (X.690 8.19):
/// two arcs in the first subidentifier, then base-128 groups. Fails with
/// BadInput when a group is unterminated or longer than 63 bits.
export Oid(a: &Arena, content: []u8): DerError!str from a {
    var out := Text.New(a)
    var acc: u64 = 0
    var first := true
    for i, b in content {
        if acc > (1 << 56) {
            fail encoding.BadInput(offset: i)
        }
        acc = (acc << 7) | u64(b & 0x7f)
        if b & 0x80 != 0 {
            continue
        }
        if first {
            x := if acc < 40 { 0 } else if acc < 80 { 1 } else { 2 }
            y := acc - Cast[u64](x) * 40
            try out.Push(try a.Fmt("{x}.{y}", (x, y)))
            first = false
        } else {
            try out.Push(try a.Fmt(".{acc}", (acc)))
        }
        acc = 0
    }
    if content.Len() == 0 || content[content.Len() - 1] & 0x80 != 0 {
        fail encoding.BadInput(offset: content.Len())
    }
    return out.Done()
}

/// An RSA public key's modulus and exponent, as views of its DER.
export struct RsaFields {
    export modulus: []u8
    export exponent: []u8
}

/// RsaKey finds the modulus and exponent of a SubjectPublicKeyInfo with
/// rsaEncryption, or of a PKCS#1 RSAPublicKey (RFC 8017 A.1.1); none for
/// another key.
export RsaKey(a: &Arena, der: []u8): DerError!?RsaFields from der {
    (value: outer, ..) := try encoding.DerRead(der)
    var parts := outer.Children()
    first := try parts.Next() else {
        fail Truncated
    }
    if first.tag == 2 {
        // RSAPublicKey ::= SEQUENCE { modulus INTEGER, publicExponent INTEGER }
        second := try parts.Next() else {
            fail Truncated
        }
        return some(RsaFields(modulus: try first.Integer(), exponent: try second.Integer()))
    }
    var alg := first.Children()
    id := try alg.Next() else {
        fail Truncated
    }
    if try Oid(a, id.content) != "1.2.840.113549.1.1.1" {
        return none
    }
    bits := try parts.Next() else {
        fail Truncated
    }
    (value: key, ..) := try encoding.DerRead(try bits.BitString())
    var nums := key.Children()
    n := try nums.Next() else {
        fail Truncated
    }
    e := try nums.Next() else {
        fail Truncated
    }
    return some(RsaFields(modulus: try n.Integer(), exponent: try e.Integer()))
}

/// Signed is the value of a DER INTEGER's content, two's complement.
/// Allocates it in a.
export Signed(a: &Arena, content: []u8): OutOfMemory!big.Int from a {
    v := try big.FromBytes(a, content)
    if content.Len() == 0 || content[0] & 0x80 == 0 {
        return v
    }
    one := try big.FromI64(a, 1)
    span := try one.Shl(a, 8 * content.Len())
    return try v.Sub(a, &span)
}
