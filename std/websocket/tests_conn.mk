// Why: websocket.Conn and Dial carry the module's concurrency and limit
// rules, which only end-to-end runs can show.
// What: messages both ways, raw peers' frames, every violation with the
// Close status it sends, the limits, owed pongs, the close handshake both
// ways, cancelled Recv and Close, many senders, and Dial against a raw server.
// How: runs under std.sim's loopback network; run by mk test
// --std=std.websocket and std/std-ws_blocks_test.go.

import std.core
import std.http
import std.net
import std.sim
import std.time
import std.url

const Addr = "127.0.0.1:7600"

// Loop is a connected loopback pair under io: the dialled end and the
// accepted one.
Loop(io: Io): net.Error!(c: net.Conn, s: net.Conn) {
    l := try net.Listen(io, Addr)
    defer l.Close()
    c := try net.Dial(io, Addr)
    s := l.Accept(io) else e {
        c.Close()
        fail e
    }
    return (c: c.Conn(), s: s.Conn())
}

// WsPair is a loopback pair as WebSocket connections with o's limits: the
// client and the server.
WsPair(h: &Heap, io: Io, o: &Options): Error!(cl: Conn, sv: Conn) from h, io {
    (c, s) := try Loop(io)
    cl := Wrap(h, c, true, o, "".AsBytes()) else e {
        c.Abandon()
        s.Abandon()
        fail e
    }
    sv := Wrap(h, s, false, o, "".AsBytes()) else e {
        cl.Abandon()
        s.Abandon()
        fail e
    }
    return (cl: cl, sv: sv)
}

// RawPair is a WebSocket connection, client side or not, and the raw peer
// that speaks to it.
RawPair(h: &Heap, io: Io, client: bool, o: &Options): Error!(ws: Conn, raw: net.Conn) from h, io {
    (c, s) := try Loop(io)
    ws := Wrap(h, c, client, o, "".AsBytes()) else e {
        c.Abandon()
        s.Abandon()
        fail e
    }
    return (ws: ws, raw: s)
}

// ReadN reads exactly n bytes of c into buf.
ReadN(c: &net.Conn, io: Io, buf: mut []u8, n: int): net.Error!void {
    var got := 0
    for got < n {
        k := try c.Read(io, mut buf[got..n])
        if k == 0 {
            fail net.Reset
        }
        got += k
    }
}

// Pattern is byte i of a test payload.
Pattern(i: int): u8 {
    return Trunc[u8](i * 31 + (i >> 8))
}

// Filled is n bytes of Pattern in a.
Filled(a: &Arena, n: int): OutOfMemory![]u8 from a {
    b := try a.Slice[u8](n)
    for i in 0..n {
        b[i] = Pattern(i)
    }
    return b[..]
}

// IsPattern reports whether b is Pattern's first b.Len() bytes.
IsPattern(b: []u8): bool {
    for i in 0..b.Len() {
        if b[i] != Pattern(i) {
            return false
        }
    }
    return true
}

// CodeOf is e's Protocol or PeerClosed code, 1 for TooLarge, 2 for
// time.TimedOut, 3 for Canceled, 0 otherwise.
CodeOf(e: Error): int {
    return match e {
        Protocol(code) { int(code) }
        PeerClosed(code) { int(code) }
        TooLarge { 1 }
        time.TimedOut { 2 }
        Canceled { 3 }
        _ { 0 }
    }
}

// RecvCode is the code CodeOf gives the error of c's next Recv, -1 when it
// delivers a message.
RecvCode(c: &Conn, a: &Arena, io: Io): int {
    _ = c.Recv(a, io) else e {
        return CodeOf(e)
    }
    return -1
}

// SendTask sends data as kind on c, n times.
SendTask(c: &Conn, io: Io, kind: Kind, data: []u8, n: int) {
    for _ in 0..n {
        c.Send(io, kind, data) else {
            return
        }
    }
}

