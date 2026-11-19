// Why: crash recovery is only trustworthy if it is tested at every write
// the log makes, under faults.
// What: `kvd sim`'s engine: Run and RunSeed, Options, Totals and the errors
// Mismatch, Unclean, ReadBack and Twice.
// How: the server and its clients run under std.sim, one seed at a time.
// Each seed picks a log write to crash at, maybe a torn write, a disk or
// network fault and a client that leaves in the middle of a command; then
// the simulated machine restarts, the log is recovered, and the recovered
// state must be what the clients were told, twice over.

import std.log
import std.net
import std.sim
import std.sync
import std.time
import src.client
import src.server
import src.store
import src.wal

/// The most clients a simulation runs.
export const MaxClients: int = 8

/// The most steps a simulated client takes.
export const MaxSteps: int = 64

/// What to run.
export struct Options {
    export first: u64
    export last: u64
    export clients: int
    export steps: int
    export verbose: bool
}

/// A check that failed: the seed, and what went wrong at which client.
export error Mismatch(seed: u64, client: int, key: int)
export error Unclean(seed: u64)
export error ReadBack(seed: u64, client: int, step: int)
export error Twice(seed: u64)

/// What a simulation can fail with.
export type Error = Mismatch | Unclean | ReadBack | Twice | wal.Error | net.Error | Canceled | WriteError

/// What one simulated client did.
struct Outcome {
    /// Steps whose request was sent whole.
    export sent: int
    /// Steps answered, in order from the first.
    export answered: int
    /// Bit `i` is set when step `i` was answered with an error.
    export refused: u64
    /// The first step whose read did not match the model, or -1.
    export misread: int
    /// Left in the middle of a command.
    export gone: bool
    /// Why the client stopped early, if it did.
    export stopped: ?client.Error
}

/// What a whole run counted.
export struct Totals {
    export runs: int
    export crashes: int
    export torn: int
    export clean: int
    export faults: int
    export partial: int
    export refused: int
    export points: int
    export recrashed: int
    export digest: u64
}

/// How seed `seed` is disturbed.
struct Plan {
    crash: wal.Crash
    fault: ?(op: sim.Op, fault: sim.Fault)
    partial: bool
}

/// The disturbances of one seed. The crash point walks through every log
/// write a run makes, seed by seed; one seed in five tears that write.
PlanFor(seed: u64, writes: int): Plan {
    at := Cast[int](seed % Cast[u64](writes + 4))
    fault := match seed % 8 {
        3 { some((op: sim.Op.Write, fault: sim.Fault.NoSpace)) }
        4 { some((op: sim.Op.Sync, fault: sim.Fault.NoSpace)) }
        5 { some((op: sim.Op.Read, fault: sim.Fault.Reset)) }
        6 { some((op: sim.Op.Connect, fault: sim.Fault.Refused)) }
        7 { some((op: sim.Op.Accept, fault: sim.Fault.Denied)) }
        _ { none }
    }
    return Plan(crash: wal.Crash(at: at, torn: seed % 5 == 1), fault: fault, partial: seed % 9 == 2)
}

/// Runs every seed of `o`, writing a line per failed seed and a summary to
/// `out`. Fails at the first seed whose recovered state is wrong.
export Run(sys: &System, o: &Options, out: mut Writer): Error|OutOfMemory!Totals {
    var t := Totals(runs: 0, crashes: 0, torn: 0, clean: 0, faults: 0, partial: 0, refused: 0, points: 0, recrashed: 0, digest: 0)
    var seen: [256]bool = [false; 256]
    var seed := o.first
    for seed <= o.last {
        var arena := try sys.arena.Sub()
        defer arena.Free()
        try RunSeed(sys, &arena, o, seed, mut t, mut seen[..], out)
        t.runs += 1
        seed += 1
    }
    for hit in seen {
        if hit {
            t.points += 1
        }
    }
    return t
}

