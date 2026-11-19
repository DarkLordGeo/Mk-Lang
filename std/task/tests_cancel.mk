// Why: a task blocked in a cancellable wait must return Canceled promptly
// when its scope, or any scope around it, is cancelled, at every kind of
// blocking point.
// What: the RT-R17-*, RT-F2-*, RT-Cancel-* and RT-CancelPoint blocks, with
// helpers such as CSlept, CWaited and CAtPoint.
// How: kind 1 blocks, run by the scheduler harness and by mk test; a lost
// cancellation shows as a time bound failing (10 s waits against a 2 s
// bound) or the watchdog.

import std.core
import std.raw
import std.rt

// CSlept sleeps 10 s and stores 1 in out when the sleep was Canceled.
CSlept(io: Io, out: &raw.Atomic[u32]) {
    io.Sleep(core.DurationOf(10_000_000_000)) else {
        out.Store(1)
        return
    }
}

// CWaited waits, cancellably, on a word nobody changes, and stores 1 in
// out when the wait was Canceled.
CWaited(io: Io, w: &raw.Atomic[u32], out: &raw.Atomic[u32]) {
    for {
        e := WaitWord(WordOf(w), 0, true)
        if e == core.SysErr.Canceled {
            out.Store(1)
            return
        }
    }
}

// CLocked locks a mutex the body holds and stores 1 in out when the lock
// was Canceled.
CLocked(io: Io, m: &Mutex[bool], out: &raw.Atomic[u32]) {
    var g := m.Lock(io) else {
        out.Store(1)
        return
    }
    g.Unlock()
}

// CBlocked is the number of tasks on r's blocked list.
CBlocked(r: *ScopeRec): int {
    var n := 0
    unsafe "the scope's blocked list, under its lock" {
        rt.Lock(&(*r).lock)
        var t := (*r).blocked
        for !raw.IsNull(t) {
            n += 1
            t = (*t).blocked_next
        }
        rt.Unlock(&(*r).lock)
    }
    return n
}

// CAwaitBlocked waits (up to 2 s, by the clock) until n tasks are on s's
// blocked list.
CAwaitBlocked(s: &Scope, n: int) {
    t0 := BenchNow()
    for CBlocked(RecOf(s)) < n && BenchNow() - t0 < 2_000_000_000 {
        YieldNow()
    }
}

test "RT-R17-CancelLevel blocked waits end with Canceled, and every later operation too" (sys: &System) {
    r1 := raw.Atomic[u32].New(0)
    r2 := raw.Atomic[u32].New(0)
    r3 := raw.Atomic[u32].New(0)
    w := raw.Atomic[u32].New(0)
    m := Mutex[bool].New(false)
    var level := 0
    var nocancel := false
    t0 := BenchNow()
    var g := m.Lock(sys.io) else {
        Assert(false)
        return
    }
    scope s := sys.io.Scope() {
        s.go CSlept(sys.io, &r1)
        s.go CWaited(sys.io, &w, &r2)
        s.go CLocked(sys.io, &m, &r3)
        CAwaitBlocked(&s, 3)
        s.Cancel()
        // Level-triggered: the body's own operations return
        // Canceled every time until the block exits; NoCancel ones run.
        for _ in 0..3 {
            sys.io.Sleep(core.DurationOf(1_000)) else {
                level += 1
            }
            sys.io.Yield() else {
                level += 1
            }
            sys.io.Sleep(core.DurationOf(0)) else {
                level += 1
            }
        }
        sys.io.NoCancel().Sleep(core.DurationOf(1_000)) else {
            nocancel = true
        }
    }
    g.Unlock()
    Assert(BenchNow() - t0 < 2_000_000_000)
    Assert(r1.Load() == 1 && r2.Load() == 1 && r3.Load() == 1)
    Assert(level == 9)
    Assert(!nocancel)
}

// CNestedBody enters a nested scope, spawns a sleeper into it and sleeps
// itself; both must see the outer cancel.
CNestedBody(io: Io, inner: &raw.Atomic[u32], own: &raw.Atomic[u32]) {
    scope s2 := io.Scope() {
        s2.go CSlept(io, inner)
        CSlept(io, own)
    }
}