test "Messages cross both ways whole: text, one frame, over the write chunk and the direct-read size" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 1)
    defer sm.Free()
    io := sm.Io()
    o := DefaultOptions
    (cl, sv) := try WsPair(sys.heap, io, &o)
    onerr cl.Abandon()
    onerr sv.Abandon()
    try cl.Send(io, Kind.Text, "hello".AsBytes())
    m := try sv.Recv(sys.arena, io)
    Assert(m.kind == Kind.Text && SameBytes(m.data, "hello".AsBytes()))
    big := try Filled(sys.arena, 100000)
    for size in [0, 1, 125, 126, 65535, 65536, 100000] {
        scope s := io.Scope() {
            s.go SendTask(&sv, io, Kind.Binary, big[0..size], 1)
            m2 := try cl.Recv(sys.arena, io)
            Assert(m2.kind == Kind.Binary && m2.data.Len() == size && IsPattern(m2.data))
        }
        scope s2 := io.Scope() {
            s2.go SendTask(&cl, io, Kind.Binary, big[0..size], 1)
            m3 := try sv.Recv(sys.arena, io)
            Assert(m3.kind == Kind.Binary && m3.data.Len() == size && IsPattern(m3.data))
        }
    }
    sv.Abandon()
    cl.Abandon()
}

test "RFC 6455 5.7's frames from a raw peer: a masked Hello to a server, a fragmented Hello with a Ping between to a client, which answers with a masked Pong" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 2)
    defer sm.Free()
    io := sm.Io()
    o := DefaultOptions
    (ws: sv, raw: peer) := try RawPair(sys.heap, io, false, &o)
    onerr sv.Abandon()
    onerr peer.Abandon()
    masked: [11]u8 = [0x81, 0x85, 0x37, 0xFA, 0x21, 0x3D, 0x7F, 0x9F, 0x4D, 0x51, 0x58]
    try peer.WriteAll(io, masked[..])
    m := try sv.Recv(sys.arena, io)
    Assert(m.kind == Kind.Text && SameBytes(m.data, "Hello".AsBytes()))
    sv.Abandon()
    peer.Abandon()

    (ws: cl, raw: p2) := try RawPair(sys.heap, io, true, &o)
    onerr cl.Abandon()
    onerr p2.Abandon()
    frames: [18]u8 = [0x01, 0x03, 0x48, 0x65, 0x6C, 0x89, 0x05, 0x48, 0x65, 0x6C, 0x6C, 0x6F, 0x80, 0x02, 0x6C, 0x6F, 0, 0]
    try p2.WriteAll(io, frames[0..16])
    m2 := try cl.Recv(sys.arena, io)
    Assert(m2.kind == Kind.Text && SameBytes(m2.data, "Hello".AsBytes()))
    var pong: [11]u8 = [0; 11]
    try ReadN(&p2, io, mut pong[..], 11)
    Assert(pong[0] == 0x8A && pong[1] == 0x85)
    key: [4]u8 = [pong[2], pong[3], pong[4], pong[5]]
    _ = Mask(mut pong[6..], key, 0)
    Assert(SameBytes(pong[6..], "Hello".AsBytes()))
    cl.Abandon()
    p2.Abandon()
}

// Violation sends bad to a server (masked true for a server's peer) and
// returns the code of the server's Recv, after checking the Close frame it
// sent names that code and that later calls fail the same way.
Violation(sys: &System, seed: u64, client: bool, bad: []u8, o: &Options): Error!int {
    var sm := try sim.Sim.New(sys.arena, seed)
    defer sm.Free()
    io := sm.Io()
    (ws, raw: peer) := try RawPair(sys.heap, io, client, o)
    onerr ws.Abandon()
    onerr peer.Abandon()
    try peer.WriteAll(io, bad)
    code := RecvCode(&ws, sys.arena, io)
    if code > 1000 {
        // The Close frame names the code: masked from a client.
        var f: [8]u8 = [0; 8]
        n := if client { 8 } else { 4 }
        try ReadN(&peer, io, mut f[..], n)
        Assert(f[0] == 0x88)
        var body: [2]u8 = [f[n - 2], f[n - 1]]
        if client {
            key: [4]u8 = [f[2], f[3], f[4], f[5]]
            _ = Mask(mut body[..], key, 0)
        }
        Assert(int((u16(body[0]) << 8) | u16(body[1])) == code)
    }
    if code >= 0 {
        Assert(RecvCode(&ws, sys.arena, io) == code || code == 1)
        _ = ws.Send(io, Kind.Text, "x".AsBytes()) else e {
            Assert(CodeOf(e) == code || code == 1)
        }
    }
    ws.Abandon()
    peer.Abandon()
    return code
}

