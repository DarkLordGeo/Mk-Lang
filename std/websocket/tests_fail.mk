// Why: once a connection fails, no task may write after this side's Close
// frame or a partial frame, and no task may read more from the peer.
// What: tests of the failed state across tasks, Close's transport close, and
// TooLarge sending status 1009 before any payload byte is read.
// How: runs under std.sim with tasks blocked on the locks and the transport;
// run by mk test --std=std.websocket and std/std-ws_blocks_test.go.

import std.core
import std.net
import std.sim
import std.time

// Tape is what a Tap saw: its WriteAll calls, the data and Ping frames
// among them (each a WriteAll of its own here), those after a Close
// frame, and the transport's Close calls.
struct Tape {
    writes: int
    frames: int
    after_close: int
    closed: bool
    closes: int
}

// NewTape is an empty Tape in a Mutex.
NewTape(): Mutex[Tape] {
    return Mutex[Tape].New(Tape(writes: 0, frames: 0, after_close: 0, closed: false, closes: 0))
}

// Tap is a transport over inner that records each WriteAll in tape by
// the opcode of the frame it starts. With cut, its first WriteAll sends
// half and waits 100 s, so a cancel lands inside it; with no_pong, a
// WriteAll of a Pong fails with net.Reset and sends nothing. A Read
// returns once it holds at least gather bytes (or the buffer is full, or
// the stream ended), so frames a peer wrote together are read together.
// With stall, its Close waits 100 s, so only a cancel or a limit ends it.
linear view struct Tap {
    inner: net.Conn
    tape: &Mutex[Tape]
    cut: bool
    no_pong: bool
    gather: int
    stall: bool
}

impl net.Transport for Tap {
    Read(&self, io: Io, buf: mut []u8): net.Error!int {
        var got := try self.inner.Read(io, buf)
        for got > 0 && got < self.gather && got < buf.Len() {
            k := try self.inner.Read(io, mut buf[got..])
            if k == 0 {
                break
            }
            got += k
        }
        return got
    }
    WriteAll(&self, io: Io, data: []u8): net.Error!void {
        has := data.Len() > 0
        op := if has { data[0] & 0x0F } else { 0 }
        if has && self.no_pong && op == OpPong {
            fail net.Reset
        }
        var g := try self.tape.Lock(io.NoCancel())
        g.Mut().writes += 1
        n := g.Ref().writes
        if has && op == OpClose {
            g.Mut().closed = true
        } else if has && (op < 8 || op == OpPing) {
            g.Mut().frames += 1
            if g.Ref().closed {
                g.Mut().after_close += 1
            }
        }
        g.Unlock()
        if self.cut && n == 1 {
            try self.inner.WriteAll(io, data[0..data.Len() / 2])
            try io.Sleep(time.Seconds(100))
        }
        try self.inner.WriteAll(io, data)
    }
    Shutdown(&self, io: Io, how: net.Shutdown): net.Error!void {
        try self.inner.Shutdown(io, how)
    }
    PeerAddr(&self): ?net.Addr {
        return none
    }
    // Counts the call, then stalls when asked; the inner Conn is a
    // stream's, which Abandon releases.
    Close(&self, io: Io): net.Error!void {
        var g := try self.tape.Lock(io.NoCancel())
        g.Mut().closes += 1
        g.Unlock()
        if self.stall {
            try io.Sleep(time.Seconds(100))
        }
    }
    Abandon(self) {
        Tap(inner, tape: _, cut: _, no_pong: _, gather: _, stall: _) := self
        inner.Abandon()
    }
}

// TapPair is a WebSocket connection over a Tap, client side or not, and
// the raw peer that speaks to it.
TapPair(h: &Heap, io: Io, client: bool, o: &Options, tape: &Mutex[Tape], cut: bool, no_pong: bool, gather: int, stall: bool): Error!(ws: Conn, raw: net.Conn) from h, io, tape {
    (c, s) := try Loop(io)
    slot := net.ReserveConn[Tap](h) else e {
        c.Abandon()
        s.Abandon()
        fail e
    }
    t := slot.Fill(Tap(inner: c, tape: tape, cut: cut, no_pong: no_pong, gather: gather, stall: stall))
    ws := Wrap(h, t, client, o, "".AsBytes()) else e {
        t.Abandon()
        s.Abandon()
        fail e
    }
    return (ws: ws, raw: s)
}

