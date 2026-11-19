// Why: shows a server and its clients working together in one command,
// with output that is the same on every run.
// What: `kvd demo`: RunDemo, the scripted DemoClient tasks, a Waiter, an
// Operator on the admin channel, and Contend for the instance lock.
// How: a server and scripted clients in one process, over loopback. Every
// client works on keys of its own, so each transcript is the same however
// the clients interleave; they are printed in client order once all are done.

import std.fs
import std.log
import std.net
import std.sync
import src.client
import src.server
import src.wal

/// The most clients a demo runs.
const MaxDemoClients: int = 32

/// One demo client's script; `{u}` is its own key prefix.
const Script: []str = [
    "NAME client",
    "SET user:name Ada Lovelace",
    "GET user:name",
    "INCR user:visits",
    "INCR user:visits",
    "INCR user:visits",
    "APPEND user:log started",
    "APPEND user:log ,stopped",
    "GETSET user:name Grace Hopper",
    "GET user:name",
    "INCR user:name",
    "DEL user:missing",
    "GET user:missing",
    "HASH user:log",
    "SET user:bad\tkey value",
    "KEYS user:",
]

/// The request line `line` of the script, for client `i`.
ScriptLine(a: &Arena, line: str, i: int): OutOfMemory!str from a, line {
    at := line.Find("user:") else {
        if line.StartsWith("NAME ") {
            return try a.Fmt("{line}{i}", (line, i))
        }
        return line
    }
    return try a.Fmt("{head}u{i}:{tail}", (head: line[..at], i, tail: line[at + 5..]))
}

/// One client: its script, then QUIT, or with `hold` a wait at the barrier
/// and then a read of what the server says when it stops. Runs as a task;
/// the transcript goes into `out[0]`, owned by the caller's allocator.
DemoClient(heap: &Heap into out, io: Io, addr: str, i: int, hold: bool, gate: &sync.Barrier, out: mut []HeapText) {
    a := heap.Alloc()
    var arena := Arena.New(heap) else {
        return
    }
    defer arena.Free()
    var t := Text.New(&arena)
    var tw := t.Writer()
    Script1(&arena, io, addr, i, hold, gate, mut tw) else e {
        Print(mut tw, "! {e}\n", (e)) else {}
    }
    out[0].Set(a, t.Str()) else {}
}

/// The body of `DemoClient`.
Script1(arena: &Arena, io: Io, addr: str, i: int, hold: bool, gate: &sync.Barrier, out: mut Writer): client.Error!void {
    conn := try client.Connect(io, addr, 5)
    defer conn.Close()
    var inbuf: [4096]u8 = [0; 4096]
    var outbuf: [4096]u8 = [0; 4096]
    var r := conn.Reader(io, mut inbuf[..])
    var w := conn.Writer(io, mut outbuf[..])
    for line in Script {
        request := try ScriptLine(arena, line, i)
        _ = try Exchange(arena, mut r, mut w, request, out, true)
    }
    if !hold {
        _ = try Exchange(arena, mut r, mut w, "QUIT", out, true)
        _ = try gate.Wait(io)
        return
    }
    _ = try gate.Wait(io)
    // held open while the server shuts down: it says so, then closes
    got := try r.Line(arena)
    if line := got {
        try Print(out, "{line}\n", (line))
    }
}

/// Runs the clients and the operator, who waits for them at the barrier,
/// What a demo shows beyond its clients' scripts.
struct Show {
    /// The clients stay connected until the server stops.
    hold: bool
    /// The admin channel's path: the operator stops the server through it.
    admin: ?str
    /// A waiter client is dropped after this long idle, in milliseconds.
    idle_millis: i64
    /// A waiter client's `WAIT` gives up after this long, in milliseconds.
    request_millis: i64
}

/// Whether the demo runs a waiter client.
Show.Waiter(&self): bool {
    return self.idle_millis > 0 || self.request_millis > 0
}

/// Runs the waiter alone first, so it is always the first connection, then
/// the clients and the operator, who waits for them at the barrier; then
/// stops the server if the operator could not. Runs as a task.
Conductor(s: &Scope, st: &server.State, heap: &Heap into outs, io: Io, addr: str, show: &Show, gate: &sync.Barrier, outs: mut []HeapText) {
    n := outs.Len() - 1
    (left: clients, right: last) := outs.SplitAt(n)
    // the clients stand for other processes: the server's shutdown does not
    // cancel them, it closes their connections
    outside := io.NoCancel()
    (left: front, right: back) := clients.SplitAt(if show.Waiter() { 1 } else { 0 })
    if show.Waiter() {
        scope w := outside.Scope() {
            w.go Waiter(heap, outside, addr, show, front)
        }
    }
    scope c := outside.Scope() {
        var i := 0
        for piece in back.ChunksMut(1) {
            c.go DemoClient(heap, outside, addr, i, show.hold, gate, piece)
            i += 1
        }
        c.go Operator(heap, outside, addr, show, gate, last)
    }
    if !st.Crashed() && !st.IsStopping() {
        st.Stop(io)
        s.Cancel()
    }
}