test "Each violation fails the connection with its Close status, and every later call reports it" (sys: &System) {
    o := DefaultOptions
    rsv: [6]u8 = [0xC1, 0x80, 0, 0, 0, 0]
    unmasked: [3]u8 = [0x81, 0x01, 0x41]
    masked_to_client: [7]u8 = [0x81, 0x81, 1, 2, 3, 4, 0x41]
    big_ping: [8]u8 = [0x89, 0xFE, 0, 126, 0, 0, 0, 0]
    frag_close: [6]u8 = [0x08, 0x80, 0, 0, 0, 0]
    lone_continue: [6]u8 = [0x80, 0x80, 0, 0, 0, 0]
    // a first fragment, then a new text frame inside the message
    restart: [12]u8 = [0x01, 0x80, 0, 0, 0, 0, 0x81, 0x80, 0, 0, 0, 0]
    // the bytes C0 80, an overlong NUL, as text
    bad_text: [8]u8 = [0x81, 0x82, 0, 0, 0, 0, 0xC0, 0x80]
    // a close body of one byte, and one with status 1005
    short_close: [7]u8 = [0x88, 0x81, 0, 0, 0, 0, 3]
    bad_status: [8]u8 = [0x88, 0x82, 0, 0, 0, 0, 0x03, 0xED]
    Assert(try Violation(sys, 3, false, rsv[..], &o) == 1002)
    Assert(try Violation(sys, 4, false, unmasked[..], &o) == 1002)
    Assert(try Violation(sys, 5, true, masked_to_client[..], &o) == 1002)
    Assert(try Violation(sys, 6, false, big_ping[..], &o) == 1002)
    Assert(try Violation(sys, 7, false, frag_close[..], &o) == 1002)
    Assert(try Violation(sys, 8, false, lone_continue[..], &o) == 1002)
    Assert(try Violation(sys, 9, false, restart[..], &o) == 1002)
    Assert(try Violation(sys, 10, false, bad_text[..], &o) == 1007)
    Assert(try Violation(sys, 11, false, short_close[..], &o) == 1002)
    Assert(try Violation(sys, 12, false, bad_status[..], &o) == 1002)
}

test "Send refuses text that is not UTF-8 and Ping and Close refuse what a control frame cannot hold, each sending nothing" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 16)
    defer sm.Free()
    io := sm.Io()
    o := DefaultOptions
    (cl, sv) := try WsPair(sys.heap, io, &o)
    onerr cl.Abandon()
    onerr sv.Abandon()
    bad: [2]u8 = [0xC0, 0x80]
    cl.Send(io, Kind.Text, bad[..]) else e {
        Assert(CodeOf(e) == 1007)
    }
    big := try Filled(sys.arena, 126)
    cl.Ping(io, big) else e {
        Assert(CodeOf(e) == 1)
    }
    // The connection still works.
    try cl.Send(io, Kind.Binary, bad[..])
    m := try sv.Recv(sys.arena, io)
    Assert(SameBytes(m.data, bad[..]))
    sv.Abandon()
    cl.Abandon()
    reason := try LongReason(sys.arena)
    Assert(try CloseRefused(sys, io, 1005, "") == 1005)
    Assert(try CloseRefused(sys, io, 0, "why") == 0)
    Assert(try CloseRefused(sys, io, 1000, reason) == 1)
    Assert(try CloseRefused(sys, io, 2999, "") == 2999)
}

// CloseRefused closes a fresh connection with code and reason, which
// Close must refuse: the CodeOf its error.
CloseRefused(sys: &System, io: Io, code: u16, reason: str): Error!int {
    o := DefaultOptions
    (ws: c, raw: peer) := try RawPair(sys.heap, io, true, &o)
    defer peer.Abandon()
    c.Close(io, code, reason) else e {
        c.Abandon()
        return CodeOf(e)
    }
    Panic("Close took what it must refuse")
}

// LongReason is 124 bytes of text, one more than a Close reason holds.
LongReason(a: &Arena): OutOfMemory!str from a {
    var t := Text.New(a)
    for _ in 0..124 {
        try t.Push("r")
    }
    return t.Done()
}

