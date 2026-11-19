// Why: every socket call names an endpoint; Addr is the plain value that
// holds one, parsed and printed exactly as Go's netip does.
// What: Addr, ParseAddr, Addr.Format and Resolve, with the parsers
// (ParseIp4, ParseIp6, ParsePortDigits) and printers (PutIp4, PutIp6).
// How: ParseAddr follows Go's netip.ParseAddrPort and Format is RFC 5952
// section 4 (netip.AddrPort.String); std/std-net_diff_test.go checks both
// against Go. Nothing here allocates except Resolve's List, in the caller's
// arena.

import std.core

/// An IP address and a port. ip is in network order; an IPv4 address is
/// in ip[0..4], with the rest 0 and v6 false. An Addr has no IPv6 zone.
/// The zero Addr (0.0.0.0:0) is what a call reports when there is no
/// address to give. Plain: copy it freely. `{a}` prints it as ParseAddr
/// reads it.
export struct Addr {
    /// The address in network order; an IPv4 address in ip[0..4], the rest 0.
    export ip: [16]u8
    /// The port.
    export port: u16
    /// Whether ip holds an IPv6 address.
    export v6: bool
}

/// Parses "a.b.c.d:port" or "[v6]:port". Anything else is InvalidAddress.
/// O(len s); allocates nothing.
///
/// An unbracketed host is a dotted IPv4 address: four decimal fields, 0
/// to 255, with no leading zero. A bracketed host is an IPv6 address in
/// RFC 4291's text form: groups of one to four hex digits, one "::" for
/// one or more zero groups, and an optional dotted IPv4 tail in the last
/// 32 bits. The port is decimal digits, at most 65535. A zone ("%eth0") is
/// InvalidAddress, since an Addr has none and a zone dropped in silence
/// would address the wrong link. Host names are Resolve's.
export ParseAddr(s: str): Error!Addr {
    var a := Addr(ip: [0; 16], port: 0, v6: false)
    if !ParseAddrInto(s.AsBytes(), mut a) {
        fail InvalidAddress
    }
    return a
}

// ParseAddrInto is ParseAddr's verdict, writing a only in part on false.
ParseAddrInto(b: []u8, a: mut Addr): bool {
    // Split at the last colon (netip.splitAddrPort).
    var i := b.Len() - 1
    for i >= 0 && b[i] != 58 {
        i -= 1
    }
    if i <= 0 || !ParsePortDigits(b[i + 1..], mut a.port) {
        return false
    }
    host := b[0..i]
    if host[0] == 91 {
        if host.Len() < 2 || host[host.Len() - 1] != 93 {
            return false
        }
        a.v6 = true
        return ParseIp6(host[1..host.Len() - 1], mut a.ip)
    }
    a.v6 = false
    return ParseIp4(host, mut a.ip, 0)
}

// ParsePortDigits: one or more decimal digits, at most 65535 (leading
// zeros allowed, as strconv.ParseUint(p, 10, 16)).
ParsePortDigits(b: []u8, port: mut u16): bool {
    if b.Len() == 0 {
        return false
    }
    var v := 0
    for c in b {
        d := int(c) - 48
        if d < 0 || d > 9 {
            return false
        }
        v = v * 10 + d
        if v > 65535 {
            return false
        }
    }
    *port = Trunc[u16](v)
    return true
}