test "RT-R17-CancelNested a cancel reaches a nested scope's tasks and its body" (sys: &System) {
    inner := raw.Atomic[u32].New(0)
    own := raw.Atomic[u32].New(0)
    t0 := BenchNow()
    scope s := sys.io.Scope() {
        s.go CNestedBody(sys.io, &inner, &own)
        sys.io.Sleep(core.DurationOf(5_000_000)) else {
            Assert(false)
        }
        s.Cancel()
    }
    Assert(BenchNow() - t0 < 2_000_000_000)
    Assert(inner.Load() == 1 && own.Load() == 1)
}

error CErrA
error CErrB
error CErrC
type CErr = CErrA | CErrB | CErrC

// CFail sleeps d, then fails with its error; a cancelled sleep returns
// without failing.
CFail(io: Io, d: i64, which: u32, tries: &raw.Atomic[u32]): CErr!void {
    io.Sleep(core.DurationOf(d)) else {
        return
    }
    _ = tries.Add(1)
    if which == 0 {
        fail CErrA
    }
    if which == 1 {
        fail CErrB
    }
    fail CErrC
}

// CFirst runs three failing tasks under ScopeOf[CErr].
CFirst(io: Io, tries: &raw.Atomic[u32]): CErr!void {
    try scope s := io.ScopeOf[CErr]() {
        s.go CFail(io, 1_000_000, 0, tries)
        s.go CFail(io, 5_000_000_000, 1, tries)
        s.go CFail(io, 5_000_000_000, 2, tries)
    }
}

test "RT-R17-FirstErrorWins the first failure cancels the others and is the scope's error" (sys: &System) {
    tries := raw.Atomic[u32].New(0)
    t0 := BenchNow()
    var failed := false
    CFirst(sys.io, &tries) else {
        failed = true
    }
    Assert(failed)
    Assert(tries.Load() == 1)
    Assert(BenchNow() - t0 < 2_000_000_000)
}

// CLimited counts itself live, records the maximum, sleeps 1 ms.
CLimited(io: Io, live: &raw.Atomic[u32], max: &raw.Atomic[u32]) {
    n := live.Add(1) + 1
    for {
        m := max.Load()
        if n <= m || max.Cas(m, n) {
            break
        }
    }
    io.Sleep(core.DurationOf(1_000_000)) else {
        _ = live.Add(0xFFFF_FFFF)
        return
    }
    _ = live.Add(0xFFFF_FFFF)
}

test "RT-R17-Limits never more than Limits.tasks live tasks; s.go waits, none fails" (sys: &System) {
    live := raw.Atomic[u32].New(0)
    max := raw.Atomic[u32].New(0)
    scope s := sys.io.ScopeWith(Limits(tasks: 3, stack: StackSize.Default)) {
        for _ in 0..60 {
            s.go CLimited(sys.io, &live, &max)
        }
    }
    Assert(max.Load() >= 1 && max.Load() <= 3)
    Assert(live.Load() == 0)
}

// CDeep recurses n frames of a 1 KiB live array each (about n KiB of
// stack); the array is read after the call, so no frame is elided.
CDeep(n: int): int {
    var buf: [1024]u8 = [0; 1024]
    buf[(n * 7) % 1024] = Trunc[u8](n)
    if n == 0 {
        return 0
    }
    r := CDeep(n - 1)
    return r + int(buf[(n * 7) % 1024]) % 2
}

CLarge(io: Io, out: &raw.Atomic[u32]) {
    out.Store(Trunc[u32](CDeep(4_000)) + 1)
}

test "RT-R17-LargeStack a StackSize.Large task recurses past 1 MiB and finishes" (sys: &System) {
    out := raw.Atomic[u32].New(0)
    scope s := sys.io.ScopeWith(Limits(tasks: 64, stack: StackSize.Large)) {
        s.go CLarge(sys.io, &out)
    }
    Assert(out.Load() == 2_001)
}

// CNap loops on 1 ns sleeps until stop is set.
CNap(io: Io, stop: &raw.Atomic[u32], naps: &raw.Atomic[u32]) {
    for stop.Load() == 0 {
        io.Sleep(core.DurationOf(1)) else {
            return
        }
        _ = naps.Add(1)
    }
}

// CCanceller cancels fresh nested scopes around a sleeping task, rounds
// times, then stops the napper.
CCanceller(io: Io, rounds: int, stop: &raw.Atomic[u32]) {
    for _ in 0..rounds {
        out := raw.Atomic[u32].New(0)
        scope s2 := io.Scope() {
            s2.go CSlept(io, &out)
            s2.Cancel()
        }
    }
    stop.Store(1)
}