/// One seed: run, crash or stop, restart, recover twice, check.
RunSeed(sys: &System, arena: &Arena, o: &Options, seed: u64, t: mut Totals, seen: mut []bool, out: mut Writer): Error|OutOfMemory!void *sys into arena {
    a := sys.heap.Alloc()
    plan := PlanFor(seed, o.clients * o.steps)
    var world := try sim.Sim.New(arena, seed)
    defer world.Free()
    io := world.Io()
    key := try HashKey.New(io)
    opened := try server.Open(a, arena, io, try wal.Dir.In(arena, "", none), key, wal.SyncPolicy.Always, plan.crash, o.clients)
    server.Opened(state: st, ..) := opened
    l := net.Listen(io, "127.0.0.1:0") else e {
        Close(sys.heap, io, st)
        fail e
    }
    addr := arena.Fmt("127.0.0.1:{p}", (p: l.Port())) else {
        l.Close()
        Close(sys.heap, io, st)
        fail OutOfMemory
    }
    cfg := server.Config(max_key: 64, max_value: 4096, compact_after: 7, slow_micros: 1_000_000, max_clients: 16, idle_millis: 0, request_millis: 0, admin: false, admin_only: false)
    shared := sync.Shared.New(sys.heap, cfg) else {
        l.Close()
        Close(sys.heap, io, st)
        fail OutOfMemory
    }
    // the clients' outcomes live in the seed's arena, out of the driver's stack
    oa := arena.Alloc()
    var box := Box.New(oa, [Outcome(sent: 0, answered: 0, refused: 0, misread: -1, gone: false, stopped: none); MaxClients]) else {
        shared.Free(sys.heap)
        l.Close()
        Close(sys.heap, io, st)
        fail OutOfMemory
    }
    onerr box.Free(oa)
    outcomes := box.Mut()
    var notes := Text.New(arena)
    var nw := notes.Writer()
    var lg := log.Logger.NewTimed(io, mut nw, if o.verbose { log.Level.Debug } else { log.Level.Warn })
    scope s := io.Scope() {
        s.go server.Accept(&st, sys.heap, shared.Clone(), &l, io)
        s.go server.Compactor(&s, &st, sys.heap, io)
        s.go server.Watch(&s, &st, io)
        s.go Clients(&s, &st, sys.heap, io, addr, seed, o.clients, o.steps, plan.partial, mut outcomes[..o.clients])
        if f := plan.fault {
            world.FailNext(f.op, f.fault)
        }
        server.Pump(&st, sys.heap, io, mut lg)
    }
    server.Drain(&st, sys.heap, io, mut lg)
    shared.Free(sys.heap)
    l.Close()
    closed := server.Close(st, sys.heap, io)
    server.Closed(store: final, crashed, ..) := closed
    final.Free(a)
    if crashed {
        t.crashes += 1
        seen[plan.crash.at % seen.Len()] = true
        if plan.crash.torn {
            t.torn += 1
        }
    } else {
        t.clean += 1
    }
    if plan.fault.IsSome() {
        t.faults += 1
    }
    if plan.partial {
        t.partial += 1
    }
    told := box.Ref()
    for oc in told[..o.clients] {
        var bits := oc.refused
        for bits != 0 {
            t.refused += Cast[int](bits & 1)
            bits = bits >> 1
        }
    }
    if o.verbose {
        try Print(out, "seed {seed}: crash {c} torn {torn} {how}\n{log}", (seed, c: plan.crash.at, torn: plan.crash.torn, how: if crashed { "crashed" } else { "stopped" }, log: notes.Str()))
        for i, oc in told[..o.clients] {
            try Print(out, "  client {i}: sent {oc.sent}, answered {oc.answered}, refused {oc.refused:x}, gone {oc.gone}, stopped {oc.stopped}\n", (i, oc))
        }
    }
    results := box.Take(oa)
    // the machine restarts: what was not synced is gone
    world.Advance(time.Seconds(1))
    io2 := world.Restart()
    digest := try Check(sys.heap, world.Arena(), io2, seed, o, results[..o.clients], some(crashed))
    // recovering the recovered log gives the same state, even when the
    // recovery itself crashes somewhere and runs again
    world.Run()
    world.CrashAfterRandomOps()
    io3 := world.Restart()
    again := Check(sys.heap, arena, io3, seed, o, results[..o.clients], some(false)) else e {
        match e {
            Canceled {
                t.recrashed += 1
                try Check(sys.heap, arena, world.Restart(), seed, o, results[..o.clients], none)
            }
            _ {
                fail e
            }
        }
    }
    if again != digest {
        fail Twice(seed: seed)
    }
    t.digest = t.digest *% 31 +% digest
}

/// Closes a server that never started serving.
Close(heap: &Heap, io: Io, st: server.State) {
    closed := server.Close(st, heap, io)
    server.Closed(store: s, ..) := closed
    s.Free(heap.Alloc())
}

/// Recovers the log and checks it against what each client was told.
/// Returns the recovered state's digest.
Check(heap: &Heap, arena: &Arena, io: Io, seed: u64, o: &Options, outcomes: []Outcome, crashed: ?bool): Error|OutOfMemory!u64 {
    a := heap.Alloc()
    key := try HashKey.New(io)
    opened := try server.Open(a, arena, io, try wal.Dir.In(arena, "", none), key, wal.SyncPolicy.Always, wal.NoCrash, 1)
    server.Opened(state: st, recovered) := opened
    closed := server.Close(st, heap, io)
    server.Closed(store: got, ..) := closed
    defer got.Free(a)
    if was := crashed {
        if recovered.clean == was {
            fail Unclean(seed: seed)
        }
    }
    var steps: [MaxSteps]Step = [Step(kind: Kind.Get, key: 0); MaxSteps]
    var present := 0
    for i, oc in outcomes {
        if oc.misread >= 0 {
            fail ReadBack(seed: seed, client: i, step: oc.misread)
        }
        Steps(seed, i, mut steps[..o.steps])
        var told := store.Store.NewFixed(a)
        defer told.Free(a)
        var maybe := store.Store.NewFixed(a)
        defer maybe.Free(a)
        for j in 0..oc.answered {
            if oc.refused & (Cast[u64](1) << Cast[u64](j)) != 0 {
                continue
            }
            try ApplyStep(a, arena, mut told, seed, i, j, &steps[j])
            try ApplyStep(a, arena, mut maybe, seed, i, j, &steps[j])
        }
        // a step sent but not answered may or may not have reached the log
        if oc.sent > oc.answered && !oc.gone {
            try ApplyStep(a, arena, mut maybe, seed, i, oc.answered, &steps[oc.answered])
        }
        for k in 0..KeysPerClient {
            name := try KeyName(arena, i, k)
            have := got.Get(name)
            if have.IsSome() {
                present += 1
            }
            if !Same(have, told.Get(name)) && !Same(have, maybe.Get(name)) {
                fail Mismatch(seed: seed, client: i, key: k)
            }
        }
    }
    if present != got.Len() {
        fail Mismatch(seed: seed, client: -1, key: got.Len() - present)
    }
    return got.Digest()
}

/// Whether two optional values are equal.
Same(x: ?str, y: ?str): bool {
    return match x {
        some(a) {
            match y {
                some(b) { a == b }
                none { false }
            }
        }
        none { y.IsNone() }
    }
}
