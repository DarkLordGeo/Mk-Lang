// Why: The accept loop, the per-connection task and shutdown belong together.
//
// What: Endings, Serve, Client, Why, Refill, Stopper and OnSignal.
//
// How: Serve admits a connection only after taking a permit and a slot, and
// runs each client in its own task. Refill tops up every slot's send rate on a
// timer; Stopper and OnSignal begin draining and cancel the scope.

import std.atomic
import std.log
import std.net
import std.proc
import std.time
import src.hub
import src.proto

/// How each connection ended, counted by reason.
export struct Endings {
    export quit: atomic.AtomicInt
    export eof: atomic.AtomicInt
    export broken: atomic.AtomicInt
    export dropped: atomic.AtomicInt
    export kicked: atomic.AtomicInt
    export closing: atomic.AtomicInt
    export canceled: atomic.AtomicInt
    export idle: atomic.AtomicInt
    /// Connections accepted so far, which numbers each recorded connection.
    export accepted: atomic.AtomicInt
}

/// Zeroed counts.
export Endings.New(): Endings {
    return Endings(
        quit: atomic.AtomicInt.New(0),
        eof: atomic.AtomicInt.New(0),
        broken: atomic.AtomicInt.New(0),
        dropped: atomic.AtomicInt.New(0),
        kicked: atomic.AtomicInt.New(0),
        closing: atomic.AtomicInt.New(0),
        canceled: atomic.AtomicInt.New(0),
        idle: atomic.AtomicInt.New(0),
        accepted: atomic.AtomicInt.New(0),
    )
}

/// The accept loop: admits a connection when a slot is free, then accepts it
/// and serves it in a task of its own, until the scope is cancelled. At most
/// `max_clients` connections are served, with the tasks to match.
export Serve(l: &net.Listener, io: Io, hb: &hub.Hub, ends: &Endings) *hb into hb {
    scope s := io.ScopeWith(Limits(tasks: hb.cfg.max_clients, stack: StackSize.Default)) {
        for {
            hb.admit.Acquire(io, 1) else {
                break
            }
            conn := Accept(l, io, hb, ends) else e {
                hb.admit.Release(1)
                match e {
                    Canceled {
                        break
                    }
                    OutOfMemory {
                        hb.log.Error(io, "no memory to record a connection") else {}
                        break
                    }
                    _ {
                        // a connection reset before it was accepted
                        continue
                    }
                }
            }
            id := hb.TakeSlot(io) else {
                Hangup(conn, io, false)
                hb.admit.Release(1)
                break
            }
            s.go Client(conn, io, hb, id, ends)
        }
    }
}

/// Accepts one connection. With `record`, the connection is wrapped in a
/// `Recorder` whose box is reserved before the wait, so a connection is never
/// accepted and then lost for want of memory.
Accept(l: &net.Listener, io: Io, hb: &hub.Hub, ends: &Endings): net.Error!net.Conn from hb *hb into hb {
    if !hb.cfg.record {
        stream := try l.Accept(io)
        return stream.Conn()
    }
    slot := net.ReserveConn[Recorder](hb.heap) else {
        fail OutOfMemory
    }
    stream := l.Accept(io) else e {
        slot.Free(hb.heap.Alloc())
        fail e
    }
    n := Cast[int](ends.accepted.Add(1))
    Opened(&hb.log, io, n, stream.PeerAddr(), stream.LocalAddr())
    return slot.Fill(Recorder.Of(stream.Conn(), n, hb.log))
}

/// With `record`, a connection's first record: the hosts at its two ends,
/// read from the stream before it becomes a `net.Conn`, without the ports,
/// so a record is the same on every run.
Opened(sink: &log.Sink, io: Io, id: int, peer: net.Addr, local: net.Addr) {
    if peer.v6 || local.v6 {
        sink.Info(io, "conn {id} accepted over IPv6", (id)) else {}
        return
    }
    sink.Info(
        io,
        "conn {id} accepted from {a}.{b}.{c}.{d} on {e}.{f}.{g}.{h}",
        (id, a: peer.ip[0], b: peer.ip[1], c: peer.ip[2], d: peer.ip[3], e: local.ip[0], f: local.ip[1], g: local.ip[2], h: local.ip[3]),
    ) else {}
}

/// Reports a new connection with its peer's host, without the port, so a
/// log line is the same on every run.
Arrived(sink: &log.Sink, io: Io, id: int, peer: ?net.Addr) {
    p := peer else {
        sink.Info(io, "client {id} connected", (id)) else {}
        return
    }
    if p.v6 {
        sink.Info(io, "client {id} connected over IPv6", (id)) else {}
        return
    }
    sink.Info(io, "client {id} connected from {a}.{b}.{c}.{d}", (id, a: p.ip[0], b: p.ip[1], c: p.ip[2], d: p.ip[3])) else {}
}

