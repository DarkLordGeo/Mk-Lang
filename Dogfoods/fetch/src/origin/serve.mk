// Why: the scripted origin answers fetch's requests over plain TCP and TLS.
// What: Serve, ServeTls, ServeAccepted, ServeConn, Answer, and Addrs, the
// listeners' addresses that URLs name as {origin}, {tls} and the like.
// How: one task per connection; each request is matched to the first route
// of the script and answered with its echo, coding, range and conditional
// rules applied.

import std.compress
import std.hash
import std.http
import std.net
import std.time
import std.tls

// the largest request head and body the origin reads
const RequestLimit = 1048576

// the size of one chunk when a route answers with chunked framing
const ChunkBytes = 256

/// The loopback addresses of the origin's listeners, as `127.0.0.1:port`,
/// "" for a listener that was not started. A script and a command line name
/// them as `{origin}`, `{origin2}`, `{tls}` and `{tls2}`.
export struct Addrs {
    export origin: str
    export origin2: str
    export tls: str
    export tls2: str
}

/// Accepts connections on l until the caller's scope is cancelled, and
/// answers each in a task of its own from script over plain TCP. A failed
/// accept is skipped. Allocates one arena per connection on h.
export Serve(l: &net.Listener, h: &Heap, io: Io, script: &Script, addrs: &Addrs) {
    scope conns := io.Scope() {
        for {
            conn := l.Accept(io) else e {
                // a failed accept loses one connection; only a cancel stops the origin
                match e {
                    Canceled | Closed { break }
                    _ { continue }
                }
            }
            conns.go ServePlain(conn, h, io, script, addrs)
        }
        conns.Cancel()
    }
}

/// As Serve, over TLS 1.3 with the identities of cfg: each connection is
/// made a tls.Conn by tls.Server before it is answered. A failed handshake
/// closes the connection. Allocates one arena per connection on h, and
/// the TLS state on h.
export ServeTls(l: &net.Listener, h: &Heap, io: Io, script: &Script, addrs: &Addrs, cfg: &tls.Config) {
    scope conns := io.Scope() {
        for {
            conn := l.Accept(io) else e {
                // a failed accept loses one connection; only a cancel stops the origin
                match e {
                    Canceled | Closed { break }
                    _ { continue }
                }
            }
            conns.go ServeSecure(conn, h, io, script, addrs, cfg)
        }
        conns.Cancel()
    }
}

/// As ServeTls, with the handshake made through a net.Secure, the form an
/// http server takes in its options: each accepted connection is handed to
/// sec.Connect and answered as a net.Conn. Allocates as ServeTls.
export ServeAccepted(l: &net.Listener, h: &Heap, io: Io, script: &Script, addrs: &Addrs, sec: net.Secure) {
    scope conns := io.Scope() {
        for {
            conn := l.Accept(io) else e {
                // a failed accept loses one connection; only a cancel stops the origin
                match e {
                    Canceled | Closed { break }
                    _ { continue }
                }
            }
            conns.go ServeConnector(conn, h, io, script, addrs, sec)
        }
        conns.Cancel()
    }
}

ServePlain(conn: net.Stream, h: &Heap, io: Io, script: &Script, addrs: &Addrs) {
    c := conn.Conn()
    ServeConn(&c, h, io, script, addrs)
    c.Close(io) else {
        c.Abandon()
    }
}

ServeConnector(conn: net.Stream, h: &Heap, io: Io, script: &Script, addrs: &Addrs, sec: net.Secure) {
    plain := conn.Conn()
    c := sec.Connect(h, io, plain, "") else {
        plain.Abandon()
        return
    }
    ServeConn(&c, h, io, script, addrs)
    c.Close(io) else {
        c.Abandon()
    }
}

ServeSecure(conn: net.Stream, h: &Heap, io: Io, script: &Script, addrs: &Addrs, cfg: &tls.Config) {
    plain := conn.Conn()
    c := tls.Server(h, io, plain, cfg) else {
        plain.Abandon()
        return
    }
    var arena := Arena.New(h) else {
        c.Abandon()
        return
    }
    var rbuf: [16384]u8 = [0; 16384]
    var wbuf: [16384]u8 = [0; 16384]
    var r := c.Reader(io, mut rbuf[..])
    var w := c.Writer(io, mut wbuf[..])
    for Answer(&arena, io, script, addrs, mut r, mut w) {
        arena.Reset()
    }
    arena.Free()
    c.Close(io) else {
        c.Abandon()
    }
}

