// Why: each client connection runs in a task of its own, reading requests
// and writing replies until the client leaves or the server stops.
// What: Serve, Loop, Answer, Dispatch and the per-connection Conn and
// Buffers.
// How: a line is read with `Until`, which keeps its "\n", so a client that
// disconnects in the middle of a command leaves a line with no "\n", and
// that command is never run.

import std.net
import std.sync
import std.time
import src.proto
import src.wal

export error AdminOnly

/// The longest request line: a command, a key and a value, with room for
/// the spaces and the line break.
LineLimit(cfg: &Config): int {
    return cfg.max_key + cfg.max_value + 64
}

/// What one connection's handlers share. The state and the allocator travel
/// beside it: a store of an allocation into the state must name both.
// They stay outside because with them in here, the clause `*c into c` is
// refused.
struct Conn {
    io: Io
    cfg: &Config
    id: u64
    heap: &Heap
}

/// What to do after a request.
enum Next {
    More
    Quit
    Stop
}

/// The buffers of one connection, kept on the heap: a task's stack is a
/// fixed slot, and the line buffer is 64 KiB.
struct Buffers {
    input: [4096]u8
    output: [4096]u8
}

/// Serves one client until it leaves, then closes its stream. Runs as a
/// task; reports to the log task through `st`.
export Serve(st: &State, heap: &Heap into st, cfg: sync.Shared[Config], conn: net.Stream, id: u64, io: Io) {
    defer conn.Close()
    defer cfg.Free(heap)
    a := heap.Alloc()
    n := st.open.Add(1)
    defer Leave(st)
    if n > i64(cfg.Ref().max_clients) {
        st.Report(io, Event.Refused(id: id))
        conn.WriteAll(io, b"-ERR TooManyClients\n") else {}
        return
    }
    var arena := Arena.New(heap) else {
        return
    }
    defer arena.Free()
    if !Register(st, a, io, id) {
        return
    }
    st.Report(io, if cfg.Ref().admin { Event.AdminConnected(id: id) } else { Event.Connected(id: id) })
    var bufs := Box.New(a, Buffers(input: [0; 4096], output: [0; 4096])) else {
        Unregister(st, a, io, id)
        return
    }
    line := arena.Slice[u8](LineLimit(cfg.Ref())) else {
        bufs.Free(a)
        Unregister(st, a, io, id)
        return
    }
    c := Conn(io: io, cfg: cfg.Ref(), id: id, heap: heap)
    b := bufs.Mut()
    commands := Loop(&c, st, a, &arena, &conn, mut b.input[..], mut b.output[..], line)
    bufs.Free(a)
    Unregister(st, a, io, id)
    st.Report(io, Event.Disconnected(id: id, commands: commands))
}

/// Counts a connection out.
Leave(st: &State) {
    _ = st.open.Add(-1)
}

/// Adds the client to the table; false when the server is out of memory or
/// stopping.
Register(st: &State, a: &Alloc into st, io: Io, id: u64): bool {
    var g := st.sessions.Lock(io) else {
        return false
    }
    defer g.Unlock()
    entry := Session(name: HeapText.New(a), commands: 0, since: time.Now(io))
    g.Mut().Set(a, id, entry) else {
        entry.Free(a)
        return false
    }
    return true
}

/// Takes the client out of the table. Uses an `Io` that cannot be
/// cancelled, so a stopping server still empties its table.
Unregister(st: &State, a: &Alloc, io: Io, id: u64) {
    var g := st.sessions.Lock(io.NoCancel()) else {
        return
    }
    defer g.Unlock()
    if gone := g.Mut().Remove(&id) {
        gone.Free(a)
    }
}

