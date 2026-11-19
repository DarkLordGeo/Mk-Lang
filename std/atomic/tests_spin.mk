// Why: Spin must exclude other tasks and hand its value back intact.
// What: tests of Spin's Lock, Ref, Mut, Store, Unlock and Into, a contended
// counter, and a Spin in a var global.
// How: run by `mk test --std=std.atomic` (std/std-missing_blocks_test.go)
// with 1 and 8 carriers.

test "Spin: Lock, Ref, Mut, Store, Unlock and Into" {
    s := Spin[i64].New(5)
    var g := s.Lock()
    Assert(*g.Ref() == 5)
    *g.Mut() = 6
    Assert(*g.Ref() == 6)
    g.Store(7)
    g.Unlock()
    var h := s.Lock()
    Assert(*h.Ref() == 7)
    h.Unlock()
    Assert(s.Into() == 7)
}

// Pair holds two counters that only change together under the lock, so a
// reader that sees them differ saw a torn critical section.
struct Pair {
    a: i64
    b: i64
}

SpinAdder(io: Io, s: &Spin[Pair], n: int, bad: &AtomicInt) {
    for i in 0..n {
        var g := s.Lock()
        p := g.Mut()
        if p.a != p.b {
            _ = bad.Add(1)
        }
        p.a += 1
        p.b += 1
        g.Unlock()
        if i % 64 == 0 {
            io.Yield() else {}
        }
    }
}

test "Spin: 16 tasks add 2,000 each under the lock" (sys: &System) {
    s := Spin[Pair].New(Pair(a: 0, b: 0))
    bad := AtomicInt.New(0)
    scope sc := sys.io.Scope() {
        for _ in 0..16 {
            sc.go SpinAdder(sys.io, &s, 2000, &bad)
        }
    }
    p := s.Into()
    Assert(p.a == 32000 && p.b == 32000)
    Assert(bad.Load() == 0)
}

// A global spin lock (no allocation in New).
var GlobalSpin: Spin[int] = Spin[int].New(0)

test "Spin: a var global" {
    var g := GlobalSpin.Lock()
    *g.Mut() += 1
    g.Unlock()
    var h := GlobalSpin.Lock()
    Assert(*h.Ref() >= 1)
    h.Unlock()
}
