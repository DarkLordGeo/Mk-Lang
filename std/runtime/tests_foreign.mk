// Why: a foreign Io must keep working across wakes and stop cleanly at
// Release.
// What: test blocks of runtime.ForeignIo's wakes and Release, run by
// `mk test --std=std.runtime -run "Foreign "`.
// How: drives Foreigns with parking and delegating wake tables, at one and
// at the default carriers, across Release and the token's wraparound.

import std.atomic
import std.core
import std.raw
import std.sys
import std.time

// FwCtx is a test table's context: the native Io its entries delegate to,
// and the word its own wake entry sets (a host's own futex).
struct FwCtx {
    native: Io
    woke: atomic.Atomic[u32]
    wakes: atomic.Atomic[u64]
}

FwOf(io: Io): &FwCtx {
    unsafe "ctx is the test's FwCtx, live until its Foreign is released" {
        return &*raw.PtrCast[u8, FwCtx](ForeignContext(io))
    }
}

// FwParkWait is a wait entry that parks until the table's own wake entry
// runs: it checks the word once, then yields the native Io until woke.
FwParkWait(io: Io, addr: *u32, expected: u32): core.IoResult {
    c := FwOf(io)
    unsafe "addr is the waited word, live for the call" {
        if (*raw.PtrCast[u32, atomic.Atomic[u32]](addr)).Load(atomic.SeqCst) != expected {
            return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
        }
    }
    for c.woke.Swap(0, atomic.AcqRel) == 0 {
        c.native.Yield() else {
            return core.IoResult(value: 0, err: core.SysErr.Canceled, os: 0)
        }
    }
    return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
}

// FwParkWake is that table's wake entry: it sets the word FwParkWait waits for.
FwParkWake(io: Io, addr: *u32, all: bool) {
    c := FwOf(io)
    _ = c.wakes.FetchAdd(1, atomic.Relaxed)
    c.woke.Store(1, atomic.Release)
}

FwWaiter(fio: Io, a: &atomic.Atomic[u32], done: &atomic.Atomic[i64]) {
    for a.Load(atomic.Acquire) == 0 {
        a.Wait(fio, 0) else {
            return
        }
    }
    _ = done.FetchAdd(1, atomic.Relaxed)
}

FwWaker(io: Io, a: &atomic.Atomic[u32]) {
    for _ in 0..64 {
        io.Yield() else {}
    }
    a.Store(1, atomic.Release)
    a.Wake(true)
}

// FwRun: a task waits on a word through the Foreign's Io, another stores it
// and wakes it with Atomic.Wake, which takes no Io; the Wake must reach the
// table's wake entry (an earlier version hung here).
FwRun(io: Io, wait: fn(Io, *u32, u32): core.IoResult, wake: fn(Io, *u32, bool)): u64 {
    var c := FwCtx(native: io, woke: atomic.Atomic[u32].New(0), wakes: atomic.Atomic[u64].New(0))
    var t := core.InertIo
    t.wait = wait
    t.wake = wake
    a := atomic.Atomic[u32].New(0)
    done := atomic.Atomic[i64].New(0)
    unsafe "c and t outlive the Foreign" {
        fg := ForeignIo(raw.PtrCast[FwCtx, u8](raw.OfMut(mut c)), &t)
        fio := fg.Io()
        scope s := io.Scope() {
            s.go FwWaiter(fio, &a, &done)
            s.go FwWaker(io, &a)
        }
        fg.Release()
    }
    Assert(done.Load(atomic.SeqCst) == 1)
    return c.wakes.Load(atomic.SeqCst)
}

test "Foreign an Atomic.Wake reaches a parking table's wake entry [env MK_CARRIERS=1]" (sys: &System) {
    Assert(FwRun(sys.io, FwParkWait, FwParkWake) >= 1)
}

test "Foreign an Atomic.Wake reaches a parking table's wake entry at the default carriers" (sys: &System) {
    for _ in 0..8 {
        Assert(FwRun(sys.io, FwParkWait, FwParkWake) >= 1)
    }
}