test "A peer's Close is answered with the same status and reported as PeerClosed; Close then only releases" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 17)
    defer sm.Free()
    io := sm.Io()
    o := DefaultOptions
    (ws: sv, raw: peer) := try RawPair(sys.heap, io, false, &o)
    onerr sv.Abandon()
    onerr peer.Abandon()
    close: [10]u8 = [0x88, 0x84, 0, 0, 0, 0, 0x03, 0xE9, 0x68, 0x69]
    try peer.WriteAll(io, close[..])
    Assert(RecvCode(&sv, sys.arena, io) == 1001)
    var echo: [4]u8 = [0; 4]
    try ReadN(&peer, io, mut echo[..], 4)
    Assert(echo[0] == 0x88 && echo[1] == 0x02 && echo[2] == 0x03 && echo[3] == 0xE9)
    Assert(RecvCode(&sv, sys.arena, io) == 1001)
    try sv.Close(io, 1000, "")
    peer.Abandon()
}

// Closer closes c with status 1000 and stores 1 in done when it returned.
Closer(c: Conn, io: Io, done: &Mutex[int]) {
    c.Close(io, 1000, "bye") else e {
        c.Abandon()
        var g := done.Lock(io) else {
            return
        }
        *g.Mut() = 2 + CodeOf(e)
        g.Unlock()
        return
    }
    var g := done.Lock(io) else {
        return
    }
    *g.Mut() = 1
    g.Unlock()
}

test "The close handshake between two connections: the closer's Close returns once the other side echoes" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 18)
    defer sm.Free()
    io := sm.Io()
    o := DefaultOptions
    (cl, sv) := try WsPair(sys.heap, io, &o)
    onerr sv.Abandon()
    done := Mutex[int].New(0)
    scope s := io.Scope() {
        s.go Closer(cl, io, &done)
        Assert(RecvCode(&sv, sys.arena, io) == 1000)
    }
    g := try done.Lock(io)
    Assert(*g.Ref() == 1)
    g.Unlock()
    try sv.Close(io, 1000, "")
}

test "a Close the peer never answers fails with time.TimedOut at the close limit, also under NoCancel, and under a cancelled scope with Canceled; the Conn is then the caller's to Abandon" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 19)
    defer sm.Free()
    io := sm.Io()
    var o := DefaultOptions
    o.close = time.Millis(200)
    for mode in 0..3 {
        (ws: cl, raw: peer) := try RawPair(sys.heap, io, true, &o)
        onerr peer.Abandon()
        var code := 0
        if mode == 2 {
            scope s := io.Scope() {
                s.Cancel()
                cl.Close(io, 1000, "") else e {
                    code = CodeOf(e)
                    cl.Abandon()
                }
            }
        } else {
            q := if mode == 1 { io.NoCancel() } else { io }
            cl.Close(q, 1000, "") else e {
                code = CodeOf(e)
                cl.Abandon()
            }
        }
        want := if mode == 2 { 3 } else { 2 }
        Assert(code == want)
        peer.Abandon()
    }
}

// CancelAt cancels s after nap ns of simulated time.
CancelAt(s: &Scope, io: Io, nap: i64) {
    io.Sleep(core.DurationOf(nap)) else {
        return
    }
    s.Cancel()
}

// Feed writes b to c in pieces, with a pause of nap ns before each.
Feed(c: &net.Conn, io: Io, b: []u8, pieces: int, nap: i64) {
    step := (b.Len() + pieces - 1) / pieces
    var at := 0
    for at < b.Len() {
        io.Sleep(core.DurationOf(nap)) else {
            return
        }
        end := if at + step < b.Len() { at + step } else { b.Len() }
        c.WriteAll(io, b[at..end]) else {
            return
        }
        at = end
    }
}

