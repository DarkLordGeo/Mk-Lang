// Why: programs parse URLs that peers hand them, so the parser must match
// the WHATWG URL Standard (url.spec.whatwg.org, whatwg/url commit 845bafc
// of 2026-10-19) and must be safe on hostile input.
//
// What: std.url's public face: Url with its accessors (Href, Scheme,
// Username, Password, Host, Hostname, Port, Path, Query, Fragment, Origin),
// Host, Part, and the errors BadUrl, BadHost and IdnaUnsupported.
//
// How: the parser is the standard's basic URL parser, one function per
// state (parse.mk), over bytes: the input is a str, so it is UTF-8, and
// every byte above 0x7F is in every percent-encode set, so encoding a
// scalar's UTF-8 bytes one at a time is what the standard's UTF-8
// percent-encode does. Hosts are host.mk (with the IDNA seam idna.mk), the
// setters set.mk, and application/x-www-form-urlencoded query.mk.
//
// A Url is its serialization (the standard's href, always ASCII) and the
// offsets of its components in it, the layout of Ada's url_aggregator: every
// accessor is a slice of href, and a Url holds nothing but href, in the
// arena it was parsed into. Every entry point reads untrusted text: none
// panics on input, none recurses, and each allocates O(n) bytes in its
// arena for n bytes of input and base.

/// Input the URL parser refuses. offset is the byte of the input, as
/// given, where the parser stopped.
export error BadUrl(offset: int)

/// A host the URL Standard's host parser refuses: a bad IPv4 or IPv6
/// address, a forbidden code point, a domain that is empty or not valid
/// UTF-8 once percent-decoded, or a domain UTS 46's ToASCII refuses.
export error BadHost

/// A host that needs UTS 46 processing (a non-ASCII code point, or a label
/// that starts with xn--) in a build of std without IDNA.
export error IdnaUnsupported

/// Every way a std.url function fails. It is open: a match on it outside
/// std keeps a `_` arm.
export open type Error = BadUrl | BadHost | IdnaUnsupported

/// A URL's host: a domain (ASCII, lower case), an IPv4 address as its
/// 32-bit value, an IPv6 address as its 16 bytes in network order, an
/// opaque host (the host of a URL whose scheme is not special,
/// percent-encoded), or Empty, which stands both for an empty host and for
/// no host at all.
export enum Host {
    /// A domain, ASCII and lower case.
    Domain(name: str)
    /// An IPv4 address as its 32-bit value.
    Ipv4(addr: u32)
    /// An IPv6 address as its 16 bytes in network order.
    Ipv6(addr: [16]u8)
    /// The percent-encoded host of a URL whose scheme is not special.
    Opaque(text: str)
    /// An empty host, or no host at all.
    Empty
}

/// The component Url.Set changes. Each member is one of the URL
/// Standard's setters: Href replaces the whole URL, Protocol the scheme,
/// Pathname the path, Search the query and Hash the fragment.
export enum Part {
    Href
    Protocol
    Username
    Password
    Host
    Hostname
    Port
    Pathname
    Search
    Hash
}

/// A parsed URL. It holds its serialization, in the arena it was parsed
/// into, and its components are views of that text. A Url is a view, not
/// plain: it cannot outlive its arena or its input. `{u}` prints it
/// without the password; Href is the exact text.
export view struct Url {
    serial: str
    // href[0..scheme_end] is the scheme; href[scheme_end] is ':'.
    scheme_end: int
    user_start: int
    user_end: int
    pass_start: int
    pass_end: int
    // href[host_start..host_end] is the host serialized (IPv6 in brackets);
    // host is none for a URL with no host, which Empty stands for in Host().
    host_start: int
    host_end: int
    host_value: ?Host
    port_value: ?u16
    path_start: int
    path_end: int
    // query_start and frag_start are -1 for a null query or fragment, else
    // the offset after the '?' or '#'.
    query_start: int
    query_end: int
    frag_start: int
    // opaque is whether the path is a single opaque segment.
    opaque: bool
}

/// The URL's serialization, the URL Standard's href: the exact text,
/// password included. O(1); allocates nothing.
export Url.Href(&self): str from self {
    return self.serial
}