// ServeConn answers requests on c until one asks to close or fails.
ServeConn(c: &net.Conn, h: &Heap, io: Io, script: &Script, addrs: &Addrs) {
    var arena := Arena.New(h) else {
        return
    }
    defer arena.Free()
    var rbuf: [16384]u8 = [0; 16384]
    var wbuf: [16384]u8 = [0; 16384]
    var r := c.Reader(io, mut rbuf[..])
    var w := c.Writer(io, mut wbuf[..])
    for Answer(&arena, io, script, addrs, mut r, mut w) {
        arena.Reset()
    }
}

// Answer reads one request from r and writes the scripted answer to w. It
// is true when the connection may carry another request.
Answer(a: &Arena into a, io: Io, script: &Script, addrs: &Addrs, r: mut Reader, w: mut Writer): bool {
    req := http.ReadRequest(a, io, r, RequestLimit) else {
        return false
    }
    route := Find(script, req.method, req.path) else {
        Respond(w, 404, "", "not found\n".AsBytes(), 10, false) else {
            return false
        }
        return true
    }
    if route.websocket {
        return Upgrade(a, &req, r, w)
    }
    if route.stall {
        // never answers: the client's deadline or a cancel ends the wait
        io.Sleep(time.Hours(24)) else {}
        return false
    }
    reply := Shape(a, route, &req, addrs) else {
        Respond(w, 500, "", "origin out of memory\n".AsBytes(), 21, false) else {}
        return false
    }
    if route.cut >= 0 && (!route.cut_unranged || req.Header("Range").IsNone()) {
        // promises the whole body, then closes the connection part way
        cut := Min(route.cut, i64(reply.body.Len()))
        Respond(w, reply.status, reply.headers, reply.body[..int(cut)], reply.body.Len(), false) else {}
        return false
    }
    body := if req.method == "HEAD" { reply.body[..0] } else { reply.body }
    Respond(w, reply.status, reply.headers, body, reply.body.Len(), route.chunked) else {
        return false
    }
    return true
}

// a route's answer to one request, ready to write
struct Reply {
    status: int
    headers: str
    body: []u8
}

// Shape computes the answer of route to req: the echo, the coding, the
// range and the conditional request applied.
Shape(a: &Arena, route: &Route, req: &http.Request, addrs: &Addrs): OutOfMemory!Reply from a, route {
    var status := route.status
    var headers := try Expand(a, route.headers, addrs)
    var body := if route.echo { try Echo(a, req) } else { route.body }
    if t := route.modified {
        stamp := try time.FormatHttpDate(a, t)
        headers = try a.Fmt("{headers}Last-Modified: {stamp}\r\n", (headers, stamp))
        if since := req.Header("If-Modified-Since") {
            seen := time.ParseHttpDate(since) else { time.FromUnix(0, 0) }
            if seen.Unix() >= t.Unix() {
                return Reply(status: 304, headers: headers, body: body[..0])
            }
        }
    }
    body = match route.coding {
        .Identity { body }
        .Gzip { try compress.Gzip(a, body) }
        .Zlib { try compress.Zlib(a, body) }
    }
    if route.coding != Coding.Identity {
        name := if route.coding == Coding.Gzip { "gzip" } else { "deflate" }
        headers = try a.Fmt("{headers}Content-Encoding: {name}\r\n", (headers, name))
    }
    if !route.ranges {
        return Reply(status: status, headers: headers, body: body)
    }
    headers = try a.Fmt("{headers}Accept-Ranges: bytes\r\n", (headers))
    spec := req.Header("Range") else {
        return Reply(status: status, headers: headers, body: body)
    }
    total := body.Len()
    start := RangeStart(spec) else {
        return Reply(status: status, headers: headers, body: body)
    }
    if start >= total {
        headers = try a.Fmt("{headers}Content-Range: bytes */{total}\r\n", (headers, total))
        return Reply(status: 416, headers: headers, body: body[..0])
    }
    last := total - 1
    headers = try a.Fmt("{headers}Content-Range: bytes {start}-{last}/{total}\r\n", (headers, start, last, total))
    status = 206
    return Reply(status: status, headers: headers, body: body[start..])
}

// RangeStart reads the first byte of a `bytes=N-` range, none for any other
// form, which the origin answers whole as RFC 9110 14.2 allows.
RangeStart(spec: str): ?int {
    if !spec.StartsWith("bytes=") || !spec.EndsWith("-") {
        return none
    }
    n := spec[6..spec.Len() - 1].ParseInt() else {
        return none
    }
    if n < 0 {
        return none
    }
    return some(int(n))
}

