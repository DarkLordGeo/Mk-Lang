// rwlock, Mk: the workload of ../README.md with std.sync.RwLock (a
// writer-preferring reader-writer lock after Go's sync.RWMutex).
// t tasks of one scope; each does n operations on one shared pair: every
// tenth (i % 10 == 0) takes the write lock and adds one to both fields,
// the others take the read lock and count a pair whose fields differ.
// Prints the final first field (t * ceil(n / 10)) and the mismatches (0).
// Usage: rwlock <n> <t>.

import std.atomic
import std.sync

struct Pair {
    a: u64
    b: u64
}

Worker(l: &sync.RwLock[Pair], io: Io, n: int, bad: &atomic.AtomicInt) {
    var miss: i64 = 0
    for i in 0..n {
        if i % 10 == 0 {
            var g := l.Lock(io) else {
                _ = bad.Add(1)
                return
            }
            p := g.Mut()
            p.a += 1
            p.b += 1
            g.Unlock()
        } else {
            r := l.LockRead(io) else {
                _ = bad.Add(1)
                return
            }
            p := r.Ref()
            if p.a != p.b {
                miss += 1
            }
            r.Unlock()
        }
    }
    _ = bad.Add(miss)
}

export Main(sys: &System): ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    t := Cast[int](try sys.args[2].ParseInt())
    l := sync.RwLock[Pair].New(Pair(a: 0, b: 0))
    bad := atomic.AtomicInt.New(0)
    scope s := sys.io.Scope() {
        for _ in 0..t {
            s.go Worker(&l, sys.io, n, &bad)
        }
    }
    p := l.Into()
    a := p.a
    m := bad.Load()
    var out := sys.Stdout()
    try Print(mut out, "{a} {m}\n", (a, m))
}
