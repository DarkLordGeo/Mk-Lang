// barrier, Mk: the workload of ../README.md with std.sync.Barrier (a
// generation barrier). t tasks of one scope wait at one barrier of
// t, r rounds; each counts the rounds it led. Prints the leaders (r) and r.
// Usage: barrier <r> <t>.

import std.atomic
import std.sync

Worker(b: &sync.Barrier, io: Io, r: int, leaders: &atomic.AtomicInt) {
    var k: i64 = 0
    for _ in 0..r {
        leader := b.Wait(io) else {
            return
        }
        if leader {
            k += 1
        }
    }
    _ = leaders.Add(k)
}

export Main(sys: &System): ParseError|WriteError!void {
    r := Cast[int](try sys.args[1].ParseInt())
    t := Cast[int](try sys.args[2].ParseInt())
    b := sync.Barrier.New(t)
    leaders := atomic.AtomicInt.New(0)
    scope sc := sys.io.Scope() {
        for _ in 0..t {
            sc.go Worker(&b, sys.io, r, &leaders)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{l} {r}\n", (l: leaders.Load(), r))
}
