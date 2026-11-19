// Limits: under ScopeWith(Limits(tasks: 3, ...)) never more
// than 3 tasks are live, s.go waits instead of failing, and all 20 tasks run. Each task counts
// itself in and out of an AtomicInt and records the largest count it saw.
import std.time
import std.atomic

Work(io: Io, live: &atomic.AtomicInt, peak: &atomic.AtomicInt, done: &atomic.AtomicInt) {
    n := live.Add(1)
    for {
        p := peak.Load()
        if n <= p {
            break
        }
        peak.Store(n)
    }
    io.Sleep(time.Millis(2)) else {
    }
    _ = live.Add(-1)
    _ = done.Add(1)
}

export Main(sys: &System): WriteError!void {
    live := atomic.AtomicInt.New(0)
    peak := atomic.AtomicInt.New(0)
    done := atomic.AtomicInt.New(0)
    scope s := sys.io.ScopeWith(Limits(tasks: 3, stack: StackSize.Default)) {
        var k := 0
        for k < 20 {
            k += 1
            s.go Work(sys.io, &live, &peak, &done)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "done {d} within {w}\n", (d: done.Load(), w: peak.Load() <= 3))
}