// FwNativeWait and FwNativeWake delegate to the native Io: the wait waits
// on the word through it, and the wake calls Atomic.Wake, whose listeners
// include ForeignIo's, which must not call this entry again.
FwNativeWait(io: Io, addr: *u32, expected: u32): core.IoResult {
    c := FwOf(io)
    unsafe "addr is the waited word, a 4-byte atomic" {
        (*raw.PtrCast[u32, atomic.Atomic[u32]](addr)).Wait(c.native, expected) else {
            return core.IoResult(value: 0, err: core.SysErr.Canceled, os: 0)
        }
    }
    return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
}

FwNativeWake(io: Io, addr: *u32, all: bool) {
    _ = FwOf(io).wakes.FetchAdd(1, atomic.Relaxed)
    unsafe "addr is the woken word, a 4-byte atomic" {
        (*raw.PtrCast[u32, atomic.Atomic[u32]](addr)).Wake(all)
    }
}

test "Foreign a wake entry that delegates to Atomic.Wake is called once per Wake, not again from inside it [env MK_CARRIERS=1]" (sys: &System) {
    Assert(FwRun(sys.io, FwNativeWait, FwNativeWake) == 1)
}

// FwPair: two delegating Foreigns, a waiter on each word and a waker of
// each, at once: a Wake of one word, made while another thread is inside
// the listener for the other, still reaches both tables.
test "Foreign two delegating tables and two words, woken at once" (sys: &System) {
    for _ in 0..16 {
        scope s := sys.io.Scope() {
            s.go FwPairOne(sys.io)
            s.go FwPairOne(sys.io)
        }
    }
}

FwPairOne(io: Io) {
    _ = FwRun(io, FwNativeWait, FwNativeWake)
}

// FrSleep is a sleep entry that delegates to the native Io: it parks its
// task for the time asked.
FrSleep(io: Io, ns: i64): core.IoResult {
    FwOf(io).native.Sleep(time.Millis(ns / 1_000_000)) else {
        return core.IoResult(value: 0, err: core.SysErr.Canceled, os: 0)
    }
    return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
}

FrSleeper(fio: Io, slept: &atomic.Atomic[i64]) {
    fio.Sleep(time.Millis(200)) else {}
    _ = slept.FetchAdd(1, atomic.Relaxed)
}

FrReleaser(io: Io, fg: Foreign, released: &atomic.Atomic[i64]) {
    for _ in 0..8 {
        io.Yield() else {}
    }
    fg.Release()
    _ = released.FetchAdd(1, atomic.Relaxed)
}

// Release, called on a task while an entry
// of its Foreign is parked on the same carrier, waits by yielding that task,
// so the parked entry runs to its end (an earlier version hung here).
test "Foreign Release waits for a parked entry on the same carrier without holding it [env MK_CARRIERS=1]" (sys: &System) {
    var c := FwCtx(native: sys.io, woke: atomic.Atomic[u32].New(0), wakes: atomic.Atomic[u64].New(0))
    var t := core.InertIo
    t.sleep = FrSleep
    slept := atomic.Atomic[i64].New(0)
    released := atomic.Atomic[i64].New(0)
    unsafe "c and t outlive the Foreign" {
        fg := ForeignIo(raw.PtrCast[FwCtx, u8](raw.OfMut(mut c)), &t)
        fio := fg.Io()
        scope s := sys.io.Scope() {
            s.go FrSleeper(fio, &slept)
            s.go FrReleaser(sys.io, fg, &released)
        }
    }
    Assert(slept.Load(atomic.SeqCst) == 1 && released.Load(atomic.SeqCst) == 1)
}

