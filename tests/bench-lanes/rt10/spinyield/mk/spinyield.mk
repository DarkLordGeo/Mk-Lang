// spinyield, Mk: t tasks each take one atomic.Spin n times, the guarded
// value updated as x = x * 31 + i (wrapping) under the lock, and the
// critical section then spins w pause-free loads of a local, so a holder
// preempted inside it keeps every waiter spinning. Run with MK_CARRIERS
// above the CPU count, the case Spin's yield after 64 failed tries is for
// (a preempted holder); run with t = 1 for the uncontended cost. Prints
// t, n and the sum of the per-task counts (t * n when every lock ran).
// Usage: spinyield <t> <n> <w>.

import std.atomic

Worker(sp: &atomic.Spin[u64], done: &atomic.Atomic[i64], n: int, w: int) {
    var c: i64 = 0
    for i in 0..n {
        var g := sp.Lock()
        x := *g.Ref()
        var k := 0
        var y := x
        for k < w {
            y = y *% 33 +% 1
            k += 1
        }
        g.Store(y *% 31 +% Cast[u64](i))
        g.Unlock()
        c += 1
    }
    _ = done.FetchAdd(c, atomic.Relaxed)
}

export Main(sys: &System): ParseError|WriteError!void {
    t := Cast[int](try sys.args[1].ParseInt())
    n := Cast[int](try sys.args[2].ParseInt())
    w := Cast[int](try sys.args[3].ParseInt())
    sp := atomic.Spin[u64].New(0)
    done := atomic.Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..t {
            s.go Worker(&sp, &done, n, w)
        }
    }
    total := done.Load(atomic.SeqCst)
    var out := sys.Stdout()
    try Print(mut out, "{t} {n} {total}\n", (t: t, n: n, total: total))
}
