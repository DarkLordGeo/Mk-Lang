// Why: every change to the store must be durable before a client is told it
// happened.
// What: Change, Decide, CompactNow and Compactor, and the read-only replies
// (HASH, STATS, KEYS, CLIENTS).
// How: each change is logged and synced before the store sees it, under the
// log's lock and then the store's, so the log's order is the store's.

import std.hash
import src.proto
import src.store
import src.wal

export error NotAnInteger

/// A change as the log records it, and what the client is told.
struct Plan {
    op: store.Op
    key: str
    value: str
    reply: Reply
}

/// The reply a change gets once it is applied.
enum Reply {
    Ok
    Count(n: i64)
    Length
    Old
}

/// Logs and applies one change, and writes its reply. A log that fails is
/// reported to the client; a simulated crash stops the server instead.
Change(c: &Conn, st: &State, a: &Alloc into st, arena: &Arena, w: mut Writer, req: &proto.Request): Canceled|OutOfMemory|WriteError!void {
    var wg := try st.wal.Lock(c.io)
    defer wg.Unlock()
    got := Decide(c, st, arena, req) else e {
        match e {
            NotAnInteger {
                try proto.Problem(w, NotAnInteger)
                return
            }
            Canceled {
                fail Canceled
            }
            OutOfMemory {
                fail OutOfMemory
            }
        }
    }
    plan := got else {
        // nothing to change: a DEL of a missing key
        try proto.Int(w, 0)
        return
    }
    wg.Mut().Append(c.io, plan.op, plan.key, plan.value) else e {
        try Failed(c, st, w, e)
        return
    }
    _ = st.changes.Add(1)
    if st.pending.Add(1) >= i64(c.cfg.compact_after) {
        st.wake.NotifyOne()
    }
    // the change is on disk, so it reaches the store even if the server is
    // stopping meanwhile
    var g := try st.store.Lock(c.io.NoCancel())
    defer g.Unlock()
    // a WAIT for this key looks again once the change is in
    defer st.changed.NotifyAll()
    s := g.Mut()
    match plan.reply {
        .Ok {
            try s.Set(a, plan.key, plan.value)
            try proto.Status(w, "OK")
        }
        .Count(n) {
            try s.Apply(a, plan.op, plan.key, plan.value)
            try proto.Int(w, n)
        }
        .Length {
            n := try s.Append(a, plan.key, plan.value)
            try proto.Int(w, i64(n))
        }
        .Old {
            old := try s.Swap(a, plan.key, plan.value)
            match old {
                some(v) {
                    onerr v.Free(a)
                    try proto.Bulk(w, v.Str())
                    v.Free(a)
                }
                none {
                    try proto.Nil(w)
                }
            }
        }
    }
}

/// Turns a request into the change it logs, reading the store where the
/// change depends on it. `none` when nothing changes.
Decide(c: &Conn, st: &State, arena: &Arena, req: &proto.Request): NotAnInteger|Canceled|OutOfMemory!?Plan from arena, req {
    return match *req {
        .Set(key, value) { some(Plan(op: store.Op.Set, key: key, value: value, reply: Reply.Ok)) }
        .Append(key, value) { some(Plan(op: store.Op.Append, key: key, value: value, reply: Reply.Length)) }
        .GetSet(key, value) { some(Plan(op: store.Op.Set, key: key, value: value, reply: Reply.Old)) }
        .Del(key) {
            g := try st.store.LockRead(c.io)
            defer g.Unlock()
            if g.Ref().Get(key).IsNone() {
                return none
            }
            some(Plan(op: store.Op.Del, key: key, value: "", reply: Reply.Count(n: 1)))
        }
        .Incr(key) {
            n := try Current(c, st, key)
            next := try arena.Fmt("{v}", (v: n + 1))
            some(Plan(op: store.Op.Set, key: key, value: next, reply: Reply.Count(n: n + 1)))
        }
        _ { none }
    }
}

/// The integer value of `key`, 0 when it is missing.
Current(c: &Conn, st: &State, key: str): NotAnInteger|Canceled!i64 {
    g := try st.store.LockRead(c.io)
    defer g.Unlock()
    text := g.Ref().Get(key) else {
        return 0
    }
    return text.ParseInt() else {
        fail NotAnInteger
    }
}

/// Handles a change the log refused. `Crashed` is the simulator's crash:
/// the server stops with no reply, as a process that died would.
Failed(c: &Conn, st: &State, w: mut Writer, e: wal.Error): WriteError!void {
    match e {
        wal.Crashed(change) {
            st.down.Store(1)
            st.Report(c.io, Event.Crashed(change: change))
            st.Stop(c.io)
        }
        _ {
            _ = st.failures.Add(1)
            st.Report(c.io, Event.Rejected(id: c.id, cause: e))
            // a failed write leaves the log broken; compaction rewrites it
            st.wake.NotifyOne()
            try proto.Problem(w, e)
        }
    }
}