/// The waiter: a `WAIT` that the request limit ends and one that is
/// answered, then silence until the server drops it as idle. Its transcript
/// goes into `out[0]`.
Waiter(heap: &Heap into out, io: Io, addr: str, show: &Show, out: mut []HeapText) {
    a := heap.Alloc()
    var arena := Arena.New(heap) else {
        return
    }
    defer arena.Free()
    var t := Text.New(&arena)
    var tw := t.Writer()
    Wait1(&arena, io, addr, show, mut tw) else e {
        Print(mut tw, "! {e}\n", (e)) else {}
    }
    out[0].Set(a, t.Str()) else {}
}

/// The body of `Waiter`.
Wait1(arena: &Arena, io: Io, addr: str, show: &Show, out: mut Writer): client.Error!void {
    conn := try client.Connect(io, addr, 5)
    defer conn.Close()
    var inbuf: [4096]u8 = [0; 4096]
    var outbuf: [4096]u8 = [0; 4096]
    var r := conn.Reader(io, mut inbuf[..])
    var w := conn.Writer(io, mut outbuf[..])
    _ = try Exchange(arena, mut r, mut w, "NAME waiter", out, true)
    if show.request_millis > 0 {
        _ = try Exchange(arena, mut r, mut w, "WAIT waiter:never", out, true)
        _ = try Exchange(arena, mut r, mut w, "SET waiter:flag up", out, true)
        _ = try Exchange(arena, mut r, mut w, "WAIT waiter:flag", out, true)
    }
    if show.idle_millis == 0 {
        _ = try Exchange(arena, mut r, mut w, "QUIT", out, true)
        return
    }
    // silent from here: the server's idle limit ends the connection
    try Print(out, "(idle)\n")
    for {
        got := try r.Line(arena)
        line := got else {
            try Print(out, "(closed)\n")
            return
        }
        try Print(out, "{line}\n", (line))
    }
}

/// The operator's session, written into `out[0]`.
Operator(heap: &Heap into out, io: Io, addr: str, show: &Show, gate: &sync.Barrier, out: mut []HeapText) {
    a := heap.Alloc()
    var arena := Arena.New(heap) else {
        return
    }
    defer arena.Free()
    var t := Text.New(&arena)
    var tw := t.Writer()
    Operate(&arena, io, addr, show, gate, mut tw) else e {
        Print(mut tw, "! {e}\n", (e)) else {}
    }
    out[0].Set(a, t.Str()) else {}
}

/// The body of `Operator`. With an admin channel, the commands that stop
/// or compact the server go through it, and the TCP connection is refused
/// them.
Operate(arena: &Arena, io: Io, addr: str, show: &Show, gate: &sync.Barrier, out: mut Writer): client.Error!void {
    // every client has run its script, and has quit unless held
    _ = try gate.Wait(io)
    conn := try client.Connect(io, addr, 5)
    defer conn.Close()
    var inbuf: [4096]u8 = [0; 4096]
    var outbuf: [4096]u8 = [0; 4096]
    var r := conn.Reader(io, mut inbuf[..])
    var w := conn.Writer(io, mut outbuf[..])
    _ = try Exchange(arena, mut r, mut w, "NAME operator", out, true)
    _ = try Exchange(arena, mut r, mut w, "DBSIZE", out, true)
    path := show.admin else {
        _ = try Exchange(arena, mut r, mut w, "COMPACT", out, true)
        if show.hold {
            _ = try Exchange(arena, mut r, mut w, "STATS", out, true)
            _ = try Exchange(arena, mut r, mut w, "CLIENTS", out, true)
        }
        _ = try Exchange(arena, mut r, mut w, "NAME -", out, true)
        _ = try Exchange(arena, mut r, mut w, "SHUTDOWN", out, true)
        return
    }
    _ = try Exchange(arena, mut r, mut w, "SHUTDOWN", out, true)
    _ = try Exchange(arena, mut r, mut w, "QUIT", out, true)
    try AdminSay(arena, io, path, "COMPACT", out)
    if show.hold {
        try AdminSay(arena, io, path, "STATS", out)
    }
    try AdminSay(arena, io, path, "SHUTDOWN", out)
}