test "Recv is repeatable: cancelled at every point of a masked message, one frame or three, short or long, the next Recv delivers it whole" (sys: &System) {
    o := DefaultOptions
    data := try Filled(sys.arena, 20000)
    for frag in [false, true] {
        for size in [5, 300, 20000] {
            // The frames a client sends: masked with key 1 2 3 4.
            key: [4]u8 = [1, 2, 3, 4]
            parts := if frag { 3 } else { 1 }
            step := (size + parts - 1) / parts
            var at := 0
            buf := try sys.arena.Slice[u8](size + 14 * parts)
            var n := 0
            for p in 0..parts {
                end := if p == parts - 1 { size } else { at + step }
                op: u8 = if p == 0 { OpBinary } else { OpContinue }
                var hd: [14]u8 = [0; 14]
                hn := PutHead(mut hd[..], op, end - at, true, key)
                if p != parts - 1 {
                    hd[0] = hd[0] & 0x7F
                }
                (mut buf[n..n + hn]).CopyFrom(hd[0..hn])
                n += hn
                (mut buf[n..n + end - at]).CopyFrom(data[at..end])
                _ = Mask(mut buf[n..n + end - at], key, 0)
                n += end - at
                at = end
            }
            // Feed's pieces: the last lands at pieces ms, so a cancel at
            // cut.5 ms interrupts the Recv exactly when cut < pieces.
            piece := (n + 9) / 10
            pieces := (n + piece - 1) / piece
            for cut in [1, 2, 3, 5, 9, 11] {
                (got, cuts) := try CutRecv(sys, &o, buf[0..n], cut)
                Assert(got == size)
                want := if cut < pieces { 1 } else { 0 }
                Assert(cuts == want)
            }
        }
    }
}

// CutRecv feeds b from a raw peer in ten pieces 1 ms apart to a server
// whose Recv a task cancels cut.5 ms in, then calls Recv again: the
// length delivered and the number of interruptions.
CutRecv(sys: &System, o: &Options, b: []u8, cut: int): Error!(got: int, cuts: int) {
    var sm := try sim.Sim.New(sys.arena, Cast[u64](cut))
    defer sm.Free()
    io := sm.Io()
    (ws: sv, raw: peer) := try RawPair(sys.heap, io, false, o)
    onerr sv.Abandon()
    onerr peer.Abandon()
    var cuts := 0
    var got := -1
    scope outer := io.Scope() {
        outer.go Feed(&peer, io, b, 10, 1_000_000)
        got = OneCut(&sv, sys.arena, io, Cast[i64](cut) * 1_000_000 + 500_000)
        if got < 0 {
            cuts = 1
            m2 := try sv.Recv(sys.arena, io)
            got = m2.data.Len()
            Assert(IsPattern(m2.data))
        }
    }
    sv.Abandon()
    peer.Abandon()
    return (got: got, cuts: cuts)
}

// OneCut is one Recv of c under a scope a task cancels at nap ns: the
// message's length, or -1 when it was Canceled.
OneCut(c: &Conn, a: &Arena, io: Io, nap: i64): int {
    var got := -1
    scope inner := io.Scope() {
        inner.go CancelAt(&inner, io, nap)
        m := c.Recv(a, io) else e {
            Assert(CodeOf(e) == 3)
            return -1
        }
        Assert(IsPattern(m.data))
        got = m.data.Len()
        inner.Cancel()
    }
    return got
}

test "pings beyond pending_pongs while another task holds the write lock fail the connection with 1008; one owed pong goes out before the next frame" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 20)
    defer sm.Free()
    io := sm.Io()
    var o := DefaultOptions
    o.pending_pongs = 2
    (ws: sv, raw: peer) := try RawPair(sys.heap, io, false, &o)
    onerr sv.Abandon()
    onerr peer.Abandon()
    ping: [6]u8 = [0x89, 0x80, 0, 0, 0, 0]
    text: [7]u8 = [0x81, 0x81, 0, 0, 0, 0, 0x41]
    hv := sv.box.Ref()
    try hv.wl.Acquire(io, 1)
    try peer.WriteAll(io, ping[..])
    try peer.WriteAll(io, text[..])
    m := try sv.Recv(sys.arena, io)
    Assert(SameBytes(m.data, "A".AsBytes()))
    hv.wl.Release(1)
    // The next writer sends the owed pong first.
    try sv.Send(io, Kind.Text, "B".AsBytes())
    var got: [5]u8 = [0; 5]
    try ReadN(&peer, io, mut got[..], 5)
    Assert(got[0] == 0x8A && got[1] == 0 && got[2] == 0x81 && got[3] == 1 && got[4] == 0x42)
    try hv.wl.Acquire(io, 1)
    for _ in 0..3 {
        try peer.WriteAll(io, ping[..])
    }
    Assert(RecvCode(&sv, sys.arena, io) == 1008)
    hv.wl.Release(1)
    sv.Abandon()
    peer.Abandon()
}

