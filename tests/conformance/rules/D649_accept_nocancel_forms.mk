// verdict: ok
// rule: D-649
// note: bounded shutdown (a scope opened from the masked Io is a new boundary with its own deadline), and the form that keeps an outer deadline in force (the scope opened outside the mask, the masked Io given only to the operation that must finish)
import std.time

// Flush stands for work that must finish once started.
Flush(io: Io): Canceled!void {
    try io.Sleep(time.Millis(1))
}

// Shutdown ignores the cancel in force and gives up after two seconds,
// telling a timeout from an end.
Shutdown(io: Io): bool {
    var timed_out := false
    q := io.NoCancel()
    scope s := q.Scope() {
        s.CancelAfter(time.Seconds(2))
        Flush(q) else {
        }
        timed_out = s.Expired()
    }
    return timed_out
}

// Bounded keeps its own deadline over everything but the one flush.
Bounded(io: Io): bool {
    var flushed := false
    scope s := io.Scope() {
        s.CancelAfter(time.Seconds(5))
        Flush(io.NoCancel()) else {
            return false
        }
        flushed = true
        io.Sleep(time.Millis(1)) else {
        }
    }
    return flushed
}