/// Sends one command over the admin channel and writes it and its reply
/// to `out`; then QUIT, unless the command ends the session itself, so the
/// server never waits for the stream's end (a Unix stream on Windows may
/// not report it).
AdminSay(arena: &Arena, io: Io, path: str, request: str, out: mut Writer): client.Error!void {
    conn := try client.ConnectAdmin(io, path)
    defer conn.Close()
    var inbuf: [4096]u8 = [0; 4096]
    var outbuf: [4096]u8 = [0; 4096]
    var r := conn.Reader(io, mut inbuf[..])
    var w := conn.Writer(io, mut outbuf[..])
    try Print(out, "admin> {request}\n", (request))
    _ = try Exchange(arena, mut r, mut w, request, out, false)
    if request != "SHUTDOWN" {
        _ = try Exchange(arena, mut r, mut w, "QUIT", out, false)
    }
}

/// Tries to open the data directory `d` a second time, as a second server
/// would, and says what happened: the instance lock refuses it.
Contend(sys: &System, d: wal.Dir, key: HashKey) {
    var scratch := sys.arena.Sub() else {
        return
    }
    defer scratch.Free()
    opened := server.Open(sys.heap.Alloc(), &scratch, sys.io, d, key, wal.SyncPolicy.Always, wal.NoCrash, 1) else e {
        sys.Print("--- a second server on the same directory: {e}\n", (e)) else {}
        return
    }
    sys.Print("--- a second server on the same directory: opened\n") else {}
    server.Opened(state: st, ..) := opened
    Discard(sys.heap, sys.io, st)
}

/// The flags `kvd demo` takes.
const DemoFlags: Accepts = Accepts(
    values: ["--clients", "--data", "--compact-after", "--admin", "--idle-ms", "--request-ms", "--sync", "--log", "--rounds", "--lock"],
    switches: ["--hold", "--verbose", "--contend", "--log-time"],
)

/// `kvd demo`: prints each client's transcript, then the operator's, then
/// the keys the log holds after the server stopped; with `--rounds N`, all
/// of it N times over the same data directory, each round recovering what
/// the last one left.
RunDemo(sys: &System, rest: []str): int *rest into sys {
    args := ParseArgs(sys.arena, rest, &DemoFlags) else e {
        return UsageError(sys, e)
    }
    n := args.Int("--clients", 1, 3) else e {
        return UsageError(sys, e)
    }
    compact := args.Int("--compact-after", 1, 10) else e {
        return UsageError(sys, e)
    }
    rounds := args.Int("--rounds", 1, 1) else e {
        return UsageError(sys, e)
    }
    idle := args.Int("--idle-ms", 0, 0) else e {
        return UsageError(sys, e)
    }
    request := args.Int("--request-ms", 0, 0) else e {
        return UsageError(sys, e)
    }
    show := Show(hold: args.Has("--hold"), admin: args.Get("--admin"), idle_millis: idle, request_millis: request)
    if n > i64(MaxDemoClients) {
        sys.PrintErr("kvd: at most {m} demo clients\n", (m: MaxDemoClients)) else {}
        return ExitUsage
    }
    policy := PolicyNamed(args.Str("--sync", "always")) else {
        sys.PrintErr("kvd: --sync takes always, never or a number of changes\n") else {}
        return ExitUsage
    }
    level := if args.Has("--verbose") { log.Level.Debug } else { log.Level.Warn }
    sink := SinkNamed(args.Str("--log", "stderr"), level) else {
        sys.PrintErr("kvd: --log takes stderr or stdout\n") else {}
        return ExitUsage
    }
    p := DataDir(sys.arena, sys.io, args.Str("--data", "kvd-demo"), args.Get("--lock")) else e {
        sys.PrintErr("kvd: data directory: {e}\n", (e)) else {}
        return ExitFailure
    }
    var cfg := server.Config.Default()
    cfg.compact_after = Cast[int](compact)
    cfg.idle_millis = idle
    cfg.request_millis = request
    cfg.admin_only = show.admin.IsSome()
    for round in 0..rounds {
        if rounds > 1 {
            sys.Print("=== round {r}\n", (r: round + 1)) else {}
        }
        code := Round(sys, &p, &show, &cfg, Cast[int](n), policy, &sink, args.Has("--log-time"), args.Has("--contend"))
        if code != 0 {
            return code
        }
    }
    return 0
}

