// Why: Io is every program's door to the OS; its cancellation points,
// randomness and scope limits must behave the same on every carrier count.
// What: test blocks for Sleep(0) as a cancellation point, Random, Yield,
// NoCancel in cleanup, ScopeWith limits, HashKey.New, an inert Io, an Io
// copied into a struct, and the size of Io.
// How: std/std-io_coreblocks_test.go's TestStdIo_CoreTestBlocks runs them in
// child processes with the counting heap, at 1 and 8 carriers. Helper names
// start with Tio; std.core cannot import std.atomic, so counters are
// raw.Atomic.

import std.raw

TioMs(n: i64): Duration {
    return DurationOf(n * 1_000_000)
}

// TioSleepFails is 1 when io.Sleep(d) is Canceled, else 0.
TioSleepFails(io: Io, d: Duration): int {
    io.Sleep(d) else {
        return 1
    }
    return 0
}

test "Sleep(0) is a cancellation point and otherwise returns at once" (sys: &System) {
    io := sys.io
    Assert(TioSleepFails(io, DurationOf(0)) == 0)
    Assert(TioSleepFails(io, DurationOf(-5)) == 0)
    Assert(TioSleepFails(io, DurationOf(-9_223_372_036_854_775_807 - 1)) == 0)
    var inside := 0
    var again := 0
    var shielded := 0
    scope s := io.Scope() {
        s.Cancel()
        inside = TioSleepFails(io, DurationOf(0))
        // Level-triggered: every later point in the body fails too.
        again = TioSleepFails(io, TioMs(1))
        shielded = TioSleepFails(io.NoCancel(), DurationOf(0))
    }
    Assert(inside == 1)
    Assert(again == 1)
    Assert(shielded == 0)
    // Outside the cancelled scope the Io is not cancelled.
    Assert(TioSleepFails(io, DurationOf(0)) == 0)
}

test "Random fills the buffer, and is Canceled in a cancelled scope" (sys: &System) {
    io := sys.io
    var a: [64]u8 = [0; 64]
    var b: [64]u8 = [0; 64]
    try io.Random(mut a[..])
    try io.Random(mut b[..])
    var nz := 0
    var same := 0
    for i in 0..64 {
        if a[i] != 0 {
            nz += 1
        }
        if a[i] == b[i] {
            same += 1
        }
    }
    // 64 random bytes have fewer than 32 zeros, and two draws differ, with
    // overwhelming probability (each failure is below 2^-100).
    Assert(nz > 32)
    Assert(same < 32)
    var e: [0]u8 = []
    try io.Random(mut e[..])
    var c: [8]u8 = [0; 8]
    var failed := 0
    scope s := io.Scope() {
        s.Cancel()
        io.Random(mut c[..]) else {
            failed = 1
        }
    }
    Assert(failed == 1)
    // A Random that is Canceled wrote nothing (cancellation is checked first).
    for i in 0..8 {
        Assert(c[i] == 0)
    }
}

TioYielder(io: Io, n: int, done: &raw.Atomic[i64]) {
    for _ in 0..n {
        io.Yield() else {
            return
        }
    }
    _ = done.Add(1)
}

test "Yield lets every task run and is Canceled in a cancelled scope" (sys: &System) {
    io := sys.io
    done := raw.Atomic[i64].New(0)
    scope s := io.Scope() {
        for _ in 0..8 {
            s.go TioYielder(io, 1_000, &done)
        }
        TioYielder(io, 1_000, &done)
    }
    Assert(done.Load() == 9)
    var failed := 0
    scope s := io.Scope() {
        s.Cancel()
        io.Yield() else {
            failed = 1
        }
    }
    Assert(failed == 1)
}

test "NoCancel lets a cleanup in a cancelled scope wait" (sys: &System) {
    io := sys.io
    var slept: i64 = 0
    var plain := 0
    scope s := io.Scope() {
        s.Cancel()
        plain = TioSleepFails(io, TioMs(1))
        t0 := IoNow(io.NoCancel()).value
        io.NoCancel().Sleep(TioMs(5)) else {
            slept = -1
        }
        if slept == 0 {
            slept = IoNow(io.NoCancel()).value - t0
        }
    }
    Assert(plain == 1)
    // It really slept: at least the 5 ms asked.
    Assert(slept >= 5_000_000)
}

TioCounted(io: Io, live: &raw.Atomic[i64], peak: &raw.Atomic[i64], done: &raw.Atomic[i64]) {
    n := live.Add(1) + 1
    for {
        p := peak.Load()
        if n <= p || peak.Cas(p, n) {
            break
        }
    }
    io.Sleep(TioMs(2)) else {}
    _ = live.Add(-1)
    _ = done.Add(1)
}

