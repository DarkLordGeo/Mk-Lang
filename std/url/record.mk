// Why: a parsed URL is stored as its serialization with offsets, but
// parsing and the setters work on a record of separate components; this
// file converts between the two.
//
// What: the URL Standard's URL record (its section 4.1) as Record, its
// special schemes and default ports (section 4.2), Serialize (the URL
// serializer of section 4.5) and RecordOf.
//
// How: Serialize writes the href once into a buffer of exactly its length,
// with the offsets of its components; RecordOf goes back, a Url's record as
// views of a copy of its href, for the setters and for a base.

import std.rt

// Record is the URL Standard's URL record while it is built: views of the
// input, of a base URL's href and of the arena. host is none for a null
// host; query and fragment are none when null.
struct Record {
    scheme: []u8
    // special, file and dport are set with the scheme (Parser.SetScheme):
    // whether it is special, whether it is "file", and its default port.
    special: bool
    file: bool
    dport: ?u16
    username: []u8
    password: []u8
    host: ?HostValue
    port: ?u16
    opaque: bool
    opath: []u8
    path: List[[]u8]
    // tail is the path's last segments as one span, joined by '/', when the
    // path state took them in one step (Parser.PathRun): the path is path's
    // segments and then tail's. Only the serializer reads it.
    tail: ?[]u8
    query: ?[]u8
    fragment: ?[]u8
}

// Record.New is the standard's new URL: every component empty or null.
Record.New(a: &Arena): Record {
    return Record(scheme: "".AsBytes(), special: false, file: false, dport: none, username: "".AsBytes(), password: "".AsBytes(), host: none, port: none, opaque: false, opath: "".AsBytes(), path: List[[]u8].New(a), tail: none, query: none, fragment: none)
}

// IsSpecialScheme is whether scheme is one of the URL Standard's special
// schemes.
IsSpecialScheme(scheme: []u8): bool {
    return match scheme.Len() {
        2 { SameText(scheme, "ws") }
        3 { SameText(scheme, "wss") || SameText(scheme, "ftp") }
        4 { SameText(scheme, "http") || SameText(scheme, "file") }
        5 { SameText(scheme, "https") }
        _ { false }
    }
}

// DefaultPort is the default port of scheme, none for a scheme with none.
DefaultPort(scheme: []u8): ?u16 {
    if SameText(scheme, "http") || SameText(scheme, "ws") {
        return some(80)
    }
    if SameText(scheme, "https") || SameText(scheme, "wss") {
        return some(443)
    }
    if SameText(scheme, "ftp") {
        return some(21)
    }
    return none
}

// SamePort is whether two ports, either null, are equal.
SamePort(x: ?u16, y: ?u16): bool {
    if a := x {
        if b := y {
            return a == b
        }
        return false
    }
    return y.IsNone()
}

// SchemeOf is a scheme's buffer lower-cased, as the scheme state builds it.
SchemeOf(a: &Arena, raw: []u8): OutOfMemory![]u8 from a, raw {
    return try LowerAscii(a, raw)
}

// Credentials is whether url includes credentials.
Credentials(url: &Record): bool {
    return url.username.Len() > 0 || url.password.Len() > 0
}

// IsEmptyHost is whether host is the empty host.
IsEmptyHost(host: &?HostValue): bool {
    if h := *host {
        return match h {
            .Empty { true }
            _ { false }
        }
    }
    return false
}

// DecimalLen is the number of decimal digits of n.
DecimalLen(n: u16): int {
    var k := 1
    var v := n
    for v >= 10 {
        v /= 10
        k += 1
    }
    return k
}

// Put copies s into out at o and returns the offset after it.
Put(out: mut []u8, o: int, s: []u8): int {
    (mut out[o..o + s.Len()]).CopyFrom(s)
    return o + s.Len()
}

// PutByte writes c into out at o and returns the offset after it.
PutByte(out: mut []u8, o: int, c: u8): int {
    out[o] = c
    return o + 1
}

