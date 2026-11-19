// Why: a server scales over cores and survives a crashing worker by running
// several worker processes on one listening socket; forking a process with
// carriers running is unsafe, so workers are started by re-exec instead.
//
// What: proc.Supervise, IsWorker, and SuperviseOn (Supervise over any
// listener's runtime handle), with Slot, the loop that keeps one worker
// running and restarts it with a backoff.
//
// How: the parent starts each worker as this executable again (an empty
// args[0] to the runtime's spawn), with this process's arguments, its
// standard streams and the listener inherited (the runtime's spawn: a handle
// list on Windows, descriptor 3 on Linux, MK_INHERIT in the child's
// environment). A child runs Main from the start, so its own Supervise
// call finds MK_INHERIT and runs the worker once instead.

import std.core
import std.net
import std.raw

// RestartBurst restarts of one slot in RestartPeriod start its backoff
// (Erlang/OTP's supervisor restart intensity and period: a backoff after
// the third restart in a minute).
const RestartBurst: int = 3
const RestartPeriod: i64 = 60_000_000_000

// The backoff: BackoffFirst, doubling per further restart inside the
// period, at most BackoffMax; a child that ran RestartPeriod resets it
// (Kubernetes' CrashLoopBackOff: doubling, capped, reset after a clean run).
const BackoffFirst: i64 = 1_000_000_000
const BackoffMax: i64 = 30_000_000_000

// Now is the monotonic clock in nanoseconds (0 for a table with none).
Now(io: Io): i64 {
    r := core.IoNow(io)
    if r.err != core.SysErr.None {
        return 0
    }
    return r.value
}

// Backoff is the state of one slot: the times of its last RestartBurst
// restarts in a ring, how many restarts there were, and the current delay.
struct Backoff {
    at: [3]i64
    n: int
    delay: i64
}

// Next returns the delay before the next restart at now and records the
// restart: 0 unless the restart RestartBurst back was less than
// RestartPeriod ago; then the delay doubles from BackoffFirst to BackoffMax.
// The window slides: at the cap, every third restart finds the restart
// three back 60 s old and is at once, so a crash loop gets at most
// RestartBurst restarts in any RestartPeriod (OTP's intensity rule).
Backoff.Next(mut self, now: i64): i64 {
    k := self.n % RestartBurst
    d: i64 = if self.n >= RestartBurst && now - self.at[k] < RestartPeriod {
        self.delay = if self.delay == 0 {
            BackoffFirst
        } else if self.delay < BackoffMax / 2 {
            self.delay * 2
        } else {
            BackoffMax
        }
        self.delay
    } else {
        0
    }
    self.at[k] = now + d
    self.n += 1
    return d
}

// Ran records that a worker ran for ran nanoseconds: a run of at least
// RestartPeriod ends the crash loop, so the next backoff starts again at
// BackoffFirst (CrashLoopBackOff's reset after a clean run).
Backoff.Ran(mut self, ran: i64) {
    if ran >= RestartPeriod {
        self.delay = 0
    }
}

// SlotTrace is Slot's log for the backoff block of tests_supervise.mk,
// so that block reads what Slot decided and when, from Slot's own
// clock readings, instead of counting a worker's answers in a fixed time.
// Tests only. Only that block's parent arms it, and it supervises one
// worker, so one Slot writes it. Unarmed, which every other process is,
// Slot pays one load per start, per exit and per restart, and logs
// nothing.
struct SlotTraceWords {
    armed: raw.Atomic[u32]
    n: raw.Atomic[i64]
    log: Mutex[SlotLog]
}

// SlotLog holds the events: kind, clock reading and argument of each.
struct SlotLog {
    kind: [SlotTraceMax]i64
    at: [SlotTraceMax]i64
    arg: [SlotTraceMax]i64
}

// The trace's capacity in events, and its event kinds: a start (at: the
// clock just before the spawn call), a failed spawn, a wait's end (at:
// just after it; arg: the exit code, -1 for a failed wait), and a restart
// decision (at: the time given to Backoff.Next; arg: the delay it gave).
const SlotTraceMax: int = 24
const SlotStart: i64 = 1
const SlotSpawnFailed: i64 = 2
const SlotExited: i64 = 3
const SlotDecided: i64 = 4

var SlotTrace: SlotTraceWords = SlotTraceWords(armed: raw.Atomic[u32].New(0), n: raw.Atomic[i64].New(0),
    log: Mutex[SlotLog].New(SlotLog(kind: [0; SlotTraceMax], at: [0; SlotTraceMax], arg: [0; SlotTraceMax])))

// SlotTraced: the trace is armed.
SlotTraced(): bool {
    // Edge: the arming block stores armed before its SuperviseOn starts
    // the slot's task, so the slot reads 1 from its first event.
    return SlotTrace.armed.Load() != 0
}