// Note stores v in out.
Note(out: &Mutex[int], io: Io, v: int) {
    var g := out.Lock(io.NoCancel()) else {
        return
    }
    *g.Mut() = v
    g.Unlock()
}

// Noted is the value in out.
Noted(out: &Mutex[int], io: Io): int {
    g := out.Lock(io.NoCancel()) else {
        return -9
    }
    v := *g.Ref()
    g.Unlock()
    return v
}

// Since is the ns of the Io's time from start.
Since(io: Io, start: time.Instant): int {
    return Cast[int](time.Now(io).Since(start).Nanos())
}

// SendNote sends "x" on c after nap ns, then notes in code CodeOf its
// error (-1 when it returned) and in at when it returned (Since start).
SendNote(c: &Conn, io: Io, nap: i64, start: time.Instant, code: &Mutex[int], at: &Mutex[int]) {
    if nap > 0 {
        io.Sleep(core.DurationOf(nap)) else {
            return
        }
    }
    var got := -1
    c.Send(io, Kind.Binary, "x".AsBytes()) else e {
        got = CodeOf(e)
    }
    Note(code, io, got)
    Note(at, io, Since(io, start))
}

// RecvNote receives on c into an arena of its own, then notes in code
// RecvCode's result and in at when Recv returned (Since start).
RecvNote(c: &Conn, h: &Heap, io: Io, start: time.Instant, code: &Mutex[int], at: &Mutex[int]) {
    var a := Arena.New(h) else {
        return
    }
    got := RecvCode(c, &a, io)
    a.Free()
    Note(code, io, got)
    Note(at, io, Since(io, start))
}

test "a Send waiting for the write lock when the peer's Close arrives sends nothing after the echo and fails with PeerClosed (RFC 6455 5.5.1)" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 60)
    defer sm.Free()
    io := sm.Io()
    o := DefaultOptions
    tape := NewTape()
    (ws: sv, raw: peer) := try TapPair(sys.heap, io, false, &o, &tape, false, false, 0, false)
    onerr sv.Abandon()
    onerr peer.Abandon()
    hv := sv.box.Ref()
    send := Mutex[int].New(-9)
    sat := Mutex[int].New(0)
    recv := Mutex[int].New(-9)
    rat := Mutex[int].New(0)
    start := time.Now(io)
    // The test holds wl: the Send passes its first look at the fault and
    // waits; then the peer's Close arrives, and Recv's echo waits too.
    try hv.wl.Acquire(io, 1)
    scope s := io.Scope() {
        s.go SendNote(&sv, io, 0, start, &send, &sat)
        try io.Sleep(time.Millis(1))
        close: [8]u8 = [0x88, 0x82, 0, 0, 0, 0, 0x03, 0xE8]
        try peer.WriteAll(io, close[..])
        s.go RecvNote(&sv, sys.heap, io, start, &recv, &rat)
        try io.Sleep(time.Millis(2))
        hv.wl.Release(1)
    }
    Assert(Noted(&recv, io) == 1000)
    Assert(Noted(&send, io) == 1000)
    g := try tape.Lock(io)
    Assert(g.Ref().frames == 0 && g.Ref().after_close == 0 && g.Ref().closed)
    g.Unlock()
    var echo: [4]u8 = [0; 4]
    try ReadN(&peer, io, mut echo[..], 4)
    Assert(echo[0] == 0x88 && echo[1] == 0x02 && echo[2] == 0x03 && echo[3] == 0xE8)
    sv.Abandon()
    peer.Abandon()
}

test "a Recv waiting for the read lock when the first fails reads nothing more: invalid UTF-8 and a valid frame in one write fail both Recvs with 1007 (RFC 6455 7.1.7)" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 61)
    defer sm.Free()
    io := sm.Io()
    o := DefaultOptions
    tape := NewTape()
    (ws: sv, raw: peer) := try TapPair(sys.heap, io, false, &o, &tape, false, false, 16, false)
    onerr sv.Abandon()
    onerr peer.Abandon()
    r1 := Mutex[int].New(-9)
    a1 := Mutex[int].New(0)
    r2 := Mutex[int].New(-9)
    a2 := Mutex[int].New(0)
    start := time.Now(io)
    // C0 80 as text, then the binary "ok", masked with a zero key.
    both: [16]u8 = [0x81, 0x82, 0, 0, 0, 0, 0xC0, 0x80, 0x82, 0x82, 0, 0, 0, 0, 0x6F, 0x6B]
    // One Recv waits in its read, the other for rd; the Tap reads both
    // frames together, so the second waits in the read-ahead when the
    // first Recv fails.
    scope s := io.Scope() {
        s.go RecvNote(&sv, sys.heap, io, start, &r1, &a1)
        s.go RecvNote(&sv, sys.heap, io, start, &r2, &a2)
        try io.Sleep(time.Millis(1))
        try peer.WriteAll(io, both[..])
    }
    Assert(Noted(&r1, io) == 1007)
    Assert(Noted(&r2, io) == 1007)
    sv.Abandon()
    peer.Abandon()
}

