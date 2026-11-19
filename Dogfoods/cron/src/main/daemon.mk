// Why: the schedule runs for real or under simulation, with the same
// output.
//
// What: RunCommand (`run`, real processes and clock), SimCommand (`sim`,
// recorded fires under std.sim's clock), Setup and Summary.
//
// How: both take the lock, open the log and print the same summary when
// they stop; the simulated clock starts at 2026-01-01T00:00:00Z on every
// seed.

import std.fs
import std.mem
import std.sim
import std.time
import src.crontab
import src.daemon
import src.jobs

/// What a run of the loop needs beyond the crontab.
struct Setup {
    lock_path: str
    lock_wait: ?time.Duration
    log_path: str
    plan: daemon.Plan
}

/// `cron run`: the daemon.
RunCommand(c: &Ctx, tab: &crontab.Crontab): int {
    sys := c.sys
    setup := Prepare(c, tab, false) else {
        return 2
    }
    return Operate(c, tab, sys.io, &setup)
}

/// `cron sim`: `--days` of the schedule under a simulated clock, with
/// the lock and the log on the simulation's disk. Prints the fires that
/// a transition moved (every fire with `--verbose`), then the summary.
SimCommand(c: &Ctx, tab: &crontab.Crontab): int {
    sys := c.sys
    seed := c.opts.seed else { 1 }
    var s := sim.Sim.New(sys.arena, Cast[u64](seed)) else {
        return 4
    }
    defer s.Free()
    setup := Prepare(c, tab, true) else {
        return 2
    }
    if fault := c.opts.fault {
        match fault {
            "log-write" { s.FailNext(sim.Op.Write, sim.Fault.NoSpace) }
            "lock-open" { s.FailNext(sim.Op.Open, sim.Fault.Denied) }
            _ {
                sys.PrintErr("cron: --fault {fault}: one of log-write, lock-open\n", (fault)) else {}
                return 2
            }
        }
    }
    io := s.Io()
    code := Operate(c, tab, io, &setup)
    if code != 0 {
        return code
    }
    text := fs.ReadText(sys.arena, io, setup.log_path) else e {
        sys.PrintErr("cron: the simulated log: {e}\n", (e)) else {}
        return 1
    }
    for line in text.Lines() {
        moved := line.Has("skipped time") || line.Has("repeated time")
        if moved || c.opts.verbose {
            sys.Print("{line}\n", (line)) else {
                return 1
            }
        }
    }
    return 0
}

/// Reads the options both commands share; none, reported, when one is bad.
Prepare(c: &Ctx, tab: &crontab.Crontab, simulated: bool): ?Setup from c, tab {
    sys := c.sys
    o := c.opts
    lock_wait := Duration(sys, "--lock-wait", o.lock_wait) else {
        return none
    }
    var stop_after := Duration(sys, "--stop-after", o.stop_after) else {
        return none
    }
    if simulated {
        days := o.days else { 365 }
        if days < 1 || days > 3660 {
            sys.PrintErr("cron: --days is from 1 to 3660\n") else {}
            return none
        }
        stop_after = some(time.Hours(i64(days) * 24))
    }
    max_jobs := o.max_jobs else { 16 }
    if max_jobs < 1 || max_jobs > 48 {
        // a job's output pipe holds one of the process's 64 blocking threads
        sys.PrintErr("cron: --max-jobs is from 1 to 48\n") else {}
        return none
    }
    lock_path := LockPath(o)
    log_path := o.log else { "cron.log" }
    plan := daemon.Plan(stop_after: stop_after, max_jobs: max_jobs, simulated: simulated)
    return some(Setup(lock_path: lock_path, lock_wait: lock_wait, log_path: log_path, plan: plan))
}

/// A duration option: none when absent, a value when it parses; a bad one
/// is reported and the whole result is none.
Duration(sys: &System, name: str, text: ?str): ??time.Duration {
    t := text else {
        return some(none)
    }
    d := time.ParseDuration(t) else e {
        sys.PrintErr("cron: {name} {t}: {e}; write a duration such as 30s or 5m\n", (name, t, e)) else {}
        return none
    }
    if d.Nanos() < 0 {
        sys.PrintErr("cron: {name} {t}: a duration is not negative\n", (name, t)) else {}
        return none
    }
    return some(some(d))
}