// SlotNote appends one event while the trace is armed and has room, under
// the log's lock; n counts the events, for a reader that only waits.
SlotNote(io: Io, kind: i64, at: i64, arg: i64) {
    if !SlotTraced() {
        return
    }
    // A NoCancel lock on a native Io does not fail; if one did, the event
    // would be missing, and the block that reads the trace fails.
    var g := SlotTrace.log.Lock(io.NoCancel()) else {
        return
    }
    // Edge: n is stored only under the lock; its load here is ordered by
    // the lock, and the waiting reader's load needs no more than its value.
    i := Trunc[int](SlotTrace.n.Load())
    if i >= 0 && i < SlotTraceMax {
        p := g.Mut()
        p.kind[i] = kind
        p.at[i] = at
        p.arg[i] = arg
        SlotTrace.n.Store(i64(i + 1))
    }
    g.Unlock()
}

// WaitEnds: a failed wait for a worker with error e ends its slot. Only a
// cancel does; anything else is that worker's crash, so one failing wait
// never stops the other slots.
WaitEnds(e: core.SysErr): bool {
    return e == core.SysErr.Canceled
}

// Slot keeps one worker running: start it, wait for it, start it again,
// with the backoff above. A failed first start is the slot's error, which
// cancels the others (Supervise's scope); a failed later start counts as
// a crash and is tried again after the backoff. On a cancel the child is
// killed and reaped before the slot returns Canceled, so Supervise's join
// waits for every child to be gone.
Slot(io: Io, args: []str, h: uint): Error!void {
    var b := Backoff(at: [0; 3], n: 0, delay: 0)
    var first := true
    for {
        if !first {
            now := Now(io)
            d := b.Next(now)
            SlotNote(io, SlotDecided, now, d)
            if d > 0 {
                try io.Sleep(core.DurationOf(d))
            }
        }
        if SlotTraced() {
            SlotNote(io, SlotStart, Now(io), 0)
        }
        var c := core.IoChild(stdio: [core.IoStdInherit, core.IoStdInherit, core.IoStdInherit], inherit: h, pipes: [0; 3], pid: 0)
        r := core.IoSpawn(io, args, mut c)
        if r.err != core.SysErr.None {
            if SlotTraced() {
                SlotNote(io, SlotSpawnFailed, Now(io), 0)
            }
            if first || r.err == core.SysErr.Canceled {
                _ = try Check(r)
            }
            continue
        }
        first = false
        ph := Trunc[uint](r.value)
        start := Now(io)
        w := core.IoProcWait(io, ph)
        if SlotTraced() {
            code: i64 = if w.err == core.SysErr.None {
                w.value
            } else {
                -1
            }
            SlotNote(io, SlotExited, Now(io), code)
        }
        if w.err == core.SysErr.None {
            _ = core.IoClose(io, ph)
        } else {
            // The child is killed and reaped on every failed wait. A
            // cancel ends the slot; any other failure is a crash of this
            // worker only: it is started again after the backoff, and the
            // other slots go on serving.
            Reap(io, ph)
            if WaitEnds(w.err) {
                fail Canceled
            }
        }
        b.Ran(Now(io) - start)
    }
}

// SupervisedChild: this process was started by a Supervise parent.
// The runtime's spawn puts MK_INHERIT only in a child given a listener, strips
// it from every environment that child passes on, and Process.env is read
// at start, before the runtime's Inherited removes the variable.
SupervisedChild(sys: &System): bool {
    return sys.Env("MK_INHERIT").IsSome()
}

/// Whether this process is a worker that Supervise started. A program
/// uses it to run the code before its Supervise call in the parent only.
/// Read from the environment as the process started. O(environment size);
/// allocates nothing.
export IsWorker(sys: &System): bool {
    return SupervisedChild(sys)
}

// SuperviseOn is Supervise over the listener's runtime handle h, for any
// listener type L (the exported Supervise passes net.Listener's handle).
// In a child it runs worker once and returns its result; in the parent it
// keeps workers children running until it is cancelled, then returns
// Canceled once every child exited.
SuperviseOn[E, L](sys: &System, workers: int, h: uint, l: &L, const worker: fn(&System, &L): E!void): E|Error!void {
    if workers < 1 {
        raw.Fail(8, "proc.Supervise: workers is less than 1")
    }
    if SupervisedChild(sys) {
        try worker(sys, l)
        return
    }
    var argv := List[str].New(sys.arena)
    try argv.Push("")
    for i in 1..sys.args.Len() {
        try argv.Push(sys.args[i])
    }
    args := argv.Done()
    try scope s := sys.io.ScopeOf[Error]() {
        for _ in 0..workers {
            s.go Slot(sys.io, args[..], h)
        }
    }
}

/// Runs worker in workers processes that share the listener l, and keeps
/// them running. This program is started again as each worker process,
/// with l inherited. In a worker, Supervise runs worker once and returns
/// its result, and net.Listen returns the inherited listener.
///
/// In the parent, a worker that exits for any reason, a panic included, is
/// started again, with a backoff after its third restart within a minute.
/// The parent returns Canceled once it is cancelled and every worker has
/// exited, or the error of the first start. workers < 1 panics (code 8).
///
/// The parent's cost is O(workers) runtime calls per round of restarts,
/// plus the backoff's sleeps; it allocates its argument list in
/// sys.arena.
export Supervise[E](sys: &System, workers: int, l: &net.Listener, const worker: fn(&System, &net.Listener): E!void): E|Error!void {
    try SuperviseOn[E, net.Listener](sys, workers, l.IoHandle(), l, worker)
}
