// Why: signal waits rest on generation words that SignalPoll advances; a
// delivery racing a park, a cancel or a busy carrier must still wake its waiter.
// What: the RT-Signal-* and RT-MN-Signal* blocks and RT-Bench-Signal, the
// scheduler side (the OS side is TestRtSched_Signal* in std/rt-sched_signal_test.go).
// How: a delivery comes from SignalRaise, which does what the OS handler does
// (count, then PollWake), from a task or a foreign thread. The blocks wait on
// Hangup (2): Windows never delivers it and nothing sends SIGHUP to a test,
// so only SignalRaise moves its count. A lost wakeup shows as the watchdog.

import std.core
import std.raw
import std.sys

// SigWaiter waits once for Hangup and adds 1 to ok on a delivery, 1 to
// canceled on Canceled.
SigWaiter(io: Io, ok: &raw.Atomic[u32], canceled: &raw.Atomic[u32]) {
    r := core.IoSignal(io, 2)
    if r.err == core.SysErr.None {
        _ = ok.Add(1)
    } else if r.err == core.SysErr.Canceled {
        _ = canceled.Add(1)
    }
}

// A relay to a foreign thread that raises Hangup once per request.
struct SigRelay {
    req: raw.Atomic[u32]
    done: raw.Atomic[u32]
    stop: raw.Atomic[u32]
    left: raw.Atomic[u32]
}

SigForeign(p: *u8) {
    unsafe "p is the test's relay, which outlives this thread's loop" {
        r := raw.PtrCast[u8, SigRelay](p)
        var seen: u32 = 0
        for (*r).stop.Load() == 0 {
            q := (*r).req.Load()
            if q != seen {
                seen = q
                SignalRaise(2)
                (*r).done.Store(q)
            } else {
                sys.ThreadYield()
            }
        }
        (*r).left.Store(1)
    }
}

// SigRelayStart starts the foreign thread; false when it cannot be.
SigRelayStart(relay: &SigRelay): bool {
    unsafe "the relay outlives the thread's loop: SigRelayStop waits for left" {
        st := sys.ThreadStart(SigForeign, raw.PtrCast[SigRelay, u8](raw.Of(relay)), 1 << 20)
        return st.err == core.SysErr.None
    }
}

// SigRelayStop stops the foreign thread and waits until it has left the
// relay.
SigRelayStop(relay: &SigRelay) {
    relay.stop.Store(1)
    for relay.left.Load() == 0 {
        sys.ThreadYield()
    }
}

// SigRaiseVia raises Hangup from the foreign thread (round r) and waits
// until it has, spinning: a raise is one atomic add and a PollWake.
SigRaiseVia(relay: &SigRelay, r: u32) {
    relay.req.Store(r)
    for relay.done.Load() != r {
        YieldNow()
    }
}

test "RT-Signal-Bad a signal outside the five is Other, Resize and Continue are Unsupported on Windows; a cancelled caller is Canceled before any wait" (sys: &System) {
    // 3 Resize and 4 Continue are Linux's SIGWINCH and SIGCONT; Windows
    // reports a console resize as an input record.
    Assert(core.IoSignal(sys.io, 5).err == core.SysErr.Other)
    if PWin() {
        Assert(core.IoSignal(sys.io, 3).err == core.SysErr.Unsupported)
        Assert(core.IoSignal(sys.io, 4).err == core.SysErr.Unsupported)
    }
    Assert(core.IoSignal(sys.io, 0xFFFF_FFFF).err == core.SysErr.Other)
    var got := core.SysErr.None
    scope s := sys.io.Scope() {
        s.Cancel()
        got = core.IoSignal(sys.io, 2).err
    }
    Assert(got == core.SysErr.Canceled)
}

test "RT-MN-SignalLockstep every waiter parked before a raise is released by it, 300 rounds of 16" (sys: &System) {
    relay := SigRelay(req: raw.Atomic[u32].New(0), done: raw.Atomic[u32].New(0), stop: raw.Atomic[u32].New(0),
        left: raw.Atomic[u32].New(0))
    Assert(SigRelayStart(&relay))
    var total: u32 = 0
    for round in 1..301 {
        ok := raw.Atomic[u32].New(0)
        canceled := raw.Atomic[u32].New(0)
        scope s := sys.io.Scope() {
            for _ in 0..16 {
                s.go SigWaiter(sys.io, &ok, &canceled)
            }
            CAwaitBlocked(&s, 16)
            // Odd rounds raise from the foreign thread (the OS handler's
            // case), even rounds from this task (a carrier).
            if (round & 1) == 1 {
                SigRaiseVia(&relay, Trunc[u32](round))
            } else {
                SignalRaise(2)
            }
        }
        Assert(ok.Load() == 16)
        Assert(canceled.Load() == 0)
        total += ok.Load()
    }
    Assert(total == 4_800)
    SigRelayStop(&relay)
}

