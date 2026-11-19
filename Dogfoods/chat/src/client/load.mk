// Why: The load mode needs many concurrent clients and a summary of what they
// saw, including one client too slow to read.
//
// What: LoadSpec, Tally, Stage, Failure, Load and LoadSlow.
//
// How: Each client is a task that joins a room, meets the others at a
// sync.Barrier, sends its messages and counts what its room sends back, into
// atomics in a shared Tally. LoadSlow pairs a client that never reads with a
// flooder until the server drops the first.

import std.atomic
import std.net
import std.sync
import src.hub

/// What a load run does.
export struct LoadSpec {
    export clients: int
    export rooms: int
    export messages: int
    /// End with the server shutting down under every client, not with QUIT.
    export shutdown: bool
}

/// What the clients of a load run saw, summed.
export struct Tally {
    export joined: atomic.AtomicInt
    export sent: atomic.AtomicInt
    export delivered: atomic.AtomicInt
    export refused: atomic.AtomicInt
    export notices: atomic.AtomicInt
    export closed: atomic.AtomicInt
    export failed: atomic.AtomicInt
    /// Clients that joined, per room, so each knows how many messages its
    /// room will carry.
    export by_room: atomic.Spin[[32]int]
    /// The first client that failed, where, and the connection's error.
    export first: Mutex[?Failure]
}

/// Where a load client gave up.
export enum Stage {
    Dial
    Memory
    Join
}

/// The first failure of a load run, for its report.
export struct Failure {
    export client: int
    export stage: Stage
    export err: ?net.Error
}

/// Zeroed counts.
export Tally.New(): Tally {
    return Tally(
        joined: atomic.AtomicInt.New(0),
        sent: atomic.AtomicInt.New(0),
        delivered: atomic.AtomicInt.New(0),
        refused: atomic.AtomicInt.New(0),
        notices: atomic.AtomicInt.New(0),
        closed: atomic.AtomicInt.New(0),
        failed: atomic.AtomicInt.New(0),
        by_room: atomic.Spin.New([0; 32]),
        first: Mutex.New(none),
    )
}

/// Counts client `i` as failed at `stage`, and keeps the first failure.
Tally.Fail(&self, io: Io, i: int, stage: Stage, err: ?net.Error) {
    _ = self.failed.Add(1)
    var g := self.first.Lock(io.NoCancel()) else {
        return
    }
    if g.Ref().IsNone() {
        g.Store(some(Failure(client: i, stage: stage, err: err)))
    }
    g.Unlock()
}

/// Counts a client that joined `room`.
Tally.Arrived(&self, room: int) {
    _ = self.joined.Add(1)
    var g := self.by_room.Lock()
    g.Mut()[room] += 1
    g.Unlock()
}

/// How many clients joined `room`; final once every client has passed the
/// first meeting.
Tally.InRoom(&self, room: int): int {
    g := self.by_room.Lock()
    n := g.Ref()[room]
    g.Unlock()
    return n
}

/// Sends client `i`'s messages and reads every message its room sends.
/// Returns how many room messages it read; stops early, with fewer, when the
/// connection fails, so the caller still meets the others at the gate.
Exchange(conn: &net.Stream, io: Io, r: mut Reader, line: mut []u8, arena: &Arena, spec: LoadSpec, i: int, t: &Tally): int {
    room := i % spec.rooms
    var got := 0
    for k in 0..spec.messages {
        msg := arena.Fmt("MSG #r{room} hello from c{i}, number {k}", (room, i, k)) else {
            return got
        }
        SendLine(conn, io, msg) else {
            _ = t.failed.Add(1)
            return got
        }
        _ = t.sent.Add(1)
        // read to this message's reply, counting the room's messages on the way
        for {
            l := ReadLine(r, line) else {
                return got
            }
            text := l else {
                return got
            }
            if text.StartsWith("ERR") {
                _ = t.refused.Add(1)
                break
            }
            if text.StartsWith("OK msg") {
                break
            }
            if text.StartsWith("<#") {
                got += 1
            }
        }
    }
    want := (t.InRoom(room) - 1) * spec.messages
    for got < want {
        l := ReadLine(r, line) else {
            break
        }
        text := l else {
            break
        }
        if text.StartsWith("<#") {
            got += 1
        }
    }
    return got
}

/// Arrives at `gate` for a client that gave up before the first meeting:
/// once, and again for the shutdown meeting, so no other client waits for it.
Skip(io: Io, gate: &sync.Barrier, spec: LoadSpec) {
    _ = gate.Wait(io) else {}
    if spec.shutdown {
        _ = gate.Wait(io) else {}
    }
}

/// Runs every client of `spec` against `addr` and waits for them all.
export Load(io: Io, addr: str, hb: &hub.Hub, spec: LoadSpec, t: &Tally) {
    gate := sync.Barrier.New(spec.clients)
    scope s := io.ScopeWith(Limits(tasks: spec.clients, stack: StackSize.Default)) {
        for i in 0..spec.clients {
            s.go LoadClient(io, addr, hb, spec, i, &gate, t)
        }
    }
}