test "ScopeWith keeps at most tasks live and runs every spawn" (sys: &System) {
    io := sys.io
    live := raw.Atomic[i64].New(0)
    peak := raw.Atomic[i64].New(0)
    done := raw.Atomic[i64].New(0)
    scope s := io.ScopeWith(Limits(tasks: 3, stack: StackSize.Default)) {
        for _ in 0..20 {
            s.go TioCounted(io, &live, &peak, &done)
        }
    }
    Assert(done.Load() == 20)
    Assert(peak.Load() >= 1)
    Assert(peak.Load() <= 3)
    Assert(live.Load() == 0)
}

struct TioCtx {
    io: Io
}

TioWork(ctx: TioCtx, ch: &Chan[int]) {
    ctx.io.Sleep(TioMs(10)) else {
        return
    }
    ch.Send(ctx.io, 42) else {}
}

TioRecv(ch: &Chan[int], io: Io): int {
    x := ch.Recv(io) else {
        return -1
    }
    return x
}

test "An Io copied into a struct acts for the task that uses it" (sys: &System) {
    a := sys.heap.Alloc()
    ch := try Chan[int].New(a, 0)
    var got := 0
    scope s := sys.io.Scope() {
        // If the runtime took the task from the Io, the child's sleep
        // would park the parent, which waits in Recv, and the test would
        // time out.
        s.go TioWork(TioCtx(io: sys.io), &ch)
        got = TioRecv(&ch, sys.io)
    }
    ch.Free(a)
    Assert(got == 42)
}

TioShielded(io: Io, res: &raw.Atomic[i64]) {
    // The scope is cancelled before this task sleeps: a NoCancel Io still
    // sleeps, a plain one would be Canceled.
    io.Sleep(TioMs(20)) else {
        res.Store(-1)
        return
    }
    res.Store(1)
}

test "NoCancel survives a spawn" (sys: &System) {
    res := raw.Atomic[i64].New(0)
    plain := raw.Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        s.go TioShielded(sys.io.NoCancel(), &res)
        s.go TioShielded(sys.io, &plain)
        s.Cancel()
    }
    Assert(res.Load() == 1)
    Assert(plain.Load() == -1)
}

test "SizeOf Io is 16" {
    Assert(SizeOf[Io]() == 16)
}

// TioOrder is the keys of m in iteration order, folded (FNV-1a).
TioOrder(m: &Map[int, int]): u64 {
    var h: u64 = 14695981039346656037
    for e in m.Iter() {
        h = (h ^ Trunc[u64](*e.key)) *% 1099511628211
    }
    return h
}

// TioKeyFails is 1 when HashKey.New(io) is Canceled, else 0.
TioKeyFails(io: Io): int {
    _ = HashKey.New(io) else {
        return 1
    }
    return 0
}

test "HashKey.New reads a random key, a keyed Map works under it, and it is Canceled in a cancelled scope" (sys: &System) {
    io := sys.io
    k1 := try HashKey.New(io)
    k2 := try HashKey.New(io)
    // Two 128-bit random keys are equal, or a key all zero or the fixed
    // key, with probability below 2^-126.
    Assert(k1.k0 != k2.k0 || k1.k1 != k2.k1)
    Assert(k1.k0 != 0 || k1.k1 != 0)
    Assert(k1.k0 != FixedK0 || k1.k1 != FixedK1)
    var m := Map[int, int].NewKeyed(sys.arena, k1)
    var f := Map[int, int].New(sys.arena)
    for i in 0..500 {
        try m.Set(i, i * 3)
        try f.Set(i, i * 3)
    }
    for i in 0..500 {
        v := m.Get(&i) else {
            Assert(false)
            return
        }
        Assert(*v == i * 3)
    }
    Assert(m.Len() == 500)
    // The key is used: 500 keys in the fixed key's order under a random
    // key has a negligible probability.
    Assert(TioOrder(&m) != TioOrder(&f))
    var failed := 0
    var shielded := 0
    scope s := io.Scope() {
        s.Cancel()
        failed = TioKeyFails(io)
        shielded = TioKeyFails(io.NoCancel())
    }
    Assert(failed == 1)
    Assert(shielded == 0)
}

// On an Io whose table reaches no OS
// (InertIo: every entry returns Other) Sleep, Random, Yield and
// HashKey.New are Canceled, the NoCancel Io too; Random writes nothing.
test "on an Io with no OS, Sleep, Random, Yield and HashKey.New are Canceled" {
    io := InertIoValue()
    Assert(TioSleepFails(io, DurationOf(0)) == 1)
    Assert(TioSleepFails(io, TioMs(1)) == 1)
    Assert(TioSleepFails(io.NoCancel(), DurationOf(0)) == 1)
    var b: [8]u8 = [7; 8]
    var r := 0
    io.Random(mut b[..]) else {
        r = 1
    }
    Assert(r == 1)
    for i in 0..8 {
        Assert(b[i] == 7)
    }
    var y := 0
    io.NoCancel().Yield() else {
        y = 1
    }
    Assert(y == 1)
    Assert(TioKeyFails(io) == 1)
    Assert(TioKeyFails(io.NoCancel()) == 1)
}
