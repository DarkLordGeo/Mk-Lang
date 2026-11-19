// Why: the schedule loop is the daemon: it must sleep until the next fire
// and stop cleanly with every job it started.
//
// What: Serve, Plan, Advance, Earliest and Watch.
//
// How: sleep until the earliest fire, start every job that is due as a
// task, compute each entry's next fire, and repeat until the scope is
// cancelled by an interrupt or by `--stop-after`. Jobs run in tasks of the
// same scope, so stopping reaches them: each forwards the interrupt to its
// group, or is left running when its entry says `detach`.

import std.proc
import std.time
import src.crontab
import src.jobs
import src.sched

/// How the loop runs.
export struct Plan {
    /// Stop after this long; none runs until an interrupt
    export stop_after: ?time.Duration
    /// Jobs that may run at once; more wait for a slot
    export max_jobs: int
    /// Record each fire instead of starting the job (under std.sim)
    export simulated: bool
}

/// Why the loop ended.
export enum Ending {
    /// `--stop-after`'s deadline passed
    Deadline
    /// An interrupt or terminate signal arrived
    Signal
}

/// Runs the schedule of `tab` until it is stopped, and says why it stopped.
/// `a` holds the loop's own state, one fire per entry.
export Serve(h: &Heap, a: &Arena, io: Io, tab: &crontab.Crontab, table: &Mutex[jobs.Table], set: &jobs.Setting, p: &Plan): OutOfMemory!Ending {
    start := time.Wall(io)
    var next := List[?sched.Fire].New(a)
    for e in &tab.entries {
        zone := &tab.zones[e.zone].zone
        first := match e.schedule {
            .Reboot { some(sched.Fire(at: start, fit: sched.Fit.Exact)) }
            _ { Following(&e.schedule, zone, start) }
        }
        try next.Push(first)
    }
    var ending := Ending.Signal
    // the instant `--stop-after` ends the loop: a fire due then is not run,
    // whichever of the sleep and the deadline ends first at that instant
    stop := if d := p.stop_after { some(start.Add(d)) } else { none }
    scope s := io.ScopeWith(Limits(tasks: p.max_jobs + 2, stack: StackSize.Default)) {
        if d := p.stop_after {
            s.CancelAfter(d)
        }
        if !p.simulated {
            s.go Watch(&s, io, proc.Signal.Interrupt)
            s.go Watch(&s, io, proc.Signal.Terminate)
        }
        for {
            now := time.Wall(io)
            for i in 0..next.Len() {
                f := next[i] else {
                    continue
                }
                if f.at.UnixNanos() > now.UnixNanos() {
                    continue
                }
                if t := stop {
                    if f.at.UnixNanos() >= t.UnixNanos() {
                        continue
                    }
                }
                e := &tab.entries[i]
                zone := &tab.zones[e.zone].zone
                if p.simulated {
                    // a simulated fire is only logged: in the loop, so fires
                    // due at one instant are logged in the table's order
                    Launch(h, io, set, table, i, e, f, zone, true)
                } else {
                    s.go Launch(h, io, set, table, i, e, f, zone, false)
                }
                next[i] = Advance(&e.schedule, zone, f, now)
            }
            wake := Earliest(next[..]) else {
                // nothing is scheduled: wait to be stopped
                io.Sleep(time.Hours(24)) else {
                    break
                }
                continue
            }
            if t := stop {
                if wake.UnixNanos() >= t.UnixNanos() {
                    // nothing more runs before the stop: wait for the deadline
                    io.Sleep(time.Hours(24)) else {
                        break
                    }
                    continue
                }
            }
            wait := wake.Sub(now)
            if wait.Nanos() > 0 {
                io.Sleep(wait) else {
                    break
                }
            }
        }
        if s.Expired() {
            ending = Ending.Deadline
        }
    }
    return ending
}

/// The fire after `after`, for any schedule; `@reboot` fires only when
/// the daemon starts.
export Following(s: &crontab.Schedule, z: &time.Zone, after: time.Time): ?sched.Fire {
    return match s {
        .Cron(spec) { sched.Next(spec, z, after) }
        .Every(nanos) { some(sched.Fire(at: after.Add(time.Nanos(*nanos)), fit: sched.Fit.Exact)) }
        .Reboot { none }
    }
}

/// The fire after the one just started. A daemon that woke late (a
/// suspended machine) does not run the fires it slept through: the next
/// one is counted from now.
Advance(s: &crontab.Schedule, z: &time.Zone, f: sched.Fire, now: time.Time): ?sched.Fire {
    return match s {
        .Every(nanos) {
            due := f.at.Add(time.Nanos(*nanos))
            if due.UnixNanos() > now.UnixNanos() {
                some(sched.Fire(at: due, fit: sched.Fit.Exact))
            } else {
                Following(s, z, now)
            }
        }
        _ { Following(s, z, now) }
    }
}

/// The earliest pending fire; none when no entry will fire again.
Earliest(next: []?sched.Fire): ?time.Time {
    var best: ?time.Time = none
    for f in next {
        x := f else {
            continue
        }
        b := best else {
            best = some(x.at)
            continue
        }
        if x.at.UnixNanos() < b.UnixNanos() {
            best = some(x.at)
        }
    }
    return best
}

/// Cancels the loop's scope when `sig` arrives.
Watch(s: &Scope, io: Io, sig: proc.Signal) {
    proc.WaitSignal(io, sig) else {
        return
    }
    s.Cancel()
}
