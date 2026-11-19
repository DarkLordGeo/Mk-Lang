// semaphore, Mk: the workload of ../README.md with std.sync.Semaphore
// (Go sync.Mutex's two modes over tokio's batch semaphore).
// t tasks of one scope; each does n rounds of Acquire(1) on a
// semaphore of 4 permits, a check that at most 4 hold one (an atomic
// count), and Release(1). Prints t * n and the violations (0).
// Usage: semaphore <n> <t>.

import std.atomic
import std.sync

Worker(s: &sync.Semaphore, io: Io, n: int, inuse: &atomic.AtomicInt, bad: &atomic.AtomicInt, done: &atomic.AtomicInt) {
    var k: i64 = 0
    for _ in 0..n {
        s.Acquire(io, 1) else {
            _ = bad.Add(1)
            return
        }
        if inuse.Add(1) > 4 {
            _ = bad.Add(1)
        }
        _ = inuse.Add(-1)
        s.Release(1)
        k += 1
    }
    _ = done.Add(k)
}

export Main(sys: &System): ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    t := Cast[int](try sys.args[2].ParseInt())
    s := sync.Semaphore.New(4)
    inuse := atomic.AtomicInt.New(0)
    bad := atomic.AtomicInt.New(0)
    done := atomic.AtomicInt.New(0)
    scope sc := sys.io.Scope() {
        for _ in 0..t {
            sc.go Worker(&s, sys.io, n, &inuse, &bad, &done)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{d} {b}\n", (d: done.Load(), b: bad.Load()))
}
