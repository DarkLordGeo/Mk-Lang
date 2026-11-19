// Why: listen, dial and rt.ListenWith all take an address as "host:port"
// text, and the parser must live in std.rt so a call-entry job here can use
// it; std.task calls it as rt.ParseAddr.
//
// What: ParseAddr, plus its parts IsLocalhost, ParsePort, ParseIp4 and
// ParseIp6, filling a sys.SockAddr.
//
// How: one pass over the bytes with no allocation. Hosts are literal IPv4,
// bracketed IPv6 (with an optional numeric zone), "localhost" or empty; any
// other name is refused here and resolved by the caller instead.

import std.sys

/// ParseAddr reads "host:port" into a. host is a dotted IPv4 address (four
/// decimal parts 0 to 255, no leading zeros, as Go 1.17 and later), an IPv6
/// address in brackets (RFC 4291 2.2: groups of one to four hex digits, one
/// "::", an optional dotted IPv4 tail, an optional numeric "%zone"),
/// "localhost" in any ASCII case (127.0.0.1), or empty (listen: 0.0.0.0,
/// dial: 127.0.0.1, as Go dials ":port" on the local system). port is one to
/// five decimal digits, at most 65535. Anything else is false: names are not
/// resolved here (NativeDial passes them to Resolve). One pass over
/// the bytes, O(len), no allocation.
export ParseAddr(s: str, listen: bool, a: mut sys.SockAddr): bool {
    b := s.AsBytes()
    n := b.Len()
    a.family = sys.Inet4
    a.port = 0
    a.addr = [0; 16]
    a.scope_id = 0
    var ps := 0
    if n > 0 && b[0] == 91 {
        var rb := -1
        for i in 1..n {
            if b[i] == 93 {
                rb = i
                break
            }
        }
        if rb < 0 || rb + 1 >= n || b[rb + 1] != 58 {
            return false
        }
        if !ParseIp6(b[1..rb], a) {
            return false
        }
        a.family = sys.Inet6
        ps = rb + 2
    } else {
        var colon := -1
        var colons := 0
        for i in 0..n {
            if b[i] == 58 {
                colon = i
                colons += 1
            }
        }
        // An IPv6 host needs brackets, or its last group would be read as
        // the port.
        if colon < 0 || colons > 1 {
            return false
        }
        host := b[0..colon]
        if host.Len() == 0 {
            if !listen {
                a.addr[0] = 127
                a.addr[3] = 1
            }
        } else if IsLocalhost(host) {
            a.addr[0] = 127
            a.addr[3] = 1
        } else if !ParseIp4(host, mut a.addr, 0) {
            return false
        }
        ps = colon + 1
    }
    return ParsePort(b[ps..n], a)
}

/// IsLocalhost: b is "localhost", ASCII letters in any case.
export IsLocalhost(b: []u8): bool {
    w := "localhost".AsBytes()
    if b.Len() != w.Len() {
        return false
    }
    for i in 0..w.Len() {
        if b[i] | 0x20 != w[i] {
            return false
        }
    }
    return true
}

/// ParsePort: one to five decimal digits, at most 65535.
export ParsePort(b: []u8, a: mut sys.SockAddr): bool {
    n := b.Len()
    if n == 0 || n > 5 {
        return false
    }
    var v := 0
    for i in 0..n {
        d := int(b[i]) - 48
        if d < 0 || d > 9 {
            return false
        }
        v = v * 10 + d
    }
    if v > 65535 {
        return false
    }
    a.port = Trunc[u16](v)
    return true
}

/// ParseIp4 writes the four parts of a dotted quad to out[at..at + 4].
export ParseIp4(b: []u8, out: mut [16]u8, at: int): bool {
    var part := 0
    var digits := 0
    var v := 0
    for i in 0..b.Len() {
        c := b[i]
        if c == 46 {
            if digits == 0 || part == 3 {
                return false
            }
            out[at + part] = Trunc[u8](v)
            part += 1
            digits = 0
            v = 0
        } else if c >= 48 && c <= 57 {
            // A second digit after a leading 0 is a leading zero.
            if digits > 0 && v == 0 {
                return false
            }
            v = v * 10 + int(c) - 48
            digits += 1
            if v > 255 {
                return false
            }
        } else {
            return false
        }
    }
    if digits == 0 || part != 3 {
        return false
    }
    out[at + 3] = Trunc[u8](v)
    return true
}

// HexVal is the value of a hex digit, or -1.
HexVal(c: u8): int {
    if c >= 48 && c <= 57 {
        return int(c) - 48
    }
    l := c | 0x20
    return if l >= 97 && l <= 102 { int(l) - 87 } else { -1 }
}

/// ParseIp6 parses the text between the brackets into a.addr and a.scope_id.
export ParseIp6(b: []u8, a: mut sys.SockAddr): bool {
    n := b.Len()
    var end := n
    for i in 0..n {
        if b[i] == 37 {
            end = i
            break
        }
    }
    if end < n {
        z := b[end + 1..n]
        if z.Len() == 0 || z.Len() > 10 {
            return false
        }
        var zv := 0
        for i in 0..z.Len() {
            d := int(z[i]) - 48
            if d < 0 || d > 9 {
                return false
            }
            zv = zv * 10 + d
        }
        if zv > 0xFFFF_FFFF {
            return false
        }
        a.scope_id = Trunc[u32](zv)
    }
    var words: [8]u32 = [0; 8]
    var nw := 0
    var gap := -1
    var i := 0
    if end >= 2 && b[0] == 58 && b[1] == 58 {
        gap = 0
        i = 2
    } else if end >= 1 && b[0] == 58 {
        return false
    }
    for i < end {
        start := i
        var v: u32 = 0
        for i < end && i - start < 4 && HexVal(b[i]) >= 0 {
            v = v * 16 + Trunc[u32](HexVal(b[i]))
            i += 1
        }
        if i < end && b[i] == 46 {
            // The dotted IPv4 tail: the last 32 bits.
            if nw > 6 {
                return false
            }
            var q: [16]u8 = [0; 16]
            if !ParseIp4(b[start..end], mut q, 0) {
                return false
            }
            words[nw] = (u32(q[0]) << 8) | u32(q[1])
            words[nw + 1] = (u32(q[2]) << 8) | u32(q[3])
            nw += 2
            i = end
            break
        }
        if i == start || nw == 8 {
            return false
        }
        if i < end && HexVal(b[i]) >= 0 {
            // A fifth hex digit.
            return false
        }
        words[nw] = v
        nw += 1
        if i == end {
            break
        }
        if b[i] != 58 {
            return false
        }
        i += 1
        if i < end && b[i] == 58 {
            if gap >= 0 {
                return false
            }
            gap = nw
            i += 1
        } else if i == end {
            // A trailing single colon.
            return false
        }
    }
    if gap < 0 {
        if nw != 8 {
            return false
        }
    } else {
        // "::" stands for at least one zero group.
        if nw > 7 {
            return false
        }
        tail := nw - gap
        for k in 0..tail {
            words[7 - k] = words[nw - 1 - k]
        }
        for k in gap..8 - tail {
            words[k] = 0
        }
    }
    for k in 0..8 {
        a.addr[2 * k] = Trunc[u8](words[k] >> 8)
        a.addr[2 * k + 1] = Trunc[u8](words[k])
    }
    return true
}