// Serialize is the URL serializer on url, as a Url in a: the href written
// once into a buffer of exactly its length, and the offsets of its
// components. O(n) in the href's length; allocates it in a.
Serialize(a: &Arena, url: &Record): OutOfMemory!Url from a {
    var ip: [48]u8 = [0; 48]
    var ip_len := 0
    var n := url.scheme.Len() + 1
    creds := Credentials(url)
    if h := url.host {
        ip_len = IpText(&h, mut ip[..])
        n += 2 + ip_len + HostBytes(&h).Len()
        if creds {
            n += url.username.Len() + 1
            if url.password.Len() > 0 {
                n += url.password.Len() + 1
            }
        }
        if port := url.port {
            n += 1 + DecimalLen(port)
        }
    }
    // Step 3 of the serializer: "/." keeps a path starting "//" from
    // reading as a host.
    dot := url.host.IsNone() && !url.opaque && url.path.Len() > 1 && url.path[0].Len() == 0
    if dot {
        n += 2
    }
    if url.opaque {
        n += url.opath.Len()
    } else {
        for seg in url.path {
            n += 1 + seg.Len()
        }
        if t := url.tail {
            n += 1 + t.Len()
        }
    }
    if q := url.query {
        n += 1 + q.Len()
    }
    if f := url.fragment {
        n += 1 + f.Len()
    }
    out := try a.Slice[u8](n)
    var o := Put(out, 0, url.scheme)
    scheme_end := o
    o = PutByte(out, o, b':')
    var user_start := o
    var user_end := o
    var pass_start := o
    var pass_end := o
    var host_start := o
    var host_end := o
    if h := url.host {
        o = PutByte(out, o, b'/')
        o = PutByte(out, o, b'/')
        if creds {
            user_start = o
            o = Put(out, o, url.username)
            user_end = o
            pass_start = o
            pass_end = o
            if url.password.Len() > 0 {
                o = PutByte(out, o, b':')
                pass_start = o
                o = Put(out, o, url.password)
                pass_end = o
            }
            o = PutByte(out, o, b'@')
        } else {
            user_start = o
            user_end = o
            pass_start = o
            pass_end = o
        }
        host_start = o
        o = Put(out, o, ip[0..ip_len])
        o = Put(out, o, HostBytes(&h))
        host_end = o
        if port := url.port {
            o = PutByte(out, o, b':')
            o = PutDecimal(out, o, u32(port))
        }
    }
    if dot {
        o = PutByte(out, o, b'/')
        o = PutByte(out, o, b'.')
    }
    path_start := o
    if url.opaque {
        o = Put(out, o, url.opath)
    } else {
        for seg in url.path {
            o = PutByte(out, o, b'/')
            o = Put(out, o, seg)
        }
        if t := url.tail {
            o = PutByte(out, o, b'/')
            o = Put(out, o, t)
        }
    }
    path_end := o
    var query_start := -1
    var query_end := -1
    if q := url.query {
        o = PutByte(out, o, b'?')
        query_start = o
        o = Put(out, o, q)
        query_end = o
    }
    var frag_start := -1
    if f := url.fragment {
        o = PutByte(out, o, b'#')
        frag_start = o
        o = Put(out, o, f)
    }
    // Must: every component is ASCII (encoded, or a host the host parser
    // made ASCII), so href is valid UTF-8; std.simd's validator checks it.
    href := rt.StrFrom(out[..]).Must()
    host := if h := url.host { some(PublicHost(&h, href[host_start..host_end])) } else { none }
    return Url(serial: href, scheme_end: scheme_end, user_start: user_start, user_end: user_end, pass_start: pass_start, pass_end: pass_end, host_start: host_start, host_end: host_end, host_value: host, port_value: url.port, path_start: path_start, path_end: path_end, query_start: query_start, query_end: query_end, frag_start: frag_start, opaque: url.opaque)
}

