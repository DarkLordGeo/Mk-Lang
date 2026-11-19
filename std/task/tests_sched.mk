// Why: the scheduler's basic promises (fair yields, a task that always
// starts, joins that wait for late spawns) must hold even on one carrier.
// What: RT-R1, RT-Limit, RT-Sleep-Zero, RT-R17-*, RT-F15-RandomLarge and
// RT-Reap-Repost, run by mk test over std on a single carrier.
// How: each name starts with a short handle for the rule it pins; the
// sleepers, spinners and late spawners below build each schedule.

import std.core
import std.raw
import std.rt
import std.sys

// RtR1Sleeper sleeps 1 ms, then sets the flag.
RtR1Sleeper(io: Io, flag: &Mutex[bool]) {
    io.Sleep(core.DurationOf(1_000_000)) else {
        return
    }
    var g := flag.Lock(io) else {
        return
    }
    g.Store(true)
    g.Unlock()
}

// RtR1Spinner yields until it sees the flag, at most 5,000,000 times.
RtR1Spinner(io: Io, flag: &Mutex[bool], seen: &Mutex[bool]) {
    for _ in 0..5_000_000 {
        var g := flag.Lock(io) else {
            return
        }
        set := *g.Ref()
        g.Unlock()
        if set {
            var h := seen.Lock(io) else {
                return
            }
            h.Store(true)
            h.Unlock()
            return
        }
        io.Yield() else {
            return
        }
    }
}

test "RT-R1 a timer fires while another task stays runnable" (sys: &System) {
    flag := Mutex[bool].New(false)
    seen := Mutex[bool].New(false)
    scope s := sys.io.Scope() {
        s.go RtR1Sleeper(sys.io, &flag)
        s.go RtR1Spinner(sys.io, &flag, &seen)
    }
    Assert(seen.Into())
}

// RtR4Meet sets its own flag, then yields until the peer's is set, at most
// 100,000 times; it records whether it met the peer.
RtR4Meet(io: Io, mine: &Mutex[bool], peer: &Mutex[bool], met: &Mutex[int]) {
    var g := mine.Lock(io) else {
        return
    }
    g.Store(true)
    g.Unlock()
    for _ in 0..100_000 {
        var p := peer.Lock(io) else {
            return
        }
        there := *p.Ref()
        p.Unlock()
        if there {
            var m := met.Lock(io) else {
                return
            }
            n := *m.Ref()
            m.Store(n + 1)
            m.Unlock()
            return
        }
        io.Yield() else {
            return
        }
    }
}

test "RT-Limit a task limit above the u32 range does not wrap" (sys: &System) {
    a := Mutex[bool].New(false)
    b := Mutex[bool].New(false)
    met := Mutex[int].New(0)
    scope s := sys.io.ScopeWith(Limits(tasks: 4_294_967_297, stack: StackSize.Default)) {
        s.go RtR4Meet(sys.io, &a, &b, &met)
        s.go RtR4Meet(sys.io, &b, &a, &met)
    }
    Assert(met.Into() == 2)
}

test "RT-Sleep-Zero Sleep(0) in a cancelled scope is Canceled" (sys: &System) {
    var canceled := false
    scope s := sys.io.Scope() {
        s.Cancel()
        sys.io.Sleep(core.DurationOf(0)) else {
            canceled = true
        }
    }
    Assert(canceled)
}

// RtMark sets the flag.
RtMark(io: Io, flag: &Mutex[bool]) {
    var g := flag.Lock(io.NoCancel()) else {
        return
    }
    g.Store(true)
    g.Unlock()
}

test "RT-R17-TaskAlwaysStarts a task spawned into a cancelled scope runs" (sys: &System) {
    flag := Mutex[bool].New(false)
    scope s := sys.io.Scope() {
        s.Cancel()
        s.go RtMark(sys.io, &flag)
    }
    Assert(flag.Into())
}

