// Why: programs show and compare a certificate's subject, issuer and
// subjectAltName names as text, written the way other tools write them.
//
// What: Certificate.Subject, Certificate.Issuer and Certificate.Names, with
// NameText, AttrText, ValueText, OidText and IpText.
//
// How: a Name is written by RFC 4514 as Go's pkix RDNSequence.String writes
// it: the RDNs last first, joined by ',', the values of one RDN by '+'.
// The details follow.

// Each value is written as its short name (CN, L, ST, O, OU, C, STREET,
// POSTALCODE, SERIALNUMBER) or its dotted OID (Go 1.27, RFC 2253 2.4).
// Text has ',', '+', '"', '\', '<', '>', ';', a leading or trailing space
// and a leading '#' escaped by '\'; a value that is not text is its dotted
// OID and '#' with the hex of its DER (RFC 4514 2.4; Go prints a
// short-named attribute's non-text value with fmt, which no RFC names, so
// std.x509 writes the hex there too). Text is UTF8String, PrintableString,
// IA5String, NumericString, T61String (its bytes read as UTF-8, as Go reads
// them) or BMPString (UTF-16BE).

// The id-at arc 2.5.4 (RFC 5280 A.1): an attribute OID is these two bytes
// and one more.
const IdAt0: u8 = 0x55
const IdAt1: u8 = 0x04

const HexDigits: str = "0123456789abcdef"

/// The subject's distinguished name as text in RFC 4514 form. Fails only
/// with OutOfMemory. O(name); allocates the text in a.
export Certificate.Subject(&self, a: &Arena): OutOfMemory!str from a {
    return try NameText(a, self.SubjectDer())
}

/// The issuer's distinguished name as text in RFC 4514 form. Fails only
/// with OutOfMemory. O(name); allocates the text in a.
export Certificate.Issuer(&self, a: &Arena): OutOfMemory!str from a {
    return try NameText(a, self.IssuerDer())
}

/// The DNS names and IP addresses of the subjectAltName, in order: the DNS
/// names as they are in the certificate, the addresses as text (dotted
/// IPv4, RFC 5952 IPv6). These are the names Verify matches a host
/// against. Fails only with OutOfMemory. O(names); allocates the list and
/// the addresses' text in a. The DNS names are views of the certificate,
/// kept in the list in a (`*self into a`).
export Certificate.Names(&self, a: &Arena): OutOfMemory!List[str] from a, self *self into a {
    return try SanNames(a, self.San())
}

// SanNames is Names over GeneralNames content san.
SanNames(a: &Arena, san: []u8 into a): OutOfMemory!List[str] from a, san {
    var out := List[str].New(a)
    var c := Cur(der: san, pos: 0, end: san.Len())
    for c.More() {
        g := c.Next() else {
            break
        }
        if g.class != 2 || g.cons {
            continue
        }
        b := Content(san, &g)
        if g.tag == NameDns {
            try out.Push(str.From(b) else { "" })
        } else if g.tag == NameIp {
            try out.Push(try IpText(a, b))
        }
    }
    return out
}

// AttrName is the short name of attribute OID o, "" when it has none.
AttrName(o: []u8): str {
    if o.Len() != 3 || o[0] != IdAt0 || o[1] != IdAt1 {
        return ""
    }
    return match o[2] {
        3 { "CN" }
        5 { "SERIALNUMBER" }
        6 { "C" }
        7 { "L" }
        8 { "ST" }
        9 { "STREET" }
        10 { "O" }
        11 { "OU" }
        17 { "POSTALCODE" }
        _ { "" }
    }
}

// NameText writes the Name element name (checked by Parse) in RFC 4514
// form.
NameText(a: &Arena, name: []u8): OutOfMemory!str from a {
    var t := Text.New(a)
    var top := Cur(der: name, pos: 0, end: name.Len())
    whole := top.Next() else {
        return ""
    }
    // The RDNs are written last first: their elements are kept in a, then
    // visited backwards, so the walk is O(name).
    var rdns := List[El].New(a)
    var rc := Inside(name, &whole)
    for rc.More() {
        e := rc.Next() else {
            break
        }
        try rdns.Push(e)
    }
    count := rdns.Len()
    for k in 0..count {
        rdn := rdns[count - 1 - k]
        if k > 0 {
            try t.Push(",")
        }
        var atvs := Inside(name, &rdn)
        var first := true
        for atvs.More() {
            atv := atvs.Next() else {
                break
            }
            if !first {
                try t.Push("+")
            }
            first = false
            var parts := Inside(name, &atv)
            oe := parts.Next() else {
                break
            }
            ve := parts.Next() else {
                break
            }
            try AttrText(a, mut t, name, &oe, &ve)
        }
    }
    return t.Done()
}

// AttrText writes one type and value.
AttrText(a: &Arena, t: mut Text, name: []u8, oe: &El, ve: &El): OutOfMemory!void {
    o := Content(name, oe)
    short := AttrName(o)
    if StringValue(ve) {
        if short.Len() > 0 {
            try t.Push(short)
        } else {
            try OidText(a, t, o)
        }
        try t.Push("=")
        try ValueText(a, t, ve.tag, Content(name, ve))
        return
    }
    try OidText(a, t, o)
    try t.Push("=#")
    for x in name[ve.start..ve.end] {
        try t.Push(HexDigits[int(x >> 4)..int(x >> 4) + 1])
        try t.Push(HexDigits[int(x & 15)..int(x & 15) + 1])
    }
}

