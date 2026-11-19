// Why: the Client's pool, limits and sharing between tasks are checked
// without a socket of the machine.
// What: cases for the pool's slots, a body cut short, a long reply head, the
// response-header limit and tasks sharing one client.
// How: a scripted peer on the Sim's loopback answers each request by its
// path, on the Sim's clock. Run by mk test --std=std.http.

import std.net
import std.raw
import std.sim
import std.time
import std.url

// The scripted replies, by path.
const CtOk = 0
const CtSlowHead = 1
const CtSlowBody = 2
const CtCut = 3
const CtBigHead = 4
const CtClose = 5
const CtSlowChunked = 6
const CtKeepClose = 7
const CtUnknown = 8

// CtBigField is the length of /bighead's X-Big value: past the Line's
// 8 KiB buffer, so the head is read on through ReadBigHead.
const CtBigField = 12000

// CtSim runs f on a task of a Sim seeded seed (a task of the Sim holds its
// baton, so deadlines fire on its clock), with the Sim's Io.
CtSim(sys: &System, seed: u64, f: fn(Io, &Heap): bool): bool {
    var s := sim.Sim.New(sys.arena, seed) else {
        return false
    }
    io := s.Io()
    out := raw.Atomic[u32].New(0)
    scope r := io.Scope() {
        r.go CtTask(io, sys.heap, f, &out)
    }
    s.Free()
    // Edge: the scope's join orders the task's Store before this Load.
    return out.Load() == 1
}

CtTask(io: Io, h: &Heap, f: fn(Io, &Heap): bool, out: &raw.Atomic[u32]) {
    if f(io, h) {
        out.Store(1)
    }
}

// CtHeadEnd is the offset just past the first CRLF CRLF in b, -1 when b
// holds none.
CtHeadEnd(b: []u8): int {
    if b.Len() < 4 {
        return -1
    }
    for i in 0..b.Len() - 3 {
        if b[i] == 13 && b[i + 1] == 10 && b[i + 2] == 13 && b[i + 3] == 10 {
            return i + 4
        }
    }
    return -1
}

// CtIs: the request line in h has the target p.
CtIs(h: []u8, p: str): bool {
    var i := 0
    for i < h.Len() && h[i] != 32 {
        i += 1
    }
    i += 1
    pb := p.AsBytes()
    if i + pb.Len() >= h.Len() {
        return false
    }
    for k in 0..pb.Len() {
        if h[i + k] != pb[k] {
            return false
        }
    }
    return h[i + pb.Len()] == 32
}

// CtWhich is the scripted reply for the request head h.
CtWhich(h: []u8): int {
    if CtIs(h, "/ok") {
        return CtOk
    }
    if CtIs(h, "/slowhead") {
        return CtSlowHead
    }
    if CtIs(h, "/slowbody") {
        return CtSlowBody
    }
    if CtIs(h, "/cut") {
        return CtCut
    }
    if CtIs(h, "/bighead") {
        return CtBigHead
    }
    if CtIs(h, "/close") {
        return CtClose
    }
    if CtIs(h, "/slowchunked") {
        return CtSlowChunked
    }
    if CtIs(h, "/kclose") {
        return CtKeepClose
    }
    return CtUnknown
}

// CtSend writes s on c; false when the write failed.
CtSend(io: Io, c: &net.Stream, s: str): bool {
    c.WriteAll(io, s.AsBytes()) else {
        return false
    }
    return true
}

// CtNap sleeps ms milliseconds on io's clock; false when cancelled.
CtNap(io: Io, ms: i64): bool {
    io.Sleep(time.Millis(ms)) else {
        return false
    }
    return true
}

