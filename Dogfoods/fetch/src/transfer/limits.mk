// Why: `--deny` and `--decode` limit what the client may connect to and
// accept.
// What: Deny, Allow, Decoders and Limits, which builds http.Limits.
// How: Deny sets a bit mask of address classes; the client calls Allow
// before every connect, redirects included.

import std.atomic
import std.compress
import std.http
import std.net

// The address classes --deny refuses, one bit each. ClientLimits.allow is a
// plain fn with no state of its own, so the classes live here, set once
// before the client is made and read on every connect.
var DeniedClasses: atomic.AtomicInt = atomic.AtomicInt.New(0)

const Loopback: i64 = 1
const Private: i64 = 2
const LinkLocal: i64 = 4
const Ipv4: i64 = 8
const Ipv6: i64 = 16

/// Makes Allow refuse the addresses of each named class: `loopback`,
/// `private`, `link-local`, `ipv4`, `ipv6` or `all`; an unknown name adds
/// nothing. O(n); allocates nothing.
export Deny(classes: []str) {
    var mask: i64 = 0
    for c in classes {
        mask |= match c {
            "loopback" { Loopback }
            "private" { Private }
            "link-local" { LinkLocal }
            "ipv4" { Ipv4 }
            "ipv6" { Ipv6 }
            "all" { Ipv4 | Ipv6 }
            _ { 0 }
        }
    }
    (&DeniedClasses).Store(mask)
}

/// Whether the client may connect to addr: false when addr is in a class
/// Deny named. The client calls it before every connect, redirects
/// included. O(1); allocates nothing.
export Allow(addr: &net.Addr): bool {
    mask := (&DeniedClasses).Load()
    if mask == 0 {
        return true
    }
    return mask & Classes(addr) == 0
}

// Classes is the set of classes addr belongs to (RFC 6890's special-purpose
// blocks for loopback, private and link-local, and RFC 4193's unique local
// addresses for IPv6 private ones).
Classes(addr: &net.Addr): i64 {
    ip := addr.ip
    if !addr.v6 {
        var bits := Ipv4
        if ip[0] == 127 {
            bits |= Loopback
        }
        if ip[0] == 10 || (ip[0] == 172 && ip[1] & 0xF0 == 16) || (ip[0] == 192 && ip[1] == 168) {
            bits |= Private
        }
        if ip[0] == 169 && ip[1] == 254 {
            bits |= LinkLocal
        }
        return bits
    }
    var bits := Ipv6
    var loop := ip[15] == 1
    for i in 0..15 {
        if ip[i] != 0 {
            loop = false
        }
    }
    if loop {
        bits |= Loopback
    }
    if ip[0] & 0xFE == 0xFC {
        bits |= Private
    }
    if ip[0] == 0xFE && ip[1] & 0xC0 == 0x80 {
        bits |= LinkLocal
    }
    return bits
}

/// The content decoders for the coding names --decode gives, in that
/// order, a name given twice kept once. O(n); allocates the slice in a.
export Decoders(a: &Arena, names: []str): OutOfMemory![]compress.Content from a *names into a {
    var list := List[compress.Content].New(a)
    var seen := Set[str].New(a)
    for name in names {
        if !try seen.Add(name) {
            continue
        }
        c := match name {
            "gzip" { compress.GzipContent }
            "deflate" { compress.DeflateContent }
            _ { compress.ZstdContent }
        }
        try list.Push(c)
    }
    return list.Done()[..]
}

/// The client's limits for one run: redirects followed, Fetch's body
/// limit, the decoders, the --deny filter and the connector for https.
export Limits(redirects: int, body_bytes: i64, decoders: []compress.Content, deny: bool, secure: ?net.Secure): http.ClientLimits from decoders, secure {
    var l := http.DefaultClientLimits
    l.redirects = redirects
    l.body_bytes = body_bytes
    l.decoders = decoders
    if deny {
        l.allow = some(Allow)
    }
    l.secure = secure
    return l
}
