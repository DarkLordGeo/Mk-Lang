// Why: One client connection must read commands, write queued frames, and be
// ended by the server at any time.
//
// What: Ended and its errors (Quit, Eof, Broken, Dropped, Kicked, Closing,
// Idle), Me, Ctx, the Kicker task and the session loop.
//
// How: The session waits on its socket or on its slot's queues, whichever is
// ready first. A separate kicker task ends the connection on request, even
// while a write to a client that does not read is blocked.

import std.mem
import std.net
import std.sync
import std.time
import src.hub
import src.proto

/// Why a connection ended. Every connection ends with one of these: it is the
/// error of the per-client scope, whose first error cancels the other task.
export error Quit
export error Eof
export error Broken
export error Dropped
export error Kicked
export error Closing
export error Idle

/// Every reason a connection ends; `Canceled` when the whole server is cancelled.
export type Ended = Quit | Eof | Broken | Dropped | Kicked | Closing | Idle | Canceled

/// What a connection knows about itself.
struct Me {
    nick: proto.Name
    op: bool
    /// The rooms joined, kept in the client's own memory budget.
    joined: HeapList[HeapText]
}

Me.Release(self, a: &Alloc) {
    Me(joined, ..) := self
    joined.Free(a)
}

/// What every command handler needs and does not change.
struct Ctx {
    io: Io
    id: int
    arena: &Arena
    ca: &Alloc
    use: &mem.Counting
}

/// The kicker task: ends the connection when the server asks it to, even
/// while the session waits on a write to a client that does not read.
Kicker(io: Io, slot: &hub.Slot): Ended!void {
    c := slot.ctl.Recv(io) else {
        // cancelled: the session ended first
        return
    }
    match c {
        .Drop {
            fail Dropped
        }
        .Kick {
            fail Kicked
        }
    }
}

/// Wakes the session's read when a frame is queued for its client.
// One task owns the Stream, so a queued frame interrupts its read.
Watch(w: &Scope, io: Io, slot: &hub.Slot) {
    _ = sync.Select(io, (note: &slot.note, out: &slot.out)) else {
        return
    }
    w.Cancel()
}

/// Reads what the client sent into `lb`. Returns the bytes read, 0 at the end
/// of the stream, or -1 when a queued frame interrupted the wait. Fails with
/// the connection's error, `time.TimedOut` when `idle` passed with nothing
/// read or queued, or `Canceled` when the server is shutting down.
ReadOrWake(io: Io, conn: &net.Conn, slot: &hub.Slot, idle: ?time.Duration, lb: mut proto.LineBuf): net.Error!int {
    var got := -1
    var failed: ?net.Error = none
    scope w := io.Scope() {
        if d := idle {
            w.CancelAfter(d)
        }
        w.go Watch(&w, io, slot)
        got = conn.Read(io, lb.Space()) else e {
            match e {
                Canceled {
                    // the deadline is this scope's own, so it is read here
                    if w.Expired() {
                        failed = some(time.TimedOut)
                    }
                }
                _ {
                    failed = some(e)
                }
            }
            -1
        }
        w.Cancel()
    }
    if e := failed {
        fail e
    }
    // the inner cancel ended with its scope; a cancel still seen is the server's
    try io.Yield()
    return got
}

/// Writes every queued frame, notices first. Fails with `Closing` after
/// writing the server's last notice, and with `Broken` when the client
/// cannot be written to.
Drain(c: &Ctx, hb: &hub.Hub, w: mut Writer): Ended!void {
    slot := hb.Slot(c.id)
    var frames := 0
    var bytes := 0
    var closing := false
    for slot.note.Ready() {
        m := slot.note.Recv(c.io) else {
            break
        }
        f := m.Ref()
        closing = closing || f.kind == proto.Kind.Closing
        w.Bytes(f.Bytes()) else {
            m.Free(hb.heap)
            fail Broken
        }
        frames += 1
        bytes += f.Bytes().Len()
        m.Free(hb.heap)
    }
    for slot.out.Ready() {
        m := slot.out.Recv(c.io) else {
            break
        }
        f := m.Ref()
        w.Bytes(f.Bytes()) else {
            m.Free(hb.heap)
            slot.credits.Release(1)
            fail Broken
        }
        frames += 1
        bytes += f.Bytes().Len()
        m.Free(hb.heap)
        slot.credits.Release(1)
    }
    if frames > 0 {
        w.Flush() else {
            fail Broken
        }
        hb.stats.Wrote(frames, bytes)
    }
    if closing {
        fail Closing
    }
}