// Hammer receives n messages on c, each Pattern of its length, and adds
// to bad each that is not.
Hammer(c: &Conn, h: &Heap, io: Io, n: int, bad: &Mutex[int]) {
    var a := Arena.New(h) else {
        return
    }
    for _ in 0..n {
        m := c.Recv(&a, io) else {
            var g := bad.Lock(io) else {
                a.Free()
                return
            }
            *g.Mut() += 1000
            g.Unlock()
            a.Free()
            return
        }
        if !IsPattern(m.data) {
            var g := bad.Lock(io) else {
                a.Free()
                return
            }
            *g.Mut() += 1
            g.Unlock()
        }
    }
    a.Free()
}

test "eight tasks sending at once never interleave their frames, while one task receives" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 21)
    defer sm.Free()
    io := sm.Io()
    o := DefaultOptions
    (cl, sv) := try WsPair(sys.heap, io, &o)
    onerr cl.Abandon()
    onerr sv.Abandon()
    data := try Filled(sys.arena, 40000)
    bad := Mutex[int].New(0)
    scope s := io.Scope() {
        s.go Hammer(&sv, sys.heap, io, 8 * 20, &bad)
        for t in 0..8 {
            s.go SendTask(&cl, io, Kind.Binary, data[0..1000 + t * 4000], 20)
        }
    }
    g := try bad.Lock(io)
    Assert(*g.Ref() == 0)
    g.Unlock()
    sv.Abandon()
    cl.Abandon()
}

// RawServer accepts one connection on l, reads the request head, answers
// with reply (a 101 with the right accept value when reply is empty) and
// then a text frame "hi".
RawServer(l: &net.Listener, io: Io, reply: str) {
    st := l.Accept(io) else {
        return
    }
    c := st.Conn()
    var buf: [2048]u8 = [0; 2048]
    var n := 0
    for {
        k := c.Read(io, mut buf[n..]) else {
            c.Abandon()
            return
        }
        if k == 0 {
            c.Abandon()
            return
        }
        n += k
        if HeadEnd(buf[0..n], 0).IsSome() {
            break
        }
    }
    head := buf[0..n]
    if reply.Len() > 0 {
        c.WriteAll(io, reply.AsBytes()) else {}
        c.Abandon()
        return
    }
    // The key is the value after "Sec-WebSocket-Key: ".
    var key: [24]u8 = [0; 24]
    tag := "Sec-WebSocket-Key: ".AsBytes()
    for i in 0..n - tag.Len() {
        if SameBytes(head[i..i + tag.Len()], tag) {
            (mut key[..]).CopyFrom(head[i + tag.Len()..i + tag.Len() + 24])
        }
    }
    var acc: [28]u8 = [0; 28]
    AcceptOf(key[..], mut acc)
    c.WriteAll(io, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ".AsBytes()) else {}
    c.WriteAll(io, acc[..]) else {}
    frame: [8]u8 = [0x0D, 0x0A, 0x0D, 0x0A, 0x81, 0x02, 0x68, 0x69]
    c.WriteAll(io, frame[..]) else {}
    // Wait for the client's close.
    var tail: [64]u8 = [0; 64]
    _ = c.Read(io, mut tail[..]) else { 0 }
    c.Abandon()
}

// DialOnce dials u against a RawServer answering reply: -1 when the
// connection worked and delivered the server's frame, else the Handshake
// status, -2 for another error.
DialOnce(sys: &System, io: Io, u: &url.Url, o: &Options, reply: str): Error!int {
    l := try net.Listen(io, Addr)
    defer l.Close()
    var code := -1
    scope s := io.Scope() {
        s.go RawServer(&l, io, reply)
        c := Dial(sys.heap, sys.arena, io, u, o) else e {
            code = match e {
                Handshake(status) { status }
                _ { -2 }
            }
            return code
        }
        m := c.Recv(sys.arena, io) else e {
            c.Abandon()
            fail e
        }
        Assert(SameBytes(m.data, "hi".AsBytes()))
        c.Abandon()
    }
    return code
}

test "Dial: the handshake with a raw server, the frame sent right after its 101 kept; ws only, wss needs a connector, a refused or broken reply is Handshake" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 22)
    defer sm.Free()
    io := sm.Io()
    o := DefaultOptions
    u := try url.Parse(sys.arena, "ws://127.0.0.1:7600/chat?room=1", none)
    for reply in ["", "HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n", "HTTP/1.1 101 OK\r\nUpgrade: websocket\r\n\r\n"] {
        code := try DialOnce(sys, io, &u, &o, reply)
        want := if reply.Len() == 0 { -1 } else if reply.StartsWith("HTTP/1.1 403") { 403 } else { 101 }
        Assert(code == want)
    }
    w := try url.Parse(sys.arena, "wss://127.0.0.1:7600/", none)
    Assert(DialFails(sys, io, &w, &o) == 1)
    h := try url.Parse(sys.arena, "http://127.0.0.1:7600/", none)
    Assert(DialFails(sys, io, &h, &o) == 2)
}