// Echo describes req: its method, target, the headers a client's options
// change, and its body's size and CRC-32.
Echo(a: &Arena, req: &http.Request): OutOfMemory![]u8 from a {
    var t := Text.New(a)
    var out := t.Writer()
    Print(mut out, "{req.method} {req.path}\n", (req)) else {}
    if req.query.Len() > 0 {
        Print(mut out, "query {req.query}\n", (req)) else {}
    }
    for name in EchoedHeaders {
        if value := req.Header(name) {
            Print(mut out, "{name}: {value}\n", (name, value)) else {}
        }
    }
    if req.body.Len() > 0 {
        n := req.body.Len()
        crc := hash.Crc32(req.body)
        Print(mut out, "body {n} bytes, crc32 {crc:08x}\n", (n, crc)) else {}
    }
    return t.Done().AsBytes()
}

// the request headers Echo reports, in this order
const EchoedHeaders: []str = ["Authorization", "Cookie", "Range", "Accept-Encoding", "If-Modified-Since", "Content-Type", "Content-Length", "Transfer-Encoding", "X-Fetch-Test"]

// Find is the first route of script for method and the target's path; a
// route's method `*` matches any, and a HEAD request matches a GET route.
Find(script: &Script, method: str, path: str): ?&Route from script {
    for i in 0..script.routes.Len() {
        r := &script.routes[i]
        if r.path != path {
            continue
        }
        if r.method == "*" || r.method == method || (method == "HEAD" && r.method == "GET") {
            return some(r)
        }
    }
    return none
}

// Respond writes a status line, headers, framing and body to w, and flushes.
Respond(w: mut Writer, status: int, headers: str, body: []u8, length: int, chunked: bool): WriteError!void {
    reason := Reason(status)
    try Print(w, "HTTP/1.1 {status} {reason}\r\n", (status, reason))
    try w.Str(headers)
    if chunked {
        try w.Str("Transfer-Encoding: chunked\r\n\r\n")
        var rest := body
        for rest.Len() > 0 {
            n := if rest.Len() < ChunkBytes { rest.Len() } else { ChunkBytes }
            try Print(w, "{n:x}\r\n", (n))
            try w.Bytes(rest[..n])
            try w.Str("\r\n")
            rest = rest[n..]
        }
        try w.Str("0\r\n\r\n")
        try w.Flush()
        return
    }
    n := length
    if status != 304 && status != 204 {
        try Print(w, "Content-Length: {n}\r\n", (n))
    }
    try w.Str("\r\n")
    try w.Bytes(body)
    try w.Flush()
}

// Reason is RFC 9110 section 15's reason phrase for the statuses scripts use.
Reason(status: int): str {
    return match status {
        200 { "OK" }
        204 { "No Content" }
        206 { "Partial Content" }
        301 { "Moved Permanently" }
        302 { "Found" }
        303 { "See Other" }
        304 { "Not Modified" }
        307 { "Temporary Redirect" }
        308 { "Permanent Redirect" }
        400 { "Bad Request" }
        401 { "Unauthorized" }
        403 { "Forbidden" }
        404 { "Not Found" }
        416 { "Range Not Satisfiable" }
        500 { "Internal Server Error" }
        503 { "Service Unavailable" }
        _ { "Status" }
    }
}

/// s with each `{origin}`, `{origin2}`, `{tls}` and `{tls2}` replaced by
/// that listener's address. O(n); allocates the result in a.
export Expand(a: &Arena, s: str, addrs: &Addrs): OutOfMemory!str from a {
    var t := try Swap(a, s, "{origin}", addrs.origin)
    t = try Swap(a, t, "{origin2}", addrs.origin2)
    t = try Swap(a, t, "{tls}", addrs.tls)
    return try Swap(a, t, "{tls2}", addrs.tls2)
}

/// s with each listener's address written back as its placeholder, so
/// output that names an address reads the same on every run. O(n);
/// allocates the result in a.
export Mask(a: &Arena, s: str, addrs: &Addrs): OutOfMemory!str from a {
    var t := try Swap(a, s, addrs.origin2, "{origin2}")
    t = try Swap(a, t, addrs.origin, "{origin}")
    t = try Swap(a, t, addrs.tls2, "{tls2}")
    return try Swap(a, t, addrs.tls, "{tls}")
}

// Swap is s with every `from` replaced by `to`; an empty `from` changes
// nothing.
Swap(a: &Arena, s: str, from: str, to: str): OutOfMemory!str from a {
    if from.Len() == 0 || !s.Has(from) {
        return try a.Dup(s)
    }
    var t := Text.New(a)
    var rest := s
    for {
        i := rest.Find(from) else {
            break
        }
        try t.Push(rest[..i])
        try t.Push(to)
        rest = rest[i + from.Len()..]
    }
    try t.Push(rest)
    return t.Done()
}

Min(x: i64, y: i64): i64 {
    return if x < y { x } else { y }
}