test "a Recv whose pong cannot be written fails with that error and does not deliver the message read with the Ping" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 62)
    defer sm.Free()
    io := sm.Io()
    o := DefaultOptions
    tape := NewTape()
    (ws: sv, raw: peer) := try TapPair(sys.heap, io, false, &o, &tape, false, true, 0, false)
    onerr sv.Abandon()
    onerr peer.Abandon()
    // A Ping, then the binary "ok", in one write.
    both: [14]u8 = [0x89, 0x80, 0, 0, 0, 0, 0x82, 0x82, 0, 0, 0, 0, 0x6F, 0x6B]
    try peer.WriteAll(io, both[..])
    // net.Reset, the pong's error, is CodeOf 0.
    Assert(RecvCode(&sv, sys.arena, io) == 0)
    Assert(RecvCode(&sv, sys.arena, io) == 0)
    var later := -9
    sv.Send(io, Kind.Binary, "x".AsBytes()) else e {
        later = CodeOf(e)
    }
    Assert(later == 0)
    sv.Abandon()
    peer.Abandon()
}

// CutSend sends "x" on c under a scope a task cancels 5 ms in, inside the
// Tap's first write: CodeOf its error, -1 when it returned.
CutSend(c: &Conn, io: Io): int {
    var code := -1
    scope inner := io.Scope() {
        inner.go CancelAt(&inner, io, 5_000_000)
        c.Send(io, Kind.Binary, "x".AsBytes()) else e {
            code = CodeOf(e)
        }
    }
    return code
}

test "failed class across tasks: a Send cut inside its write fails the connection, and in other tasks a Send waiting for the write lock and a Recv blocked on the transport return the stored Canceled at once, the waiting Send writing nothing" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 63)
    defer sm.Free()
    io := sm.Io()
    o := DefaultOptions
    for client in [true, false] {
        tape := NewTape()
        (ws, raw: peer) := try TapPair(sys.heap, io, client, &o, &tape, true, false, 0, false)
        onerr ws.Abandon()
        onerr peer.Abandon()
        bc := Mutex[int].New(-9)
        bat := Mutex[int].New(0)
        rc := Mutex[int].New(-9)
        rat := Mutex[int].New(0)
        start := time.Now(io)
        var a := -9
        var aat := 0
        scope s := io.Scope() {
            // A Recv that does not return at once returns Canceled at 1 s,
            // which the time assertion below refuses.
            s.CancelAfter(time.Seconds(1))
            s.go RecvNote(&ws, sys.heap, io, start, &rc, &rat)
            s.go SendNote(&ws, io, 1_000_000, start, &bc, &bat)
            a = CutSend(&ws, io)
            aat = Since(io, start)
        }
        Assert(a == 3)
        Assert(Noted(&bc, io) == 3)
        Assert(Noted(&rc, io) == 3)
        Assert(Noted(&bat, io) - aat < 1_000_000)
        Assert(Noted(&rat, io) - aat < 1_000_000)
        g := try tape.Lock(io)
        Assert(g.Ref().writes == 1)
        g.Unlock()
        ws.Abandon()
        peer.Abandon()
    }
}

// EchoClose reads a client's Close frame of status 1000 and no reason
// from c, and answers with its own.
EchoClose(c: &net.Conn, io: Io) {
    var f: [8]u8 = [0; 8]
    ReadN(c, io, mut f[..], 8) else {
        return
    }
    reply: [4]u8 = [0x88, 0x02, 0x03, 0xE8]
    c.WriteAll(io, reply[..]) else {}
}

