// Why: a running server is several tasks: accept, admin, signal watch and
// the loop that writes its log.
// What: Accept, Admin, Watch, PumpTo, DrainTo, Pump and Drain.
// How: the tasks run in the caller's scope. Only the caller's own task holds
// a Writer (a Writer does not cross tasks), so every other task reports
// through a channel and the caller's body writes the log.

import std.log
import std.net
import std.proc
import std.sync
import std.time

/// Accepts connections and serves each in a task of its own, in a scope of
/// its own under the caller's, until the caller's scope is cancelled. A
/// connection that stops the server says so through `State.Stop`, and
/// `Watch` cancels the server's scope.
export Accept(st: &State, heap: &Heap into st, cfg: sync.Shared[Config], l: &net.Listener, io: Io) {
    defer cfg.Free(heap)
    scope conns := io.Scope() {
        for {
            conn := l.Accept(io) else e {
                match e {
                    Canceled {
                        break
                    }
                    _ {
                        // a failed accept costs that one connection, not the server
                        st.Report(io, Event.AcceptFailed(cause: e))
                        continue
                    }
                }
            }
            id := Cast[u64](st.accepted.Add(1))
            if !cfg.Ref().admin {
                Tune(st, io, &conn, id)
            }
            conns.go Serve(st, heap, cfg.Clone(), conn, id, io)
        }
    }
}

/// Serves the admin channel on `l`, one connection at a time, in the
/// caller's task, until the scope is cancelled: call it in the body of the
/// server's scope, with the log written by a `PumpTo` task.
export Admin(st: &State, heap: &Heap into st, cfg: &sync.Shared[Config], l: &net.Listener, io: Io) {
    for {
        conn := l.Accept(io) else e {
            match e {
                Canceled {
                    return
                }
                _ {
                    st.Report(io, Event.AcceptFailed(cause: e))
                    continue
                }
            }
        }
        id := Cast[u64](st.accepted.Add(1))
        shared := cfg.Clone()
        Serve(st, heap, shared, conn, id, io)
    }
}

/// Sets a client's TCP stream to send small replies at once and to probe
/// an idle peer. Where the system has no such options (std.sim) nothing is
/// set; any other failure is reported, and the client is served anyway.
Tune(st: &State, io: Io, conn: &net.Stream, id: u64) {
    conn.SetNoDelay(true) else e {
        Untuned(st, io, id, e)
        return
    }
    conn.SetKeepAlive(some(time.Seconds(60))) else e {
        Untuned(st, io, id, e)
    }
}

/// Reports a socket option that could not be set, unless the system has none.
Untuned(st: &State, io: Io, id: u64, e: net.Error) {
    match e {
        net.Unsupported {}
        _ {
            st.Report(io, Event.OptionFailed(id: id, cause: e))
        }
    }
}

/// Stops the server on an interrupt or a terminate signal, and cancels its
/// scope once a task that does not hold the scope has begun to stop it
/// (`State.Stop`). Runs as a task; a cancelled scope ends it.
export Watch(s: &Scope, st: &State, io: Io) {
    var interrupt := false
    var terminate := false
    scope inner := io.Scope() {
        inner.go WaitFor(&inner, proc.Signal.Interrupt, io, mut interrupt)
        inner.go WaitFor(&inner, proc.Signal.Terminate, io, mut terminate)
        for !st.IsStopping() {
            st.halt.Wait(io) else {
                break
            }
        }
        inner.Cancel()
    }
    if (interrupt || terminate) && st.stopping.Load() == 0 && !st.Crashed() {
        st.Stop(io)
    }
    if st.IsStopping() {
        s.Cancel()
    }
}

/// Waits for one signal, then ends the other wait. `came` says whether it
/// came: a wait also ends when the scope is cancelled.
WaitFor(inner: &Scope, sig: proc.Signal, io: Io, came: mut bool) {
    proc.WaitSignal(io, sig) else {
        return
    }
    *came = true
    inner.Cancel()
}

/// Writes events to `sink` until the scope is cancelled, each record after
/// the wall-clock time in RFC 3339 when `stamp`. Call it last in the body of
/// the scope the server's tasks run in: as the one writer of the log, it
/// keeps the records in the order the events were sent. Each record is
/// formatted in a scratch arena on `heap`.
export PumpTo(st: &State, heap: &Heap, io: Io, sink: &log.Sink, stamp: bool) {
    var arena := Arena.New(heap) else {
        return
    }
    defer arena.Free()
    for {
        ev := st.events.Recv(io) else {
            return
        }
        Emit(sink, &arena, io, stamp, &ev) else {
            return
        }
        arena.Reset()
    }
}

/// Writes the events still queued once every task has ended, to `sink`.
export DrainTo(st: &State, heap: &Heap, io: Io, sink: &log.Sink, stamp: bool) {
    st.events.Close()
    var arena := Arena.New(heap) else {
        return
    }
    defer arena.Free()
    for {
        ev := st.events.Recv(io.NoCancel()) else {
            return
        }
        Emit(sink, &arena, io, stamp, &ev) else {
            return
        }
        arena.Reset()
    }
}

