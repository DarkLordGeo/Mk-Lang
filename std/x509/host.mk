// Why: a valid chain proves nothing unless its leaf names the host the
// client meant to reach.
//
// What: HostMatches, the name check of Verify, with MatchHostnames,
// MatchExactly, ValidHostname, IpOf and the in-place helpers.
//
// How: the host is checked against the leaf's subjectAltName only, never
// the Common Name, as Go's VerifyHostname does (RFC 6125 6.4), in place,
// with no allocation. The matching rules follow.

// An IP address, bare or in brackets, matches only an iPAddress name, IPv4
// and IPv4-mapped IPv6 equal; any other host matches a dNSName
// case-insensitively in ASCII, with a wildcard only as a whole left-most
// label and only when both are valid host names.

// Ip is an address in 16 bytes, an IPv4 address IPv4-mapped.
struct Ip {
    b: [16]u8
}

// IpFrom is the address of a 4- or 16-byte iPAddress name.
IpFrom(raw: []u8): Ip {
    var b: [16]u8 = [0; 16]
    if raw.Len() == 4 {
        b[10] = 0xFF
        b[11] = 0xFF
        (mut b[12..16]).CopyFrom(raw)
    } else {
        (mut b[..]).CopyFrom(raw)
    }
    return Ip(b: b)
}

// Hex is the value of the hex digit x, or -1.
Hex(x: u8): int {
    if x >= b'0' && x <= b'9' {
        return int(x - b'0')
    }
    if x >= b'a' && x <= b'f' {
        return int(x - b'a') + 10
    }
    if x >= b'A' && x <= b'F' {
        return int(x - b'A') + 10
    }
    return -1
}

// Ip4Of reads a dotted IPv4 address (four decimals, each 0 to 255 with no
// leading zero, as Go's netip.ParseAddr) from s into out[at..at + 4].
Ip4Of(s: []u8, out: mut []u8, at: int): bool {
    var part := 0
    var i := 0
    for part < 4 {
        var v := 0
        var digits := 0
        for i < s.Len() && s[i] >= b'0' && s[i] <= b'9' {
            if digits > 0 && v == 0 {
                return false
            }
            v = v * 10 + int(s[i] - b'0')
            digits += 1
            i += 1
            if v > 255 {
                return false
            }
        }
        if digits == 0 {
            return false
        }
        out[at + part] = Trunc[u8](v)
        part += 1
        if part < 4 {
            if i >= s.Len() || s[i] != b'.' {
                return false
            }
            i += 1
        }
    }
    return i == s.Len()
}

/// The IP address s writes, or none: dotted IPv4, or IPv6 in RFC 4291
/// 2.2's text form (at most one "::", standing for at least one zero group, and
/// an optional IPv4 tail). Text with a zone gives none.
IpOf(s: []u8): ?Ip {
    var b: [16]u8 = [0; 16]
    if Ip4Of(s, mut b[..], 12) {
        b[10] = 0xFF
        b[11] = 0xFF
        return some(Ip(b: b))
    }
    var n := 0
    var gap := -1
    var i := 0
    if s.Len() >= 2 && s[0] == b':' && s[1] == b':' {
        gap = 0
        i = 2
        if i == s.Len() {
            return some(Ip(b: b))
        }
    }
    for i < s.Len() {
        if n == 16 {
            return none
        }
        // An IPv4 tail fills the last four bytes.
        var dot := false
        var j := i
        for j < s.Len() && s[j] != b':' {
            if s[j] == b'.' {
                dot = true
            }
            j += 1
        }
        if dot {
            if n > 12 || j != s.Len() || !Ip4Of(s[i..], mut b[..], n) {
                return none
            }
            n += 4
            break
        }
        var v := 0
        var k := 0
        for i < s.Len() && k < 5 && Hex(s[i]) >= 0 {
            v = v * 16 + Hex(s[i])
            i += 1
            k += 1
        }
        if k == 0 || k > 4 {
            return none
        }
        b[n] = Trunc[u8](v >> 8)
        b[n + 1] = Trunc[u8](v & 0xFF)
        n += 2
        if i == s.Len() {
            break
        }
        if s[i] != b':' {
            return none
        }
        i += 1
        if i < s.Len() && s[i] == b':' {
            if gap >= 0 {
                return none
            }
            gap = n
            i += 1
            if i == s.Len() {
                break
            }
        } else if i == s.Len() {
            return none
        }
    }
    if gap < 0 {
        if n != 16 {
            return none
        }
        return some(Ip(b: b))
    }
    if n == 16 {
        return none
    }
    // Move what follows the gap to the end.
    var moved: [16]u8 = [0; 16]
    tail := n - gap
    for k in 0..gap {
        moved[k] = b[k]
    }
    for k in 0..tail {
        moved[16 - tail + k] = b[gap + k]
    }
    return some(Ip(b: moved))
}

