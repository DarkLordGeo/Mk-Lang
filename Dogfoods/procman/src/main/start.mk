// Why: `procman start` runs the Procfile's processes until they end, a
// `main` process ends, a signal comes or `--stop-after` runs out.
//
// What: Start, Select, Supervise, Listen, ServeStatus and StatusWorker.
//
// How: one task per process sends events on a channel the main task
// prints; the status port, when asked for, is served by worker processes.

import std.fs
import std.log
import std.mem
import std.net
import std.proc
import std.time
import src.manager
import src.procfile
import src.rundir
import src.status

/// The most events that wait for the console while the status port holds
/// the main task; a process task waits when it is full.
const EventBacklog: int = 256

Start(sys: &System, cfg: &Config): WriteError!int {
    if proc.IsWorker(sys) {
        return try ServeAsWorker(sys, cfg)
    }
    var err := sys.Stderr()
    var console := ConsoleLogs(mut err, cfg.level)
    loaded := try LoadProcfile(sys, cfg, mut console)
    entries := loaded else {
        return 2
    }
    procs := try Select(sys, cfg, entries.Items()) else {
        return 2
    }
    dir := rundir.Open(sys.io, cfg.run_dir) else e {
        try sys.PrintErr("procman: run directory {d}: {e}\n", (d: cfg.run_dir, e))
        return 3
    }
    log_path := dir.File(sys.arena, "procman.log") else {
        try sys.PrintErr("procman: out of memory\n")
        return 1
    }
    var f := fs.Create(sys.io, log_path) else e {
        try sys.PrintErr("procman: {log_path}: {e}\n", (log_path, e))
        return 3
    }
    onerr f.Abandon()
    var fbuf: [4096]u8 = [0; 4096]
    var fw := f.Writer(sys.io, mut fbuf[..])
    var logs := Logs(console: log.Logger.New(mut err, cfg.level), file: some(log.Logger.NewTimed(sys.io, mut fw, cfg.level)))
    code := try Listen(sys, cfg, mut logs, procs, dir)
    fw.Flush() else {}
    f.Close(sys.io) else e {
        // A failed Close leaves the file open and owned.
        f.Abandon()
        try sys.PrintErr("procman: {log_path}: {e}\n", (log_path, e))
    }
    if cfg.clean {
        try CleanUp(sys, dir)
    }
    return code
}

// `--clean`: what `procman clean` does, once the log is closed.
CleanUp(sys: &System, dir: rundir.RunDir): WriteError!void {
    left := dir.Clean(sys.arena, sys.io) else e {
        try sys.PrintErr("procman: run directory {d}: {e}\n", (d: dir.path, e))
        return
    }
    if left.kept > 0 {
        try sys.PrintErr("procman: kept {d}, which holds files procman did not write ({n})\n", (d: dir.path, n: left.kept))
    }
}

// The processes `start` runs: every process entry, or the ones named.
Select(sys: &System, cfg: &Config, entries: []procfile.Entry): WriteError!?[]procfile.Entry from sys, entries *entries into sys {
    var out := List[procfile.Entry].New(sys.arena)
    for name in cfg.args {
        e := procfile.Find(entries, name) else {
            try sys.PrintErr("procman: {path} has no entry `{name}`\n", (path: cfg.procfile, name))
            return none
        }
        if e.kind != procfile.Kind.Process {
            try sys.PrintErr("procman: `{name}` is a task: run it with `procman run {name}`\n", (name))
            return none
        }
    }
    for e in entries {
        if e.kind != procfile.Kind.Process {
            continue
        }
        if cfg.args.Len() > 0 && !cfg.args.Has(&e.name) {
            continue
        }
        out.Push(e) else {
            return none
        }
    }
    if out.Len() == 0 {
        try sys.PrintErr("procman: {path} has no processes to start\n", (path: cfg.procfile))
        return none
    }
    return some(out.Done()[..])
}

// Opens the status listener when one is asked for, and writes its address
// where `procman status` finds it.
Listen(sys: &System, cfg: &Config, logs: mut Logs, procs: []procfile.Entry, dir: rundir.RunDir): WriteError!int {
    addr := cfg.status else {
        return try Supervise(sys, cfg, logs, procs, dir, none)
    }
    l := net.Listen(sys.io, addr) else e {
        try sys.PrintErr("procman: status port {addr}: {e}\n", (addr, e))
        return 4
    }
    defer l.Close()
    host := addr[..addr.Find(":") else { addr.Len() }]
    port_line := sys.arena.Fmt("{host}:{port}\n", (host, port: l.Port())) else {
        return 1
    }
    dir.Replace(sys.arena, sys.io, "status.port", port_line.AsBytes()) else e {
        try sys.PrintErr("procman: run directory {d}: {e}\n", (d: dir.path, e))
        return 3
    }
    logs.Info("status port open with {n} workers", (n: cfg.workers))
    return try Supervise(sys, cfg, logs, procs, dir, some(&l))
}