/// Runs the loop with the job table on the heap, or, under `--max-memory`,
/// in a buffer of exactly that many bytes: the table is the one store that
/// grows with the crontab.
Operate(c: &Ctx, tab: &crontab.Crontab, io: Io, setup: &Setup): int {
    sys := c.sys
    limit := c.opts.max_memory else {
        return Guarded(c, tab, io, setup, sys.heap.Alloc())
    }
    if limit < 0 {
        sys.PrintErr("cron: --max-memory is a number of bytes\n") else {}
        return 2
    }
    buf := sys.arena.Slice[u8](limit) else {
        sys.PrintErr("cron: --max-memory {limit}: out of memory\n", (limit)) else {}
        return 4
    }
    fixed := mem.Fixed.New(buf)
    return Guarded(c, tab, io, setup, fixed.Alloc())
}

/// Takes the lock, opens the log, runs the loop, and prints the summary.
Guarded(c: &Ctx, tab: &crontab.Crontab, io: Io, setup: &Setup, alloc: &Alloc): int {
    sys := c.sys
    var table := jobs.Table.New(alloc, tab.entries.Len()) else {
        sys.PrintErr("cron: the job table does not fit in --max-memory\n") else {}
        return 4
    }
    lock := daemon.Lock(io, setup.lock_path, setup.lock_wait) else e {
        table.Free(alloc)
        path := setup.lock_path
        return match e {
            daemon.Held {
                sys.PrintErr("cron: another instance holds {path}\n", (path)) else {}
                3
            }
            _ {
                sys.PrintErr("cron: the lock file {path}: {e}\n", (path, e)) else {}
                1
            }
        }
    }
    logf := fs.OpenWith(io, setup.log_path, fs.Append) else e {
        path := setup.log_path
        sys.PrintErr("cron: the log file {path}: {e}\n", (path, e)) else {}
        daemon.Unlock(io, lock)
        table.Free(alloc)
        return 1
    }
    file := Mutex[fs.File].New(logf)
    env := Environment(sys.arena, tab) else {
        sys.PrintErr("cron: out of memory\n") else {}
        var unused := file.Into()
        unused.Close(io) else {
            unused.Abandon()
        }
        daemon.Unlock(io, lock)
        table.Free(alloc)
        return 4
    }
    set := jobs.Setting(env: env, log_path: setup.log_path, journal: jobs.Journal.Over(&file, c.opts.echo))
    books := Mutex[jobs.Table].New(table)
    ending := daemon.Serve(sys.heap, sys.arena, io, tab, &books, &set, &setup.plan) else {
        daemon.Ending.Signal
    }
    var log_file := file.Into()
    log_file.Close(io) else {
        log_file.Abandon()
    }
    daemon.Unlock(io, lock)
    final := books.Into()
    code := Summary(c, tab, &final, ending)
    final.Free(alloc)
    return code
}

/// `NAME=value` for each crontab variable, for the jobs' environment.
Environment(a: &Arena, tab: &crontab.Crontab): OutOfMemory![]str from a {
    var out := List[str].New(a)
    for v in &tab.vars {
        try out.Push(try a.Fmt("{v.name}={v.value}", (v)))
    }
    done := out.Done()
    return done[..]
}

/// Prints why the daemon stopped and a line of counts per job.
Summary(c: &Ctx, tab: &crontab.Crontab, table: &jobs.Table, ending: daemon.Ending): int {
    sys := c.sys
    why := match ending {
        .Deadline {
            if c.opts.rest[0] == "sim" { "the simulated days ran out" } else { "the time given by --stop-after passed" }
        }
        .Signal { "an interrupt arrived" }
    }
    sys.Print("cron: stopped: {why}\n", (why)) else {
        return 1
    }
    sys.Print("{j:<16} {s:>7} {o:>5} {f:>7} {t:>9} {i:>12} {d:>9} {k:>8}\n", (
        j: "job", s: "started", o: "ok", f: "failed", t: "timed out", i: "interrupted", d: "detached", k: "overlaps",
    )) else {
        return 1
    }
    for i, e in &tab.entries {
        st := table.Get(i)
        sys.Print("{e.name:<16} {st.started:>7} {st.ok:>5} {st.failed:>7} {st.timed_out:>9} {st.interrupted:>12} {st.detached:>9} {st.overlapped:>8}\n", (e, st)) else {
            return 1
        }
    }
    return 0
}

/// The lock file: `--lock`, else `cron.lock` in the working directory
/// (on the simulated disk under `sim`).
LockPath(o: &Options): str from o {
    return o.lock else { "cron.lock" }
}
