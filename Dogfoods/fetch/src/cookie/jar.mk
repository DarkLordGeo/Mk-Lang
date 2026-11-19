// Why: `-b` and `-c` keep cookies between runs, as a browser's jar does.
// What: Cookie and Jar, with Receive (a Set-Cookie header), Header (the
// Cookie header for a request), Load and Save.
// How: the storage and matching rules of RFC 6265: domain and path match,
// default path, Max-Age over Expires; the jar file is one cookie a line.

import std.fs
import std.time

/// One cookie as RFC 6265 section 5.3 stores it.
export struct Cookie {
    export name: str
    export value: str
    // lower case, with no leading dot
    export domain: str
    export path: str
    // none for a session cookie
    export expires: ?time.Time
    export host_only: bool
    export secure: bool
    export http_only: bool
}

/// The cookies a run has seen, in the order they were first set.
export struct Jar {
    export cookies: List[Cookie]
}

/// An empty jar on a.
export NewJar(a: &Arena): Jar from a {
    return Jar(cookies: List[Cookie].New(a))
}

/// A jar file line without the seven fields of the Netscape format.
export error BadJar(line: int)

/// Why a jar file cannot be read.
export type JarError = BadJar | fs.Error | OutOfMemory

/// Receives one Set-Cookie value for a response to a request for host and
/// path (RFC 6265 section 5.2 and 5.3), at now: a cookie whose expiry has
/// passed removes the one it names, a cookie for a domain that host is not
/// part of is ignored, and any other replaces the one with its name,
/// domain and path. O(n) in the jar; copies what it keeps into a.
export Jar.Receive(mut self, a: &Arena into self, host: str, path: str, header: str, now: time.Time): OutOfMemory!void {
    var parts := header.Split(";")
    pair := parts.Next() else { return }
    eq := pair.Find("=") else { return }
    name := pair[..eq].Trim()
    if name.Len() == 0 {
        return
    }
    var c := Cookie(
        name: try a.Dup(name),
        value: try a.Dup(pair[eq + 1..].Trim()),
        domain: try a.Dup(host),
        path: try a.Dup(DefaultPath(path)),
        expires: none,
        host_only: true,
        secure: false,
        http_only: false,
    )
    var max_age: ?i64 = none
    for raw in parts {
        attr := raw.Trim()
        (key, value) := Attribute(attr)
        k := try key.ToLower(a)
        match k {
            "expires" {
                // an Expires that does not parse is ignored (RFC 6265 section 5.2.1)
                match time.ParseHttpDate(value) {
                    ok(t) { c.expires = some(t) }
                    err(_) {}
                }
            }
            "max-age" {
                match value.ParseInt() {
                    ok(n) { max_age = some(n) }
                    err(_) {}
                }
            }
            "domain" {
                d := try value.ToLower(a)
                d2 := if d.StartsWith(".") { d[1..] } else { d }
                if d2.Len() > 0 {
                    if !DomainMatch(host, d2) {
                        return
                    }
                    c.domain = d2
                    c.host_only = false
                }
            }
            "path" {
                if value.StartsWith("/") {
                    c.path = try a.Dup(value)
                }
            }
            "secure" {
                c.secure = true
            }
            "httponly" {
                c.http_only = true
            }
            _ {}
        }
    }
    // Max-Age wins over Expires (RFC 6265 section 5.3 step 3); zero or less is already past
    if n := max_age {
        c.expires = some(if n <= 0 { time.FromUnix(0, 0) } else { now.Add(time.Seconds(Clamp(n))) })
    }
    expired := if t := c.expires { t.Unix() <= now.Unix() } else { false }
    for i in 0..self.cookies.Len() {
        old := &self.cookies[i]
        if old.name == c.name && old.domain == c.domain && old.path == c.path {
            if expired {
                _ = self.cookies.Remove(i)
            } else {
                self.cookies[i] = c
            }
            return
        }
    }
    if !expired {
        try self.cookies.Push(c)
    }
}

/// The Cookie header the jar sends with a request for host and path, at
/// now, over https when secure; none when no cookie applies. Cookies are
/// joined in the order they were set (RFC 6265 section 5.4). O(n); allocates the
/// header in a.
export Jar.Header(&self, a: &Arena, host: str, path: str, secure: bool, now: time.Time): OutOfMemory!?str from a {
    var t := Text.New(a)
    for c in self.cookies {
        if e := c.expires {
            if e.Unix() <= now.Unix() {
                continue
            }
        }
        if c.secure && !secure {
            continue
        }
        if c.host_only && c.domain != host {
            continue
        }
        if !c.host_only && !DomainMatch(host, c.domain) {
            continue
        }
        if !PathMatch(path, c.path) {
            continue
        }
        if t.Str().Len() > 0 {
            try t.Push("; ")
        }
        try t.Push(c.name)
        try t.Push("=")
        try t.Push(c.value)
    }
    if t.Str().Len() == 0 {
        return none
    }
    return some(t.Done())
}

