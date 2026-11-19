// Why: ServeWith's per-connection loop must honour its byte limits exactly.
// What: a head past 8 KiB read on up to header_bytes, beyond 32 KiB, and a
// chunked body of many small chunks over body_bytes.
// How: a simulated Io with a raw peer, so the bytes on the wire are exactly
// the test's. Run by mk test --std=std.http.

import std.net
import std.time

// SwApp is the cases' context.
struct SwApp {
    n: int
}

// SwEcho answers 200 with the request's body.
SwEcho(c: &SwApp, a: &Arena, io: Io, req: &Request, res: mut Response, link: &Link): Error!void *res into res {
    try res.Body(req.body)
}

// SwServeOne accepts one connection and serves it with o.
SwServeOne(io: Io, l: &net.Listener, h: &Heap, o: &ServerOptions) {
    st := l.Accept(io) else {
        return
    }
    conn := st.Conn()
    app := SwApp(n: 0)
    ServeOwned(conn, h, io, o, &app, SwEcho)
}

// SwSendHead writes a request head with an X-Big field of n bytes, then
// the bytes of body; false on a write error.
SwSendHead(io: Io, c: &net.Stream, n: int, chunked: bool): bool {
    pre := if chunked { "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\nX-Big: " } else { "GET / HTTP/1.1\r\nHost: x\r\nX-Big: " }
    c.WriteAll(io, pre.AsBytes()) else {
        return false
    }
    var xs: [1000]u8 = [120; 1000]
    var left := n
    for left > 0 {
        k := if left > 1000 { 1000 } else { left }
        c.WriteAll(io, xs[0..k]) else {
            return false
        }
        left -= k
    }
    c.WriteAll(io, "\r\n\r\n".AsBytes()) else {
        return false
    }
    return true
}

// SwStatus reads the reply's status code from c: the three digits after
// "HTTP/1.1 ", 0 when the connection ends first or a read fails.
SwStatus(io: Io, c: &net.Stream): int {
    var b: [16]u8 = [0; 16]
    var n := 0
    for n < 12 {
        k := c.Read(io, mut b[n..16]) else {
            return 0
        }
        if k == 0 {
            return 0
        }
        n += k
    }
    return int(b[9] - 48) * 100 + int(b[10] - 48) * 10 + int(b[11] - 48)
}

// SwHeadCase serves one connection with header_bytes limit and sends a
// head with an X-Big field of n bytes: the reply's status.
SwHeadCase(io: Io, h: &Heap, port: str, limit: int, n: int): int {
    l := net.Listen(io, port) else {
        return -1
    }
    var o := DefaultServerOptions
    o.header_bytes = limit
    var code := -1
    scope top := io.Scope() {
        top.go SwServeOne(io, &l, h, &o)
        code = SwHeadClient(io, port, n)
        top.Cancel()
    }
    l.Close()
    return code
}

// SwHeadClient dials port, sends a head with an X-Big field of n bytes and
// reads the reply's status; -1 when the dial fails.
SwHeadClient(io: Io, port: str, n: int): int {
    c := net.Dial(io, port) else {
        return -1
    }
    var code := 0
    if SwSendHead(io, &c, n, false) {
        code = SwStatus(io, &c)
    }
    c.Close()
    return code
}

SwHeads(io: Io, h: &Heap): bool {
    a := SwHeadCase(io, h, "127.0.0.1:7460", 65536, 40000)
    b := SwHeadCase(io, h, "127.0.0.1:7461", 65536, 70000)
    c := SwHeadCase(io, h, "127.0.0.1:7462", 32768, 20000)
    d := SwHeadCase(io, h, "127.0.0.1:7463", 32768, 33000)
    return a == 200 && b == 431 && c == 200 && d == 431
}

test "ServeWith reads a head past 8 KiB and past 32 KiB up to header_bytes, and answers 431 beyond" (sys: &System) {
    Assert(CtSim(sys, 6, SwHeads))
}

// SwChunkCase sends a chunked body of k chunks of four bytes to a server
// whose body_bytes is limit: the reply's status.
SwChunkCase(io: Io, h: &Heap, port: str, limit: int, k: int): int {
    l := net.Listen(io, port) else {
        return -1
    }
    var o := DefaultServerOptions
    o.body_bytes = limit
    var code := -1
    scope top := io.Scope() {
        top.go SwServeOne(io, &l, h, &o)
        code = SwChunkClient(io, port, k)
        top.Cancel()
    }
    l.Close()
    return code
}

// SwChunkClient dials port and sends a chunked body of k chunks of four
// bytes: the reply's status, -1 when the dial fails.
SwChunkClient(io: Io, port: str, k: int): int {
    c := net.Dial(io, port) else {
        return -1
    }
    var ok := SwSendHead(io, &c, 1, true)
    for _ in 0..k {
        if ok {
            c.WriteAll(io, "4\r\nabcd\r\n".AsBytes()) else {
                ok = false
            }
        }
    }
    if ok {
        c.WriteAll(io, "0\r\n\r\n".AsBytes()) else {
            ok = false
        }
    }
    code := SwStatus(io, &c)
    c.Close()
    return code
}

SwChunks(io: Io, h: &Heap): bool {
    // 300 chunks of 4 bytes are 1,200 body bytes (2,700 with framing).
    a := SwChunkCase(io, h, "127.0.0.1:7464", 1000, 300)
    b := SwChunkCase(io, h, "127.0.0.1:7465", 4000, 300)
    return a == 413 && b == 200
}

test "ServeWith answers 413 to a chunked body of many small chunks whose sum passes body_bytes" (sys: &System) {
    Assert(CtSim(sys, 7, SwChunks))
}

// SwStallClient dials port, sends a chunked request whose trailer never
// ends, and reads until the connection ends: 0 at its end, -2 on a read
// error (a deadlock under the Sim answers Canceled), -1 when the dial
// fails.
SwStallClient(io: Io, port: str): int {
    c := net.Dial(io, port) else {
        return -1
    }
    c.WriteAll(io, "POST / HTTP/1.1\r\nHost: x\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n0\r\nT: v\r\n".AsBytes()) else {
        c.Close()
        return -2
    }
    var b: [64]u8 = [0; 64]
    for {
        k := c.Read(io, mut b[..]) else {
            c.Close()
            return -2
        }
        if k == 0 {
            c.Close()
            return 0
        }
    }
}

// SwStall: a body that stops arriving (here a trailer with no end) ends
// the connection once request_header passes with no byte, at 1 s on the
// Sim's clock.
SwStall(io: Io, h: &Heap): bool {
    l := net.Listen(io, "127.0.0.1:7466") else {
        return false
    }
    var o := DefaultServerOptions
    o.request_header = time.Millis(1000)
    var code := -1
    t0 := time.Now(io)
    scope top := io.Scope() {
        top.go SwServeOne(io, &l, h, &o)
        code = SwStallClient(io, "127.0.0.1:7466")
        top.Cancel()
    }
    took := time.Now(io).Since(t0).Nanos()
    l.Close()
    return code == 0 && took >= 1_000_000_000 && took < 2_000_000_000
}

test "ServeWith closes a connection whose body stops arriving once request_header passes with no byte (FuzzStdHttp_ServeWith's hang)" (sys: &System) {
    Assert(CtSim(sys, 8, SwStall))
}
