// Why: name constraints (RFC 5280 4.2.1.10) on email addresses and URIs are
// only as strong as the reading of those names; a host two readers parse
// differently could slip past an excluded subtree.
//
// What: Box and Mailbox2821 (a mailbox's key), MailboxValid, and UriHost
// and UriNamed (a URI's host), with the byte classes they use.
//
// How: a mailbox is read as Go's crypto/x509 reads it; a URI's host is
// taken only when every reader of URLs reads the same one, and a URI whose
// host the readers could read differently is refused. Details below.

// Mailboxes follow Go's parseRFC2821Mailbox (RFC 2821 4.1.2 with RFC 3696's
// escapes): a quoted local part is unquoted and an escaped byte unescaped,
// so "foo"@example.com and fo\o@example.com are the mailbox
// foo@example.com. The readers of URLs are RFC 3986, Go's net/url and the
// URL Standard that std.url implements; std.x509 does not parse URLs, so a
// percent-encoded host or a '\' in the userinfo cannot pass an excluded
// subtree.

// Box is a mailbox read by Mailbox2821: its key (the unquoted local part,
// '@' and the domain in lower case) is n bytes, and the domain starts at
// at in the input.
struct Box {
    n: int
    at: int
}

// AText is whether x is an atext byte of RFC 2822 3.2.4, or '.'.
AText(x: u8): bool {
    if (x >= b'0' && x <= b'9') || (x >= b'a' && x <= b'z') || (x >= b'A' && x <= b'Z') {
        return true
    }
    return x == b'!' || x == b'#' || x == b'$' || x == b'%' || x == b'&' || x == b'\'' || x == b'*' || x == b'+' || x == b'-' || x == b'/' || x == b'=' || x == b'?' || x == b'^' || x == b'_' || x == b'`' || x == b'{' || x == b'|' || x == b'}' || x == b'~' || x == b'.'
}

// QText is whether x may stand unescaped in a quoted local part: RFC
// 2821's qtext and the space Go also takes.
QText(x: u8): bool {
    return x == 11 || x == 12 || x == 32 || x == 33 || x == 127 || (x >= 1 && x <= 8) || (x >= 14 && x <= 31) || (x >= 35 && x <= 91) || (x >= 93 && x <= 126)
}

// Mailbox2821 reads b as Go's parseRFC2821Mailbox does, writing the key
// into out when out is not empty (out holds at least b.Len() bytes, since
// the key is never longer than b); none when b is not a mailbox. O(b);
// allocates nothing.
Mailbox2821(b: []u8, out: mut []u8): ?Box {
    write := out.Len() > 0
    var i := 0
    var n := 0
    if b.Len() == 0 {
        return none
    }
    if b[0] == b'"' {
        i = 1
        for {
            if i == b.Len() {
                return none
            }
            x := b[i]
            i += 1
            if x == b'"' {
                break
            }
            if x == b'\\' {
                if i == b.Len() {
                    return none
                }
                y := b[i]
                if !(y == 11 || y == 12 || (y >= 1 && y <= 9) || (y >= 14 && y <= 127)) {
                    return none
                }
                if write {
                    out[n] = y
                }
                n += 1
                i += 1
                continue
            }
            if !QText(x) {
                return none
            }
            if write {
                out[n] = x
            }
            n += 1
        }
    } else {
        var first: u8 = 0
        var prev: u8 = 0
        for i < b.Len() {
            var x := b[i]
            if x == b'\\' {
                // RFC 3696 escapes outside quotes: any byte, taken as is.
                i += 1
                if i == b.Len() {
                    return none
                }
                x = b[i]
            } else if !AText(x) {
                break
            }
            if n == 0 {
                first = x
            }
            if x == b'.' && prev == b'.' && n > 0 {
                return none
            }
            prev = x
            if write {
                out[n] = x
            }
            n += 1
            i += 1
        }
        // RFC 3696 3: no '.' first, last, or twice in a row.
        if n == 0 || first == b'.' || prev == b'.' {
            return none
        }
    }
    if i == b.Len() || b[i] != b'@' {
        return none
    }
    i += 1
    domain := b[i..]
    if !DomainValid(domain, false) || HasByte(domain, b'@') {
        return none
    }
    if write {
        out[n] = b'@'
        for k in 0..domain.Len() {
            out[n + 1 + k] = Lower(domain[k])
        }
    }
    return some(Box(n: n + 1 + domain.Len(), at: i))
}

// MailboxValid is whether an email constraint holding '@' is a mailbox.
MailboxValid(b: []u8): bool {
    var empty: [1]u8 = [0; 1]
    return Mailbox2821(b, mut empty[0..0]).IsSome()
}

// UriByte is whether x may stand in a URI's userinfo (RFC 3986's
// unreserved, sub-delims, ':' and '%').
UriByte(x: u8): bool {
    if (x >= b'0' && x <= b'9') || (x >= b'a' && x <= b'z') || (x >= b'A' && x <= b'Z') {
        return true
    }
    return x == b'-' || x == b'.' || x == b'_' || x == b'~' || x == b'!' || x == b'$' || x == b'&' || x == b'\'' || x == b'(' || x == b')' || x == b'*' || x == b'+' || x == b',' || x == b';' || x == b'=' || x == b':' || x == b'%'
}