// StringValue is whether v is a string type NameText writes as text.
StringValue(v: &El): bool {
    return v.class == 0 && !v.cons && StringTag(v.tag)
}

// ValueText writes a string value with RFC 4514's escapes as Go writes
// them: the value decoded to UTF-8 (T61String's bytes read as UTF-8 with
// U+FFFD for each byte that is not, BMPString from UTF-16BE without a
// final NUL), then ',', '+', '"', '\', '<', '>' and ';', a space first or
// last and a '#' first, each after a backslash.
ValueText(a: &Arena, t: mut Text, tag: u32, b: []u8): OutOfMemory!void {
    var plain := Text.New(a)
    if tag == 30 {
        var n := b.Len()
        if n >= 2 && b[n - 1] == 0 && b[n - 2] == 0 {
            n -= 2
        }
        var i := 0
        for i + 1 < n {
            // Parse refused surrogates and noncharacters.
            try plain.PushChar(Cast[char]((u32(b[i]) << 8) | u32(b[i + 1])))
            i += 2
        }
    } else {
        var i := 0
        for i < b.Len() {
            d := Utf8At(b, i)
            try plain.PushChar(Cast[char](d.c))
            i += d.w
        }
    }
    v := plain.Str()
    n := v.Len()
    var k := 0
    for c in v.Scalars() {
        x := u32(c)
        special := x == 0x2C || x == 0x2B || x == 0x22 || x == 0x5C || x == 0x3C || x == 0x3E || x == 0x3B
        if special || (x == 0x20 && (k == 0 || k == n - 1)) || (x == 0x23 && k == 0) {
            try t.Push("\\")
        }
        try t.PushChar(c)
        k += if x < 0x80 { 1 } else if x < 0x800 { 2 } else if x < 0x10000 { 3 } else { 4 }
    }
}

// Decoded is one scalar and the bytes it took.
type Decoded = (c: u32, w: int)

// Utf8At decodes the UTF-8 scalar at b[i], U+FFFD and one byte for a byte
// that does not start a well-formed sequence (as Go's range over a
// string).
Utf8At(b: []u8, i: int): Decoded {
    x := u32(b[i])
    if x < 0x80 {
        return (c: x, w: 1)
    }
    var need := 0
    var c: u32 = 0
    var lo: u32 = 0x80
    var hi: u32 = 0xBF
    if x >= 0xC2 && x <= 0xDF {
        need = 1
        c = x & 0x1F
    } else if x >= 0xE0 && x <= 0xEF {
        need = 2
        c = x & 0x0F
        if x == 0xE0 {
            lo = 0xA0
        }
        if x == 0xED {
            hi = 0x9F
        }
    } else if x >= 0xF0 && x <= 0xF4 {
        need = 3
        c = x & 0x07
        if x == 0xF0 {
            lo = 0x90
        }
        if x == 0xF4 {
            hi = 0x8F
        }
    } else {
        return (c: 0xFFFD, w: 1)
    }
    if i + need >= b.Len() {
        return (c: 0xFFFD, w: 1)
    }
    for j in 1..need + 1 {
        y := u32(b[i + j])
        if y < lo || y > hi {
            return (c: 0xFFFD, w: 1)
        }
        lo = 0x80
        hi = 0xBF
        c = (c << 6) | (y & 0x3F)
    }
    return (c, w: need + 1)
}

// OidText writes OID content o in dotted form (X.690 8.19); Parse checked
// it (minimal, every arc below 2^31).
OidText(a: &Arena, t: mut Text, o: []u8): OutOfMemory!void {
    var v: u64 = 0
    var first := true
    for x in o {
        v = (v << 7) | u64(x & 0x7F)
        if (x & 0x80) != 0 {
            continue
        }
        if first {
            top: u64 = if v < 40 { 0 } else if v < 80 { 1 } else { 2 }
            try t.Push(try a.Fmt("{top}.{rest}", (top, rest: v - 40 * top)))
            first = false
        } else {
            try t.Push(try a.Fmt(".{v}", (v)))
        }
        v = 0
    }
}

// IpText is a 4- or 16-byte address as text: dotted IPv4, or IPv6 by RFC
// 5952 (lower-case hex, no leading zeros, the first longest run of two or
// more zero groups as "::"), as Go's net.IP.String writes them.
IpText(a: &Arena, b: []u8): OutOfMemory!str from a {
    var t := Text.New(a)
    if b.Len() == 4 {
        try t.Push(try a.Fmt("{x}.{y}.{z}.{w}", (x: b[0], y: b[1], z: b[2], w: b[3])))
        return t.Done()
    }
    var zs := 8
    var ze := 8
    var i := 0
    for i < 8 {
        var j := i
        for j < 8 && b[2 * j] == 0 && b[2 * j + 1] == 0 {
            j += 1
        }
        if j - i >= 2 && (zs == 8 || j - i > ze - zs) {
            zs = i
            ze = j
        }
        i = if j > i { j } else { i + 1 }
    }
    var g := 0
    for g < 8 {
        if g == zs {
            try t.Push("::")
            g = ze
            continue
        }
        if g > 0 && g != ze {
            try t.Push(":")
        }
        v := (int(b[2 * g]) << 8) | int(b[2 * g + 1])
        var started := false
        for shift in [12, 8, 4, 0] {
            d := (v >> shift) & 15
            if d != 0 || started || shift == 0 {
                try t.Push(HexDigits[d..d + 1])
                started = true
            }
        }
        g += 1
    }
    return t.Done()
}