// SerializeSame is Serialize for a URL whose serialization is src, the
// text it was parsed from, so the href is one copy of src: none when that
// cannot be shown, and the caller then calls Serialize. It is shown from the
// finished record and src alone: the record has no credentials, no segment
// list (its path, if any, is a PathRun span), no IP host and is not a file
// URL; the layout's length is src's; src holds ':', "//", the port's ':' and
// digits, '/', '?' and '#' where the layout puts them; and the scheme and the
// host are the bytes of src there. Every other component is src's own span
// encoded, and an encoded span is longer than the span unless it is the
// span, so equal lengths make it src's bytes. O(n); allocates the href.
SerializeSame(a: &Arena, url: &Record, src: str): OutOfMemory!?Url from a {
    s := src.AsBytes()
    n := s.Len()
    if url.file || Credentials(url) || url.path.Len() > 0 {
        return none
    }
    scheme_end := url.scheme.Len()
    if scheme_end >= n || s[scheme_end] != b':' || !Same(s[0..scheme_end], url.scheme) {
        return none
    }
    var o := scheme_end + 1
    var host_start := o
    var host_end := o
    if h := url.host {
        name := match h {
            .Ipv4 | .Ipv6 { return none }
            _ { HostBytes(&h) }
        }
        if o + 2 + name.Len() > n || s[o] != b'/' || s[o + 1] != b'/' || !Same(s[o + 2..o + 2 + name.Len()], name) {
            return none
        }
        host_start = o + 2
        host_end = host_start + name.Len()
        o = host_end
        if port := url.port {
            var digits: [5]u8 = [0; 5]
            k := PutDecimal(mut digits[..], 0, u32(port))
            if o + 1 + k > n || s[o] != b':' || !Same(s[o + 1..o + 1 + k], digits[0..k]) {
                return none
            }
            o += 1 + k
        }
    } else if !url.opaque {
        return none
    }
    path_start := o
    if url.opaque {
        o += url.opath.Len()
    } else if t := url.tail {
        if o >= n || s[o] != b'/' {
            return none
        }
        o += 1 + t.Len()
    }
    path_end := o
    var query_start := -1
    var query_end := -1
    if q := url.query {
        if o >= n || s[o] != b'?' {
            return none
        }
        query_start = o + 1
        query_end = query_start + q.Len()
        o = query_end
    }
    var frag_start := -1
    if f := url.fragment {
        if o >= n || s[o] != b'#' {
            return none
        }
        frag_start = o + 1
        o = frag_start + f.Len()
    }
    if o != n {
        return none
    }
    href := try a.Dup(src)
    host := if h := url.host { some(PublicHost(&h, href[host_start..host_end])) } else { none }
    return some(Url(serial: href, scheme_end: scheme_end, user_start: host_start, user_end: host_start, pass_start: host_start, pass_end: host_start, host_start: host_start, host_end: host_end, host_value: host, port_value: url.port, path_start: path_start, path_end: path_end, query_start: query_start, query_end: query_end, frag_start: frag_start, opaque: url.opaque))
}

// PublicHost is h as a Host, its text the view of the href that serializes
// it.
PublicHost(h: &HostValue, text: str): Host from text {
    return match *h {
        .Domain { Host.Domain(name: text) }
        .Opaque { Host.Opaque(text: text) }
        .Ipv4(addr) { Host.Ipv4(addr: addr) }
        .Ipv6(pieces) { Host.Ipv6(addr: PiecesToBytes(&pieces)) }
        .Empty { Host.Empty }
    }
}

// PiecesToBytes is an IPv6 address's pieces as 16 bytes in network order.
PiecesToBytes(pieces: &[8]u16): [16]u8 {
    var out: [16]u8 = [0; 16]
    for i in 0..8 {
        out[2 * i] = Trunc[u8](pieces[i] >> 8)
        out[2 * i + 1] = Trunc[u8](pieces[i])
    }
    return out
}

// BytesToPieces is 16 bytes in network order as an IPv6 address's pieces.
BytesToPieces(addr: &[16]u8): [8]u16 {
    var out: [8]u16 = [0; 8]
    for i in 0..8 {
        out[i] = (u16(addr[2 * i]) << 8) | u16(addr[2 * i + 1])
    }
    return out
}

// RecordOf is u's URL record: views of its href, and its path's segments
// listed in a. O(n); allocates the segment list in a.
RecordOf(a: &Arena, u: &Url): OutOfMemory!Record from a {
    // A copy, so the record holds no loan of u.
    href := (try a.Dup(u.serial)).AsBytes()
    path := href[u.path_start..u.path_end]
    var segments := List[[]u8].New(a)
    if !u.opaque && path.Len() > 0 {
        // A list path serializes as "/" before each segment.
        var start := 1
        for k in 1..path.Len() + 1 {
            if k == path.Len() || path[k] == b'/' {
                try segments.Push(path[start..k])
                start = k + 1
            }
        }
    }
    host: ?HostValue = if h := u.host_value {
        some(match h {
            .Domain { HostValue.Domain(name: href[u.host_start..u.host_end]) }
            .Opaque { HostValue.Opaque(text: href[u.host_start..u.host_end]) }
            .Ipv4(addr) { HostValue.Ipv4(addr: addr) }
            .Ipv6(addr) { HostValue.Ipv6(pieces: BytesToPieces(&addr)) }
            .Empty { HostValue.Empty }
        })
    } else {
        none
    }
    scheme := href[0..u.scheme_end]
    query: ?[]u8 = if u.query_start >= 0 { some(href[u.query_start..u.query_end]) } else { none }
    fragment: ?[]u8 = if u.frag_start >= 0 { some(href[u.frag_start..]) } else { none }
    opath := if u.opaque { path } else { "".AsBytes() }
    return Record(scheme: scheme, special: IsSpecialScheme(scheme), file: SameText(scheme, "file"), dport: DefaultPort(scheme), username: href[u.user_start..u.user_end], password: href[u.pass_start..u.pass_end], host: host, port: u.port_value, opaque: u.opaque, opath: opath, path: segments, tail: none, query: query, fragment: fragment)
}
