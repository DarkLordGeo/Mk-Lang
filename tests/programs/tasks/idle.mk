// Idle tasks, for the idle-task and task-memory budgets:
// n tasks park at once in Recv on one capacity-0 channel that nobody sends on; once all n have
// counted themselves in, Main waits 20 ms more for the last ones to park, then closes the
// channel, which wakes every waiter with Closed (`Close` wakes every
// waiter). Usage: idle <n> [hold <ms>]. Without "hold" it prints only `idle <n> woke <count>`,
// so the .cases file can check it; with "hold" Main also sleeps <ms> milliseconds while the n
// tasks are parked, so a measurement can read the process's memory from outside (Windows
// Get-Process, Linux /proc/<pid>/status) at n = 100,000 and 1,000,000, and prints
// `ns <total> per-task <total / n>` for the spawn and park phase (a nondeterministic line,
// never in a case).
import std.time
import std.atomic

Park(io: Io, ch: &Chan[int], parked: &atomic.AtomicInt, woke: &atomic.AtomicInt) {
    _ = parked.Add(1)
    x := ch.Recv(io) else {
        _ = woke.Add(1)
        return
    }
    _ = x
}

export Main(sys: &System): ParseError|OutOfMemory|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    var hold: i64 = -1
    if sys.args.Len() > 3 && sys.args[2] == "hold" {
        hold = try sys.args[3].ParseInt()
    }
    a := sys.heap.Alloc()
    ch := try Chan[int].New(a, 0)
    parked := atomic.AtomicInt.New(0)
    woke := atomic.AtomicInt.New(0)
    var ns: i64 = 0
    scope s := sys.io.Scope() {
        t0 := time.Now(sys.io)
        for _ in 0..n {
            s.go Park(sys.io, &ch, &parked, &woke)
        }
        for parked.Load() < i64(n) {
            sys.io.Sleep(time.Millis(1)) else {
                break
            }
        }
        ns = time.Now(sys.io).Since(t0).Nanos()
        sys.io.Sleep(time.Millis(20)) else {
        }
        if hold >= 0 {
            sys.io.Sleep(time.Millis(hold)) else {
            }
        }
        ch.Close()
    }
    ch.Free(a)
    var out := sys.Stdout()
    try Print(mut out, "idle {n} woke {w}\n", (n, w: woke.Load()))
    if hold >= 0 {
        try Print(mut out, "ns {ns} per-task {per}\n", (ns, per: ns / i64(n)))
    }
}