test "RFC 6455 7.1.1 Close ends the closing handshake, then closes the transport with its own Close, which over TLS sends close_notify (RFC 6455 7.1.1)" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 64)
    defer sm.Free()
    io := sm.Io()
    o := DefaultOptions
    tape := NewTape()
    (ws: cl, raw: peer) := try TapPair(sys.heap, io, true, &o, &tape, false, false, 0, false)
    onerr peer.Abandon()
    var closed := true
    scope s := io.Scope() {
        s.go EchoClose(&peer, io)
        cl.Close(io, 1000, "") else {
            cl.Abandon()
            closed = false
        }
    }
    Assert(closed)
    g := try tape.Lock(io)
    Assert(g.Ref().closes == 1)
    g.Unlock()
    peer.Abandon()
}

test "A transport Close that never ends stops at the close limit, also under NoCancel: the WebSocket close is done, so Close returns and the transport is abandoned, leaking nothing" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 65)
    defer sm.Free()
    io := sm.Io()
    var o := DefaultOptions
    o.close = time.Millis(200)
    for mode in 0..2 {
        tape := NewTape()
        (ws: cl, raw: peer) := try TapPair(sys.heap, io, true, &o, &tape, false, false, 0, true)
        onerr peer.Abandon()
        q := if mode == 1 { io.NoCancel() } else { io }
        start := time.Now(io)
        var closed := true
        scope s := io.Scope() {
            s.go EchoClose(&peer, io)
            cl.Close(q, 1000, "") else {
                cl.Abandon()
                closed = false
            }
        }
        took := Since(io, start)
        Assert(closed)
        // The handshake takes no simulated time beyond the network's
        // latency; the stalled transport Close ends at the 200 ms limit.
        Assert(took >= 150_000_000 && took <= 201_000_000)
        g := try tape.Lock(io)
        Assert(g.Ref().closes == 1)
        g.Unlock()
        peer.Abandon()
    }
}

// Oversize sends bad to a server with o's limits. Its Recv must fail with
// TooLarge having appended nothing to the message past its first msg
// bytes and consumed nothing of the failing frame, which starts at off in
// bad with a header of hl bytes: the read-ahead still starts with that
// header, so no payload byte was read and nothing was reserved for it.
// The server must send a Close frame of status 1009, and the next Recv
// must fail the same way.
Oversize(sys: &System, seed: u64, bad: []u8, o: &Options, msg: int, off: int, hl: int): Error!void {
    var sm := try sim.Sim.New(sys.arena, seed)
    defer sm.Free()
    io := sm.Io()
    (ws: sv, raw: peer) := try RawPair(sys.heap, io, false, o)
    onerr sv.Abandon()
    onerr peer.Abandon()
    try peer.WriteAll(io, bad)
    Assert(RecvCode(&sv, sys.arena, io) == 1)
    hv := sv.box.Ref()
    g := try hv.rd.Lock(io)
    Assert(g.Ref().msg.Len() == msg)
    r := g.Ref()
    Assert(r.have - r.start >= hl && r.buf[r.start] == bad[off] && r.buf[r.start + 1] == bad[off + 1])
    g.Unlock()
    var f: [4]u8 = [0; 4]
    try ReadN(&peer, io, mut f[..], 4)
    Assert(f[0] == 0x88 && f[1] == 0x02 && f[2] == 0x03 && f[3] == 0xF1)
    Assert(RecvCode(&sv, sys.arena, io) == 1)
    sv.Abandon()
    peer.Abandon()
}

test "TooLarge before anything is allocated or read: a frame over frame_bytes, a 64-bit length, and a second fragment over message_bytes after the first was appended, each with status 1009" (sys: &System) {
    var o := DefaultOptions
    o.frame_bytes = 10
    o.message_bytes = 12
    // 11 bytes announced and sent: the header and the 11 stay unread.
    var eleven: [17]u8 = [0; 17]
    eleven[0] = 0x82
    eleven[1] = 0x8B
    try Oversize(sys, 13, eleven[..], &o, 0, 0, 6)
    // two fragments of 8: the first is appended, the second passes the
    // frame limit and not the message limit, and stays unread
    var two: [28]u8 = [0; 28]
    two[0] = 0x02
    two[1] = 0x88
    two[14] = 0x80
    two[15] = 0x88
    try Oversize(sys, 14, two[..], &o, 8, 14, 6)
    // a 64-bit length near 2^63
    huge: [14]u8 = [0x82, 0xFF, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0, 0, 0, 0]
    try Oversize(sys, 15, huge[..], &o, 0, 0, 14)
}