/// Serves one connection until it ends; always fails, with the reason. The
/// connection is handed over through `handoff`, since a fallible function
/// owns a by-value argument only once it succeeds (E0411), and a session
/// never succeeds.
Session(handoff: &Mutex[?net.Conn], io: Io, hb: &hub.Hub, id: int): Ended!void *hb into hb {
    var hg := handoff.Lock(io) else {
        fail Canceled
    }
    taken := Replace(hg.Mut(), none)
    hg.Unlock()
    conn := taken else {
        fail Broken
    }
    Converse(&conn, io, hb, id) else e {
        // at shutdown every client is told and closed at once: wait for each
        // to close its side, so none is reset before it read the notice
        linger := match e {
            Closing { true }
            _ { false }
        }
        Hangup(conn, io, linger)
        fail e
    }
    Hangup(conn, io, false)
    // a session only ends by failing, with its reason
    Panic("a session ended without a reason")
}

/// The session's work on `conn`, from the greeting until the connection
/// ends; always fails, with the reason.
Converse(conn: &net.Conn, io: Io, hb: &hub.Hub, id: int): Ended!void *hb into hb {
    var arena := Arena.New(hb.heap) else {
        fail Broken
    }
    defer arena.Free()
    budget := arena.Slice[u8](hb.cfg.client_memory) else {
        fail Broken
    }
    fixed := mem.Fixed.New(budget)
    counting := mem.Counting.New(fixed.Alloc())
    c := Ctx(io: io, id: id, arena: &arena, ca: counting.Alloc(), use: &counting)
    var me := Me(nick: proto.Name.Of(""), op: false, joined: HeapList[HeapText].New(c.ca))
    defer me.Release(c.ca)
    var wbuf: [8192]u8 = [0; 8192]
    var w := conn.Writer(io, mut wbuf[..])
    try Greet(&c, hb, mut w)
    var lb := proto.LineBuf.New()
    idle := if hb.cfg.idle_ms > 0 { some(time.Millis(i64(hb.cfg.idle_ms))) } else { none }
    for {
        try Drain(&c, hb, mut w)
        n := ReadOrWake(io, conn, hb.Slot(id), idle, mut lb) else e {
            match e {
                Canceled {
                    fail Canceled
                }
                time.TimedOut {
                    Print(mut w, "NOTICE idle for {s} ms, closing\n", (s: hb.cfg.idle_ms)) else {
                        fail Broken
                    }
                    w.Flush() else {
                        fail Broken
                    }
                    fail Idle
                }
                _ {
                    fail Broken
                }
            }
        }
        if n == 0 {
            fail Eof
        }
        if n < 0 {
            continue
        }
        lb.Fill(n)
        for {
            got := lb.Next()
            match got {
                .More {
                    break
                }
                .Overlong {
                    try Reply(mut w, "ERR 414 line too long")
                }
                .Line(len) {
                    line := lb.Line(len) else {
                        try Reply(mut w, "ERR 400 not UTF-8")
                        continue
                    }
                    // queued frames go first, so a client sees events in the
                    // order they happened before its reply
                    try Drain(&c, hb, mut w)
                    try Handle(&c, hb, mut me, mut w, line)
                }
            }
        }
        w.Flush() else {
            fail Broken
        }
    }
}

/// How long a hang-up with `linger` waits for the client to close its side.
const LingerMs: i64 = 2000

/// Closes `conn` under `io.NoCancel()`, so a connection ended by shutdown is
/// still closed; a close that fails releases it without a word. With
/// `linger`, it first reads and drops what the client still sends until the
/// client closes its side, for at most `LingerMs`: a close with the client's
/// bytes unread may reset the connection, and a reset loses on the client's
/// side what it had not read yet.
Hangup(conn: net.Conn, io: Io, linger: bool) {
    calm := io.NoCancel()
    // the end of the stream first, so the client reads all that was written
    conn.Shutdown(calm, net.Shutdown.Write) else {}
    if linger {
        var buf: [512]u8 = [0; 512]
        scope s := calm.Scope() {
            s.CancelAfter(time.Millis(LingerMs))
            for {
                n := conn.Read(calm, mut buf[..]) else {
                    break
                }
                if n == 0 {
                    break
                }
            }
        }
    }
    conn.Close(calm) else {
        conn.Abandon()
    }
}

/// Writes the greeting: the message of the day and the welcome line.
Greet(c: &Ctx, hb: &hub.Hub, w: mut Writer): Ended!void {
    m := hb.Greeting(c.io) else {
        fail Canceled
    }
    Print(w, "MOTD {t}\nOK welcome\n", (t: m.Ref().Text())) else {
        m.Free(hb.heap)
        fail Broken
    }
    m.Free(hb.heap)
    w.Flush() else {
        fail Broken
    }
}

/// Writes one reply line.
Reply(w: mut Writer, s: str): Ended!void {
    Print(w, "{s}\n", (s)) else {
        fail Broken
    }
}
