// Why: the server as an operator starts it, on a real socket with its data
// directory, logging and admin channel.
// What: `kvd serve` (RunServe) and its helpers: DataDir, ConfigFrom,
// ReportRecovery, OpenAdmin and Finish; `kvd demo` uses the same pieces.
// How: opens the data directory under its instance lock, recovers the store,
// runs the accept, admin, compaction and signal tasks in one scope, and
// writes the log from the caller's task.

import std.fs
import std.log
import std.net
import std.sync
import src.server
import src.snap
import src.wal

/// The files of the data directory `dir`, made if it is missing; the
/// instance lock is at `lock` when it is given, else in `dir`.
DataDir(a: &Arena, io: Io, dir: str, lock: ?str): fs.Error|OutOfMemory!wal.Dir from a, lock {
    fs.MakeDir(io, dir) else e {
        match e {
            fs.Exists {}
            _ {
                fail e
            }
        }
    }
    return try wal.Dir.In(a, dir, lock)
}

/// The log level a `--log-level` name stands for.
LevelNamed(name: str): ?log.Level {
    return match name {
        "debug" { some(log.Level.Debug) }
        "info" { some(log.Level.Info) }
        "warn" { some(log.Level.Warn) }
        "error" { some(log.Level.Error) }
        _ { none }
    }
}

/// The sync policy a `--sync` value names: `always`, `never`, or a count
/// of changes between syncs.
PolicyNamed(text: str): ?wal.SyncPolicy {
    return match text {
        "always" { some(wal.SyncPolicy.Always) }
        "never" { some(wal.SyncPolicy.Never) }
        _ {
            n := text.ParseInt() else {
                return none
            }
            if n < 1 || n > 1_000_000 {
                return none
            }
            some(wal.SyncPolicy.Every(n: Cast[int](n)))
        }
    }
}

/// The sink a `--log` name stands for, at `level`.
SinkNamed(name: str, level: log.Level): ?log.Sink {
    return match name {
        "stderr" { some(log.Stderr(level)) }
        "stdout" { some(log.Stdout(level)) }
        _ { none }
    }
}

/// The flags `kvd serve` takes.
const ServeFlags: Accepts = Accepts(
    values: ["--addr", "--data", "--port-file", "--max-key", "--max-value", "--compact-after", "--max-clients", "--slow-ms", "--log-level", "--log", "--sync", "--idle-ms", "--request-ms", "--admin", "--lock"],
    switches: ["--log-time"],
)

/// The server settings from the command line.
ConfigFrom(args: &Args): BadNumber!server.Config {
    d := server.Config.Default()
    return server.Config(
        max_key: Cast[int](try args.Int("--max-key", 1, i64(d.max_key))),
        max_value: Cast[int](try args.Int("--max-value", 0, i64(d.max_value))),
        compact_after: Cast[int](try args.Int("--compact-after", 1, i64(d.compact_after))),
        slow_micros: try args.Int("--slow-ms", 0, d.slow_micros / 1000) * 1000,
        max_clients: Cast[int](try args.Int("--max-clients", 1, i64(d.max_clients))),
        idle_millis: try args.Int("--idle-ms", 0, d.idle_millis),
        request_millis: try args.Int("--request-ms", 0, d.request_millis),
        admin: false,
        admin_only: args.Get("--admin").IsSome(),
    )
}

/// Prints what recovery found, on stderr.
ReportRecovery(sys: &System, path: str, r: &wal.Recovered) {
    how := if r.clean { "clean shutdown" } else { "after a crash" }
    if info := r.snapshot {
        stale := if r.stale { ", the log's records already in it" } else { "" }
        sys.PrintErr("kvd: snapshot of generation {info.generation}: {info.count} keys{stale}\n", (info, stale)) else {}
    }
    sys.PrintErr("kvd: recovered {path}: {n} records, generation {g}, {how}, {end}\n", (path, n: r.records, g: r.generation, how, end: r.end)) else {}
}

