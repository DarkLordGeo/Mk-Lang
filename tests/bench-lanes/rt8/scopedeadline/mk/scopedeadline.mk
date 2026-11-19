// scopedeadline, Mk: n rounds of a scope with a one-second deadline
// (Scope.CancelAfter), which never fires, split evenly over t tasks.
// Task k runs rounds i = k m .. k m + m - 1 (m = n / t), each opening a
// scope, arming its deadline, updating x = x * 31 + i (wrapping) and
// exiting the scope (Join's synchronous disarm). Each deadline is armed on
// the timer heap of the carrier that runs its task, so the tasks'
// arms and disarms do not share one lock. Prints n, t, the wrapping sum of
// the tasks' x and the rounds whose scope expired (0). Usage:
// scopedeadline <n> <t>.

import std.time

Worker(io: Io, k: int, m: int, xs: &Chan[u64], es: &Chan[u64]) {
    var x: u64 = 0
    var e: u64 = 0
    for j in 0..m {
        i := k * m + j
        scope s := io.Scope() {
            s.CancelAfter(time.Seconds(1))
            x = x *% 31 +% Cast[u64](i)
            if s.Expired() {
                e += 1
            }
        }
    }
    xs.Send(io, x) else {
        return
    }
    es.Send(io, e) else {
        return
    }
}

export Main(sys: &System): ParseError|WriteError|OutOfMemory!void {
    n := Cast[int](try sys.args[1].ParseInt())
    t := Cast[int](try sys.args[2].ParseInt())
    m := if t > 0 { n / t } else { 0 }
    a := sys.heap.Alloc()
    xs := try Chan[u64].New(a, t + 1)
    es := Chan[u64].New(a, t + 1) else e {
        xs.FreeEmpty(a)
        fail e
    }
    scope s := sys.io.Scope() {
        for k in 0..t {
            s.go Worker(sys.io, k, m, &xs, &es)
        }
    }
    var sum: u64 = 0
    var expired: u64 = 0
    for _ in 0..t {
        x := xs.Recv(sys.io) else {
            expired += 1
            0
        }
        sum = sum +% x
        v := es.Recv(sys.io) else {
            expired += 1
            0
        }
        expired += v
    }
    xs.FreeEmpty(a)
    es.FreeEmpty(a)
    var out := sys.Stdout()
    try Print(mut out, "{n} {t} {sum} {expired}\n", (n, t, sum, expired))
}
