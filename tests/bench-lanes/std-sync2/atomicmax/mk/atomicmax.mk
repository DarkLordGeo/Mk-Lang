// atomicmax, Mk: the workload of ../README.md with std.atomic.Atomic[i64]
// with explicit orders. t tasks of one scope each take a ticket n times with
// FetchAdd(1, Relaxed) on one shared counter, then raise a shared maximum
// to the ticket with a CompareExchangeWeak loop (Relaxed, Relaxed): the
// lock-free max of C++ and Rust. Relaxed is enough, since nothing else is
// published through either word and the scope's join is the edge that
// makes both visible to Main. Prints the counter and the maximum (t * n
// and t * n - 1). Usage: atomicmax <n> <t>.

import std.atomic

Worker(c: &atomic.Atomic[i64], m: &atomic.Atomic[i64], n: int) {
    for _ in 0..n {
        x := c.FetchAdd(1, atomic.Relaxed)
        var cur := m.Load(atomic.Relaxed)
        for x > cur {
            r := m.CompareExchangeWeak(cur, x, atomic.Relaxed, atomic.Relaxed)
            if r.ok {
                break
            }
            cur = r.value
        }
    }
}

export Main(sys: &System): ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    t := Cast[int](try sys.args[2].ParseInt())
    c := atomic.Atomic[i64].New(0)
    m := atomic.Atomic[i64].New(0)
    scope sc := sys.io.Scope() {
        for _ in 0..t {
            sc.go Worker(&c, &m, n)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{c} {m}\n", (c: c.Load(atomic.Relaxed), m: m.Load(atomic.Relaxed)))
}