/// Reports a log error with the exit code it maps to.
LogFailure(sys: &System, what: str, e: wal.Error): int {
    sys.PrintErr("kvd: {what}: {e}\n", (what, e)) else {}
    return match e {
        wal.Corrupt | wal.BadHeader | snap.NotSnapshot | snap.Damaged { ExitCorrupt }
        OutOfMemory { ExitMemory }
        _ { ExitFailure }
    }
}

/// Reports why the data directory `d` could not be opened, naming the file
/// at fault, with the exit code it maps to.
OpenFailure(sys: &System, d: &wal.Dir, e: wal.Error): int {
    return match e {
        snap.NotSnapshot | snap.Damaged { LogFailure(sys, d.snap, e) }
        wal.InUse {
            sys.PrintErr("kvd: {d.lock}: another server holds this data directory\n", (d)) else {}
            ExitFailure
        }
        _ { LogFailure(sys, d.log, e) }
    }
}

/// `kvd serve`: recovers the log, serves until SHUTDOWN or a signal, then
/// compacts the log and marks it clean.
RunServe(sys: &System, rest: []str): int *rest into sys {
    io := sys.io
    a := sys.heap.Alloc()
    args := ParseArgs(sys.arena, rest, &ServeFlags) else e {
        return UsageError(sys, e)
    }
    cfg := ConfigFrom(&args) else e {
        return UsageError(sys, e)
    }
    addr := args.Str("--addr", "127.0.0.1:7379")
    dir := args.Str("--data", "kvd-data")
    level_name := args.Str("--log-level", "info")
    if args.plain.Len() > 0 {
        sys.PrintErr("kvd: serve takes no arguments; see kvd --help\n") else {}
        return ExitUsage
    }
    level := LevelNamed(level_name) else {
        sys.PrintErr("kvd: unknown log level {level_name}\n", (level_name)) else {}
        return ExitUsage
    }
    where := net.ParseAddr(addr) else e {
        sys.PrintErr("kvd: bad address {addr}: {e}\n", (addr, e)) else {}
        return ExitUsage
    }
    policy := PolicyNamed(args.Str("--sync", "always")) else {
        sys.PrintErr("kvd: --sync takes always, never or a number of changes\n") else {}
        return ExitUsage
    }
    sink := SinkNamed(args.Str("--log", "stderr"), level) else {
        sys.PrintErr("kvd: --log takes stderr or stdout\n") else {}
        return ExitUsage
    }
    p := DataDir(sys.arena, io, dir, args.Get("--lock")) else e {
        match e {
            fs.Os(code) {
                sys.PrintErr("kvd: data directory {dir}: system error {code}\n", (dir, code)) else {}
            }
            _ {
                sys.PrintErr("kvd: data directory {dir}: {e}\n", (dir, e)) else {}
            }
        }
        return ExitFailure
    }
    key := HashKey.New(io) else e {
        sys.PrintErr("kvd: no random hash key: {e}\n", (e)) else {}
        return ExitFailure
    }
    var scratch := sys.arena.Sub() else {
        return ExitMemory
    }
    opened := server.Open(a, &scratch, io, p, key, policy, wal.NoCrash, cfg.max_clients) else e {
        scratch.Free()
        return OpenFailure(sys, &p, e)
    }
    scratch.Free()
    server.Opened(state: st, recovered) := opened
    ReportRecovery(sys, p.log, &recovered)
    var o := net.DefaultListenOptions
    o.reuse_addr = true
    o.no_delay = true
    l := net.ListenWith(io, addr, o) else e {
        sys.PrintErr("kvd: listen on {addr}: {e}\n", (addr, e)) else {}
        Discard(sys.heap, io, st)
        return ExitFailure
    }
    bound := net.Addr(ip: where.ip, port: Cast[u16](l.Port()), v6: where.v6)
    sys.PrintErr("kvd: listening on {bound}\n", (bound)) else {}
    if file := args.Get("--port-file") {
        // for scripts that start a server on port 0 and then connect to it
        text := sys.arena.Fmt("{port}\n", (port: l.Port())) else { "" }
        fs.WriteAll(io, file, text.AsBytes()) else e {
            sys.PrintErr("kvd: {file}: {e}\n", (file, e)) else {}
        }
    }
    stamp := args.Has("--log-time")
    shared := sync.Shared.New(sys.heap, cfg) else {
        l.Close()
        Discard(sys.heap, io, st)
        return ExitMemory
    }
    path := args.Get("--admin") else {
        scope s := io.Scope() {
            s.go server.Accept(&st, sys.heap, shared.Clone(), &l, io)
            s.go server.Compactor(&s, &st, sys.heap, io)
            s.go server.Watch(&s, &st, io)
            server.PumpTo(&st, sys.heap, io, &sink, stamp)
        }
        server.DrainTo(&st, sys.heap, io, &sink, stamp)
        shared.Free(sys.heap)
        l.Close()
        return Finish(sys, sys.heap, io, st)
    }
    ashared := sync.Shared.New(sys.heap, AdminConfig(&cfg)) else {
        shared.Free(sys.heap)
        l.Close()
        Discard(sys.heap, io, st)
        return ExitMemory
    }
    al := OpenAdmin(io, path) else e {
        sys.PrintErr("kvd: admin socket {path}: {e}\n", (path, e)) else {}
        ashared.Free(sys.heap)
        shared.Free(sys.heap)
        l.Close()
        Discard(sys.heap, io, st)
        return ExitFailure
    }
    sys.PrintErr("kvd: admin channel on {path}\n", (path)) else {}
    scope s := io.Scope() {
        s.go server.Accept(&st, sys.heap, shared.Clone(), &l, io)
        s.go server.Compactor(&s, &st, sys.heap, io)
        s.go server.Watch(&s, &st, io)
        s.go server.PumpTo(&st, sys.heap, io, &sink, stamp)
        server.Admin(&st, sys.heap, &ashared, &al, io)
    }
    server.DrainTo(&st, sys.heap, io, &sink, stamp)
    ashared.Free(sys.heap)
    shared.Free(sys.heap)
    al.Close()
    fs.Remove(io, path) else {}
    l.Close()
    return Finish(sys, sys.heap, io, st)
}