/// Compacts the log now. Returns false when it failed, which is reported;
/// a simulated crash stops the server (`State.Stop`).
export CompactNow(st: &State, heap: &Heap, io: Io): Canceled!bool {
    var arena := Arena.New(heap) else {
        st.Report(io, Event.CompactFailed(cause: OutOfMemory))
        return false
    }
    defer arena.Free()
    var wg := try st.wal.Lock(io)
    defer wg.Unlock()
    g := try st.store.LockRead(io)
    defer g.Unlock()
    wg.Mut().Compact(&arena, io, g.Ref()) else e {
        match e {
            wal.Crashed(change) {
                st.down.Store(1)
                st.Report(io, Event.Crashed(change: change))
                st.Stop(io)
            }
            Canceled {
                // the server is stopping, and closing it compacts once more;
                // any other cancel is the caller's to see
                if !st.IsStopping() {
                    fail Canceled
                }
            }
            _ {
                st.Report(io, Event.CompactFailed(cause: e))
            }
        }
        return false
    }
    st.pending.Store(0)
    records := wg.Ref().Records()
    bytes := wg.Ref().Size()
    var last := try st.last.Lock(io)
    defer last.Unlock()
    count := last.Ref().count + 1
    last.Store(Compaction(records: records, bytes: bytes, count: count))
    st.Report(io, Event.Compacted(records: records, bytes: bytes, generation: wg.Ref().Generation()))
    return true
}

/// Waits for the signal that the log has grown, or broken, and compacts
/// it, until the server stops. Runs as a task.
export Compactor(s: &Scope, st: &State, heap: &Heap, io: Io) {
    for {
        st.wake.Wait(io) else {
            return
        }
        if st.stopping.Load() != 0 {
            return
        }
        _ = CompactNow(st, heap, io) else {
            return
        }
        if st.Crashed() {
            s.Cancel()
            return
        }
    }
}

/// `COMPACT`: compacts now.
Compact(c: &Conn, st: &State): Canceled!bool {
    return try CompactNow(st, c.heap, c.io)
}

/// `HASH key`: the value's xxHash-64 and its SipHash-1-3 under a fixed key,
/// for a client to check a copy against.
HashReply(c: &Conn, st: &State, w: mut Writer, key: str): Canceled|WriteError!void {
    g := try st.store.LockRead(c.io)
    defer g.Unlock()
    v := g.Ref().Get(key) else {
        try proto.Nil(w)
        return
    }
    xx := hash.Xx(v.AsBytes())
    sip := hash.Sip13(v.AsBytes(), HashKey.Of(0x6B7664, 0x68617368))
    try Print(w, "$xx={xx:016x} sip={sip:016x}\n", (xx, sip))
}

/// `STATS`: the server's counters, one `name value` line each.
Stats(c: &Conn, st: &State, w: mut Writer): Canceled|WriteError!void {
    keys := try Keys(c, st)
    wg := try st.wal.Lock(c.io)
    records := wg.Ref().Records()
    generation := wg.Ref().Generation()
    wg.Unlock()
    last := try st.last.Lock(c.io)
    compactions := last.Ref().count
    last.Unlock()
    clients := try ClientCount(c, st)
    try proto.Array(w, 8)
    try Print(w, "$clients {n}\n", (n: clients))
    try Print(w, "$accepted {n}\n", (n: st.accepted.Load()))
    try Print(w, "$commands {n}\n", (n: st.commands.Load()))
    try Print(w, "$changes {n}\n", (n: st.changes.Load()))
    try Print(w, "$failures {n}\n", (n: st.failures.Load()))
    try Print(w, "$keys {n}\n", (n: keys))
    try Print(w, "$log_records {records}\n", (records))
    try Print(w, "$compactions {compactions} generation {generation}\n", (compactions, generation))
}

/// The number of keys.
Keys(c: &Conn, st: &State): Canceled!int {
    g := try st.store.LockRead(c.io)
    defer g.Unlock()
    return g.Ref().Len()
}

/// The number of clients in the table.
ClientCount(c: &Conn, st: &State): Canceled!int {
    var g := try st.sessions.Lock(c.io)
    defer g.Unlock()
    return g.Ref().Len()
}

/// `CLIENTS`: one `name commands` line per client, by name; the asking
/// client's line ends with `(you)`. A client with no name shows `-`.
Clients(c: &Conn, st: &State, arena: &Arena, w: mut Writer): Canceled|OutOfMemory|WriteError!void {
    var g := try st.sessions.Lock(c.io)
    defer g.Unlock()
    var rows := List[str].New(arena)
    for (key: id, value: s) in g.Ref().Iter() {
        name := if s.name.Len() == 0 { "-" } else { s.name.Str() }
        me := if *id == c.id { " (you)" } else { "" }
        try rows.Push(try arena.Fmt("{name} {n}{me}", (name, n: s.commands, me)))
    }
    rows.Sort(fn(x: &str, y: &str): bool { return *x < *y })
    try proto.Array(w, rows.Len())
    for row in rows {
        try proto.Bulk(w, row)
    }
}