/// Reads a jar file in the Netscape format curl writes: one cookie a line,
/// seven fields separated by tabs (domain, whether subdomains match, path,
/// secure, expiry in Unix seconds with 0 for a session cookie, name,
/// value), a `#HttpOnly_` prefix on the domain of an HTTP-only cookie, and
/// comment lines starting `#`. Cookies already expired at now are skipped.
/// Fails with BadJar at a line without seven fields, and with the file
/// system's error. O(n); allocates the cookies in a.
export Jar.Load(mut self, a: &Arena into self, io: Io, path: str, now: time.Time): JarError!void {
    text := try fs.ReadText(a, io, path)
    var n := 0
    for raw in text.Lines() {
        n += 1
        var line := raw
        var http_only := false
        if line.StartsWith("#HttpOnly_") {
            line = line[10..]
            http_only = true
        }
        if line.Trim().Len() == 0 || line.StartsWith("#") {
            continue
        }
        var f: [7]str = [""; 7]
        var count := 0
        for field in line.Split("\t") {
            if count == 7 {
                fail BadJar(line: n)
            }
            f[count] = field
            count += 1
        }
        if count != 7 {
            fail BadJar(line: n)
        }
        stamp := f[4].ParseInt() else { fail BadJar(line: n) }
        expires: ?time.Time = if stamp == 0 { none } else { some(time.FromUnix(stamp, 0)) }
        if t := expires {
            if t.Unix() <= now.Unix() {
                continue
            }
        }
        domain := if f[0].StartsWith(".") { f[0][1..] } else { f[0] }
        try self.cookies.Push(Cookie(
            name: f[5],
            value: f[6],
            domain: domain,
            path: f[2],
            expires: expires,
            host_only: f[1] != "TRUE",
            secure: f[3] == "TRUE",
            http_only: http_only,
        ))
    }
}

/// Writes the jar to path in the format Load reads, cookies expired at now
/// left out. Fails with the file system's error. O(n); allocates the text
/// in a.
export Jar.Save(&self, a: &Arena, io: Io, path: str, now: time.Time): fs.Error|OutOfMemory!void {
    var t := Text.New(a)
    try t.Push("# Netscape HTTP Cookie File\n")
    for c in self.cookies {
        var stamp: i64 = 0
        if e := c.expires {
            if e.Unix() <= now.Unix() {
                continue
            }
            stamp = e.Unix()
        }
        prefix := if c.http_only { "#HttpOnly_" } else { "" }
        sub := if c.host_only { "FALSE" } else { "TRUE" }
        secure := if c.secure { "TRUE" } else { "FALSE" }
        try t.Push(try a.Fmt("{prefix}{c.domain}\t{sub}\t{c.path}\t{secure}\t{stamp}\t{c.name}\t{c.value}\n", (prefix, c, sub, secure, stamp)))
    }
    try fs.WriteAll(io, path, t.Str().AsBytes())
}

// Attribute splits `key=value` or a bare `key` of a cookie attribute.
Attribute(attr: str): (key: str, value: str) {
    eq := attr.Find("=") else { return (key: attr, value: "") }
    return (key: attr[..eq].Trim(), value: attr[eq + 1..].Trim())
}

// DefaultPath is RFC 6265 section 5.1.4's default-path of a request path: up to,
// not including, its last slash, or "/".
DefaultPath(path: str): str {
    if !path.StartsWith("/") {
        return "/"
    }
    var last := 0
    for i in 0..path.Len() {
        if path.Byte(i) == b'/' {
            last = i
        }
    }
    return if last == 0 { "/" } else { path[..last] }
}

// DomainMatch is RFC 6265 section 5.1.3: host is domain or ends with "." and domain.
DomainMatch(host: str, domain: str): bool {
    if host == domain {
        return true
    }
    return host.EndsWith(domain) && host.Len() > domain.Len() && host.Byte(host.Len() - domain.Len() - 1) == b'.'
}

// PathMatch is RFC 6265 section 5.1.4: the cookie path is a prefix of the request
// path at a segment boundary.
PathMatch(path: str, cookie: str): bool {
    if path == cookie {
        return true
    }
    if !path.StartsWith(cookie) {
        return false
    }
    return cookie.EndsWith("/") || path.Byte(cookie.Len()) == b'/'
}

// Clamp keeps a Max-Age within a century, so the expiry never overflows.
Clamp(n: i64): i64 {
    return if n > 3153600000 { 3153600000 } else { n }
}
