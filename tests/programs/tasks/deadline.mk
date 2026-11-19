// Scope deadlines: a zero or
// negative duration cancels at once; of two deadlines the earliest counts, in either order;
// Expired is true only when the scope's own timer fired while its cancel flag was clear, so it
// stays false after Cancel and after an ancestor's cancel, and stays true after a later Cancel;
// Remaining is none with no deadline and the least time left over the scope and its ancestors;
// a deadline cancels the body, the tasks and the nested scopes; under ScopeOf[E] a task maps
// its Canceled to time.TimedOut; a deadline is observed after a join by nesting; and std.sim
// fires a deadline on the simulated clock. Short deadlines are 20 ms against hour-long sleeps,
// every wait for a timer polls up to 10 s, and no line needs two statements to run within a
// short deadline of each other, so the output does not depend on the machine or its load.
import std.atomic
import std.sim
import std.time

error Busy

type Error = Busy | time.TimedOut | Canceled

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

// Hold waits d whatever the scopes around it do.
Hold(io: Io, d: time.Duration) {
    _ = Slept(io.NoCancel(), d)
}

// Fires arms the deadlines in ds, in order, then sleeps an hour, and reports whether the sleep
// was cancelled and whether the scope expired.
Fires(io: Io, ds: []time.Duration): (canceled: bool, expired: bool) {
    var canceled := false
    var expired := false
    scope s := io.Scope() {
        for d in ds {
            s.CancelAfter(d)
        }
        canceled = !Slept(io, time.Seconds(3_600))
        expired = s.Expired()
    }
    return (canceled: canceled, expired: expired)
}

// Sleeper sleeps an hour, and counts itself in n when it was cancelled.
Sleeper(io: Io, n: &atomic.AtomicInt) {
    if !Slept(io, time.Seconds(3_600)) {
        _ = n.Add(1)
    }
}

// Wait sleeps an hour unless its scope ends first, and says why it ended.
Wait(io: Io, s: &Scope[Error]): Error!void {
    io.Sleep(time.Seconds(3_600)) else {
        if s.Expired() {
            fail time.TimedOut
        }
        fail Canceled
    }
}

// Typed runs Wait under a 20 ms deadline on a ScopeOf[Error].
Typed(io: Io): Error!void {
    try scope s := io.ScopeOf[Error]() {
        s.CancelAfter(time.Millis(20))
        s.go Wait(io, &s)
    }
}

// Within reports whether the optional duration d is present and within (lo, hi].
Within(d: ?time.Duration, lo: time.Duration, hi: time.Duration): bool {
    v := d else {
        return false
    }
    return v.Nanos() > lo.Nanos() && v.Nanos() <= hi.Nanos()
}

// Simulated fires a 5 ms deadline under a Sim of seed against an hour-long sleep, and reports
// whether the sleep was cancelled, whether the scope expired, and whether the simulated time it
// took is at least 5 ms and under a second.
Simulated(a: &Arena, seed: u64): OutOfMemory!(canceled: bool, expired: bool, timely: bool) {
    var sm := try sim.Sim.New(a, seed)
    io := sm.Io()
    var canceled := false
    var expired := false
    t0 := time.Now(io)
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(5))
        canceled = !Slept(io, time.Seconds(3_600))
        expired = s.Expired()
    }
    took := time.Now(io).Since(t0).Nanos()
    sm.Free()
    return (canceled: canceled, expired: expired, timely: took >= 5_000_000 && took < 1_000_000_000)
}