test "RT-F2-SleepCancelOrder sleeps and cancels of nested scopes do not deadlock" (sys: &System) {
    // The lock order: a sleep takes its scope's lock, then the timer lock;
    // a cancel takes the scope's lock, then the timer lock. 20,000 rounds on
    // every carrier count, under the watchdog.
    stop := raw.Atomic[u32].New(0)
    naps := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        s.go CNap(sys.io, &stop, &naps)
        s.go CNap(sys.io, &stop, &naps)
        s.go CCanceller(sys.io, 20_000, &stop)
    }
    Assert(stop.Load() == 1)
}

// CSeq runs the cancellation points in order: 0 a word wait, 1 a sleep,
// 2 a mutex lock, then a yield, a zero sleep, 5 a recv on sk, 6 a file
// read (on the poller where the backend reads files, else the pool), 7 a
// pool read blocked in its OS call (src, tests_poolcancel.mk's source)
// and 8 a signal wait (Hangup, which nothing delivers; last, because
// nothing ends it but a cancel). Point i blocks (the word keeps its value,
// the sleep is 10 s, the mutex is held by the body, nothing was sent to
// sk, src never gets data, the signal never comes); the others do not (the
// caller sent sk one byte unless i is 5; a file read never waits for a
// peer; past point 7, i.e. for i 8, point 7 is a zero sleep, since src
// would block). It records in canceled the bit of each point that returned
// Canceled, and in at the point it is about to run.
CSeq(io: Io, i: int, w: &raw.Atomic[u32], m: &Mutex[bool], f: uint, sk: uint, src: uint, at: &raw.Atomic[u32],
    canceled: &raw.Atomic[u32]) {
    for j in 0..9 {
        SetWord(at, Trunc[u32](j + 1))
        var c := false
        if j == 0 {
            want: u32 = if i == 0 { 0 } else { 99 }
            c = WaitWord(WordOf(w), want, true) == core.SysErr.Canceled
        } else if j == 1 {
            d: i64 = if i == 1 { 10_000_000_000 } else { 1_000 }
            io.Sleep(core.DurationOf(d)) else {
                c = true
            }
        } else if j == 2 {
            var g := m.Lock(io) else {
                c = true
                CMark(canceled, j, c)
                continue
            }
            g.Unlock()
        } else if j == 3 {
            io.Yield() else {
                c = true
            }
        } else if j == 4 {
            io.Sleep(core.DurationOf(0)) else {
                c = true
            }
        } else if j == 5 {
            var b: [4]u8 = [0; 4]
            r := NativeRecv(io, sk, mut b[..])
            c = r.err == core.SysErr.Canceled
            if !c && (r.err != core.SysErr.None || r.value != 1) {
                CBad(at, canceled, j, r)
                return
            }
        } else if j == 6 {
            var b: [4]u8 = [0; 4]
            r := NativeRead(io, f, mut b[..], 0)
            c = r.err == core.SysErr.Canceled
            if !c && (r.err != core.SysErr.None || r.value != 4) {
                CBad(at, canceled, j, r)
                return
            }
        } else if j == 7 {
            if i <= 7 {
                // i < 7: the scope is cancelled, and NativeRead's check
                // comes before it reads the handle (src may be 0 then).
                var b: [4]u8 = [0; 4]
                r := NativeRead(io, src, mut b[..], -1)
                c = r.err == core.SysErr.Canceled
                if !c {
                    CBad(at, canceled, j, r)
                    return
                }
            } else {
                io.Sleep(core.DurationOf(0)) else {
                    c = true
                }
            }
        } else {
            c = core.IoSignal(io, 2).err == core.SysErr.Canceled
        }
        CMark(canceled, j, c)
    }
}

// CBad records that point j returned r, neither its data nor Canceled: at
// becomes 100 + j and canceled 0x1_0000 + 256 * (r.value & 255) + the
// error's position in SysErr (CAtPoint returns it).
CBad(at: &raw.Atomic[u32], canceled: &raw.Atomic[u32], j: int, r: core.IoResult) {
    var e: u32 = 0
    if r.err != core.SysErr.None {
        e = 1
    }
    if r.err == core.SysErr.Canceled {
        e = 2
    }
    if r.err == core.SysErr.Other {
        e = 3
    }
    canceled.Store(0x1_0000 + 256 * Trunc[u32](r.value & 255) + e)
    SetWord(at, Trunc[u32](100 + j))
}

// CAwaitAt parks until at is want or a point reported a bad result (100
// and up).
CAwaitAt(at: &raw.Atomic[u32], want: u32) {
    for {
        v := at.Load()
        if v == want || v >= 100 {
            return
        }
        _ = WaitWord(WordOf(at), v, false)
    }
}

