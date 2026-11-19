// Why: net.Conn's locks, boxing and close rules are what std.tls and
// std.websocket build on; they must hold under many tasks.
// What: test blocks for net.Conn, net.Transport, net.NewConn, ConnSlot and
// Stream.Conn: an in-memory transport shared by many tasks, a stream's Conn
// used by two tasks, the peer's address, a Close that speaks to the peer,
// a layered transport, and a cancelled Read that loses nothing.
// How: run by mk test --std=std.net and by std/std-net_connblocks_test.go at 1
// and 16 carriers with the counting heap.

import std.core
import std.raw
import std.time

// Tally is an in-memory transport: WriteAll adds data's length to the
// total, Read reports the total in 8 bytes. Each yields inside its
// critical part and counts a call that found another of its kind inside,
// which the Conn's locks must make impossible.
linear view struct Tally {
    total: Mutex[int]
    in_read: raw.Atomic[u32]
    in_write: raw.Atomic[u32]
    overlaps: &raw.Atomic[u32]
}

NewTally(overlaps: &raw.Atomic[u32]): Tally from overlaps {
    return Tally(total: Mutex[int].New(0), in_read: raw.Atomic[u32].New(0), in_write: raw.Atomic[u32].New(0),
        overlaps: overlaps)
}

// Enter marks one call of a kind inside; a second is an overlap.
Enter(inside: &raw.Atomic[u32], overlaps: &raw.Atomic[u32]) {
    if !inside.Cas(0, 1) {
        _ = overlaps.Add(1)
    }
}

impl Transport for Tally {
    Read(&self, io: Io, buf: mut []u8): Error!int {
        Enter(&self.in_read, self.overlaps)
        defer self.in_read.Store(0)
        io.Yield() else {
            fail Canceled
        }
        g := try self.total.Lock(io)
        n := *g.Ref()
        g.Unlock()
        for i in 0..8 {
            buf[i] = Trunc[u8](n >> (8 * i))
        }
        return 8
    }
    WriteAll(&self, io: Io, data: []u8): Error!void {
        Enter(&self.in_write, self.overlaps)
        defer self.in_write.Store(0)
        io.Yield() else {
            fail Canceled
        }
        var g := try self.total.Lock(io)
        *g.Mut() += data.Len()
        g.Unlock()
    }
    Shutdown(&self, io: Io, how: Shutdown): Error!void {
    }
    PeerAddr(&self): ?Addr {
        return none
    }
    Close(&self, io: Io): Error!void {
    }
    Abandon(self) {
        Tally(total, in_read: _, in_write: _, overlaps: _) := self
        _ = total.Into()
    }
}

// TallyHammer writes and reads c 50 times, directly and through c's own
// Writer and Reader.
TallyHammer(io: Io, c: &Conn, bad: &raw.Atomic[u32]) {
    var wb: [4]u8 = [0; 4]
    var rb: [16]u8 = [0; 16]
    for _ in 0..50 {
        c.WriteAll(io, "abcde".AsBytes()) else {
            _ = bad.Add(1)
            return
        }
        var got: [8]u8 = [0; 8]
        n := c.Read(io, mut got[..]) else {
            _ = bad.Add(1)
            return
        }
        if n != 8 {
            _ = bad.Add(1)
        }
        var w := c.Writer(io, mut wb[..])
        w.Str("xyz") else {
            _ = bad.Add(1)
            return
        }
        w.Flush() else {
            _ = bad.Add(1)
            return
        }
        var r := c.Reader(io, mut rb[..])
        k := r.Read(mut got[..]) else {
            _ = bad.Add(1)
            return
        }
        if k != 8 {
            _ = bad.Add(1)
        }
    }
}

// TallyTotal reads c's total.
TallyTotal(c: &Conn, io: Io): Error!int {
    var buf: [8]u8 = [0; 8]
    n := try c.Read(io, mut buf[..])
    Assert(n == 8)
    var total := 0
    for i in 0..8 {
        total |= int(buf[i]) << (8 * i)
    }
    return total
}