test "RT-Signal-Cancel a cancel ends every signal wait of the scope with Canceled" (sys: &System) {
    ok := raw.Atomic[u32].New(0)
    canceled := raw.Atomic[u32].New(0)
    t0 := BenchNow()
    scope s := sys.io.Scope() {
        for _ in 0..8 {
            s.go SigWaiter(sys.io, &ok, &canceled)
        }
        CAwaitBlocked(&s, 8)
        s.Cancel()
    }
    Assert(ok.Load() == 0)
    Assert(canceled.Load() == 8)
    Assert(BenchNow() - t0 < 2_000_000_000)
    // The scope is gone: the next wait of this task blocks again and a
    // raise ends it (the cancel did not leave the word or the count
    // behind).
    ok2 := raw.Atomic[u32].New(0)
    scope s2 := sys.io.Scope() {
        s2.go SigWaiter(sys.io, &ok2, &canceled)
        CAwaitBlocked(&s2, 1)
        SignalRaise(2)
    }
    Assert(ok2.Load() == 1)
}

// SigNoCancel waits for Hangup with a NoCancel Io: the scope's cancel does
// not end it.
SigNoCancel(io: Io, at: &raw.Atomic[u32], res: &raw.Atomic[u32]) {
    at.Store(1)
    r := core.IoSignal(io.NoCancel(), 2)
    if r.err == core.SysErr.None {
        res.Store(1)
    } else {
        res.Store(2)
    }
}

test "RT-Signal-NoCancel a NoCancel signal wait outlives its scope's cancel and ends on the raise" (sys: &System) {
    at := raw.Atomic[u32].New(0)
    res := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        s.go SigNoCancel(sys.io, &at, &res)
        for at.Load() == 0 {
            YieldNow()
        }
        sys.io.NoCancel().Sleep(core.DurationOf(20_000_000)) else {}
        s.Cancel()
        sys.io.NoCancel().Sleep(core.DurationOf(20_000_000)) else {}
        Assert(res.Load() == 0)
        SignalRaise(2)
    }
    Assert(res.Load() == 1)
}

// SigSpin yields until stop is set (or 10 s pass), keeping its carrier
// busy, so no carrier waits in PollWait.
SigSpin(io: Io, stop: &raw.Atomic[u32], late: &raw.Atomic[u32]) {
    t0 := BenchNow()
    for stop.Load() == 0 {
        if BenchNow() - t0 > 10_000_000_000 {
            late.Store(1)
            return
        }
        YieldNow()
    }
}

// SigWaitThenStop waits for Hangup, then stops the spinners.
SigWaitThenStop(io: Io, stop: &raw.Atomic[u32], ok: &raw.Atomic[u32]) {
    r := core.IoSignal(io, 2)
    if r.err == core.SysErr.None {
        ok.Store(1)
    }
    stop.Store(1)
}

test "RT-Signal-Busy a delivery reaches its waiter while every carrier is busy (the timer tick's SignalPoll)" (sys: &System) {
    relay := SigRelay(req: raw.Atomic[u32].New(0), done: raw.Atomic[u32].New(0), stop: raw.Atomic[u32].New(0),
        left: raw.Atomic[u32].New(0))
    Assert(SigRelayStart(&relay))
    stop := raw.Atomic[u32].New(0)
    late := raw.Atomic[u32].New(0)
    ok := raw.Atomic[u32].New(0)
    n := int(StartedCarriers()) + 1
    scope s := sys.io.Scope() {
        s.go SigWaitThenStop(sys.io, &stop, &ok)
        CAwaitBlocked(&s, 1)
        for _ in 0..n {
            s.go SigSpin(sys.io, &stop, &late)
        }
        sys.io.NoCancel().Sleep(core.DurationOf(5_000_000)) else {}
        SigRaiseVia(&relay, 1)
        for stop.Load() == 0 && late.Load() == 0 {
            YieldNow()
        }
    }
    Assert(ok.Load() == 1)
    Assert(late.Load() == 0)
    SigRelayStop(&relay)
}

