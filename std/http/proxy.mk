// Why: a client behind an HTTP proxy must send every connection through it
// without ever resolving the origin's name itself.
//
// What: ProxyCheck, ProxyHost and ProxyPort read ClientLimits.proxy; Reach
// connects through the proxy; Tunnel asks for a CONNECT tunnel.
//
// How: an http request is sent to the proxy in absolute form (RFC 9112
// 3.2.2); an https one asks for a tunnel with CONNECT host:port (RFC 9110
// 9.3.6), reads its 2xx reply head, and runs the secure handshake with the
// origin through it. More below.

// allow sees the proxy's addresses only. Pool keys stay the origin's: the
// proxy is the client's own and the same for every request.

import std.net
import std.rt
import std.url

// ProxyCheck: p is a proxy the client can use, an http URL with a host and
// no userinfo; else BadUrl.
ProxyCheck(p: &url.Url): Error!void {
    if p.Scheme() != "http" || p.Hostname().Len() == 0 || p.Username().Len() > 0 || p.Password().Len() > 0 {
        fail BadUrl
    }
}

// ProxyHost is p's host to dial, an IPv6 address without its brackets.
ProxyHost(p: &url.Url): str from p {
    host := p.Hostname()
    if host.Len() > 2 && host.Byte(0) == 0x5B {
        return host[1..host.Len() - 1]
    }
    return host
}

// ProxyPort is p's port, 80 when it states none.
ProxyPort(p: &url.Url): int {
    if x := p.Port() {
        return int(x)
    }
    return 80
}

// Reach is the connection a request to host:port goes on before any
// secure handshake: a direct dial with no proxy; else the proxy's, and for
// https a tunnel through it to host:port. host has no brackets.
Reach(lim: &ClientLimits, a: &Arena, io: Io, host: str, port: int, https: bool): Error!net.Conn from io {
    p := lim.proxy else {
        return try Dial(lim, a, io, host, port)
    }
    c := try Dial(lim, a, io, ProxyHost(p), ProxyPort(p))
    if !https {
        return c
    }
    Tunnel(a, io, &c, host, port, lim.header_bytes) else e {
        c.Abandon()
        fail e
    }
    return c
}

// Tunnel asks the proxy on c for a tunnel to host:port with CONNECT and
// reads its reply's head: a 2xx makes the tunnel; another status is
// ProxyRefused(code). The head is bounded by limit (TooLarge) and framed as
// any reply's (BadRequest at its offset); its end before a byte is Closed.
// A byte that arrives with a 2xx head, in the read that ends it, is
// BadRequest at that byte: the origin speaks only after the client's
// secure hello, so the proxy sent it. A byte that arrives later is the
// secure handshake's to judge. The head is read into a buffer in a that
// grows by doubling (GrowBytes).
Tunnel(a: &Arena, io: Io, c: &net.Conn, host: str, port: int, limit: int): Error!void {
    t := if host.Has(":") { try a.Fmt("[{host}]:{port}", (host, port)) } else { try a.Fmt("{host}:{port}", (host, port)) }
    req := try a.Fmt("CONNECT {t} HTTP/1.1\r\nHost: {t}\r\n\r\n", (t))
    try c.WriteAll(io, req.AsBytes())
    var b := try a.Slice[u8](256)
    var e := 0
    var p := HeadPos(s: 0, scan: 0, skipped: 0, hend: -1, bad: -1)
    for {
        p = HeadScan(b[..], p, e)
        if p.bad >= 0 {
            fail BadRequest(offset: p.bad)
        }
        if p.hend >= 0 {
            break
        }
        if e >= limit {
            fail TooLarge
        }
        if e == b.Len() {
            b = try GrowBytes(a, b[0..e], e + 1)
        }
        n := try c.Read(io, mut b[e..b.Len()])
        if n == 0 {
            if e == 0 {
                fail Closed
            }
            fail BadRequest(offset: e)
        }
        e += n
    }
    if p.hend > limit {
        fail TooLarge
    }
    text := rt.StrFrom(b[p.s..p.hend]) else {
        fail BadRequest(offset: p.skipped + Utf8Bad(b[p.s..p.hend]))
    }
    h := try ParseHead(a, text, true, p.skipped)
    if h.status < 200 || h.status > 299 {
        // A refusal may carry a body (an error page): the status decides,
        // and the connection is closed unread.
        fail ProxyRefused(code: h.status)
    }
    if e > p.hend {
        fail BadRequest(offset: p.hend)
    }
}
