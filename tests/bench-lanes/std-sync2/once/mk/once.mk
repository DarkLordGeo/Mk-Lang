// once, Mk: the workload of ../README.md with std.sync.Once. t
// tasks of one scope; each does n rounds of Get on one shared Once[i64],
// whose init returns 7 the first time, and adds the value. Prints the sum
// (7 * t * n). Usage: once <n> <t>.

import std.atomic
import std.sync

Worker(o: &sync.Once[i64], io: Io, n: int, sum: &atomic.AtomicInt) {
    var k: i64 = 0
    for _ in 0..n {
        v := o.Get(io, fn(): i64 { return 7 }) else {
            return
        }
        k += *v
    }
    _ = sum.Add(k)
}

export Main(sys: &System): ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    t := Cast[int](try sys.args[2].ParseInt())
    o := sync.Once[i64].New()
    sum := atomic.AtomicInt.New(0)
    scope sc := sys.io.Scope() {
        for _ in 0..t {
            sc.go Worker(&o, sys.io, n, &sum)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{s}\n", (s: sum.Load()))
}
