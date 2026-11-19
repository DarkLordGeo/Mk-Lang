// atomicadd, Mk: the workload of ../README.md with std.atomic.AtomicInt
// (SYNC2-R6). t tasks of one scope each add 1 to one shared AtomicInt n
// times: every Add is a lock xadd on one contended cache line, then the
// overflow check (AtomicInt.Add returns the new value and
// panics on overflow). Prints the counter (t * n). Usage: atomicadd <n> <t>.

import std.atomic

Worker(c: &atomic.AtomicInt, n: int) {
    for _ in 0..n {
        _ = c.Add(1)
    }
}

export Main(sys: &System): ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    t := Cast[int](try sys.args[2].ParseInt())
    c := atomic.AtomicInt.New(0)
    scope sc := sys.io.Scope() {
        for _ in 0..t {
            sc.go Worker(&c, n)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{c}\n", (c: c.Load()))
}