// Slow is a transport whose writes take 10 ms of the Io's time and whose
// reads wait 100 s: an interruption lands inside the write.
linear view struct Slow {
    tag: u8
}

impl net.Transport for Slow {
    Read(&self, io: Io, buf: mut []u8): net.Error!int {
        try io.Sleep(time.Seconds(100))
        return 0
    }
    WriteAll(&self, io: Io, data: []u8): net.Error!void {
        try io.Sleep(time.Millis(10))
    }
    Shutdown(&self, io: Io, how: net.Shutdown): net.Error!void {
    }
    PeerAddr(&self): ?net.Addr {
        return none
    }
    Close(&self, io: Io): net.Error!void {
    }
    Abandon(self) {
        Slow(tag: _) := self
    }
}

// SlowConn is a net.Conn over Slow on h.
SlowConn(h: &Heap): OutOfMemory!net.Conn from h {
    t := Slow(tag: 0)
    return net.NewConn(h, t) else e {
        t.Abandon()
        fail e
    }
}

// SendCut sends on c under a scope a task cancels 5 ms in: the CodeOf its
// error, -1 when it returned.
SendCut(c: &Conn, io: Io): int {
    var code := -1
    scope s := io.Scope() {
        s.go CancelAt(&s, io, 5_000_000)
        c.Send(io, Kind.Binary, "x".AsBytes()) else e {
            code = CodeOf(e)
        }
    }
    return code
}

test "a Send interrupted inside its write fails the connection: Canceled, then every later call reports it, and Abandon frees all" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 40)
    defer sm.Free()
    io := sm.Io()
    o := DefaultOptions
    for client in [true, false] {
        c := try SlowConn(sys.heap)
        ws := Wrap(sys.heap, c, client, &o, "".AsBytes()) else e {
            c.Abandon()
            fail e
        }
        Assert(SendCut(&ws, io) == 3)
        Assert(RecvCode(&ws, sys.arena, io) == 3)
        var later := -1
        ws.Ping(io, "p".AsBytes()) else e {
            later = CodeOf(e)
        }
        Assert(later == 3)
        ws.Abandon()
    }
}

// SilentServer accepts one connection on l, reads what comes, and never
// answers.
SilentServer(l: &net.Listener, io: Io) {
    st := l.Accept(io) else {
        return
    }
    c := st.Conn()
    var buf: [2048]u8 = [0; 2048]
    for {
        k := c.Read(io, mut buf[..]) else {
            break
        }
        if k == 0 {
            break
        }
    }
    c.Abandon()
}

// DialCut dials u against a SilentServer under a scope a task cancels 5
// ms in: the CodeOf Dial's error, -1 when it returned a Conn.
DialCut(sys: &System, io: Io, u: &url.Url, o: &Options): Error!int {
    l := try net.Listen(io, Addr)
    defer l.Close()
    var code := -1
    scope outer := io.Scope() {
        outer.go SilentServer(&l, io)
        scope s := io.Scope() {
            s.go CancelAt(&s, io, 5_000_000)
            c := Dial(sys.heap, sys.arena, io, u, o) else e {
                code = CodeOf(e)
                return code
            }
            c.Abandon()
        }
        outer.Cancel()
    }
    return code
}

// AcceptCut accepts RFC 6455 1.3's request over a Taken of a Slow
// connection under a scope a task cancels 5 ms in, inside the 101's
// write: the CodeOf Accept's error, the Taken then abandoned by the
// caller, as it is still the caller's.
AcceptCut(sys: &System, io: Io, req: &http.Request): Error!int {
    c := try SlowConn(sys.heap)
    t := http.Taken(conn: c, rest: "".AsBytes())
    var code := -1
    scope s := io.Scope() {
        s.go CancelAt(&s, io, 5_000_000)
        ws := Accept(sys.heap, io, req, t, DefaultOptions.protocols, AllowAll) else e {
            code = CodeOf(e)
            t.Abandon()
            return code
        }
        ws.Abandon()
    }
    return code
}