// RtLateSpawner sleeps 1 ms, so the body is in its join, then spawns into
// the scope it was given.
RtLateSpawner(s: &Scope, io: Io, flag: &Mutex[bool] into s) {
    io.Sleep(core.DurationOf(1_000_000)) else {
        return
    }
    s.go RtLateMark(io, flag)
}

RtLateMark(io: Io, flag: &Mutex[bool]) {
    io.Sleep(core.DurationOf(1_000_000)) else {
        return
    }
    RtMark(io, flag)
}

test "RT-R17-JoinSpawnedDuringJoin the join waits for a task spawned during it" (sys: &System) {
    flag := Mutex[bool].New(false)
    scope s := sys.io.Scope() {
        s.go RtLateSpawner(&s, sys.io, &flag)
    }
    Assert(flag.Into())
}

test "RT-F15-RandomLarge 64 MiB of random bytes, no zero 4 KiB block" (sys: &System) {
    n := 64 << 20
    p := rt.HeapAlloc(n, 4096)
    Assert(!raw.IsNull(p))
    unsafe "p is a fresh 64 MiB block" {
        raw.Fill(p, 0, n)
        buf := raw.SliceMut(p, n)
        sys.io.Random(buf) else {
            Assert(false)
        }
        for b in 0..n / 4096 {
            var any := false
            for i in 0..4096 {
                if buf[b * 4096 + i] != 0 {
                    any = true
                    break
                }
            }
            Assert(any)
        }
        sys.io.Random(raw.SliceMut(p, 0)) else {
            Assert(false)
        }
    }
    rt.HeapFree(p, n, 4096)
}

// RWaiter parks on w (cancellably, so the scope lists it as blocked) and
// sets ran when it runs again.
RWaiter(io: Io, w: &raw.Atomic[u32], ran: &raw.Atomic[u32]) {
    _ = WaitWord(WordOf(w), 0, true)
    ran.Store(1)
}

RNothing(io: Io) {}

// RReapNow is a busy carrier's PollNow without its npending check: a
// zero-timeout Reap on this task's carrier.
RReapNow() {
    var done: [64]*sys.Op = [raw.Null[sys.Op](); 64]
    _ = Reap(Here(), 0, mut done[..])
}

test "RT-Reap-Repost a busy carrier's poll that eats an idle carrier's wake posts it again" (sys: &System) {
    // Controlled schedule (IdleGate): an idle carrier is held after it
    // counted itself idle and before PollWait. A foreign-style ready (no
    // carrier: the global queue, then WakeOne's PollWake) posts one wake;
    // this task's zero-timeout Reap, as a busy carrier's PollNow does,
    // consumes it; then the gate opens. Without Reap's re-post the idle
    // carrier would sleep in PollWait with the task on the global queue
    // while this one spins without yielding (2 s bound). Runs at 2 carriers
    // and more; at 1 there is no idle carrier.
    w := raw.Atomic[u32].New(0)
    ran := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..4 {
            s.go RNothing(sys.io)
        }
        t0 := BenchNow()
        for StartedCarriers() < 2 && BenchNow() - t0 < 200_000_000 {
            YieldNow()
        }
        if StartedCarriers() >= 2 {
            s.go RWaiter(sys.io, &w, &ran)
            CAwaitBlocked(&s, 1)
            IdleGate.Store(1)
            s.go RNothing(sys.io)
            t1 := BenchNow()
            for IdleGate.Load() != 2 && BenchNow() - t1 < 2_000_000_000 {
                YieldNow()
            }
            Assert(IdleGate.Load() == 2)
            w.Store(1)
            WakeWordOn(raw.Null[Carrier](), WordOf(&w), false)
            RReapNow()
            IdleGate.Store(3)
            t2 := BenchNow()
            for ran.Load() == 0 && BenchNow() - t2 < 2_000_000_000 {}
            IdleGate.Store(0)
            Assert(ran.Load() == 1)
        }
    }
}