// SigForeignRace raises Hangup once each time the waiter announces a new
// round in req, as soon as it sees it: the raise lands anywhere in the
// waiter's SignalWaitFrom, its count-and-gen window included.
SigForeignRace(p: *u8) {
    unsafe "p is the test's relay, which outlives this thread's loop" {
        r := raw.PtrCast[u8, SigRelay](p)
        var seen: u32 = 0
        for (*r).stop.Load() == 0 {
            q := (*r).req.Load()
            if q != seen {
                seen = q
                SignalRaise(2)
            }
        }
        (*r).left.Store(1)
    }
}

// SigRaceStart starts SigForeignRace on relay.
SigRaceStart(relay: &SigRelay): bool {
    unsafe "the relay outlives the thread's loop: SigRelayStop waits for left" {
        st := sys.ThreadStart(SigForeignRace, raw.PtrCast[SigRelay, u8](raw.Of(relay)), 1 << 20)
        return st.err == core.SysErr.None
    }
}

test "RT-MN-SignalRace a raise that lands anywhere after the baseline ends the wait, 3,000 rounds" (sys: &System) {
    // The baseline is read before the round is announced, so every raise
    // comes after it, and the wait must end whatever point of
    // SignalWaitFrom the raise and its SignalPoll advance land in (a lost
    // wakeup is the watchdog). Without the count read after the gen load,
    // the advance can fall between the two.
    relay := SigRelay(req: raw.Atomic[u32].New(0), done: raw.Atomic[u32].New(0), stop: raw.Atomic[u32].New(0),
        left: raw.Atomic[u32].New(0))
    Assert(SigRaceStart(&relay))
    // Watched, so SignalPoll publishes (the table entry does this first).
    Assert(SignalWatchOn(2))
    for r in 1..3_001 {
        base := SignalNow(2)
        relay.req.Store(Trunc[u32](r))
        Assert(SignalWaitFrom(2, base, false) == core.SysErr.None)
    }
    SigRelayStop(&relay)
}

// SigBenchWaiter waits for Hangup n times, announcing each wait in at.
SigBenchWaiter(io: Io, n: int, at: &raw.Atomic[u32]) {
    for r in 1..n + 1 {
        base := SignalNow(2)
        SetWord(at, Trunc[u32](r))
        _ = SignalWaitFrom(2, base, false)
    }
}

test "RT-Bench-Signal 20,000 deliveries, raise to the waiter's return" (sys: &System) {
    // One waiter; this task raises each round as soon as the waiter has
    // announced it, and waits for the next announcement: one round is a
    // raise (count, PollWake), the SignalPoll that publishes it (on the
    // carrier that returns from PollWait, or the tick), the wake and the
    // waiter's resume.
    Assert(SignalWatchOn(2))
    at := raw.Atomic[u32].New(0)
    t0 := BenchNow()
    scope s := sys.io.Scope() {
        s.go SigBenchWaiter(sys.io, 20_000, &at)
        for r in 1..20_001 {
            AwaitWord(&at, Trunc[u32](r))
            SignalRaise(2)
        }
    }
    ns := BenchNow() - t0
    var out := sys.Stdout()
    BenchLine(mut out, "signal-delivery", 20_000, ns)
    out.Flush() else {
        return
    }
}

test "RT-Signal-ResizeContinue Linux delivers SIGWINCH as 3 and SIGCONT as 4 to a waiting task" (sys: &System) {
    if PWin() {
        return
    }
    io := sys.io
    for k in 3..5 {
        got := raw.Atomic[u32].New(0)
        scope sc := io.Scope() {
            sc.go SigOsWait(io, Trunc[u32](k), &got)
            // A raise before the waiter's watch is installed finds no
            // handler (Raise answers false and sends nothing), or lands
            // before its baseline: raise again until the wait answers.
            for got.Load() == 0 {
                _ = SigOsRaise(Trunc[u32](k))
                io.Sleep(core.DurationOf(5 * DlMs)) else {}
            }
        }
        Assert(got.Load() == 1)
    }
}

// SigOsWait waits for OS signal s: got 1 when the wait answers, 2 on an
// error.
SigOsWait(io: Io, s: u32, got: &raw.Atomic[u32]) {
    r := core.IoSignal(io, s)
    got.Store(if r.err == core.SysErr.None { 1 } else { 2 })
}

// SigOsRaise sends OS signal s to this process (the test blocks' sys names
// their System).
SigOsRaise(s: u32): bool {
    return sys.Raise(s)
}
