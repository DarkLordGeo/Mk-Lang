// Why: an interrupted DoStreaming must leave the client's pool clean.
// What: DoStreaming interrupted at each await point (connect, the head,
// each slow body piece, the reply), then the client's next request.
// How: ServeWith's loop with tests_servewith.mk's echo handler serves; the
// body is a Reader over a second connection fed 4 pieces 10 ms apart.

import std.net
import std.raw
import std.time
import std.url

// SmSpan is the sweep's end in milliseconds: past the feeder's last piece
// (40 ms) and the reply.
const SmSpan = 48

// SmServe accepts connections on l and serves each with ServeWith's loop
// and the echo handler, until the scope is cancelled.
SmServe(io: Io, l: &net.Listener, h: &Heap, accepts: &raw.Atomic[u32]) {
    scope cs := io.Scope() {
        for {
            st := l.Accept(io) else {
                return
            }
            // Edge: none needed; the count is read after the scope's join.
            _ = accepts.Add(1)
            cs.go SmServeOne(io, st.Conn(), h)
        }
    }
}

// SmServeOne serves one connection with the default options.
SmServeOne(io: Io, conn: net.Conn, h: &Heap) {
    app := SwApp(n: 0)
    ServeOwned(conn, h, io, &DefaultServerOptions, &app, SwEcho)
}

// SmFeed dials addr and writes "abcd" 4 times, 10 ms apart, then closes.
SmFeed(io: Io, addr: str) {
    c := net.Dial(io, addr) else {
        return
    }
    for _ in 0..4 {
        if !CtNap(io, 10) || !CtSend(io, &c, "abcd") {
            break
        }
    }
    c.Close()
}

// SmOnce streams the feeder's body to u with a deadline of d ms: 200 when
// the echo came back whole, else CtCode's number of the error (or -2 for
// a wrong body).
SmOnce(c: &Client, h: &Heap, io: Io, bl: &net.Listener, baddr: str, u: str, d: i64): int {
    var r := 0
    scope s := io.Scope() {
        s.go SmFeed(io, baddr)
        r = SmRun(c, h, io, bl, u, d)
    }
    return r
}

// SmRun takes the feeder's connection and streams it under the deadline.
SmRun(c: &Client, h: &Heap, io: Io, bl: &net.Listener, u: str, d: i64): int {
    st := bl.Accept(io) else {
        return -50
    }
    var r := 0
    scope q := io.Scope() {
        q.CancelAfter(time.Millis(d))
        r = SmStream(c, h, io, &st, u)
    }
    st.Close()
    return r
}

// SmStream sends the body read from st to u through DoStreaming, chunked,
// and reads the echo.
SmStream(c: &Client, h: &Heap, io: Io, st: &net.Stream, u: str): int {
    var ar := Arena.New(h) else {
        return -100
    }
    defer ar.Free()
    pu := url.Parse(&ar, u, none) else {
        return -101
    }
    hs := List[Header].New(&ar).Done()
    req := ClientRequest(method: "POST", url: &pu, headers: hs[..], body: "".AsBytes())
    var sb: [64]u8 = [0; 64]
    var body := st.Reader(io, mut sb[..])
    var ex := c.DoStreaming(&ar, io, &req, mut body, none) else e {
        return CtCode(e)
    }
    var got: [64]u8 = [0; 64]
    n := CnReadAll(mut ex, mut got[..])
    if n < 0 {
        ex.Abandon()
        return -4
    }
    status := ex.status
    ex.Close(io) else e {
        ex.Abandon()
        return CtCode(e)
    }
    if !CtSame(got[0..n], "abcdabcdabcdabcd") {
        return -2
    }
    return status
}

// SmSweep: for each deadline from 0 to SmSpan ms, DoStreaming either
// delivers the echo with its connection kept, or fails with no slot busy
// and no connection kept; either way a GET after it is answered, over a
// new connection exactly when the interrupted one was dropped.
SmSweep(io: Io, h: &Heap): bool {
    l := net.Listen(io, "127.0.0.1:7490") else {
        return false
    }
    bl := net.Listen(io, "127.0.0.1:7491") else {
        l.Close()
        return false
    }
    n := raw.Atomic[u32].New(0)
    var ok := true
    var c := Client.New(h, DefaultClientLimits)
    var fails := 0
    var wins := 0
    scope srv := io.Scope() {
        srv.go SmServe(io, &l, h, &n)
        for d in 0..SmSpan + 1 {
            r := SmOnce(&c, h, io, &bl, "127.0.0.1:7491", "http://127.0.0.1:7490/", i64(d))
            before := n.Load()
            if r == 200 {
                wins += 1
                ok = ok && CtBusy(&c) == 0 && CtKept(&c, io) == 1
                ok = ok && CtGet(&c, h, io, "http://127.0.0.1:7490/", "") == 200 && n.Load() == before
            } else {
                fails += 1
                ok = ok && r != -2 && CtBusy(&c) == 0 && CtKept(&c, io) == 0
                ok = ok && CtGet(&c, h, io, "http://127.0.0.1:7490/", "") == 200 && n.Load() == before + 1
            }
            if !ok {
                break
            }
        }
        srv.Cancel()
    }
    c.Close()
    bl.Close()
    l.Close()
    // The sweep reached both ends: early deadlines fail, late ones do not.
    return ok && fails > 0 && wins > 0
}

test "a DoStreaming interrupted at each await point is closed, never pooled, and the client goes on" (sys: &System) {
    for seed in 1..4 {
        Assert(CtSim(sys, Cast[u64](seed), SmSweep))
    }
}
