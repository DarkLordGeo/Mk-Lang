// notify, Mk: the workload of ../README.md with std.sync.Notify.
// Two tasks of one scope alternate n times: the first notifies the second
// and waits for its own Notify, the second waits for its own and notifies
// the first. A notification that finds nobody waiting is a stored permit,
// so no turn variable or lock is needed (the reference's replacement for
// a condition variable). Each counts its rounds; prints the total (2 * n).
// Usage: notify <n>.

import std.atomic
import std.sync

Side(mine: &sync.Notify, other: &sync.Notify, io: Io, n: int, first: bool, total: &atomic.AtomicInt) {
    var k: i64 = 0
    for _ in 0..n {
        if first {
            other.NotifyOne()
            mine.Wait(io) else {
                return
            }
        } else {
            mine.Wait(io) else {
                return
            }
            other.NotifyOne()
        }
        k += 1
    }
    _ = total.Add(k)
}

export Main(sys: &System): ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    a := sync.Notify.New()
    b := sync.Notify.New()
    total := atomic.AtomicInt.New(0)
    scope sc := sys.io.Scope() {
        sc.go Side(&a, &b, sys.io, n, true, &total)
        sc.go Side(&b, &a, sys.io, n, false, &total)
    }
    var out := sys.Stdout()
    try Print(mut out, "{t}\n", (t: total.Load()))
}
