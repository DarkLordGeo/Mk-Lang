// Why: every task of one `procman start` must agree on how many processes
// still run and why procman stopped.
//
// What: Control (New, Stop, Reason, Ended), WatchSignal and StopAfter.
//
// How: atomics only, so Control is `Share` and every task holds it.

import std.atomic
import std.proc
import std.time

export view struct Control {
    live: atomic.AtomicInt
    stop_code: atomic.Atomic[i64]
}

/// A control for `tasks` process tasks, none of them ended.
export Control.New(tasks: int): Control {
    return Control(live: atomic.AtomicInt.New(i64(tasks)), stop_code: atomic.Atomic[i64].New(0))
}

/// Records `r` unless a reason is already recorded, then cancels `s`, which
/// stops every process task, the signal watchers and the status workers.
export Control.Stop(&self, s: &Scope, r: StopReason) {
    _ = self.stop_code.CompareExchange(0, Code(r), atomic.SeqCst, atomic.SeqCst)
    s.Cancel()
}

/// Why procman stopped: `Finished` unless a stop was recorded.
export Control.Reason(&self): StopReason {
    return match self.stop_code.Load(atomic.SeqCst) {
        2 { StopReason.MainEnded }
        3 { StopReason.Interrupt }
        4 { StopReason.Terminate }
        5 { StopReason.Hangup }
        6 { StopReason.Timeout }
        _ { StopReason.Finished }
    }
}

/// Counts one process task as ended, and returns true for the last one.
export Control.Ended(&self): bool {
    return self.live.Add(-1) == 0
}

Code(r: StopReason): i64 {
    return match r {
        .Finished { 1 }
        .MainEnded { 2 }
        .Interrupt { 3 }
        .Terminate { 4 }
        .Hangup { 5 }
        .Timeout { 6 }
    }
}

/// A task that stops everything when `sig` arrives. Watching a signal takes
/// its default action away for the rest of the process, so procman, not the
/// OS, decides what Ctrl-C does.
export WatchSignal(s: &Scope, io: Io, sig: proc.Signal, c: &Control) {
    proc.WaitSignal(io, sig) else {
        return
    }
    reason := match sig {
        .Interrupt { StopReason.Interrupt }
        .Terminate { StopReason.Terminate }
        .Hangup { StopReason.Hangup }
        _ {
            Panic("proc.Signal gained a member this match does not handle")
        }
    }
    c.Stop(s, reason)
}

/// A task that stops everything after `ms` milliseconds.
export StopAfter(s: &Scope, io: Io, ms: i64, c: &Control) {
    io.Sleep(time.Millis(ms)) else {
        return
    }
    c.Stop(s, StopReason.Timeout)
}