// CtReply writes the scripted reply w on c. True when the connection stays
// open for another request.
CtReply(io: Io, c: &net.Stream, w: int): bool {
    if w == CtOk {
        return CtSend(io, c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
    }
    if w == CtSlowHead {
        return CtNap(io, 50) && CtSend(io, c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
    }
    if w == CtSlowBody {
        return CtSend(io, c, "HTTP/1.1 200 OK\r\nContent-Length: 4\r\n\r\nab") && CtNap(io, 50) && CtSend(io, c, "cd")
    }
    if w == CtCut {
        _ = CtSend(io, c, "HTTP/1.1 200 OK\r\nContent-Length: 10\r\n\r\nabc")
        return false
    }
    if w == CtBigHead {
        if !CtSend(io, c, "HTTP/1.1 200 OK\r\nX-Big: ") {
            return false
        }
        var xs: [1000]u8 = [120; 1000]
        for _ in 0..CtBigField / 1000 {
            c.WriteAll(io, xs[..]) else {
                return false
            }
        }
        return CtSend(io, c, "\r\nContent-Length: 2\r\n\r\nok")
    }
    if w == CtClose {
        _ = CtSend(io, c, "HTTP/1.1 200 OK\r\nConnection: close\r\nContent-Length: 2\r\n\r\nok")
        return false
    }
    if w == CtKeepClose {
        // A reply that lets the connection stay, then the close: the
        // client pools a connection its server already closed.
        _ = CtSend(io, c, "HTTP/1.1 200 OK\r\nContent-Length: 2\r\n\r\nok")
        return false
    }
    if w == CtSlowChunked {
        return CtSend(io, c, "HTTP/1.1 200 OK\r\nTransfer-Encoding: chunked\r\n\r\n2\r\nab\r\n") && CtNap(io, 50) && CtSend(io, c, "2\r\ncd\r\n0\r\n\r\n")
    }
    _ = CtSend(io, c, "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
    return false
}

// CtConn serves one connection of the scripted peer: each request's head
// is read whole (these requests have no body) and its path picks the
// reply, until a reply ends the connection or the peer closes it.
CtConn(io: Io, c: net.Stream) {
    var rb: [4096]u8 = [0; 4096]
    var e := 0
    for {
        var end := CtHeadEnd(rb[0..e])
        for end < 0 {
            if e == rb.Len() {
                c.Close()
                return
            }
            n := c.Read(io, mut rb[e..rb.Len()]) else {
                c.Close()
                return
            }
            if n == 0 {
                c.Close()
                return
            }
            e += n
            end = CtHeadEnd(rb[0..e])
        }
        keep := CtReply(io, &c, CtWhich(rb[0..end]))
        k := e - end
        for x in 0..k {
            rb[x] = rb[end + x]
        }
        e = k
        if !keep {
            c.Close()
            return
        }
    }
}

// CtServe accepts on l until its scope is cancelled, counting the
// connections in accepts, each served by CtConn in a task of its own.
CtServe(io: Io, l: &net.Listener, accepts: &raw.Atomic[u32]) {
    scope cs := io.Scope() {
        for {
            st := l.Accept(io) else {
                return
            }
            // Edge: none needed; the count is read after the scope's join.
            _ = accepts.Add(1)
            cs.go CtConn(io, st)
        }
    }
}

// CtCode is a number for e, so a test can compare outcomes.
CtCode(e: Error): int {
    match e {
        TooLarge {
            return -3
        }
        ReadError {
            return -4
        }
        BadRequest(offset: _) {
            return -5
        }
        time.TimedOut {
            return -6
        }
        Canceled {
            return -7
        }
        _ {
            return -9
        }
    }
}

// CtSame: b holds the bytes of s.
CtSame(b: []u8, s: str): bool {
    sb := s.AsBytes()
    if b.Len() != sb.Len() {
        return false
    }
    for i in 0..b.Len() {
        if b[i] != sb[i] {
            return false
        }
    }
    return true
}

// CtGet fetches u with c and is the reply's status when its body is want,
// -2 when the body differs, CtCode of the error otherwise.
CtGet(c: &Client, h: &Heap, io: Io, u: str, want: str): int {
    var ar := Arena.New(h) else {
        return -100
    }
    defer ar.Free()
    pu := url.Parse(&ar, u, none) else {
        return -101
    }
    hs := List[Header].New(&ar).Done()
    req := ClientRequest(method: "GET", url: &pu, headers: hs[..], body: "".AsBytes())
    r := c.Fetch(&ar, io, &req) else e {
        return CtCode(e)
    }
    if !CtSame(r.body, want) {
        return -2
    }
    return r.status
}

// CtBusy is the number of the client's slots a task holds.
CtBusy(c: &Client): int {
    var n := 0
    for i in 0..MaxSlots {
        if SlotAt(&c.slots, i).busy.Load() != 0 {
            n += 1
        }
    }
    return n
}

// CtKept is the number of the client's slots that hold a connection.
CtKept(c: &Client, io: Io): int {
    var n := 0
    for i in 0..MaxSlots {
        g := SlotAt(&c.slots, i).st.Lock(io.NoCancel()) else {
            return -1
        }
        if g.Ref().conn.IsSome() {
            n += 1
        }
        g.Unlock()
    }
    return n
}

// CtFetchTask fetches u with the shared client and counts a 200 with the
// body want in got.
CtFetchTask(c: &Client, h: &Heap, io: Io, u: str, want: str, got: &raw.Atomic[u32]) {
    if CtGet(c, h, io, u, want) == 200 {
        // Edge: none needed; the count is read after the scope's join.
        _ = got.Add(1)
    }
}

// CtLeak: a request to B that passes A's idle slot on its way to B's own
// gives A's back, so no slot stays busy, and each origin keeps one
// connection.
CtLeak(io: Io, h: &Heap): bool {
    la := net.Listen(io, "127.0.0.1:7410") else {
        return false
    }
    lb := net.Listen(io, "127.0.0.1:7411") else {
        la.Close()
        return false
    }
    na := raw.Atomic[u32].New(0)
    nb := raw.Atomic[u32].New(0)
    var ok := true
    var c := Client.New(h, DefaultClientLimits)
    scope srv := io.Scope() {
        srv.go CtServe(io, &la, &na)
        srv.go CtServe(io, &lb, &nb)
        ok = CtGet(&c, h, io, "http://127.0.0.1:7410/ok", "ok") == 200
        ok = ok && CtGet(&c, h, io, "http://127.0.0.1:7411/ok", "ok") == 200
        ok = ok && CtGet(&c, h, io, "http://127.0.0.1:7411/ok", "ok") == 200
        ok = ok && CtGet(&c, h, io, "http://127.0.0.1:7410/ok", "ok") == 200
        ok = ok && CtBusy(&c) == 0 && CtKept(&c, io) == 2
        srv.Cancel()
    }
    ok = ok && na.Load() == 1 && nb.Load() == 1
    c.Close()
    la.Close()
    lb.Close()
    return ok
}

test "Claim gives back the slots it passed, so no slot stays busy, and each origin keeps its connection" (sys: &System) {
    Assert(CtSim(sys, 1, CtLeak))
}

// CtShare: two tasks fetch through one client at once, then 20 tasks
// each hold a slow request, past the 16 slots: four go over connections
// of their own, closed after, and the pool ends with 16 idle connections
// and no busy slot.
CtShare(io: Io, h: &Heap): bool {
    l := net.Listen(io, "127.0.0.1:7420") else {
        return false
    }
    n := raw.Atomic[u32].New(0)
    two := raw.Atomic[u32].New(0)
    many := raw.Atomic[u32].New(0)
    var ok := true
    var c := Client.New(h, DefaultClientLimits)
    scope srv := io.Scope() {
        srv.go CtServe(io, &l, &n)
        scope t := io.Scope() {
            t.go CtFetchTask(&c, h, io, "http://127.0.0.1:7420/ok", "ok", &two)
            t.go CtFetchTask(&c, h, io, "http://127.0.0.1:7420/slowbody", "abcd", &two)
        }
        ok = two.Load() == 2 && CtBusy(&c) == 0
        scope t := io.Scope() {
            for _ in 0..20 {
                t.go CtFetchTask(&c, h, io, "http://127.0.0.1:7420/slowhead", "ok", &many)
            }
        }
        ok = ok && many.Load() == 20 && CtBusy(&c) == 0 && CtKept(&c, io) == MaxSlots
        srv.Cancel()
    }
    // The first two requests opened two connections; the 20 reused those
    // two and opened 18 more (16 slots in all, four over).
    ok = ok && n.Load() == 20
    c.Close()
    l.Close()
    return ok
}

test "Two tasks share one Client, and more requests at once than slots go over connections of their own" (sys: &System) {
    Assert(CtSim(sys, 2, CtShare))
}

// CtFaults: a body cut short is ReadError and its connection is not kept;
// a reply head past the Line's buffer is read whole and its connection
// kept; a head later than response_header is time.TimedOut and its
// connection not kept; Connection: close is honoured.
CtFaults(io: Io, h: &Heap): bool {
    l := net.Listen(io, "127.0.0.1:7430") else {
        return false
    }
    n := raw.Atomic[u32].New(0)
    var ok := true
    var lim := DefaultClientLimits
    lim.response_header = time.Millis(20)
    var c := Client.New(h, lim)
    scope srv := io.Scope() {
        srv.go CtServe(io, &l, &n)
        ok = CtGet(&c, h, io, "http://127.0.0.1:7430/cut", "") == -4
        ok = ok && CtBusy(&c) == 0 && CtKept(&c, io) == 0
        ok = ok && CtGet(&c, h, io, "http://127.0.0.1:7430/bighead", "ok") == 200
        ok = ok && CtBusy(&c) == 0 && CtKept(&c, io) == 1
        ok = ok && CtGet(&c, h, io, "http://127.0.0.1:7430/ok", "ok") == 200
        ok = ok && n.Load() == 2
        ok = ok && CtGet(&c, h, io, "http://127.0.0.1:7430/slowhead", "ok") == -6
        ok = ok && CtBusy(&c) == 0 && CtKept(&c, io) == 0
        ok = ok && CtGet(&c, h, io, "http://127.0.0.1:7430/close", "ok") == 200
        ok = ok && CtBusy(&c) == 0 && CtKept(&c, io) == 0
        ok = ok && CtGet(&c, h, io, "http://127.0.0.1:7430/ok", "ok") == 200
        ok = ok && n.Load() == 4
        srv.Cancel()
    }
    c.Close()
    l.Close()
    return ok
}

test "A body cut short, a long head, the response-header limit and Connection: close, each leaving the pool right" (sys: &System) {
    Assert(CtSim(sys, 3, CtFaults))
}

// CtBigOne fetches /bighead from port 7440: its X-Big field arrives whole.
CtBigOne(c: &Client, h: &Heap, io: Io): bool {
    var ar := Arena.New(h) else {
        return false
    }
    defer ar.Free()
    pu := url.Parse(&ar, "http://127.0.0.1:7440/bighead", none) else {
        return false
    }
    hs := List[Header].New(&ar).Done()
    req := ClientRequest(method: "GET", url: &pu, headers: hs[..], body: "".AsBytes())
    r := c.Fetch(&ar, io, &req) else {
        return false
    }
    v := Lookup(r.headers, "x-big") else {
        return false
    }
    return v.Len() == CtBigField && v.Byte(0) == 120 && v.Byte(CtBigField - 1) == 120
}

// CtBigHeadValue: the X-Big field of /bighead arrives whole.
CtBigHeadValue(io: Io, h: &Heap): bool {
    l := net.Listen(io, "127.0.0.1:7440") else {
        return false
    }
    n := raw.Atomic[u32].New(0)
    var ok := false
    var c := Client.New(h, DefaultClientLimits)
    scope srv := io.Scope() {
        srv.go CtServe(io, &l, &n)
        ok = CtBigOne(&c, h, io)
        srv.Cancel()
    }
    c.Close()
    l.Close()
    return ok
}

test "A reply head longer than the Line's 8 KiB buffer is read whole by ReadBigHead" (sys: &System) {
    Assert(CtSim(sys, 4, CtBigHeadValue))
}

// CtRetry: two requests at once leave two pooled connections their server
// closed; the next GET fails on the first and is retried once on a new
// connection, never on the second pooled one, so it is answered over one
// new accept.
CtRetry(io: Io, h: &Heap): bool {
    l := net.Listen(io, "127.0.0.1:7470") else {
        return false
    }
    n := raw.Atomic[u32].New(0)
    two := raw.Atomic[u32].New(0)
    var ok := true
    var c := Client.New(h, DefaultClientLimits)
    scope srv := io.Scope() {
        srv.go CtServe(io, &l, &n)
        scope t := io.Scope() {
            t.go CtFetchTask(&c, h, io, "http://127.0.0.1:7470/kclose", "ok", &two)
            t.go CtFetchTask(&c, h, io, "http://127.0.0.1:7470/kclose", "ok", &two)
        }
        ok = two.Load() == 2 && CtKept(&c, io) == 2 && n.Load() == 2
        ok = ok && CtGet(&c, h, io, "http://127.0.0.1:7470/ok", "ok") == 200
        ok = ok && n.Load() == 3 && CtBusy(&c) == 0
        srv.Cancel()
    }
    c.Close()
    l.Close()
    return ok
}

test "A request on a pooled connection its server closed is retried once on a new connection, never on another pooled one" (sys: &System) {
    for seed in 1..4 {
        Assert(CtSim(sys, Cast[u64](seed), CtRetry))
    }
}