// Lower is x in ASCII lower case.
Lower(x: u8): u8 {
    return if x >= b'A' && x <= b'Z' { x + 32 } else { x }
}

// SameFold is whether a and b are equal ignoring ASCII case.
SameFold(a: []u8, b: []u8): bool {
    if a.Len() != b.Len() {
        return false
    }
    for i in 0..a.Len() {
        if Lower(a[i]) != Lower(b[i]) {
            return false
        }
    }
    return true
}

// TrimDot is s without one trailing '.'.
TrimDot(s: []u8): []u8 from s {
    if s.Len() > 0 && s[s.Len() - 1] == b'.' {
        return s[..s.Len() - 1]
    }
    return s
}

// ValidHostname is Go's validHostname: non-empty labels of ASCII letters,
// digits, '_' and '-' (not first), not "*" alone, a pattern's first label
// "*" allowed; an input loses one trailing dot first.
ValidHostname(raw: []u8, pattern: bool): bool {
    s := if pattern { raw } else { TrimDot(raw) }
    if s.Len() == 0 || (s.Len() == 1 && s[0] == b'*') {
        return false
    }
    var label := 0
    var first := true
    var i := 0
    for i <= s.Len() {
        if i == s.Len() || s[i] == b'.' {
            if label == 0 {
                return false
            }
            label = 0
            first = false
            i += 1
            continue
        }
        x := s[i]
        if pattern && first && label == 0 && x == b'*' && (i + 1 == s.Len() || s[i + 1] == b'.') {
            label = 1
            i += 1
            continue
        }
        ok := (x >= b'a' && x <= b'z') || (x >= b'A' && x <= b'Z') || (x >= b'0' && x <= b'9') || x == b'_' || (x == b'-' && label > 0)
        if !ok {
            return false
        }
        label += 1
        i += 1
    }
    return true
}

// MatchHostnames is Go's matchHostnames over a valid pattern and a valid
// host without its trailing dot: the same number of labels, each equal
// ignoring case, the pattern's first "*" matching any one label.
MatchHostnames(pattern: []u8, host: []u8): bool {
    var p := 0
    var h := 0
    var first := true
    for p <= pattern.Len() && h <= host.Len() {
        var pe := p
        for pe < pattern.Len() && pattern[pe] != b'.' {
            pe += 1
        }
        var he := h
        for he < host.Len() && host[he] != b'.' {
            he += 1
        }
        star := first && pe - p == 1 && pattern[p] == b'*'
        if !star && !SameFold(pattern[p..pe], host[h..he]) {
            return false
        }
        first = false
        if pe == pattern.Len() || he == host.Len() {
            return pe == pattern.Len() && he == host.Len()
        }
        p = pe + 1
        h = he + 1
    }
    return false
}

// MatchExactly is Go's matchExactly: neither empty nor ".", equal ignoring
// case.
MatchExactly(a: []u8, b: []u8): bool {
    if a.Len() == 0 || b.Len() == 0 || (a.Len() == 1 && a[0] == b'.') || (b.Len() == 1 && b[0] == b'.') {
        return false
    }
    return SameFold(a, b)
}

// HostMatches is whether host names the certificate by its
// subjectAltName.
HostMatches(c: &Certificate, host: str): bool {
    hb := host.AsBytes()
    var cand := hb
    if hb.Len() >= 3 && hb[0] == b'[' && hb[hb.Len() - 1] == b']' {
        cand = hb[1..hb.Len() - 1]
    }
    san := c.San()
    var names := Cur(der: san, pos: 0, end: san.Len())
    if want := IpOf(cand) {
        for names.More() {
            g := names.Next() else {
                return false
            }
            if g.class == 2 && !g.cons && g.tag == NameIp {
                got := IpFrom(Content(san, &g))
                if Same(got.b[..], want.b[..]) {
                    return true
                }
            }
        }
        return false
    }
    valid := ValidHostname(hb, false)
    trimmed := TrimDot(hb)
    for names.More() {
        g := names.Next() else {
            return false
        }
        if g.class != 2 || g.cons || g.tag != NameDns {
            continue
        }
        m := Content(san, &g)
        if valid && ValidHostname(m, true) {
            if MatchHostnames(m, trimmed) {
                return true
            }
        } else if MatchExactly(m, hb) {
            return true
        }
    }
    return false
}