// CMark sets bit j of canceled when c.
CMark(canceled: &raw.Atomic[u32], j: int, c: bool) {
    if c {
        _ = canceled.Add(Trunc[u32](1) << Trunc[u32](j))
    }
}

// CAtPoint runs CSeq with the cancel landing while it is blocked at point
// i, and returns the bits of the points that returned Canceled. m is held
// by the caller when i is 2.
//
// The file and the socket pair are made here and closed after the scope:
// a file of 4 bytes named after the process and i, and a loopback pair
// whose accepted end has sent 1 byte unless i is 5. A point that returned
// something other than its data or Canceled stores 100 + its number in
// at (CBad), and the block then fails with both words in its report.
CAtPoint(io: Io, i: int, m: &Mutex[bool], src: uint): u32 {
    w := raw.Atomic[u32].New(0)
    at := raw.Atomic[u32].New(0)
    canceled := raw.Atomic[u32].New(0)
    var nb: [256]u8 = [0; 256]
    name := FlName(mut nb, "every", i)
    FlCreate(io, name, 4)
    o := NativeOpen(io, name, 0)
    Assert(o.err == core.SysErr.None)
    f := Trunc[uint](o.value)
    l := NListen(io)
    p := NMakePair(io, l)
    if i != 5 {
        var one: [1]u8 = [5]
        Assert(NSendAll(io, p.a, one[..]))
    }
    scope s := io.Scope() {
        s.go CSeq(io, i, &w, m, f, p.c, src, &at, &canceled)
        CAwaitAt(&at, Trunc[u32](i + 1))
        if at.Load() < 100 {
            CAwaitBlocked(&s, 1)
        }
        s.Cancel()
    }
    rt.Abandon(p.a)
    rt.Abandon(p.c)
    rt.Abandon(Trunc[uint](l.value))
    rt.Abandon(f)
    Assert(NativeRemove(io, name).err == core.SysErr.None)
    // A point that returned neither its data nor Canceled (CBad).
    Assert(at.Load() < 100)
    return canceled.Load()
}

// CAtLock is CAtPoint for point 2, with the mutex held around it.
CAtLock(io: Io, src: uint): u32 {
    m := Mutex[bool].New(false)
    var g := m.Lock(io) else {
        return 0xFFFF
    }
    got := CAtPoint(io, 2, &m, src)
    g.Unlock()
    return got
}

test "RT-Cancel-EveryPoint a cancel at each blocking point ends it and every later one" (sys: &System) {
    // Points before the cancel complete; the blocked one and all after it
    // return Canceled (bits i and up of the nine). Point 2, a Mutex.Lock of
    // a free mutex after the cancel landed, returns Canceled too: every
    // operation with Canceled in its error set checks before it starts
    // so no bit is masked. Point 7, a pool read in its OS call,
    // runs where a blocking source exists (tests_poolcancel.mk: always on Linux;
    // on Windows under the harness, which keeps standard input open).
    stdin := sys.Env("MK_TEST_STDIN") else { "" }
    src := FSource(sys.io, stdin == "open")
    free := Mutex[bool].New(false)
    Assert(CAtPoint(sys.io, 0, &free, src.h) == 0x1FF)
    Assert(CAtPoint(sys.io, 1, &free, src.h) == 0x1FE)
    Assert(CAtLock(sys.io, src.h) == 0x1FC)
    Assert(CAtPoint(sys.io, 5, &free, src.h) == 0x1E0)
    if src.h != 0 {
        Assert(CAtPoint(sys.io, 7, &free, src.h) == 0x180)
    }
    Assert(CAtPoint(sys.io, 8, &free, src.h) == 0x100)
    FClose(&src)
}

// A native task's cancellation point is two
// loads while no live scope has its flag set (core.CancelPoint,
// core.CancelLiveAdd). These blocks check that the fast path never answers
// "not cancelled" to a cancelled task, and that the count of set flags
// returns to 0 when the cancelled scopes end.

