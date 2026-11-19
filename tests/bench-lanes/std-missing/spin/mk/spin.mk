// spin, Mk: the workload of ../README.md with std.atomic.Spin (a
// test-and-test-and-set lock with bounded exponential backoff, Anderson
// 1990; layer 0, no Io). t tasks of one scope; each takes the lock n times
// and adds one to the shared counter. Prints the counter (t * n).
// Usage: spin <n> <t>.

import std.atomic

Worker(l: &atomic.Spin[u64], n: int) {
    for _ in 0..n {
        var g := l.Lock()
        *g.Mut() += 1
        g.Unlock()
    }
}

export Main(sys: &System): ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    t := Cast[int](try sys.args[2].ParseInt())
    l := atomic.Spin[u64].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..t {
            s.go Worker(&l, n)
        }
    }
    c := l.Into()
    var out := sys.Stdout()
    try Print(mut out, "{c}\n", (c))
}
