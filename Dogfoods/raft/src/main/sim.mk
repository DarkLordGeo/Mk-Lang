// Why: each seed is one deterministic run that must be checked and
// reproducible.
// What: Simulate, RunSeed, SeedResult and the recording helpers.
// How: runs the cluster under std.sim, checks the history, replays the
// recording and compares the two histories.

import std.fs
import std.hash
import std.math
import std.mem
import std.net
import std.sim
import std.time
import src.check
import src.cluster
import src.history
import src.raft

// What one seed gave: the checker's verdict, whether the replay of its
// recording gave the same history, and what to print and keep.
struct SeedResult {
    export verdict: check.Verdict
    export same: bool
    export saved: bool
    export digest: u64
    export ops: int
    export answered: int
    export phases: int
    export stats: cluster.Stats
}

// A seed's run, recorded, then replayed from the recording.
type RunError = cluster.Error | cluster.PeerCount | check.TooManyOps | WriteError

// The values each setting may take, from an option or from a recording's
// header alike; a flag is 0 or 1.
Bounds(key: str): ?(lo: i64, hi: i64) {
    return match key {
        "seed" { some((lo: 0, hi: MaxSeed)) }
        "nodes" { some((lo: 1, hi: i64(raft.MaxNodes))) }
        "clients" { some((lo: 1, hi: i64(raft.MaxClients))) }
        "ops" { some((lo: 1, hi: i64(check.MaxOps))) }
        "keys" { some((lo: 1, hi: i64(raft.MaxKeys))) }
        "loss" | "delay" | "corrupt" { some((lo: 0, hi: 100)) }
        "duplicate" { some((lo: 0, hi: 50)) }
        "inbox" { some((lo: 1, hi: 256)) }
        "duration" { some((lo: 100, hi: 600_000)) }
        "memory" { some((lo: 0, hi: 1_073_741_824)) }
        "partitions" | "faults" | "crash" | "tasks" | "stale" { some((lo: 0, hi: 1)) }
        _ { none }
    }
}

// The largest seed: seeds run as int, and a range's end plus one fits.
const MaxSeed: i64 = 4_611_686_018_427_387_904

// What settings no option or header may combine, or none.
Conflict(s: &cluster.Settings): ?str {
    if s.crash && s.tasks {
        return some("--crash keeps the nodes in this task: leave out --tasks")
    }
    if s.clients * s.ops > check.MaxOps {
        return some("--clients times --ops must be at most 64, the checker's limit per register")
    }
    return none
}

Count(sys: &System, name: str, v: ?int, default: int): int {
    n := v else {
        return default
    }
    // every option read here has its bounds in Bounds
    b := Bounds(name).Must()
    if i64(n) < b.lo || i64(n) > b.hi {
        what := sys.arena.Fmt("--{name} must be from {b.lo} to {b.hi}", (name, b)) else {
            "an option is out of range"
        }
        UsageError(sys, what)
    }
    return n
}

// The settings the options give, for seed. Exits with a usage error on a
// value out of range.
Configure(sys: &System, o: &Options, seed: u64): cluster.Settings {
    var s := cluster.Settings.Default(seed)
    s.nodes = Count(sys, "nodes", o.nodes, s.nodes)
    s.clients = Count(sys, "clients", o.clients, s.clients)
    s.ops = Count(sys, "ops", o.ops, s.ops)
    s.keys = Count(sys, "keys", o.keys, s.keys)
    s.loss = Count(sys, "loss", o.loss, 0)
    s.delay = Count(sys, "delay", o.delay, 0)
    s.duplicate = Count(sys, "duplicate", o.duplicate, 0)
    s.corrupt = Count(sys, "corrupt", o.corrupt, 0)
    s.inbox = Count(sys, "inbox", o.inbox, 256)
    ms := Count(sys, "duration", o.duration, 3000)
    s.duration = time.Millis(i64(ms))
    _ = Count(sys, "memory", o.memory, 0)
    s.partitions = o.partitions
    s.faults = o.faults
    s.crash = o.crash
    s.tasks = o.tasks
    s.params.stale_reads = o.stale
    if what := Conflict(&s) {
        UsageError(sys, what)
    }
    return s
}

SeedRange(sys: &System, o: &Options): (lo: int, hi: int) {
    text := o.seeds else {
        one := o.seed else {
            return (lo: 1, hi: 1)
        }
        if one < 0 || i64(one) > MaxSeed {
            UsageError(sys, "--seed must be from 0 to 2^62")
        }
        return (lo: one, hi: one)
    }
    dots := text.Find("..") else {
        UsageError(sys, "--seeds takes A..B")
    }
    lo := text[..dots].ParseInt() else {
        UsageError(sys, "--seeds takes A..B")
    }
    hi := text[dots + 2..].ParseInt() else {
        UsageError(sys, "--seeds takes A..B")
    }
    if lo < 0 || hi < lo || hi > MaxSeed {
        UsageError(sys, "--seeds takes A..B with 0 <= A <= B <= 2^62")
    }
    return (lo: Trunc[int](lo), hi: Trunc[int](hi))
}