test "A Conn over an in-memory transport serialises its Reads and its WriteAlls across 16 tasks and frees its box" (sys: &System) {
    overlaps := raw.Atomic[u32].New(0)
    t := NewTally(&overlaps)
    c := NewConn(sys.heap, t) else e {
        t.Abandon()
        fail e
    }
    onerr c.Abandon()
    bad := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..16 {
            s.go TallyHammer(sys.io, &c, &bad)
        }
    }
    Assert(bad.Load() == 0)
    Assert(overlaps.Load() == 0)
    Assert(try TallyTotal(&c, sys.io) == 16 * 50 * 8)
    Assert(c.PeerAddr().IsNone())
    try c.Shutdown(sys.io, Shutdown.Write)
    try c.Close(sys.io)
}

test "A Conn over a transport that refuses its Close can be abandoned" (sys: &System) {
    // NewConn keeps t when it fails, and Abandon of the Conn reaches the
    // transport's Abandon: the counting heap finds no leak either way.
    overlaps := raw.Atomic[u32].New(0)
    t := NewTally(&overlaps)
    c := NewConn(sys.heap, t) else e {
        t.Abandon()
        fail e
    }
    c.Abandon()
}

// Polite is a transport whose Close speaks to the peer: it waits for the
// peer's answer (acked) for at most limit ns, and reports TimedOut past it
// and Canceled when the caller's scope is cancelled first, as a TLS or
// WebSocket close does.
linear view struct Polite {
    acked: &raw.Atomic[u32]
    limit: i64
}

impl Transport for Polite {
    Read(&self, io: Io, buf: mut []u8): Error!int {
        return 0
    }
    WriteAll(&self, io: Io, data: []u8): Error!void {
    }
    Shutdown(&self, io: Io, how: Shutdown): Error!void {
    }
    PeerAddr(&self): ?Addr {
        return none
    }
    Close(&self, io: Io): Error!void {
        var acked := false
        var expired := false
        scope s := io.Scope() {
            s.CancelAfter(core.DurationOf(self.limit))
            for {
                if self.acked.Load() == 1 {
                    acked = true
                    break
                }
                io.Sleep(core.DurationOf(1_000_000)) else {
                    break
                }
            }
            expired = s.Expired()
        }
        if acked {
            return
        }
        if expired {
            fail time.TimedOut
        }
        fail Canceled
    }
    Abandon(self) {
        Polite(acked: _, limit: _) := self
    }
}

// ErrorName is e's member name, for the blocks' asserts.
ErrorName(e: Error): str {
    return match e {
        Canceled { "Canceled" }
        time.TimedOut { "TimedOut" }
        Unsupported { "Unsupported" }
        InvalidAddress { "InvalidAddress" }
        AddrInUse { "AddrInUse" }
        Refused { "Refused" }
        _ { "other" }
    }
}

test "Conn.Close over a transport that speaks to the peer is cancellable and bounded, and keeps the Conn on failure" (sys: &System) {
    acked := raw.Atomic[u32].New(0)
    p := Polite(acked: &acked, limit: 20_000_000)
    c := NewConn(sys.heap, p) else e {
        p.Abandon()
        fail e
    }
    scope s := sys.io.Scope() {
        s.Cancel()
        // A cancelled scope: Canceled, and c is still the caller's.
        c.Close(sys.io) else e {
            Assert(ErrorName(e) == "Canceled")
            // Masked from the cancel, the close limit still holds.
            c.Close(sys.io.NoCancel()) else e2 {
                Assert(ErrorName(e2) == "TimedOut")
                Assert(c.PeerAddr().IsNone())
                // Once the peer answers, Close succeeds and frees the box.
                acked.Store(1)
                c.Close(sys.io.NoCancel()) else {
                    Panic("a Close the peer answered failed")
                }
                return
            }
            Panic("a Close past its limit succeeded")
        }
        Panic("a Close under a cancelled scope succeeded")
    }
}