/// Reads and answers lines until the client leaves; returns how many
/// commands it ran. Each request's scratch memory is a sub-arena of
/// `arena`, reset after it.
Loop(c: &Conn, st: &State, a: &Alloc into st, arena: &Arena, conn: &net.Stream, input: mut []u8, output: mut []u8, line: mut []u8): int {
    var scratch := arena.Sub() else {
        return 0
    }
    defer scratch.Free()
    var r := conn.Reader(c.io, input)
    var w := conn.Writer(c.io, output)
    var n := 0
    for {
        got := ReadRequest(c, mut r, line) else e {
            match e {
                time.TimedOut {
                    st.Report(c.io, Event.IdleTimeout(id: c.id))
                    conn.WriteAll(c.io, b"-ERR IdleTimeout\n") else {}
                }
                _ {
                    if !Goodbye(c, st, conn) {
                        st.Report(c.io, Event.Dropped(id: c.id, cause: ReadError))
                    }
                }
            }
            return n
        }
        if got == 0 {
            return n
        }
        if line[got - 1] != b'\n' {
            if got == line.Len() {
                st.Report(c.io, Event.TooLong(id: c.id))
                conn.WriteAll(c.io, b"-ERR LineTooLong\n") else {}
            } else {
                st.Report(c.io, Event.Partial(id: c.id, bytes: got))
            }
            return n
        }
        n += 1
        next := Answer(c, st, a, &scratch, mut w, line[..got - 1]) else {
            _ = Goodbye(c, st, conn)
            return n
        }
        w.Flush() else {
            return n
        }
        scratch.Reset()
        match next {
            .More {}
            .Quit {
                return n
            }
            .Stop {
                st.Stop(c.io)
                return n
            }
        }
    }
}

/// Reads one request line into `line`, waiting at most the idle limit: past
/// it, `TimedOut`, told from the server's shutdown by the deadline's own
/// scope. A read cut off part way has taken an unknown prefix of a line, so
/// the caller closes the connection after either error.
ReadRequest(c: &Conn, r: mut Reader, line: mut []u8): ReadError|time.TimedOut!int {
    if c.cfg.idle_millis == 0 {
        return try r.Until(b'\n', line)
    }
    var n := 0
    scope idle := c.io.Scope() {
        idle.CancelAfter(time.Millis(c.cfg.idle_millis))
        n = r.Until(b'\n', line) else e {
            if idle.Expired() {
                fail time.TimedOut
            }
            fail e
        }
    }
    return n
}

/// Tells a client the server is stopping, when that is why its read
/// failed. Uses an `Io` that cannot be cancelled: the scope already is.
/// Returns whether the server is stopping.
Goodbye(c: &Conn, st: &State, conn: &net.Stream): bool {
    if st.stopping.Load() == 0 {
        return false
    }
    if !st.Crashed() {
        conn.WriteAll(c.io.NoCancel(), b"-ERR ShuttingDown\n") else {}
    }
    return true
}

/// Answers one line, timing it.
Answer(c: &Conn, st: &State, a: &Alloc into st, arena: &Arena, w: mut Writer, bytes: []u8): Canceled|OutOfMemory|WriteError!Next {
    start := time.Now(c.io)
    _ = st.commands.Add(1)
    CountCommand(c, st)
    text := str.From(bytes) else e {
        try proto.Problem(w, e)
        return Next.More
    }
    b := proto.Bounds(max_key: c.cfg.max_key, max_value: c.cfg.max_value)
    req := proto.Parse(text.Trim(), &b) else e {
        try proto.Problem(w, e)
        return Next.More
    }
    next := try Within(c, st, a, arena, w, &req)
    took := time.Now(c.io).Since(start).Nanos()
    _ = st.busy_nanos.Add(took)
    micros := took / 1000
    if micros > c.cfg.slow_micros {
        st.Report(c.io, Event.Slow(id: c.id, micros: micros))
    }
    return next
}

/// Runs `req` under the request limit when it only reads: a read past the
/// limit is answered `-ERR TimedOut`, and the connection goes on. A change
/// is not cut off once it holds the log, so the log and the store agree,
/// nor is a compaction, which a cut would leave half done.
Within(c: &Conn, st: &State, a: &Alloc into st, arena: &Arena, w: mut Writer, req: &proto.Request): Canceled|OutOfMemory|WriteError!Next {
    if c.cfg.request_millis == 0 || req.Writes() || req.Compacts() {
        return try Dispatch(c, st, a, arena, w, req)
    }
    var next := Next.More
    var late := false
    scope limit := c.io.Scope() {
        limit.CancelAfter(time.Millis(c.cfg.request_millis))
        next = Dispatch(c, st, a, arena, w, req) else e {
            if !limit.Expired() {
                fail e
            }
            late = true
            Next.More
        }
    }
    if late {
        st.Report(c.io, Event.RequestTimeout(id: c.id))
        try proto.Problem(w, time.TimedOut)
    }
    return next
}

