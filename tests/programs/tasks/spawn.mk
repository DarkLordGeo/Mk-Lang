// Task spawn (a target of at most 1 µs per spawn): n spawns of
// a task that only adds 1 to a counter, in batches of 1,000 per scope, so the figure is time / n,
// spawn and join included. Usage: spawn <n> [time], n a multiple of 1,000. Without "time" it
// prints only `spawn <n> ran <count>`, so the .cases file can check it; with "time" it also
// prints `ns <total> per-spawn <total / n>` for a timing measurement at n = 1,000,000 (a
// nondeterministic line, never in a case).
import std.time
import std.atomic

Bump(count: &atomic.AtomicInt) {
    _ = count.Add(1)
}

export Main(sys: &System): ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    timed := sys.args.Len() > 2 && sys.args[2] == "time"
    count := atomic.AtomicInt.New(0)
    t0 := time.Now(sys.io)
    for _ in 0..n / 1000 {
        scope s := sys.io.Scope() {
            for _ in 0..1000 {
                s.go Bump(&count)
            }
        }
    }
    t1 := time.Now(sys.io)
    var out := sys.Stdout()
    try Print(mut out, "spawn {n} ran {ran}\n", (n, ran: count.Load()))
    if timed {
        ns := t1.Since(t0).Nanos()
        try Print(mut out, "ns {ns} per-spawn {per}\n", (ns, per: ns / i64(n)))
    }
}