// ParseIp4 writes a dotted quad to out[at..at + 4] (netip's
// parseIPv4Fields): four fields of decimal digits, each 0 to 255 with no
// leading zero; nothing else.
ParseIp4(b: []u8, out: mut [16]u8, at: int): bool {
    var v := 0
    var digits := 0
    var part := 0
    for c in b {
        if c >= 48 && c <= 57 {
            if digits == 1 && v == 0 {
                return false
            }
            v = v * 10 + int(c) - 48
            digits += 1
            if v > 255 {
                return false
            }
        } else if c == 46 {
            if digits == 0 || part == 3 {
                return false
            }
            out[at + part] = Trunc[u8](v)
            part += 1
            v = 0
            digits = 0
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

// HexDigit is the value of an ASCII hex digit, or -1.
HexDigit(c: u8): int {
    if c >= 48 && c <= 57 {
        return int(c) - 48
    }
    l := c | 0x20
    if l >= 97 && l <= 102 {
        return int(l) - 87
    }
    return -1
}

// ParseIp6 parses an IPv6 address with no brackets and no zone into ip
// (netip's parseIPv6, the zone refused): groups of one to four hex
// digits separated by single colons, at most one "::" that stands for at
// least one zero group, and a dotted IPv4 tail only as the last 32 bits.
ParseIp6(b: []u8, ip: mut [16]u8): bool {
    *ip = [0; 16]
    var s := 0
    n := b.Len()
    var ellipsis := -1
    if n >= 2 && b[0] == 58 && b[1] == 58 {
        ellipsis = 0
        s = 2
        if s == n {
            return true
        }
    }
    var i := 0
    for i < 16 {
        var off := 0
        var acc := 0
        for s + off < n {
            d := HexDigit(b[s + off])
            if d < 0 {
                break
            }
            if off > 3 {
                return false
            }
            acc = acc * 16 + d
            off += 1
        }
        if off == 0 {
            return false
        }
        if s + off < n && b[s + off] == 46 {
            // The dotted tail: the hex digits read so far were its first
            // field.
            if (ellipsis < 0 && i != 12) || i + 4 > 16 {
                return false
            }
            if !ParseIp4(b[s..n], ip, i) {
                return false
            }
            s = n
            i += 4
            break
        }
        ip[i] = Trunc[u8](acc >> 8)
        ip[i + 1] = Trunc[u8](acc)
        i += 2
        s += off
        if s == n {
            break
        }
        if b[s] != 58 || s + 1 == n {
            return false
        }
        s += 1
        if b[s] == 58 {
            if ellipsis >= 0 {
                return false
            }
            ellipsis = i
            s += 1
            if s == n {
                break
            }
        }
    }
    if s != n {
        return false
    }
    if i < 16 {
        if ellipsis < 0 {
            return false
        }
        // Move the groups after the ellipsis to the end; zero the gap.
        gap := 16 - i
        var j := i - 1
        for j >= ellipsis {
            ip[j + gap] = ip[j]
            j -= 1
        }
        for k in ellipsis..ellipsis + gap {
            ip[k] = 0
        }
    } else if ellipsis >= 0 {
        return false
    }
    return true
}

// IsMapped: ip is an IPv4-mapped IPv6 address (::ffff:0:0/96).
IsMapped(ip: &[16]u8): bool {
    for k in 0..10 {
        if ip[k] != 0 {
            return false
        }
    }
    return ip[10] == 0xFF && ip[11] == 0xFF
}

// Unmapped is a: an IPv4-mapped IPv6 address becomes the IPv4 one.
Unmapped(a: Addr): Addr {
    if !a.v6 || !IsMapped(&a.ip) {
        return a
    }
    var ip: [16]u8 = [0; 16]
    ip[0] = a.ip[12]
    ip[1] = a.ip[13]
    ip[2] = a.ip[14]
    ip[3] = a.ip[15]
    return Addr(ip: ip, port: a.port, v6: false)
}

/// Writes "a.b.c.d:port" or "[v6]:port". An IPv6 address is in RFC 5952's
/// canonical form: lower-case hex with no leading zeros, the longest run
/// of two or more zero groups as "::" (the first of equal runs), and an
/// IPv4-mapped address as ::ffff:a.b.c.d. One Writer.Bytes call; allocates
/// nothing.
impl Format for Addr {
    Format(&self, w: mut Writer): WriteError!void {
        var buf: [64]u8 = [0; 64]
        n := FormatAddr(self, mut buf)
        try w.Bytes(buf[0..n])
    }
}

// FormatAddr writes a's text into buf (at most 53 bytes) and returns its
// length.
FormatAddr(a: &Addr, buf: mut [64]u8): int {
    n := if a.v6 { PutBracketed(buf, &a.ip) } else { PutIp4(buf, 0, a.ip[0], a.ip[1], a.ip[2], a.ip[3]) }
    buf[n] = 58
    return PutDec(buf, n + 1, int(a.port))
}

// PutBracketed writes "[v6]" at buf[0..]: "::ffff:" and the dotted quad for
// an IPv4-mapped address, else the eight groups. The new length.
PutBracketed(buf: mut [64]u8, ip: &[16]u8): int {
    buf[0] = 91
    var n := 1
    if IsMapped(ip) {
        for c in "::ffff:".AsBytes() {
            buf[n] = c
            n += 1
        }
        n = PutIp4(buf, n, ip[12], ip[13], ip[14], ip[15])
    } else {
        n = PutIp6(buf, n, ip)
    }
    buf[n] = 93
    return n + 1
}

// PutDec writes v (0 to 65535) in decimal at buf[n..]; the new length.
PutDec(buf: mut [64]u8, n: int, v: int): int {
    var d: [5]u8 = [0; 5]
    var k := 0
    var x := v
    for {
        d[k] = Trunc[u8](48 + x % 10)
        k += 1
        x = x / 10
        if x == 0 {
            break
        }
    }
    var m := n
    for k > 0 {
        k -= 1
        buf[m] = d[k]
        m += 1
    }
    return m
}

// PutIp4 writes a.b.c.d in decimal at buf[n..]; the new length.
PutIp4(buf: mut [64]u8, n: int, a: u8, b: u8, c: u8, d: u8): int {
    var m := PutDec(buf, n, int(a))
    buf[m] = 46
    m = PutDec(buf, m + 1, int(b))
    buf[m] = 46
    m = PutDec(buf, m + 1, int(c))
    buf[m] = 46
    return PutDec(buf, m + 1, int(d))
}

// PutIp6 writes the eight groups of ip by RFC 5952 at buf[n..].
PutIp6(buf: mut [64]u8, n: int, ip: &[16]u8): int {
    // The longest run of zero groups, at least two long, the first on a
    // tie.
    var best := -1
    var best_len := 1
    var g := 0
    for g < 8 {
        if ip[2 * g] == 0 && ip[2 * g + 1] == 0 {
            var e := g
            for e < 8 && ip[2 * e] == 0 && ip[2 * e + 1] == 0 {
                e += 1
            }
            if e - g > best_len {
                best = g
                best_len = e - g
            }
            g = e
        } else {
            g += 1
        }
    }
    hex := "0123456789abcdef".AsBytes()
    var m := n
    var k := 0
    for k < 8 {
        if k == best {
            buf[m] = 58
            buf[m + 1] = 58
            m += 2
            k += best_len
            continue
        }
        if k > 0 && k != best + best_len {
            buf[m] = 58
            m += 1
        }
        v := (int(ip[2 * k]) << 8) | int(ip[2 * k + 1])
        var started := false
        var sh := 12
        for sh >= 0 {
            d := (v >> sh) & 15
            if d != 0 || started || sh == 0 {
                buf[m] = hex[d]
                m += 1
                started = true
            }
            sh -= 4
        }
        k += 1
    }
    return m
}

// MaxAnswers is how many addresses one Resolve returns at most: the
// runtime's bound (std.task's MaxAnswers).
const MaxAnswers: int = 32

/// Resolves a host name to its addresses, in the order the system gives
/// them, each with port 0. Errors: InvalidAddress for an unknown, empty or
/// over-long name; Canceled; OutOfMemory. One runtime
/// call (a network round trip at worst); allocates the List of at most 32
/// Addrs in a.
///
/// A numeric address answers itself, and "localhost" is 127.0.0.1 then
/// ::1. IPv4-mapped answers come back as IPv4. An answer with an IPv6 zone
/// ("fe80::1%5", or a zoned hosts line) is InvalidAddress, as ParseAddr
/// refuses one. On Windows the system's resolver answers
/// (GetAddrInfoW); on Linux std reads /etc/hosts, then asks the
/// nameservers of /etc/resolv.conf.
export Resolve(a: &Arena, io: Io, host: str): Error|OutOfMemory!List[Addr] from a {
    var out: [32]core.IoAddr = [core.IoAddr(ip: [0; 16], port: 0, v6: false, zone: 0); 32]
    r := core.IoResolve(io, host, mut out[..])
    if r.err == core.SysErr.NotFound {
        // An unknown name: InvalidAddress (no member of net.Error says
        // NotFound).
        fail InvalidAddress
    }
    n := try CheckCount(r, MaxAnswers)
    // Every answer is checked before the first Push, so a refusal leaves
    // nothing in the arena.
    for i in 0..n {
        if out[i].zone != 0 {
            fail InvalidAddress
        }
    }
    var l := List[Addr].New(a)
    for i in 0..n {
        try l.Push(Unmapped(Addr(ip: out[i].ip, port: 0, v6: out[i].v6)))
    }
    return l
}