/// One connection: its session and its kicker under one scope, whose first
/// error, the reason the connection ended, cancels the other.
export Client(conn: net.Conn, io: Io, hb: &hub.Hub, id: int, ends: &Endings) *hb into hb {
    Arrived(&hb.log, io, id, conn.PeerAddr())
    handoff := Mutex.New(some(conn))
    defer CloseLeft(handoff, io)
    RunClient(&handoff, io, hb, id) else e {
        hb.log.Info(io, "client {id} left: {why}", (id, why: Why(e))) else {}
        Depart(io, hb, id, e)
        match e {
            Quit {
                _ = ends.quit.Add(1)
            }
            Eof {
                _ = ends.eof.Add(1)
            }
            Broken {
                _ = ends.broken.Add(1)
            }
            Dropped {
                _ = ends.dropped.Add(1)
                _ = hb.stats.dropped.Add(1)
            }
            Kicked {
                _ = ends.kicked.Add(1)
            }
            Closing {
                _ = ends.closing.Add(1)
            }
            Idle {
                _ = ends.idle.Add(1)
            }
            Canceled {
                _ = ends.canceled.Add(1)
            }
        }
        return
    }
    // a session only ends by failing, with its reason
    Panic("a session ended without a reason")
}

/// Closes a connection no session took. A task always starts, so this is
/// for the case the session failed before taking it.
CloseLeft(handoff: Mutex[?net.Conn], io: Io) {
    if left := handoff.Into() {
        Hangup(left, io, false)
    }
}

RunClient(handoff: &Mutex[?net.Conn], io: Io, hb: &hub.Hub, id: int): Ended!void *hb into hb {
    try scope cs := io.ScopeOf[Ended]() {
        cs.go Session(handoff, io, hb, id)
        cs.go Kicker(io, hb.Slot(id))
    }
}

/// Takes slot `id` out of every room, tells the rooms why, and frees the
/// slot for the next connection.
Depart(io: Io, hb: &hub.Hub, id: int, why: Ended) *hb into hb {
    calm := io.NoCancel()
    var left: [32]proto.Name = [proto.Name.Of(""); 32]
    var n := 0
    var nick := proto.Name.Of("")
    // calm is never cancelled, so neither call below can fail
    table := hb.Table(calm) else {
        Panic("the room table failed under NoCancel")
    }
    var g := table.Lock(calm) else {
        Panic("a lock failed under NoCancel")
    }
    nick = proto.Name.Of(g.Ref().Nick(id))
    for i in 0..g.Ref().Len() {
        if g.Ref().At(i).Has(id) {
            left[n] = g.Ref().At(i).name
            n += 1
        }
    }
    _ = g.Mut().Leave(id)
    g.Unlock()
    if !hb.stats.Draining() {
        for i in 0..n {
            var f := proto.Frame.Empty(proto.Kind.Line)
            f.Push("* ")
            f.Push(nick.Str())
            f.Push(" left ")
            f.Push(left[i].Str())
            f.Push(" (")
            f.Push(Why(why))
            f.Push(")\n")
            _ = hb.Broadcast(calm, left[i].Str(), f, id) else {}
        }
    }
    _ = hb.GiveBack(calm, id)
}

/// The word a departure notice gives for each reason.
export Why(e: Ended): str {
    return match e {
        Quit { "quit" }
        Eof { "connection closed" }
        Broken { "connection lost" }
        Dropped { "too slow" }
        Kicked { "kicked" }
        Closing { "server shutdown" }
        Idle { "idle" }
        Canceled { "canceled" }
    }
}

/// The rate refill task: every `refill_ms`, gives back to each client the
/// permits it used, until cancelled.
export Refill(io: Io, hb: &hub.Hub) {
    if hb.cfg.burst == 0 {
        return
    }
    for {
        io.Sleep(time.Millis(i64(hb.cfg.refill_ms))) else {
            return
        }
        for s in &hb.slots {
            n := s.used.Swap(0, atomic.AcqRel)
            if n > 0 {
                s.rate.Release(Cast[int](n))
            }
        }
    }
}

/// Waits for the stop signal, tells every client the server is closing,
/// gives them and the web requests up to five seconds to go, then cancels
/// `root`.
export Stopper(root: &Scope, io: Io, hb: &hub.Hub) *hb into hb {
    hb.stop.Wait(io) else {
        return
    }
    if hb.stats.BeginDrain() {
        _ = hb.NoticeAll(io, proto.Frame.Of(proto.Kind.Closing, "NOTICE server shutting down")) else {}
        for _ in 0..500 {
            if hb.stats.online.Load(atomic.Acquire) == 0 && hb.stats.web.Load(atomic.Acquire) == 0 {
                break
            }
            io.Sleep(time.Millis(10)) else {
                break
            }
        }
    }
    hb.history.wake.NotifyAll()
    root.Cancel()
}

/// Begins shutdown on Ctrl-C.
export OnSignal(io: Io, hb: &hub.Hub) {
    proc.WaitSignal(io, proc.Signal.Interrupt) else {
        return
    }
    hb.stop.NotifyOne()
}