// RFast runs fast-path operations (a free Mutex, a Chan with room and a
// value) until one returns Canceled, then checks that the next ones do
// too. out: 1 when every operation after the first Canceled was Canceled,
// 2 when an operation succeeded after it, 3 when no cancel was seen in
// 50,000,000 rounds (a missed cancel).
RFast(io: Io, m: &Mutex[int], c: &Chan[int], out: &raw.Atomic[u32]) {
    var seen := false
    for _ in 0..50_000_000 {
        var g := m.Lock(io) else {
            seen = true
            break
        }
        g.Unlock()
        c.Send(io, 1) else {
            seen = true
            break
        }
        _ = c.Recv(io) else {
            seen = true
            break
        }
    }
    if !seen {
        out.Store(3)
        return
    }
    var later := true
    for _ in 0..3 {
        var g := m.Lock(io) else {
            continue
        }
        g.Unlock()
        later = false
    }
    c.Send(io, 1) else {
        if later {
            out.Store(1)
        } else {
            out.Store(2)
        }
        return
    }
    out.Store(2)
}

test "RT-CancelPoint a cancel reaches the fast cancellation point at once, and every later one, native, 200 rounds" (sys: &System) {
    for round in 0..200 {
        m := Mutex[int].New(0)
        c := Chan[int].New(sys.arena.Alloc(), 4) else {
            Assert(false)
            return
        }
        out1 := raw.Atomic[u32].New(0)
        out2 := raw.Atomic[u32].New(0)
        scope s := sys.io.Scope() {
            s.go RFast(sys.io, &m, &c, &out1)
            s.go RFast(sys.io, &m, &c, &out2)
            // Cancel 0 to 99 us after the spawn: before the tasks' first
            // operation, between operations, or while one runs.
            t0 := BenchNow()
            for BenchNow() - t0 < Trunc[i64](round % 100) * 1_000 {
            }
            s.Cancel()
        }
        Assert(out1.Load() == 1 && out2.Load() == 1)
        // The scope ended: its set flag left the count.
        Assert(core.CancelQuiet())
        c.Free(sys.arena.Alloc())
    }
}

// RNest opens a scope inside the scope it runs in (so a cancel of an outer
// one reaches it through Enter's copy or the walk) and parks in it until
// cancelled.
RNest(io: Io, depth: int, out: &raw.Atomic[u32]) {
    scope s := io.Scope() {
        if depth > 0 {
            s.go RNest(io, depth - 1, out)
            s.go RNest(io, depth - 1, out)
        }
        io.Sleep(core.DurationOf(10_000_000_000)) else {
            _ = out.Add(1)
        }
    }
}

test "RT-CancelPoint the count of set cancel flags returns to 0 when cancelled scopes end" (sys: &System) {
    Assert(core.CancelQuiet())
    out := raw.Atomic[u32].New(0)
    t0 := BenchNow()
    scope s := sys.io.Scope() {
        // 1 + 2 + 4 + 8 nested scopes (depth 3), each a sleeper.
        s.go RNest(sys.io, 3, &out)
        sys.io.Sleep(core.DurationOf(5_000_000)) else {
            Assert(false)
        }
        Assert(core.CancelQuiet())
        s.Cancel()
        // A second Cancel finds every flag set: its count is undone.
        s.Cancel()
        Assert(!core.CancelQuiet())
    }
    Assert(out.Load() == 15)
    Assert(BenchNow() - t0 < 2_000_000_000)
    Assert(core.CancelQuiet())
    // A scope entered inside a cancelled one (Enter's copy), and a failed
    // Scope[E] (Fail's CancelRec): both end with the count at 0.
    scope s2 := sys.io.Scope() {
        s2.Cancel()
        scope s3 := sys.io.Scope() {
            s3.go RNest(sys.io, 0, &out)
            Assert(!core.CancelQuiet())
        }
    }
    Assert(core.CancelQuiet())
    Assert(out.Load() == 16)
    tries := raw.Atomic[u32].New(0)
    CFirst(sys.io, &tries) else {}
    Assert(core.CancelQuiet())
}

// RHooked stores 1 in out when the running task's HookedBit is clear.
RHooked(out: &raw.Atomic[u32]) {
    t := Current()
    unsafe "the running task's own record" {
        if ((*t).flags & HookedBit) == 0 {
            out.Store(1)
        }
    }
}

test "RT-CancelPoint a native task is not hooked, so its suspension points call no hook" (sys: &System) {
    // HookMark does not compare the task's table with `&NativeTable`, a
    // const whose address differs per site, which would hook every native
    // task (two indirect calls per suspension point). The hooked side
    // (a task under another table) is the Hooks-Pair block's (tests_hooks.mk).
    Assert(core.IsNative(sys.io))
    out := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        s.go RHooked(&out)
    }
    Assert(out.Load() == 1)
}
