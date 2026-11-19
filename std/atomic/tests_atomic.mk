// Why: AtomicInt and AtomicBool must count and swap exactly under contention.
// What: tests of New, Load, Store, Swap and Add, at the i64 edges and from
// many tasks.
// How: run by `mk test --std=std.atomic` from std's Go tests. The overflow
// panic of Add is a Go test (TestSTDSYNC_AtomicAddMatchesBigInt, against
// math/big), since a test that must panic cannot be a passing block.

test "New, Load, Store and Swap" {
    a := AtomicInt.New(40)
    Assert(a.Load() == 40)
    a.Store(-7)
    Assert(a.Load() == -7)
    b := AtomicBool.New(false)
    Assert(!b.Load())
    Assert(!b.Swap(true))
    Assert(b.Swap(true))
    Assert(b.Load())
    b.Store(false)
    Assert(!b.Swap(false))
    Assert(!b.Load())
}

test "Add returns the new value up to the i64 edges" {
    a := AtomicInt.New(MaxI64 - 1)
    Assert(a.Add(1) == MaxI64)
    Assert(a.Add(0) == MaxI64)
    Assert(a.Add(MinI64) == -1)
    Assert(a.Add(MinI64 + 1) == MinI64)
    Assert(a.Load() == MinI64)
    Assert(a.Add(MaxI64) == -1)
    Assert(a.Add(-5) == -6)
    Assert(a.Add(6) == 0)
}

Adder(io: Io, a: &AtomicInt, k: i64) {
    for i in 0..1000 {
        _ = a.Add(k)
        if i % 7 == 0 {
            io.Yield() else {}
        }
    }
}

test "Add from 8 tasks counts every add once" (sys: &System) {
    a := AtomicInt.New(0)
    scope s := sys.io.Scope() {
        for k in 1..9 {
            s.go Adder(sys.io, &a, i64(k))
        }
    }
    // 1000 * (1 + ... + 8)
    Assert(a.Load() == 36000)
}

Flagger(io: Io, f: &AtomicBool, wins: &AtomicInt) {
    io.Yield() else {}
    if !f.Swap(true) {
        _ = wins.Add(1)
    }
}

test "exactly one of 16 concurrent Swap(true) sees false" (sys: &System) {
    f := AtomicBool.New(false)
    wins := AtomicInt.New(0)
    scope s := sys.io.Scope() {
        for _ in 0..16 {
            s.go Flagger(sys.io, &f, &wins)
        }
    }
    Assert(wins.Load() == 1)
    Assert(f.Load())
}
