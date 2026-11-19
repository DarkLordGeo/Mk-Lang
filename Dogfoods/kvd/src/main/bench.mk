// Why: a rough load test of the server that needs no second machine or
// process.
// What: `kvd bench`: RunBench, Bench, Generate and Report (request kinds and
// a latency histogram).
// How: a load generator against an in-process server. One task holds many
// connections in a map and sends requests round-robin, each answered before
// the next goes to that connection; halfway it replaces connection 0 with a
// fresh one, as a client that reconnects does.

import std.log
import std.net
import std.sync
import std.time
import src.client
import src.server
import src.wal

/// The flags `kvd bench` takes.
const BenchFlags: Accepts = Accepts(values: ["--clients", "--requests", "--data"], switches: ["--verbose"])

/// What the load generator counted.
struct Load {
    sent: int
    failed: int
    report: HeapText
}

/// Reads one reply line from `conn` into `buf` with plain reads: replies to
/// these requests are one line, and the next request waits for this reply.
ReadReply(io: Io, conn: &net.Stream, buf: mut []u8): net.Error|client.Disconnected!str from buf {
    var n := 0
    for n == 0 || buf[n - 1] != b'\n' {
        if n == buf.Len() {
            fail client.Disconnected
        }
        got := try conn.Read(io, mut buf[n..])
        if got == 0 {
            fail client.Disconnected
        }
        n += got
    }
    return str.From(buf[..n - 1]) else { "" }
}

/// The latency bucket of `micros`: the power of two at or above it.
Bucket(micros: i64): i64 {
    var b: i64 = 1
    for b < micros {
        b *= 2
    }
    return b
}

/// Sends `requests` rounds over `clients` connections and writes a report
/// into `out`. Runs as a task, with an `Io` the server's shutdown does not
/// cancel.
Generate(heap: &Heap into out, io: Io, addr: str, clients: int, requests: int, verbose: bool, out: mut Load) {
    a := heap.Alloc()
    var arena := Arena.New(heap) else {
        return
    }
    defer arena.Free()
    var conns := HeapMap[u64, net.Stream].NewKeyed(a, HashKey.Of(0x6B7664, 0x62656E63))
    defer conns.FreeEach(a, fn(id: u64, c: net.Stream) { c.Close() })
    // a connection opened ahead, which the reconnect halfway takes
    var spares := TextMap[net.Stream].New(a)
    defer spares.FreeEach(a, fn(c: net.Stream) { c.Close() })
    var kinds := TextMap[int].New(a)
    var latency := HeapMap[i64, int].New(a)
    defer latency.Free(a)
    for i in 0..clients {
        conn := client.Connect(io, addr, 5) else {
            out.failed += 1
            continue
        }
        old := conns.Replace(a, Cast[u64](i), conn) else {
            conn.Close()
            out.failed += 1
            continue
        }
        if c := old {
            c.Close()
        }
    }
    if !OpenSpare(a, io, addr, mut spares) {
        out.failed += 1
    }
    var buf: [256]u8 = [0; 256]
    for r in 0..requests {
        if r == requests / 2 {
            Reconnect(a, io, addr, mut conns, mut spares)
        }
        for i in 0..clients {
            conn := conns.Get(&Cast[u64](i)) else {
                continue
            }
            request := Request(&arena, i, r) else {
                out.failed += 1
                continue
            }
            start := time.Now(io)
            out.sent += 1
            conn.WriteAll(io, request.AsBytes()) else {
                out.failed += 1
                continue
            }
            reply := ReadReply(io, conn, mut buf[..]) else {
                out.failed += 1
                continue
            }
            micros := time.Now(io).Since(start).Nanos() / time.Micros(1).Nanos()
            n := latency.Ensure(a, Bucket(micros), 0) else {
                continue
            }
            *n += 1
            kind := if reply.Len() == 0 { "?" } else { reply[..1] }
            seen := kinds.Ensure(a, kind, 0) else {
                continue
            }
            *seen += 1
        }
        arena.Reset()
    }
    var t := Text.New(&arena)
    var tw := t.Writer()
    Report(mut tw, a, kinds, &latency, verbose)
    out.report.Set(a, t.Str()) else {}
}

/// The request of round `r` for client `i`: writes and reads on its keys.
Request(a: &Arena, i: int, r: int): OutOfMemory!str from a {
    return match r % 4 {
        0 { try a.Fmt("SET b{i}:k{r} value-{r}\n", (i, r)) }
        1 { try a.Fmt("INCR b{i}:n{r}\n", (i, r)) }
        2 { try a.Fmt("APPEND b{i}:log {r},\n", (i, r)) }
        _ { try a.Fmt("GET b{i}:k{k}\n", (i, k: r - 3)) }
    }
}

/// Opens the spare connection; false when it cannot.
OpenSpare(a: &Alloc, io: Io, addr: str, spares: mut TextMap[net.Stream]): bool {
    spare := client.Connect(io, addr, 5) else {
        return false
    }
    prior := spares.Replace(a, "spare", spare) else {
        spare.Close()
        return false
    }
    if c := prior {
        c.Close()
    }
    return true
}