/// Counts a command against the client's entry in the table.
CountCommand(c: &Conn, st: &State) {
    var g := st.sessions.Lock(c.io) else {
        return
    }
    defer g.Unlock()
    if s := g.Mut().GetMut(&c.id) {
        s.commands += 1
    }
}

/// Runs one parsed request and writes its reply.
Dispatch(c: &Conn, st: &State, a: &Alloc into st, arena: &Arena, w: mut Writer, req: &proto.Request): Canceled|OutOfMemory|WriteError!Next {
    if req.Writes() {
        try Change(c, st, a, arena, w, req)
        return Next.More
    }
    match *req {
        .Ping {
            try proto.Status(w, "PONG")
        }
        .Get(key) {
            g := try st.store.LockRead(c.io)
            defer g.Unlock()
            if v := g.Ref().Get(key) {
                try proto.Bulk(w, v)
            } else {
                try proto.Nil(w)
            }
        }
        .Keys(prefix) {
            g := try st.store.LockRead(c.io)
            defer g.Unlock()
            keys := try g.Ref().Keys(arena, prefix)
            try proto.Array(w, keys.Len())
            for k in keys {
                try proto.Bulk(w, k)
            }
        }
        .Size {
            g := try st.store.LockRead(c.io)
            defer g.Unlock()
            try proto.Int(w, i64(g.Ref().Len()))
        }
        .Hash(key) {
            try HashReply(c, st, w, key)
        }
        .Stats {
            try Stats(c, st, w)
        }
        .Name(name) {
            try Rename(c, st, a, name)
            try proto.Status(w, "OK")
        }
        .Clients {
            try Clients(c, st, arena, w)
        }
        .Wait(key) {
            try AwaitKey(c, st, w, key)
        }
        .Compact {
            if c.cfg.admin_only {
                try proto.Problem(w, AdminOnly)
                return Next.More
            }
            if try Compact(c, st) {
                try proto.Status(w, "OK")
            } else {
                try proto.Problem(w, wal.Broken)
            }
        }
        .Quit {
            try proto.Status(w, "BYE")
            return Next.Quit
        }
        .Shutdown {
            if c.cfg.admin_only {
                try proto.Problem(w, AdminOnly)
                return Next.More
            }
            try proto.Status(w, "BYE")
            return Next.Stop
        }
        _ {}
    }
    return Next.More
}

/// `WAIT key`: the value of `key` once it has one. Looks again after each
/// change, and at least every 50 ms, since a change can land between a look
/// and the wait; the request limit or the shutdown ends the wait.
AwaitKey(c: &Conn, st: &State, w: mut Writer, key: str): Canceled|WriteError!void {
    for {
        g := try st.store.LockRead(c.io)
        if v := g.Ref().Get(key) {
            defer g.Unlock()
            try proto.Bulk(w, v)
            return
        }
        g.Unlock()
        scope poll := c.io.Scope() {
            poll.CancelAfter(time.Millis(50))
            st.changed.Wait(c.io) else {
                // the poll's own deadline only means: look again
                if !poll.Expired() {
                    fail Canceled
                }
            }
        }
    }
}

/// Sets the client's name in the table, in place.
Rename(c: &Conn, st: &State, a: &Alloc into st, name: str): Canceled|OutOfMemory!void {
    var g := try st.sessions.Lock(c.io)
    defer g.Unlock()
    if s := g.Mut().GetMut(&c.id) {
        // `NAME -` takes the name away again
        if name == "-" {
            s.name.Clear()
        } else {
            try s.name.Set(a, name)
        }
    }
}