/// Writes one event to `sink` at its level.
Emit(sink: &log.Sink, arena: &Arena, io: Io, stamp: bool, ev: &Event): WriteError!void {
    var t := Text.New(arena)
    var tw := t.Writer()
    level := try Describe(mut tw, ev)
    message := t.Str()
    if stamp {
        try sink.Log(io, level, "{at} {message}", (at: time.Wall(io), message))
        return
    }
    match level {
        .Debug {
            try sink.Debug(io, "{message}", (message))
        }
        .Info {
            try sink.Info(io, "{message}", (message))
        }
        .Warn {
            try sink.Warn(io, "{message}", (message))
        }
        .Error {
            try sink.Error(io, "{message}", (message))
        }
    }
}

/// Writes events to `lg` until the scope is cancelled: the log `kvd sim`
/// keeps in a buffer. Call it last in the body of the scope the server's
/// tasks run in.
export Pump(st: &State, heap: &Heap, io: Io, lg: mut log.Logger) {
    var arena := Arena.New(heap) else {
        return
    }
    defer arena.Free()
    for {
        ev := st.events.Recv(io) else {
            return
        }
        Note(lg, &arena, &ev) else {
            return
        }
        arena.Reset()
    }
}

/// Writes the events still queued once every task has ended, to `lg`.
export Drain(st: &State, heap: &Heap, io: Io, lg: mut log.Logger) {
    st.events.Close()
    var arena := Arena.New(heap) else {
        return
    }
    defer arena.Free()
    for {
        ev := st.events.Recv(io.NoCancel()) else {
            return
        }
        Note(lg, &arena, &ev) else {
            return
        }
        arena.Reset()
    }
}

/// Writes one event to `lg` at its level.
Note(lg: mut log.Logger, arena: &Arena, ev: &Event): WriteError!void {
    var t := Text.New(arena)
    var tw := t.Writer()
    level := try Describe(mut tw, ev)
    message := t.Str()
    match level {
        .Debug {
            try lg.Log(level, "{message}", (message))
        }
        .Info {
            try lg.Info("{message}", (message))
        }
        .Warn {
            try lg.Warn("{message}", (message))
        }
        .Error {
            try lg.Error("{message}", (message))
        }
    }
}

/// Writes what `ev` says to `w`, and returns the level it is logged at.
Describe(w: mut Writer, ev: &Event): WriteError!log.Level {
    match *ev {
        .Connected(id) {
            try Print(w, "client {id} connected", (id))
            return log.Level.Debug
        }
        .AdminConnected(id) {
            try Print(w, "admin client {id} connected", (id))
            return log.Level.Debug
        }
        .Disconnected(id, commands) {
            try Print(w, "client {id} left after {commands} commands", (id, commands))
            return log.Level.Debug
        }
        .Refused(id) {
            try Print(w, "client {id} refused: too many clients", (id))
            return log.Level.Warn
        }
        .Partial(id, bytes) {
            try Print(w, "client {id} left in the middle of a command ({bytes} bytes dropped)", (id, bytes))
            return log.Level.Warn
        }
        .Dropped(id, cause) {
            try Print(w, "client {id} dropped: {cause}", (id, cause))
            return log.Level.Warn
        }
        .TooLong(id) {
            try Print(w, "client {id} sent a line over the limit", (id))
            return log.Level.Warn
        }
        .Slow(id, micros) {
            try Print(w, "client {id}: request took {micros} us", (id, micros))
            return log.Level.Warn
        }
        .Rejected(id, cause) {
            try Print(w, "client {id}: change not logged: {cause}", (id, cause))
            return log.Level.Error
        }
        .Compacted(records, bytes, generation) {
            try Print(w, "log compacted: {records} records, {bytes} bytes, generation {generation}", (records, bytes, generation))
            return log.Level.Info
        }
        .CompactFailed(cause) {
            try Print(w, "compaction failed: {cause}", (cause))
            return log.Level.Error
        }
        .Crashed(change) {
            try Print(w, "simulated crash at log write {change}", (change))
            return log.Level.Error
        }
        .AcceptFailed(cause) {
            try Print(w, "accept failed: {cause}", (cause))
            return log.Level.Warn
        }
        .Stopping(clients) {
            try Print(w, "shutting down with {clients} clients connected", (clients))
            return log.Level.Info
        }
        .IdleTimeout(id) {
            try Print(w, "client {id} dropped: idle too long", (id))
            return log.Level.Info
        }
        .RequestTimeout(id) {
            try Print(w, "client {id}: request timed out", (id))
            return log.Level.Warn
        }
        .OptionFailed(id, cause) {
            try Print(w, "client {id}: socket options not set: {cause}", (id, cause))
            return log.Level.Warn
        }
    }
}