/// One client: joins its room, meets the others at `gate`, sends its
/// messages, reads every message its room sends, then quits, or waits for
/// the server to shut down under it.
LoadClient(io: Io, addr: str, hb: &hub.Hub, spec: LoadSpec, i: int, gate: &sync.Barrier, t: &Tally) {
    conn := net.Dial(io, addr) else e {
        t.Fail(io, i, Stage.Dial, some(e))
        Skip(io, gate, spec)
        return
    }
    defer conn.Close()
    var arena := Arena.New(hb.heap) else {
        t.Fail(io, i, Stage.Memory, none)
        Skip(io, gate, spec)
        return
    }
    defer arena.Free()
    var rbuf: [4096]u8 = [0; 4096]
    var r := conn.Reader(io, mut rbuf[..])
    var line: [600]u8 = [0; 600]
    room := i % spec.rooms
    nick := arena.Fmt("NICK c{i}", (i)) else {
        Skip(io, gate, spec)
        return
    }
    join := arena.Fmt("JOIN #r{room}", (room)) else {
        Skip(io, gate, spec)
        return
    }
    ok := Await(mut r, mut line[..]) && Ask(&conn, io, mut r, mut line[..], nick) && Ask(&conn, io, mut r, mut line[..], join)
    if ok {
        t.Arrived(room)
    } else {
        t.Fail(io, i, Stage.Join, none)
    }
    _ = gate.Wait(io) else {
        return
    }
    if !ok {
        if spec.shutdown {
            _ = gate.Wait(io) else {}
        }
        return
    }
    got := Exchange(&conn, io, mut r, mut line[..], &arena, spec, i, t)
    _ = t.delivered.Add(i64(got))
    if !spec.shutdown {
        if Ask(&conn, io, mut r, mut line[..], "QUIT") {
            _ = t.closed.Add(1)
        }
        return
    }
    // everyone has every message; the last to arrive starts the shutdown
    last := gate.Wait(io) else {
        return
    }
    if last {
        hb.stop.NotifyOne()
    }
    for {
        l := ReadLine(mut r, mut line[..]) else {
            break
        }
        text := l else {
            _ = t.closed.Add(1)
            break
        }
        if text.StartsWith("NOTICE server shutting down") {
            _ = t.notices.Add(1)
        }
    }
}

/// The slow-consumer run: one client joins `#r0` and never reads, another
/// floods `#r0` until the server says the first left as too slow, or until
/// `limit` messages. Then the slow client reads what it was sent, to the
/// end of its stream.
export LoadSlow(io: Io, addr: str, hb: &hub.Hub, limit: int, t: &Tally) {
    gate := sync.Barrier.New(2)
    done := sync.Notify.New()
    scope s := io.Scope() {
        s.go SlowReader(io, addr, &gate, &done, t)
        s.go Flooder(io, addr, limit, &gate, &done, t)
    }
}

SlowReader(io: Io, addr: str, gate: &sync.Barrier, done: &sync.Notify, t: &Tally) {
    conn := net.Dial(io, addr) else {
        _ = gate.Wait(io) else {}
        return
    }
    defer conn.Close()
    var rbuf: [4096]u8 = [0; 4096]
    var r := conn.Reader(io, mut rbuf[..])
    var line: [600]u8 = [0; 600]
    ok := Await(mut r, mut line[..]) && Ask(&conn, io, mut r, mut line[..], "NICK slow") && Ask(&conn, io, mut r, mut line[..], "JOIN #r0")
    _ = gate.Wait(io) else {
        return
    }
    if !ok {
        return
    }
    done.Wait(io) else {
        return
    }
    for {
        l := ReadLine(mut r, mut line[..]) else {
            break
        }
        _ = l else {
            break
        }
    }
    _ = t.closed.Add(1)
}

Flooder(io: Io, addr: str, limit: int, gate: &sync.Barrier, done: &sync.Notify, t: &Tally) {
    defer done.NotifyOne()
    conn := net.Dial(io, addr) else {
        _ = gate.Wait(io) else {}
        return
    }
    defer conn.Close()
    var rbuf: [4096]u8 = [0; 4096]
    var r := conn.Reader(io, mut rbuf[..])
    var line: [600]u8 = [0; 600]
    ok := Await(mut r, mut line[..]) && Ask(&conn, io, mut r, mut line[..], "NICK flood") && Ask(&conn, io, mut r, mut line[..], "JOIN #r0")
    _ = gate.Wait(io) else {
        return
    }
    if !ok {
        return
    }
    var text: [400]u8 = [b'x'; 400]
    for i, b in "MSG #r0 ".AsBytes() {
        text[i] = b
    }
    // only ASCII bytes were written above, so the text is UTF-8
    msg := str.From(text[..]).Must()
    for _ in 0..limit {
        SendPaced(&conn, io, msg) else {
            return
        }
        for {
            l := ReadLine(mut r, mut line[..]) else {
                return
            }
            got := l else {
                return
            }
            if got.StartsWith("* slow left #r0") {
                _ = t.notices.Add(1)
                _ = Ask(&conn, io, mut r, mut line[..], "QUIT")
                return
            }
            if Final(got) {
                break
            }
        }
    }
}