// The history's text, in a.
HistoryText(a: &Arena, h: &history.History): WriteError|OutOfMemory!str from a {
    var t := Text.New(a)
    var w := t.Writer()
    try h.Write(mut w)
    return t.Done()
}

// Runs one seed under a simulator, each phase through a recorder, then
// again under replayers of those recordings, and checks the history. With
// --crash the cluster crashes once at a seeded point, stays down for a
// while and restarts from what its nodes made durable, so a seed has two
// phases. The node tasks and their channels run on sys.io, so a recording
// holds only the network, the disk and the clock, which one task drives.
RunSeed(sys: &System, o: &Options, s: &cluster.Settings, peers: str, last: bool): RunError!SeedResult {
    var arena := try Arena.New(sys.heap)
    defer arena.Free()
    var sm := try sim.Sim.New(&arena, s.seed)
    defer sm.Free()
    cfg := try cluster.Config.Parse(sm.Arena(), sm.Io(), peers, s.nodes, s.clients)
    var phase := try cluster.Phase.New(&arena, s)
    var logs := List[[]u8].New(&arena)
    if s.crash {
        sm.CrashAfterRandomOps()
    }
    var io := sm.Io()
    var crashed_at: i64 = -1
    var stats := cluster.Stats.Zero()
    for {
        r := try RecordPhase(&arena, sys, io, mut sm, o, s, &cfg, mut phase)
        try logs.Push(r.log)
        if !r.crashed {
            stats = r.stats
            break
        }
        crashed_at = phase.clock
        sm.Advance(cluster.Downtime)
        io = sm.Restart()
    }
    text := try HistoryText(&arena, &phase.history)
    verdict := try check.Check(&arena, &phase.history)
    same := try ReplayMatches(&arena, sys, logs.Items(), o, s, &cfg, text)
    var answered := 0
    for op in phase.history.ops {
        if op.ret >= 0 {
            answered += 1
        }
    }
    if o.verbose {
        var out := sys.Stdout()
        try out.Str("peers: ")
        try cfg.Describe(mut out, s.nodes)
        if crashed_at >= 0 {
            try sys.Print("crashed at {crashed_at} ns, restarted {d} ns later\n", (crashed_at, d: cluster.Downtime.Nanos()))
        }
        try sys.Print("{text}", (text))
    }
    saved := if last { try SaveLast(sys, o, s, peers, text, logs.Items()) } else { true }
    return SeedResult(verdict: verdict, same: same, saved: saved, digest: hash.Xx(text.AsBytes()), ops: phase.history.ops.Len(), answered: answered, phases: phase.count, stats: stats)
}

// One phase under a recorder. A Canceled in the first phase of a --crash
// run is the crash: the phase ends there, and its log is kept.
RecordPhase(a: &Arena, sys: &System, io: Io, sm: mut sim.Sim, o: &Options, s: &cluster.Settings, cfg: &cluster.Config, phase: mut cluster.Phase): RunError!(log: []u8, crashed: bool, stats: cluster.Stats) from a {
    rec := try sim.Recorder.New(io)
    defer rec.Free()
    var crashed := false
    stats := RunBudget(a, sys, rec.Io(), sm, o, s, cfg, phase) else e {
        match e {
            Canceled {
                if !s.crash || phase.count > 1 {
                    fail e
                }
                crashed = true
                cluster.Stats.Zero()
            }
            _ {
                fail e
            }
        }
    }
    log := try rec.Log(a)
    return (log, crashed, stats)
}

// Runs the seed again from its recordings, one replayer per phase; the
// replay matches when it gives the same history and uses each recording
// whole. A phase that ended in the crash may leave its replayer marked
// diverged: the crash answered its last call.
ReplayMatches(a: &Arena, sys: &System, logs: [][]u8, o: &Options, s: &cluster.Settings, cfg: &cluster.Config, text: str): OutOfMemory|WriteError!bool {
    // the replay's faults come from the recording; this Sim takes the calls
    var quiet := try sim.Sim.New(a, s.seed)
    defer quiet.Free()
    var phase := try cluster.Phase.New(a, s)
    for i, log in logs {
        rep := try sim.Replayer.New(log)
        defer rep.Free()
        last := i == logs.Len() - 1
        var failed := false
        _ = RunBudget(a, sys, rep.Io(), mut quiet, o, s, cfg, mut phase) else {
            failed = true
        }
        if failed != !last || !rep.Done() || last && rep.Diverged() {
            return false
        }
    }
    again := try HistoryText(a, &phase.history)
    return again == text
}

// A phase on the heap, or within --memory bytes of a fixed buffer.
RunBudget(a: &Arena, sys: &System, io: Io, sm: mut sim.Sim, o: &Options, s: &cluster.Settings, cfg: &cluster.Config, phase: mut cluster.Phase): cluster.Error!cluster.Stats {
    if bytes := o.memory {
        buf := try a.Slice[u8](math.Max(bytes, 0))
        fixed := mem.Fixed.New(buf)
        return try cluster.Run(a, sys.heap, fixed.Alloc(), io, sys.io, sm, s, cfg, phase)
    }
    return try cluster.Run(a, sys.heap, sys.heap.Alloc(), io, sys.io, sm, s, cfg, phase)
}