/// One round of `kvd demo`: recover, serve the clients, stop, report.
Round(sys: &System, p: &wal.Dir, show: &Show, cfg: &server.Config, n: int, policy: wal.SyncPolicy, sink: &log.Sink, stamp: bool, contend: bool): int {
    io := sys.io
    a := sys.heap.Alloc()
    key := HashKey.New(io) else e {
        sys.PrintErr("kvd: no random hash key: {e}\n", (e)) else {}
        return ExitFailure
    }
    opened := server.Open(a, sys.arena, io, *p, key, policy, wal.NoCrash, MaxDemoClients + 2) else e {
        return OpenFailure(sys, p, e)
    }
    server.Opened(state: st, recovered) := opened
    ReportRecovery(sys, p.log, &recovered)
    if contend {
        Contend(sys, *p, key)
    }
    var o := net.DefaultListenOptions
    o.reuse_addr = true
    l := net.ListenWith(io, "127.0.0.1:0", o) else e {
        sys.PrintErr("kvd: listen: {e}\n", (e)) else {}
        Discard(sys.heap, io, st)
        return ExitFailure
    }
    addr := sys.arena.Fmt("127.0.0.1:{port}", (port: l.Port())) else {
        l.Close()
        Discard(sys.heap, io, st)
        return ExitMemory
    }
    shared := sync.Shared.New(sys.heap, *cfg) else {
        l.Close()
        Discard(sys.heap, io, st)
        return ExitMemory
    }
    waiters := if show.Waiter() { 1 } else { 0 }
    var outs := HeapList[HeapText].New(a)
    for _ in 0..n + waiters + 1 {
        t := HeapText.New(a)
        outs.Push(a, t) else {
            t.Free(a)
            outs.Free(a)
            shared.Free(sys.heap)
            l.Close()
            Discard(sys.heap, io, st)
            return ExitMemory
        }
    }
    gate := sync.Barrier.New(n + 1)
    if path := show.admin {
        ashared := sync.Shared.New(sys.heap, AdminConfig(cfg)) else {
            outs.Free(a)
            shared.Free(sys.heap)
            l.Close()
            Discard(sys.heap, io, st)
            return ExitMemory
        }
        al := OpenAdmin(io, path) else e {
            // through the log, as a server that cannot open its admin channel reports it
            sink.Error(io, "admin socket {path}: {e}", (path, e)) else {}
            ashared.Free(sys.heap)
            outs.Free(a)
            shared.Free(sys.heap)
            l.Close()
            Discard(sys.heap, io, st)
            return ExitFailure
        }
        scope s := io.Scope() {
            s.go server.Accept(&st, sys.heap, shared.Clone(), &l, io)
            s.go server.Compactor(&s, &st, sys.heap, io)
            s.go server.Watch(&s, &st, io)
            s.go Conductor(&s, &st, sys.heap, io, addr, show, &gate, mut outs[..])
            s.go server.PumpTo(&st, sys.heap, io, sink, stamp)
            server.Admin(&st, sys.heap, &ashared, &al, io)
        }
        al.Close()
        fs.Remove(io, path) else {}
        ashared.Free(sys.heap)
    } else {
        scope s := io.Scope() {
            s.go server.Accept(&st, sys.heap, shared.Clone(), &l, io)
            s.go server.Compactor(&s, &st, sys.heap, io)
            s.go server.Watch(&s, &st, io)
            s.go Conductor(&s, &st, sys.heap, io, addr, show, &gate, mut outs[..])
            server.PumpTo(&st, sys.heap, io, sink, stamp)
        }
    }
    server.DrainTo(&st, sys.heap, io, sink, stamp)
    shared.Free(sys.heap)
    l.Close()
    for i, t in &outs {
        who := if i == outs.Len() - 1 { "operator" } else if waiters == 1 && i == 0 { "waiter" } else { "client" }
        sys.Print("--- {who} {i}\n{t}", (who, i, t: t.Str())) else {}
    }
    outs.Free(a)
    closed := server.Close(st, sys.heap, io)
    server.Closed(store: final, crashed, failure) := closed
    defer final.Free(a)
    _ = crashed
    if e := failure {
        return LogFailure(sys, "closing the log", e)
    }
    keys := final.Keys(sys.arena, "") else {
        return ExitMemory
    }
    sys.Print("--- the log holds {k} keys, digest {d:016x}\n", (k: keys.Len(), d: final.Digest())) else {}
    for k in keys {
        sys.Print("{k} = {v}\n", (k, v: final.Get(k) else { "" })) else {}
    }
    return 0
}