// HostByte is whether x may stand in a host std.x509 matches.
HostByte(x: u8): bool {
    return (x >= b'0' && x <= b'9') || (x >= b'a' && x <= b'z') || (x >= b'A' && x <= b'Z') || x == b'-' || x == b'.' || x == b'_'
}

// NumericLast is whether host's last label (a trailing '.' aside) is a
// number, decimal or 0x hex, which the URL Standard's host parser reads as
// an IPv4 address ("ends in a number") where RFC 3986 and Go read a name.
NumericLast(host: []u8): bool {
    var end := host.Len()
    if end > 0 && host[end - 1] == b'.' {
        end -= 1
    }
    var start := end
    for start > 0 && host[start - 1] != b'.' {
        start -= 1
    }
    if start == end {
        return false
    }
    if end - start >= 2 && host[start] == b'0' && (host[start + 1] == b'x' || host[start + 1] == b'X') {
        return true
    }
    for k in start..end {
        if host[k] < b'0' || host[k] > b'9' {
            return false
        }
    }
    return true
}

// UriHost is the host of URI u:u is a scheme (RFC 3986 3.1), "://" and
// an authority that ends at the first '/', '?' or '#', with at most one
// '@' (userinfo of UriByte bytes before it), a host of HostByte bytes and
// an optional ':' and port of digits, its last label not a number. None
// for anything else, and for any
// byte of u outside 0x21 to 0x7E or a '\' anywhere in it, since there the
// readers of URLs disagree on the host (the URL Standard drops tabs and
// reads '\' as '/', Go's net/url refuses both and percent-encoded hosts).
// O(u); allocates nothing.
UriHost(u: []u8): ?[]u8 from u {
    for x in u {
        if x < 0x21 || x > 0x7E || x == b'\\' {
            return none
        }
    }
    var i := 0
    for i < u.Len() && u[i] != b':' {
        x := u[i]
        letter := (x >= b'a' && x <= b'z') || (x >= b'A' && x <= b'Z')
        if !letter && (i == 0 || !((x >= b'0' && x <= b'9') || x == b'+' || x == b'-' || x == b'.')) {
            return none
        }
        i += 1
    }
    if i == 0 || i + 3 > u.Len() || u[i + 1] != b'/' || u[i + 2] != b'/' {
        return none
    }
    start := i + 3
    var end := start
    var at := -1
    for end < u.Len() && u[end] != b'/' && u[end] != b'?' && u[end] != b'#' {
        if u[end] == b'@' {
            if at >= 0 {
                return none
            }
            at = end
        }
        end += 1
    }
    var h := start
    if at >= 0 {
        for k in start..at {
            if !UriByte(u[k]) {
                return none
            }
        }
        h = at + 1
    }
    var stop := h
    for stop < end && u[stop] != b':' {
        if !HostByte(u[stop]) {
            return none
        }
        stop += 1
    }
    if stop == h || NumericLast(u[h..stop]) {
        return none
    }
    var k := stop + 1
    for k < end {
        if u[k] < b'0' || u[k] > b'9' {
            return none
        }
        k += 1
    }
    return some(u[h..stop])
}

// UriNamed is whether URI u names a host Go's parseURIs takes (not empty,
// not an IP address), read loosely as Go's net/url reads a URI it parses:
// a scheme (RFC 3986 3.1: a letter, then letters, digits, '+', '-', '.';
// anything else before the first ':', a '#' or '?' included, leaves Go
// no scheme and so no host), "://", the authority up to '/', '?' or '#',
// the host after the last '@' and before a single ':'. Used where no URI
// constraint applies, so nothing is matched against the host. O(u);
// allocates nothing.
UriNamed(u: []u8): bool {
    var i := 0
    for i < u.Len() && u[i] != b':' {
        x := u[i]
        letter := (x >= b'a' && x <= b'z') || (x >= b'A' && x <= b'Z')
        if !letter && (i == 0 || !((x >= b'0' && x <= b'9') || x == b'+' || x == b'-' || x == b'.')) {
            return false
        }
        i += 1
    }
    if i == 0 || i + 3 > u.Len() || u[i + 1] != b'/' || u[i + 2] != b'/' {
        return false
    }
    var start := i + 3
    var end := start
    for end < u.Len() && u[end] != b'/' && u[end] != b'?' && u[end] != b'#' {
        if u[end] == b'@' {
            start = end + 1
        }
        end += 1
    }
    if start == end || u[start] == b'[' {
        return false
    }
    var colons := 0
    var stop := end
    for k in start..end {
        if u[k] == b':' {
            colons += 1
            stop = k
        }
    }
    if colons > 1 || stop == start {
        return false
    }
    return IpOf(u[start..stop]).IsNone()
}