/// Listens on the Unix-domain socket `path`, first removing a socket left
/// there by a server that did not stop: the caller holds the data
/// directory's instance lock, so no running server owns it.
OpenAdmin(io: Io, path: str): fs.Error|net.Error!net.Listener {
    fs.Remove(io, path) else e {
        match e {
            fs.NotFound {}
            _ {
                fail e
            }
        }
    }
    return try net.ListenUnix(io, path)
}

/// The settings of the admin channel's connections: what `cfg` says, with
/// the admin commands allowed and an idle limit of ten seconds when `cfg`
/// sets none, since on Windows a Unix stream may never see its peer leave.
AdminConfig(cfg: &server.Config): server.Config {
    var ac := *cfg
    ac.admin = true
    ac.admin_only = false
    if ac.idle_millis == 0 {
        ac.idle_millis = 10_000
    }
    return ac
}

/// Closes the state and reports how the log ended.
Finish(sys: &System, heap: &Heap, io: Io, st: server.State): int {
    closed := server.Close(st, heap, io)
    server.Closed(store: s, crashed, failure) := closed
    keys := s.Len()
    s.Free(heap.Alloc())
    if e := failure {
        return LogFailure(sys, "closing the log", e)
    }
    _ = crashed
    sys.PrintErr("kvd: stopped with {keys} keys; log closed clean\n", (keys)) else {}
    return 0
}

/// Closes a server that never started.
Discard(heap: &Heap, io: Io, st: server.State) {
    closed := server.Close(st, heap, io)
    server.Closed(store: s, ..) := closed
    s.Free(heap.Alloc())
}
