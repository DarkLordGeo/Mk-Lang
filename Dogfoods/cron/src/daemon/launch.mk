// Why: an entry must not start again while its last run is still going.
//
// What: Launch, Begin and End.
//
// How: each fire runs in a task of the loop's scope; a shared table under a
// mutex marks the entry busy. Under simulation the fire is only logged.

import std.time
import src.crontab
import src.jobs
import src.sched

/// Starts entry `i` for fire `f`, unless its last run is still going, and
/// records how the run ended. Under simulation the fire is logged with its
/// local time and nothing is started.
Launch(h: &Heap, io: Io, set: &jobs.Setting, table: &Mutex[jobs.Table], i: int, e: &crontab.Entry, f: sched.Fire, z: &time.Zone, simulated: bool) {
    // the books are kept even while the loop is being stopped
    q := io.NoCancel()
    if !Begin(q, table, i) {
        set.journal.Warn(io, "{e.name}: still running; this run is skipped", (e))
        return
    }
    var a := Arena.New(h) else {
        End(q, table, i, jobs.Outcome.NotStarted)
        return
    }
    defer a.Free()
    shown := sched.Show(&a, &f, z) else { "?" }
    outcome := if simulated {
        set.journal.Info(io, "{e.name}: {shown}", (e, shown))
        jobs.Outcome.Recorded
    } else {
        if f.fit != sched.Fit.Exact {
            set.journal.Info(io, "{e.name}: due {shown}", (e, shown))
        } else {
            set.journal.FileOnly(io, "{e.name}: due {shown}", (e, shown))
        }
        jobs.Execute(h, io, set, e)
    }
    End(q, table, i, outcome)
}

Begin(io: Io, table: &Mutex[jobs.Table], i: int): bool {
    var g := table.Lock(io) else {
        return false
    }
    ok := g.Mut().Begin(i)
    g.Unlock()
    return ok
}

End(io: Io, table: &Mutex[jobs.Table], i: int, o: jobs.Outcome) {
    var g := table.Lock(io) else {
        return
    }
    g.Mut().End(i, o)
    g.Unlock()
}