// Runs one task per process, watches the signals and the clock, and shows
// what happens; the status workers hold the main task while they run.
Supervise(sys: &System, cfg: &Config, logs: mut Logs, procs: []procfile.Entry, dir: rundir.RunDir, listener: ?&net.Listener): WriteError!int {
    ea := sys.heap.Alloc()
    events := Chan[manager.Event].New(ea, EventBacklog) else {
        try sys.PrintErr("procman: out of memory\n")
        return 1
    }
    defer events.Free(ea)
    var budget := TailBudget(sys.arena, cfg.tail_memory) else {
        try sys.PrintErr("procman: out of memory\n")
        return 1
    }
    counting := mem.Counting.New(budget.Alloc())
    ta := counting.Alloc()
    var report := Report.New(sys.arena, ta, procs) else {
        try sys.PrintErr("procman: out of memory\n")
        return 1
    }
    defer report.Free(ta)
    if report.tails.Len() < procs.Len() {
        logs.Warn("--tail-memory of {n} bytes holds no output; the summary will show none", (n: cfg.tail_memory))
    }
    var out := sys.Stdout()
    c := manager.Control.New(procs.Len())
    t0 := time.Now(sys.io)
    scope s := sys.io.Scope() {
        s.go manager.WatchSignal(&s, sys.io, proc.Signal.Interrupt, &c)
        s.go manager.WatchSignal(&s, sys.io, proc.Signal.Terminate, &c)
        s.go manager.WatchSignal(&s, sys.io, proc.Signal.Hangup, &c)
        if ms := cfg.stop_after {
            s.go manager.StopAfter(&s, sys.io, i64(ms), &c)
        }
        for i, e in &procs {
            s.go manager.RunProcess(&s, sys.heap, sys.io, dir, e, i, &events, &c)
        }
        if l := listener {
            ServeStatus(sys, cfg, logs, l)
        }
        for {
            ev := events.Recv(sys.io) else {
                break
            }
            defer ev.Free(ea)
            try report.Handle(sys.arena, ta, mut out, logs, &ev)
        }
    }
    // The tasks have ended, so the rest of the events wait in the channel.
    for {
        ev := events.Recv(sys.io.NoCancel()) else {
            break
        }
        defer ev.Free(ea)
        try report.Handle(sys.arena, ta, mut out, logs, &ev)
    }
    reason := c.Reason()
    ms := time.Now(sys.io).Since(t0).Nanos() / 1_000_000
    logs.Debug("ran for {ms}ms; tails peaked at {peak} bytes in {n} allocations", (ms, peak: counting.Peak(), n: counting.Live()))
    if cfg.summary {
        try report.Summary(mut out)
    }
    return try Ending(sys, logs, reason, report.Failed())
}

// The status workers, until `start` stops. A failure to start them is
// logged and the processes go on without the port.
ServeStatus(sys: &System, cfg: &Config, logs: mut Logs, l: &net.Listener) {
    proc.Supervise[status.Error](sys, cfg.workers, l, StatusWorker) else e {
        match e {
            Canceled {}
            _ { logs.Error("status port: {e}", (e)) }
        }
    }
}

// What a status worker runs: Main ran again in this process, up to
// `Supervise`, which calls this once.
StatusWorker(sys: &System, l: &net.Listener): status.Error!void {
    // The parent parsed these same arguments before it started any worker.
    opts := Options.Parse(sys.arena, sys.args[1..]).Must()
    cfg := Configure(sys, &opts).Must()
    try status.Serve(sys.heap, sys.io, l, rundir.RunDir(path: cfg.run_dir), cfg.status_debug)
}

// In a worker, `start` only listens: `net.Listen` hands back the parent's
// listener, and `Supervise` runs `StatusWorker` once.
ServeAsWorker(sys: &System, cfg: &Config): WriteError!int {
    addr := cfg.status else {
        return 2
    }
    l := net.Listen(sys.io, addr) else {
        return 4
    }
    defer l.Close()
    proc.Supervise[status.Error](sys, cfg.workers, &l, StatusWorker) else {
        return 1
    }
    return 0
}

// The exit code, and a last line saying why procman stopped.
Ending(sys: &System, logs: mut Logs, reason: manager.StopReason, failed: bool): WriteError!int {
    code := match reason {
        .Interrupt { 130 }
        .Terminate { 143 }
        .Hangup { 129 }
        _ { if failed { 1 } else { 0 } }
    }
    match reason {
        .Finished { logs.Info("every process has ended") }
        .MainEnded { logs.Info("a main process ended; stopped the others") }
        .Interrupt { logs.Warn("interrupted; stopped every process") }
        .Terminate { logs.Warn("asked to terminate; stopped every process") }
        .Hangup { logs.Warn("hung up; stopped every process") }
        .Timeout { logs.Info("--stop-after ran out; stopped every process") }
    }
    _ = sys
    return code
}
