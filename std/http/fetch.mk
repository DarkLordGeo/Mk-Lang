// Why: the simplest client call: one request, the whole reply in memory,
// no pool and no shared state.
//
// What: Fetch, one HTTP/1.1 request over a new connection with
// `Connection: close`, and WriteRequest, which writes its head and body.
//
// How: the reply's head is read by the same parser as requests (RFC 9112
// section 4 for the status line), its body by Content-Length, chunked, or
// to the end of the connection (RFC 9112 6.3). At most MaxInterim interim
// 1xx replies are skipped. Only http:// URLs: Fetch has no TLS.

import std.net

// MaxInterim is the most interim 1xx replies Fetch reads before the final
// one; one more is BadRequest at its first byte (Go's client stops after
// 5, net/http max1xxResponses).
const MaxInterim = 5

/// Sends one request over a new plain connection and reads the whole
/// reply into a. url is an http URL; Fetch has no https, no pool, no
/// redirects and no limits record: use a Client for those. O(n) in the
/// bytes sent and read; allocates the request head and the reply, head
/// and body, in a.
///
/// Errors: a URL that is not http://host[:port][/path][?query] is
/// BadRequest at the offset in url where it stops being one. The reply's
/// bytes, heads and body together, interim replies included, are bounded
/// by limit: TooLarge one byte past it (a negative limit acts as 0). A
/// malformed reply is BadRequest at its offset. A read or write cut off by
/// a cancel of io's scope is Canceled; another read failure is ReadError,
/// and a write failure WriteError. Connecting fails with net.Error.
///
/// The request carries Host, then headers as given, with CR, LF and NUL in
/// a value replaced by a space. A field whose name is not a token is left
/// out, and so are Host, Connection, Content-Length and Transfer-Encoding,
/// which Fetch writes itself, with `Connection: close`. Content-Length is
/// written when body is not empty or the method is POST, PUT or PATCH. Up
/// to 5 interim 1xx replies other than 101 are read and dropped; a sixth is
/// BadRequest. The reply's fields are views of its head in a.
export Fetch(a: &Arena, io: Io, method: str, url: str, headers: []Header, body: []u8, limit: int): Error!Reply from a {
    if !IsToken(method) {
        fail BadRequest(offset: 0)
    }
    u := url.AsBytes()
    k := SchemeEnd(u, 0, u.Len())
    if k < 0 || !EqFold(url[0..k], "http://") {
        fail BadRequest(offset: 0)
    }
    var j := k
    var colon := -1
    for j < u.Len() && u[j] != 0x2F && u[j] != 0x3F && u[j] != 0x23 {
        c := u[j]
        if c == Colon {
            colon = j
        } else if c == 0x5D {
            colon = -1
        } else if c <= 0x20 || c >= 0x7F || c == 0x40 {
            fail BadRequest(offset: j)
        }
        j += 1
    }
    if j == k {
        fail BadRequest(offset: k)
    }
    auth := url[k..j]
    var dial := auth
    if colon < 0 {
        dial = try a.Fmt("{auth}:80", (auth))
    } else {
        if colon + 1 == j {
            fail BadRequest(offset: j)
        }
        for x in colon + 1..j {
            if !IsDigit(u[x]) {
                fail BadRequest(offset: x)
            }
        }
    }
    var te := j
    for te < u.Len() && u[te] != 0x23 {
        c := u[te]
        if c <= 0x20 || c >= 0x7F {
            fail BadRequest(offset: te)
        }
        te += 1
    }
    path := url[j..te]
    target := if path.Len() == 0 || path.Byte(0) == 0x3F { try a.Fmt("/{path}", (path)) } else { path }
    conn := try net.Dial(io, dial)
    defer conn.Close()
    var wb: [4096]u8 = [0; 4096]
    var w := conn.Writer(io, mut wb[..])
    WriteRequest(a, mut w, method, target, auth, headers, body) else e {
        match e {
            WriteError {
                // A send cut off by the scope's cancel is Canceled.
                try CancelCheck(io)
                fail WriteError
            }
            _ {
                fail e
            }
        }
    }

    lim := if limit < 0 { 0 } else { limit }
    var rb: [8192]u8 = [0; 8192]
    var r := conn.Reader(io, mut rb[..])
    // consumed counts the bytes of the interim replies read so far: they
    // share limit with the final reply, and offsets count from the
    // first byte of the first.
    var consumed := 0
    var interim := 0
    for {
        raw := try ReadHead(a, io, mut r, lim - consumed, consumed)
        h := try ParseHead(a, raw.text, true, consumed + raw.skipped)
        if h.status >= 100 && h.status < 200 && h.status != 101 {
            interim += 1
            if interim > MaxInterim {
                fail BadRequest(offset: consumed + raw.skipped)
            }
            consumed += raw.skipped + raw.text.Len()
            continue
        }
        used := consumed + raw.skipped + raw.text.Len()
        budget := lim - used
        if method == "HEAD" || h.status < 200 || h.status == 204 || h.status == 304 {
            return Reply(status: h.status, headers: h.headers, body: "".AsBytes())
        }
        if h.chunked {
            b := try ReadChunked(a, io, mut r, budget, used)
            return Reply(status: h.status, headers: h.headers, body: b)
        }
        if h.length >= 0 {
            if h.length > budget {
                fail TooLarge
            }
            b := try ReadBody(a, io, mut r, h.length, used)
            return Reply(status: h.status, headers: h.headers, body: b)
        }
        // Delimited by the end of the connection (RFC 9112 6.3 rule 8): read
        // to the end, and one byte past the budget is TooLarge.
        var b := try a.Slice[u8](0)
        var bn := 0
        for {
            if bn == budget {
                // The budget is full: one byte more decides, the end or
                // TooLarge (a read into an empty slice would return 0).
                var one: [1]u8 = [0; 1]
                got := r.Read(mut one[..]) else {
                    try CancelCheck(io)
                    fail ReadError
                }
                if got > 0 {
                    fail TooLarge
                }
                break
            }
            if bn == b.Len() {
                b = try GrowFor(a, b[0..bn], budget - bn)
            }
            got := r.Read(mut b[bn..b.Len()]) else {
                try CancelCheck(io)
                fail ReadError
            }
            if got == 0 {
                break
            }
            bn += got
        }
        return Reply(status: h.status, headers: h.headers, body: b[0..bn])
    }
}

// WriteRequest writes the request line, Host, the caller's fields, the
// framing and the body to w, then flushes it. Fetch owns the framing and
// the connection: a caller's Host, Connection, Content-Length and
// Transfer-Encoding are left out (RFC 9112 6.2: no Content-Length beside a
// Transfer-Encoding), and the body is sent with its Content-Length.
WriteRequest(a: &Arena, w: mut Writer, method: str, target: str, auth: str, headers: []Header, body: []u8): Error!void {
    try Print(w, "{method} {target} HTTP/1.1\r\nHost: {auth}\r\n", (method, target, auth))
    for i in 0..headers.Len() {
        h := headers[i]
        if !IsToken(h.name) || EqFold(h.name, "host") || EqFold(h.name, "connection") || EqFold(h.name, "content-length") || EqFold(h.name, "transfer-encoding") {
            continue
        }
        v := try CleanValue(a, h.value)
        try Print(w, "{n}: {v}\r\n", (n: h.name, v))
    }
    if body.Len() > 0 || method == "POST" || method == "PUT" || method == "PATCH" {
        n := body.Len()
        try Print(w, "Content-Length: {n}\r\n", (n))
    }
    try w.Str("Connection: close\r\n\r\n")
    try w.Bytes(body)
    try w.Flush()
}