/// Swaps connection 0 for the spare, or a fresh one, and closes the old one.
Reconnect(a: &Alloc, io: Io, addr: str, conns: mut HeapMap[u64, net.Stream], spares: mut TextMap[net.Stream]) {
    fresh := spares.Remove(a, "spare") else {
        conn := client.Connect(io, addr, 5) else {
            return
        }
        conn
    }
    old := conns.Replace(a, 0, fresh) else {
        fresh.Close()
        return
    }
    if c := old {
        c.Close()
    }
}

/// Writes how many replies of each kind came, and with `verbose` the
/// latency histogram, which depends on the machine. Frees `kinds`, which
/// holds no kind of reply but those named here.
Report(w: mut Writer, a: &Alloc, kinds: TextMap[int], latency: &HeapMap[i64, int], verbose: bool) {
    var left := kinds
    for name in ["+", ":", "$", "_", "-", "?"] {
        n := left.Remove(a, name) else { 0 }
        Print(w, "replies {name} {n}\n", (name, n)) else {}
    }
    left.FreeEmpty(a)
    if !verbose {
        return
    }
    var b: i64 = 1
    var shown := 0
    for shown < latency.Len() {
        if n := latency.Get(&b) {
            Print(w, "latency <= {b} us: {n}\n", (b, n: *n)) else {}
            shown += 1
        }
        b *= 2
    }
}

/// Runs the load generator against an in-process server, then stops it.
/// Runs as a task.
Bench(s: &Scope, st: &server.State, heap: &Heap into out, io: Io, addr: str, clients: int, requests: int, verbose: bool, out: mut Load) {
    outside := io.NoCancel()
    scope c := outside.Scope() {
        c.go Generate(heap, outside, addr, clients, requests, verbose, out)
    }
    if !StopServer(outside, addr) {
        st.Stop(io)
        s.Cancel()
    }
}

/// `kvd bench`: prints the requests sent and the replies by kind; the time
/// it takes is the harness's to measure.
RunBench(sys: &System, rest: []str): int *rest into sys {
    io := sys.io
    a := sys.heap.Alloc()
    args := ParseArgs(sys.arena, rest, &BenchFlags) else e {
        return UsageError(sys, e)
    }
    clients := args.Int("--clients", 1, 8) else e {
        return UsageError(sys, e)
    }
    requests := args.Int("--requests", 1, 200) else e {
        return UsageError(sys, e)
    }
    p := DataDir(sys.arena, io, args.Str("--data", "kvd-bench"), none) else e {
        sys.PrintErr("kvd: data directory: {e}\n", (e)) else {}
        return ExitFailure
    }
    key := HashKey.New(io) else e {
        sys.PrintErr("kvd: no random hash key: {e}\n", (e)) else {}
        return ExitFailure
    }
    opened := server.Open(a, sys.arena, io, p, key, wal.SyncPolicy.Always, wal.NoCrash, Cast[int](clients) + 1) else e {
        return OpenFailure(sys, &p, e)
    }
    server.Opened(state: st, ..) := opened
    l := net.Listen(io, "127.0.0.1:0") else e {
        sys.PrintErr("kvd: listen: {e}\n", (e)) else {}
        Discard(sys.heap, io, st)
        return ExitFailure
    }
    addr := sys.arena.Fmt("127.0.0.1:{port}", (port: l.Port())) else {
        l.Close()
        Discard(sys.heap, io, st)
        return ExitMemory
    }
    var cfg := server.Config.Default()
    cfg.compact_after = 500
    cfg.max_clients = Cast[int](clients) + 1
    shared := sync.Shared.New(sys.heap, cfg) else {
        l.Close()
        Discard(sys.heap, io, st)
        return ExitMemory
    }
    var load := Load(sent: 0, failed: 0, report: HeapText.New(a))
    var errw := sys.Stderr()
    var lg := log.Logger.New(mut errw, log.Level.Warn)
    scope s := io.Scope() {
        s.go server.Accept(&st, sys.heap, shared.Clone(), &l, io)
        s.go server.Compactor(&s, &st, sys.heap, io)
        s.go server.Watch(&s, &st, io)
        s.go Bench(&s, &st, sys.heap, io, addr, Cast[int](clients), Cast[int](requests), args.Has("--verbose"), mut load)
        server.Pump(&st, sys.heap, io, mut lg)
    }
    server.Drain(&st, sys.heap, io, mut lg)
    shared.Free(sys.heap)
    l.Close()
    Load(sent, failed, report) := load
    sys.Print("bench: {clients} connections, {sent} requests, {failed} failed\n{r}", (clients, sent, failed, r: report.Str())) else {}
    report.Free(a)
    code := Finish(sys, sys.heap, io, st)
    return if failed > 0 { ExitFailure } else { code }
}