export Main(sys: &System): WriteError!void {
    io := sys.io
    var out := sys.Stdout()
    hour := time.Seconds(3_600)
    short := time.Millis(20)
    zero := [time.Millis(0)]
    z := Fires(io, zero[..])
    try Print(mut out, "zero: canceled {z.canceled}, expired {z.expired}\n", (z: z))
    negative := [time.Seconds(-1)]
    g := Fires(io, negative[..])
    try Print(mut out, "negative: canceled {g.canceled}, expired {g.expired}\n", (g: g))
    long_short := [hour, short]
    ls := Fires(io, long_short[..])
    try Print(mut out, "earliest, long then short: canceled {ls.canceled}, expired {ls.expired}\n", (ls: ls))
    short_long := [short, hour]
    sl := Fires(io, short_long[..])
    try Print(mut out, "earliest, short then long: canceled {sl.canceled}, expired {sl.expired}\n", (sl: sl))

    // C8-2: no stall between two statements may change these two lines. The deadline armed
    // before the Cancel is an hour away, and the short one is armed after the Cancel, which a
    // cancelled scope never arms (the race of a short deadline with Cancel is std's
    // RT8-Dl-RaceCancel, std/task/tests_deadline.mk).
    var after_cancel := true
    scope s := io.Scope() {
        s.CancelAfter(hour)
        s.Cancel()
        s.CancelAfter(short)
        Hold(io, time.Millis(100))
        after_cancel = s.Expired()
    }
    try Print(mut out, "cancel first: expired {e}\n", (e: after_cancel))

    var after_ancestor := true
    scope outer := io.Scope() {
        scope inner := io.Scope() {
            inner.CancelAfter(hour)
            outer.Cancel()
            inner.CancelAfter(short)
            Hold(io, time.Millis(100))
            after_ancestor = inner.Expired()
        }
    }
    try Print(mut out, "ancestor cancelled first: expired {e}\n", (e: after_ancestor))

    var fired := false
    var still := false
    scope s := io.Scope() {
        s.CancelAfter(short)
        fired = WaitExpired(io, &s)
        s.Cancel()
        still = s.Expired()
    }
    try Print(mut out, "fired: expired {fired}, after a later cancel {still}\n", (fired: fired, still: still))

    var unset := false
    var own := false
    var inherited := false
    var later := false
    var earlier := false
    var outer_kept := false
    scope outer := io.Scope() {
        unset = outer.Remaining().IsNone()
        outer.CancelAfter(hour)
        own = Within(outer.Remaining(), time.Seconds(3_000), hour)
        scope inner := io.Scope() {
            inherited = Within(inner.Remaining(), time.Seconds(3_000), hour)
            inner.CancelAfter(time.Seconds(7_200))
            later = Within(inner.Remaining(), time.Seconds(3_000), hour)
            inner.CancelAfter(time.Seconds(600))
            earlier = Within(inner.Remaining(), time.Seconds(500), time.Seconds(600))
            outer_kept = Within(outer.Remaining(), time.Seconds(3_000), hour)
        }
    }
    try Print(mut out, "remaining: none without a deadline {unset}, own {own}, from an ancestor {inherited}\n", (unset: unset, own: own, inherited: inherited))
    try Print(mut out, "remaining: a later own deadline {later}, an earlier one {earlier}, the ancestor's unchanged {outer_kept}\n", (later: later, earlier: earlier, outer_kept: outer_kept))

    n := atomic.AtomicInt.New(0)
    var body := false
    scope s := io.Scope() {
        s.CancelAfter(short)
        s.go Sleeper(io, &n)
        scope inner := io.Scope() {
            inner.go Sleeper(io, &n)
            inner.go Sleeper(io, &n)
        }
        body = !Slept(io, hour)
    }
    try Print(mut out, "cancelled by a deadline: body {body}, tasks {k}\n", (body: body, k: n.Load()))

    Typed(io) else e {
        try Print(mut out, "typed: {e}\n", (e: e))
    }

    m := atomic.AtomicInt.New(0)
    var joined := false
    scope outer := io.Scope() {
        outer.CancelAfter(short)
        scope inner := io.Scope() {
            inner.go Sleeper(io, &m)
            inner.go Sleeper(io, &m)
        }
        joined = outer.Expired()
    }
    try Print(mut out, "observed after a join: expired {joined}, tasks {k}\n", (joined: joined, k: m.Load()))

    for seed in [1, 2, 3] {
        r := Simulated(sys.arena, Cast[u64](seed)) else {
            try Print(mut out, "sim {seed}: out of memory\n", (seed: seed))
            continue
        }
        try Print(mut out, "sim {seed}: canceled {r.canceled}, expired {r.expired}, on the simulated clock {r.timely}\n", (seed: seed, r: r))
    }
}