// The 31-bit token wraps past 0 rather than panic
// ("token space exhausted" on bd81ffa2). The counter is set just below the
// wrap; three Foreigns made after it each work and are released.
test "Foreign the token wraps past 0, and the Foreigns made across the wrap work" {
    var count: u64 = 0
    var t := core.InertIo
    t.random = FgRandom
    Next().Store(0x7FFF_FFFD)
    unsafe "count and t outlive every Foreign below" {
        for _ in 0..3 {
            fg := ForeignIo(raw.PtrCast[u64, u8](raw.OfMut(mut count)), &t)
            Assert(fg.token != 0)
            var b: [1]u8 = [0]
            fg.Io().Random(mut b[..]) else {
                Assert(false)
            }
            Assert(raw.Addr(ForeignContext(fg.Io())) == raw.Addr(raw.OfMut(mut count)))
            fg.Release()
        }
    }
    Assert(count == 3)
}

// A failure once seen on Linux in a release build ("two delegating
// tables"): the listener had called a Foreign's wake entry (Begin held its
// slot) when another task's Release began, and Release's first store
// cleared the token, so ForeignContext inside that entry gave null (a fault
// at FwOf(io).wakes, address 0x18). Made certain here: the wake entry, once
// called, waits until Release has begun, then asks for its ctx.
struct RelCtx {
    seen: atomic.Atomic[u64]
    entered: atomic.Atomic[u32]
    slot: int
    token: u64
}

RelWake(io: Io, addr: *u32, all: bool) {
    unsafe "ctx is the test's RelCtx while the token's low bits match; this entry holds the slot" {
        c := raw.PtrCast[u8, RelCtx](ForeignContext(io))
        if raw.IsNull(c) {
            return
        }
        (*c).entered.Store(1, atomic.SeqCst)
        // Wait (bounded) until Release has begun: the slot's token word
        // changes.
        var k := 0
        for k < 200_000_000 && W((*c).slot, SlotToken).Load() == (*c).token {
            k += 1
        }
        c2 := raw.PtrCast[u8, RelCtx](ForeignContext(io))
        if !raw.IsNull(c2) {
            _ = (*c2).seen.FetchAdd(1, atomic.SeqCst)
        }
    }
}

// RelWaker runs on a thread of its own (no carrier needed): its Wake
// reaches RelWake through ForeignIo's listener.
RelWaker(arg: *u8) {
    unsafe "arg is the test's word, live until the join below returns" {
        (*raw.PtrCast[u8, atomic.Atomic[u32]](arg)).Wake(true)
    }
}

RelReleaser(io: Io, fg: Foreign, c: &RelCtx) {
    for c.entered.Load(atomic.SeqCst) == 0 {
        io.Yield() else {}
    }
    fg.Release()
}

test "Foreign a wake entry in flight keeps its ctx while another task releases its Foreign" (env: &System) {
    var c := RelCtx(seen: atomic.Atomic[u64].New(0), entered: atomic.Atomic[u32].New(0), slot: 0, token: 0)
    var t := core.InertIo
    t.wake = RelWake
    word := atomic.Atomic[u32].New(0)
    unsafe "c and t outlive the Foreign" {
        fg := ForeignIo(raw.PtrCast[RelCtx, u8](raw.OfMut(mut c)), &t)
        c.slot = fg.slot
        c.token = fg.token
        r := sys.ThreadStartJoin(RelWaker, raw.PtrCast[atomic.Atomic[u32], u8](raw.Of(&word)), 1 << 16)
        Assert(r.err == core.SysErr.None)
        scope s := env.io.Scope() {
            s.go RelReleaser(env.io, fg, &c)
        }
        _ = Blocking(env.io, r.value, JoinOn) else {
            Assert(false)
            0
        }
    }
    Assert(c.seen.Load(atomic.SeqCst) == 1)
}

// The same race at random (the "two delegating tables" block, wider): eight
// tasks each make, wake and release a delegating Foreign 16 times, while
// the others' wakes reach its wake entry, 32 rounds.
test "Foreign eight tasks make, wake and release delegating Foreigns while the others' wakes reach them" (sys: &System) {
    for _ in 0..32 {
        scope s := sys.io.Scope() {
            for _ in 0..8 {
                s.go FwPairMany(sys.io)
            }
        }
    }
}

FwPairMany(io: Io) {
    for _ in 0..16 {
        _ = FwRun(io, FwNativeWait, FwNativeWake)
    }
}