/// The URL's scheme, lower case, without the ':'. O(1); allocates
/// nothing.
export Url.Scheme(&self): str from self {
    return self.serial[0..self.scheme_end]
}

/// The URL's username, percent-encoded; "" when it has none. O(1);
/// allocates nothing.
export Url.Username(&self): str from self {
    return self.serial[self.user_start..self.user_end]
}

/// The URL's password, percent-encoded; "" when it has none. O(1);
/// allocates nothing.
export Url.Password(&self): str from self {
    return self.serial[self.pass_start..self.pass_end]
}

/// The URL's host; Empty for an empty host and for a URL with none.
/// O(1); allocates nothing.
export Url.Host(&self): Host from self {
    return self.host_value else { Host.Empty }
}

/// The URL's host as text, an IPv6 address in brackets, or "" when it has
/// none: the URL Standard's hostname. O(1); allocates nothing.
export Url.Hostname(&self): str from self {
    return self.serial[self.host_start..self.host_end]
}

/// The URL's port; none when it has none, or when the port is its
/// scheme's default (80 for http, 443 for https), which the parser drops.
/// O(1); allocates nothing.
export Url.Port(&self): ?u16 {
    return self.port_value
}

/// The URL's path as text, the URL Standard's pathname. O(1); allocates
/// nothing.
export Url.Path(&self): str from self {
    return self.serial[self.path_start..self.path_end]
}

/// The URL's query without its '?'; "" when it has none. Read its pairs
/// with ParseQuery. O(1); allocates nothing.
export Url.Query(&self): str from self {
    if self.query_start < 0 {
        return ""
    }
    return self.serial[self.query_start..self.query_end]
}

/// The URL's fragment without its '#'; "" when it has none. O(1);
/// allocates nothing.
export Url.Fragment(&self): str from self {
    if self.frag_start < 0 {
        return ""
    }
    return self.serial[self.frag_start..]
}

/// The URL's origin as text: "scheme://host", with ":port" when it has a
/// port, for http, https, ws, wss and ftp, and for a blob URL whose path
/// parses as an http or https URL; "null" for every other URL. O(n);
/// allocates the result in a, and for a blob URL the parse of its path.
export Url.Origin(&self, a: &Arena): OutOfMemory!str from a {
    scheme := self.Scheme()
    if scheme == "blob" {
        // Parse stores its input into its arena: a scratch arena
        // keeps self's loan out of a.
        var scratch := try a.Sub()
        defer scratch.Free()
        inner := Parse(&scratch, self.Path(), none) else e {
            match e {
                OutOfMemory {
                    fail OutOfMemory
                }
                _ {
                    return "null"
                }
            }
        }
        s := inner.Scheme()
        if s != "http" && s != "https" {
            return "null"
        }
        return try inner.TupleOrigin(a)
    }
    if !HasTupleOrigin(scheme) {
        return "null"
    }
    return try self.TupleOrigin(a)
}

// HasTupleOrigin is whether a URL of scheme has a tuple origin.
HasTupleOrigin(scheme: str): bool {
    return match scheme {
        "http" | "https" | "ws" | "wss" | "ftp" { true }
        _ { false }
    }
}

// TupleOrigin is the serialized tuple origin of a URL whose scheme has
// one, in a: "scheme://", the host and the port, without the credentials.
// Such a URL is special, so it has a host and href[scheme_end..] starts
// "://"; its port, when it has one, ends where the path starts.
Url.TupleOrigin(&self, a: &Arena): OutOfMemory!str from a {
    end := if self.port_value.IsSome() { self.path_start } else { self.host_end }
    head := self.serial[0..self.scheme_end + 3]
    tail := self.serial[self.host_start..end]
    return try a.Fmt("{head}{tail}", (head, tail))
}

impl Format for Url {
    /// Writes the URL's serialization without its password: what `{u}`
    /// prints, so a URL can go to a log. Href is the exact text. O(n);
    /// allocates nothing. As with any write to a stream, a write that
    /// fails part way may have written part of the URL.
    Format(&self, w: mut Writer): WriteError!void {
        if self.pass_end == self.pass_start {
            try w.Str(self.serial)
            return
        }
        // The password is preceded by ':' and followed by '@'.
        try w.Str(self.serial[0..self.pass_start - 1])
        try w.Str(self.serial[self.pass_end..])
    }
}