// Writes --history and --record for the last seed; reports a failure and
// returns false.
SaveLast(sys: &System, o: &Options, s: &cluster.Settings, peers: str, text: str, logs: [][]u8): WriteError!bool {
    if path := o.history {
        fs.WriteAll(sys.io, path, text.AsBytes()) else e {
            try sys.PrintErr("raft: {path}: {e}\n", (path, e))
            return false
        }
    }
    if path := o.record {
        WriteRecording(sys, s, peers, path, logs) else e {
            try sys.PrintErr("raft: {path}: {e}\n", (path, e))
            return false
        }
    }
    return true
}

// A recording: the header line, then each phase's log as a line "log N"
// and N bytes.
WriteRecording(sys: &System, s: &cluster.Settings, peers: str, path: str, logs: [][]u8): fs.Error|OutOfMemory!void {
    header := try Header(sys.arena, s, peers)
    var f := try fs.Create(sys.io, path)
    onerr f.Abandon()
    try f.WriteAll(sys.io, header.AsBytes())
    for log in logs {
        line := try sys.arena.Fmt("log {n}\n", (n: log.Len()))
        try f.WriteAll(sys.io, line.AsBytes())
        try f.WriteAll(sys.io, log)
    }
    try f.Close(sys.io)
}

// raft sim: runs each seed, checks it, replays it, and prints a summary
// whose digest covers every history.
Simulate(sys: &System, o: &Options): WriteError!int {
    range := SeedRange(sys, o)
    base := Configure(sys, o, 0)
    peers := o.peers else {
        cluster.DefaultPeers(sys.arena, base.nodes) else {
            try sys.PrintErr("raft: OutOfMemory\n")
            return ExitMemory
        }
    }
    var linearizable := 0
    var identical := 0
    var digest: u64 = 0
    var first_bad := -1
    var saved := true
    for seed in range.lo..range.hi + 1 {
        var s := base
        s.seed = Cast[u64](seed)
        r := RunSeed(sys, o, &s, peers, seed == range.hi) else e {
            return try Failure(sys, seed, e)
        }
        if r.verdict.ok {
            linearizable += 1
        } else if first_bad < 0 {
            first_bad = seed
        }
        if r.same {
            identical += 1
        }
        saved = saved && r.saved
        digest = (digest *% 1_099_511_628_211) ^ r.digest
        if o.verbose {
            v := if r.verdict.ok { "linearizable" } else { "NOT linearizable" }
            same := if r.same { "identical" } else { "DIFFERS" }
            try sys.Print("seed {seed}: {v}, {r.ops} ops, {r.answered} answered, replay {same}\n", (seed, v, r, same))
            try sys.Print("  raft: {r.stats.elections} elections, term {r.stats.term}, commit {r.stats.commit}, leaders {r.stats.leaders:05b}, leader hint moved {r.stats.repoints} times\n", (r))
            try sys.Print("  network: {r.stats.steps} steps, {r.stats.partitions} partitions, sent {r.stats.sent}, dropped {r.stats.dropped}, delayed {r.stats.delayed}, duplicated {r.stats.duplicated}, corrupted {r.stats.corrupted}, rejected {r.stats.rejected}, faults {r.stats.faults}, overflowed {r.stats.overflowed}\n", (r))
            try sys.Print("  delivery: delivered {r.stats.delivered}, handled {r.stats.handled}, late {r.stats.late}, lost {r.stats.lost} ({r.stats.held} for nodes), checksum {r.stats.checksum:016x}\n", (r))
            if s.crash {
                try sys.Print("  disk: {r.phases} phases, {r.stats.restored} nodes restored, {r.stats.saved} saves, {r.stats.unsaved} failed, {r.stats.muted} messages held back for them\n", (r))
            }
        }
    }
    count := range.hi - range.lo + 1
    try sys.Print("seeds {range.lo}..{range.hi}: {linearizable} of {count} linearizable, {identical} replays identical, history digest {digest:016x}\n", (range, linearizable, count, identical, digest))
    if linearizable != count {
        try sys.Print("first history not linearizable: seed {first_bad}\n", (first_bad))
        return ExitNotLinearizable
    }
    if identical != count {
        return ExitReplay
    }
    if !saved {
        return ExitInput
    }
    return ExitOk
}

Failure(sys: &System, seed: int, e: RunError): WriteError!int {
    try sys.PrintErr("raft: seed {seed}: {e}\n", (seed, e))
    return match e {
        OutOfMemory { ExitMemory }
        cluster.PeerCount { ExitUsage }
        net.InvalidAddress { ExitUsage }
        check.TooManyOps { ExitUsage }
        Canceled { ExitReplay }
        _ { ExitNetwork }
    }
}
