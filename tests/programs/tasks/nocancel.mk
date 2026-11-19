// NoCancel's boundary (doc/reference.md "Cancellation" and "Cancellation and deadlines"):
// io.NoCancel() masks the cancellation in force where it is called, so neither the
// Cancel nor the deadline of a scope opened before the mask cancels an operation on the masked
// Io, while an operation on the unmasked Io is still cancelled; a scope opened from the masked
// Io afterwards is a new boundary whose own Cancel and deadline cancel the operations under it,
// and Expired tells which ended it; a scope opened below the mask from the unmasked Io does not
// cancel the masked Io; cancellation follows the scope tree for the unmasked Io; and
// the bounded shutdown: ignore the cancel in force, give up after a deadline. Short deadlines
// are 20 ms against hour-long sleeps, a masked wait that must finish is 100 ms, the shutdown's
// 20 ms flush runs under a 2 s bound, and every wait for a timer polls up to 10 s, so the
// output does not depend on the machine or its load.
import std.time

// Slept reports whether a sleep of d finished, false when it was cancelled.
Slept(io: Io, d: time.Duration): bool {
    io.Sleep(d) else {
        return false
    }
    return true
}

// WaitExpired polls s, through an Io no scope can cancel, until its deadline has fired or 10 s
// have passed, and returns Expired.
WaitExpired(io: Io, s: &Scope): bool {
    q := io.NoCancel()
    for _ in 0..1_000 {
        if s.Expired() {
            return true
        }
        _ = Slept(q, time.Millis(10))
    }
    return s.Expired()
}

export Main(sys: &System): WriteError!void {
    io := sys.io
    var out := sys.Stdout()
    hour := time.Seconds(3_600)
    short := time.Millis(20)
    masked := time.Millis(100)

    var expired := false
    var finished := false
    var unmasked := false
    scope before := io.Scope() {
        before.CancelAfter(short)
        expired = WaitExpired(io, &before)
        finished = Slept(io.NoCancel(), masked)
        unmasked = !Slept(io, hour)
    }
    try Print(mut out, "deadline of a scope opened before the mask: expired {expired}, masked sleep finished {finished}, unmasked sleep cancelled {unmasked}\n", (expired: expired, finished: finished, unmasked: unmasked))

    var after_cancel := false
    scope before := io.Scope() {
        before.Cancel()
        after_cancel = Slept(io.NoCancel(), masked)
    }
    try Print(mut out, "cancel of a scope opened before the mask: masked sleep finished {f}\n", (f: after_cancel))

    var own_deadline := false
    var own_expired := false
    scope before := io.Scope() {
        before.Cancel()
        q := io.NoCancel()
        scope after := q.Scope() {
            after.CancelAfter(short)
            own_deadline = !Slept(q, hour)
            own_expired = after.Expired()
        }
    }
    try Print(mut out, "deadline of a scope opened after the mask: masked sleep cancelled {c}, expired {e}\n", (c: own_deadline, e: own_expired))

    var own_cancel := false
    var cancel_expired := true
    cleanup := io.NoCancel()
    scope after := cleanup.Scope() {
        after.CancelAfter(hour)
        after.Cancel()
        own_cancel = !Slept(cleanup, hour)
        cancel_expired = after.Expired()
    }
    try Print(mut out, "cancel of a scope opened after the mask: masked sleep cancelled {c}, expired {e}\n", (c: own_cancel, e: cancel_expired))

    var below := false
    kept := io.NoCancel()
    scope under := io.Scope() {
        under.Cancel()
        below = Slept(kept, masked)
    }
    try Print(mut out, "cancel of a scope opened below the mask from the unmasked Io: masked sleep finished {f}\n", (f: below))

    var enclosing := false
    scope outer := io.Scope() {
        outer.Cancel()
        region := io.NoCancel()
        scope inner := io.Scope() {
            inner.CancelAfter(hour)
            enclosing = Slept(region, masked)
        }
    }
    try Print(mut out, "cancel of the scope enclosing the mask, under a scope opened below it from the unmasked Io: masked sleep finished {f}\n", (f: enclosing))

    // Cancellation follows the scope tree. An operation on the unmasked Io under no
    // scope of the masked one is still cancelled by the scopes enclosing the NoCancel call,
    // while a scope opened from the unmasked Io inside a scope of the masked Io
    // belongs to that scope, so they do not cancel the operations under it.
    var outside := false
    var plain_expired := true
    var inside := false
    scope outer := io.Scope() {
        outer.Cancel()
        region := io.NoCancel()
        scope plain := io.Scope() {
            outside = !Slept(io, hour)
            plain_expired = plain.Expired()
        }
        scope guarded := region.Scope() {
            guarded.CancelAfter(hour)
            scope inner := io.Scope() {
                inner.CancelAfter(hour)
                inside = Slept(io, masked)
            }
        }
    }
    try Print(mut out, "unmasked sleep in a scope from the unmasked Io, under no masked scope: cancelled {c}, expired {e}\n", (c: outside, e: plain_expired))
    try Print(mut out, "unmasked sleep in a scope from the unmasked Io, inside a masked scope: finished {f}\n", (f: inside))

    var flushed := false
    var gave_up := false
    var timed_out := false
    scope serving := io.Scope() {
        serving.Cancel()
        quiet := io.NoCancel()
        scope shutdown := quiet.Scope() {
            // The bound is 2 s, so the 20 ms flush finishes under it whatever the load.
            shutdown.CancelAfter(time.Seconds(2))
            flushed = Slept(quiet, short)
            gave_up = !Slept(quiet, hour)
            timed_out = shutdown.Expired()
        }
    }
    try Print(mut out, "bounded shutdown: flushed {flushed}, gave up {gave_up}, timed out {timed_out}\n", (flushed: flushed, gave_up: gave_up, timed_out: timed_out))
}