test "Dial and Accept interrupted mid-handshake fail with Canceled and leave nothing behind: Dial releases what it made, Accept leaves the Taken to its caller" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 41)
    defer sm.Free()
    io := sm.Io()
    o := DefaultOptions
    u := try url.Parse(sys.arena, "ws://127.0.0.1:7600/chat", none)
    Assert(try DialCut(sys, io, &u, &o) == 3)
    hs := try Upgrade(sys.arena, "13")
    req := http.Request(method: "GET", path: "/chat", query: "", headers: hs, params: hs[0..0], body: "".AsBytes())
    Assert(try AcceptCut(sys, io, &req) == 3)
}

// FeedTo writes b to c up to each of ends in turn, with a pause of nap ns
// before each piece, so the reader sees the pieces apart.
FeedTo(c: &net.Conn, io: Io, b: []u8, ends: []int, nap: i64) {
    var at := 0
    for end in ends {
        io.Sleep(core.DurationOf(nap)) else {
            return
        }
        c.WriteAll(io, b[at..end]) else {
            return
        }
        at = end
    }
}

// SplitRecv sends one unmasked binary frame of n Pattern bytes from a raw
// server to a client in the pieces ends gives (offsets into the frame,
// its header included), and returns RecvLen of its Recv, given a second
// of simulated time.
SplitRecv(sys: &System, n: int, ends: []int): Error!int {
    var sm := try sim.Sim.New(sys.arena, 7)
    defer sm.Free()
    io := sm.Io()
    o := DefaultOptions
    var hd: [14]u8 = [0; 14]
    hn := PutHead(mut hd[..], OpBinary, n, false, [0, 0, 0, 0])
    frame := try sys.arena.Slice[u8](hn + n)
    (mut frame[0..hn]).CopyFrom(hd[0..hn])
    for i in 0..n {
        frame[hn + i] = Pattern(i)
    }
    (ws: cl, raw: peer) := try RawPair(sys.heap, io, true, &o)
    var got := -2
    scope s := io.Scope() {
        s.go FeedTo(&peer, io, frame[..], ends, 1000000)
        scope t := io.Scope() {
            t.CancelAfter(time.Seconds(1))
            got = RecvLen(&cl, sys.arena, io)
        }
    }
    cl.Abandon()
    peer.Abandon()
    return got
}

// RecvLen is the length of the message c's next Recv delivers, -1 when its
// bytes are not the pattern, -2 when Recv fails.
RecvLen(c: &Conn, a: &Arena, io: Io): int {
    m := c.Recv(a, io) else {
        return -2
    }
    return if IsPattern(m.data) { m.data.Len() } else { -1 }
}

test "A one-frame message past 64 KiB arrives whole however its bytes are split: the read-ahead is emptied before any read, so a Fill that brought the rest is not followed by one that waits for more, and no direct read overtakes it" (sys: &System) {
    // 70,000 bytes: the header and 1,000 bytes, then up to 63,702 or
    // 65,150 (the reads seen on x86_64-linux), then the rest, which a Fill
    // takes whole while the 64 KiB reservation has room for less.
    for cut in [63712, 65160, 65545, 65000, 57355] {
        ends: [3]int = [1010, cut, 70010]
        Assert(try SplitRecv(sys, 70000, ends[..]) == 70000)
    }
    // 200,000 bytes: after the reservation grows the room is past the
    // direct-read size, while the read-ahead still holds bytes.
    for cut in [63712, 65160, 65545] {
        ends: [4]int = [1010, cut, 70010, 200010]
        Assert(try SplitRecv(sys, 200000, ends[..]) == 200000)
    }
    // Pieces of 7,000 bytes: Fill and direct reads alternate.
    var many: [29]int = [0; 29]
    for i in 0..28 {
        many[i] = 10 + 7000 * (i + 1)
    }
    many[28] = 200010
    Assert(try SplitRecv(sys, 200000, many[..]) == 200000)
}