// SlotRun reserves twice on h: it frees the first slot unused, and fills
// the second with a Tally over a local. (The heap is a parameter: a slot
// reserved on sys.heap cannot take a transport that borrows a local, since
// Fill stores into the slot.)
SlotRun(h: &Heap, io: Io): Error|OutOfMemory!void {
    unused := try ReserveConn[Tally](h)
    unused.Free(h.Alloc())
    overlaps := raw.Atomic[u32].New(0)
    t := NewTally(&overlaps)
    slot := ReserveConn[Tally](h) else e {
        t.Abandon()
        fail e
    }
    c := slot.Fill(t)
    onerr c.Abandon()
    try c.WriteAll(io, "abc".AsBytes())
    Assert(try TallyTotal(&c, io) == 3)
    Assert(c.PeerAddr().IsNone())
    try c.Close(io)
}

test "ReserveConn then Free gives the box back, and a filled slot is a Conn that runs and frees it" (sys: &System) {
    // The counting heap finds a leak if Free or Close keeps the box.
    try SlotRun(sys.heap, sys.io)
}

// Shout is a transport over another Conn that upper-cases what it reads:
// a layer over a connection, as a TLS connection is over its stream.
linear view struct Shout {
    inner: Conn
}

impl Transport for Shout {
    Read(&self, io: Io, buf: mut []u8): Error!int {
        n := try self.inner.Read(io, buf)
        for i in 0..n {
            if buf[i] >= 0x61 && buf[i] <= 0x7a {
                buf[i] -= 0x20
            }
        }
        return n
    }
    WriteAll(&self, io: Io, data: []u8): Error!void {
        try self.inner.WriteAll(io, data)
    }
    Shutdown(&self, io: Io, how: Shutdown): Error!void {
        try self.inner.Shutdown(io, how)
    }
    PeerAddr(&self): ?Addr {
        return self.inner.PeerAddr()
    }
    // The inner Conn is a stream's, whose Close only releases.
    Close(&self, io: Io): Error!void {
    }
    Abandon(self) {
        Shout(inner) := self
        inner.Abandon()
    }
}

// Layer takes conn and wraps it in a Shout, or refuses and leaves conn
// with its caller, as a connector does: it reserves first and fills
// last, so nothing can fail after conn moves (E0411 with NewConn).
Layer(h: &Heap, io: Io, conn: Conn, refuse: bool): Error|OutOfMemory!Conn from h, conn {
    slot := try ReserveConn[Shout](h)
    if refuse {
        slot.Free(h.Alloc())
        fail SecureFailed(why: SecureFailure.Handshake)
    }
    return slot.Fill(Shout(inner: conn))
}

test "A function that takes a Conn wraps it through ReserveConn and Fill, or fails and leaves it with its caller" (sys: &System) {
    l := TnListen(sys.io)
    defer l.Close()
    (c, s) := try TnPair(sys.io, &l)
    defer s.Close()
    peer := c.PeerAddr()
    conn := c.Conn()
    // The refusal leaves conn usable by its caller.
    w := Layer(sys.heap, sys.io, conn, true) else e {
        onerr conn.Abandon()
        Assert(match e {
            SecureFailed(why: _) { true }
            _ { false }
        })
        try conn.WriteAll(sys.io, "x".AsBytes())
        var one: [1]u8 = [0; 1]
        Assert(try s.Read(sys.io, mut one[..]) == 1 && one[0] == 0x78)
        // Then the wrap succeeds, and the layer reads through conn.
        up := Layer(sys.heap, sys.io, conn, false) else e2 {
            conn.Abandon()
            fail e2
        }
        onerr up.Abandon()
        p := up.PeerAddr() else {
            Panic("the layer lost the stream's peer")
        }
        Assert(SameAddr(p, peer))
        try s.WriteAll(sys.io, "hello".AsBytes())
        var buf: [8]u8 = [0; 8]
        var n := 0
        for n < 5 {
            n += try up.Read(sys.io, mut buf[n..])
        }
        Assert(buf[0] == 0x48 && buf[4] == 0x4f)
        try up.WriteAll(sys.io, "ok".AsBytes())
        Assert(try s.Read(sys.io, mut buf[..]) == 2 && buf[0] == 0x6f)
        try up.Close(sys.io)
        return
    }
    w.Abandon()
    Panic("a refusing layer succeeded")
}
