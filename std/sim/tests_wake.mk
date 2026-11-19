// Why: a wake with no Io must reach a task parked on a word through a
// Sim's Io, or the task waits for ever.
// What: the SIM-Wake blocks: Atomic.Wake and a Waker woken by a task wake
// tasks parked through a Sim's Io (WordWake, wake.mk); helper WkWaiter.
// How: tasks park on an atomic under a Sim and a wake comes from outside
// any Io. Run by `mk test --std=std.sim -run "SIM-Wake "`, also at
// MK_CARRIERS=1.

import std.atomic
import std.runtime

WkWaiter(io: Io, a: &atomic.Atomic[u32], seen: &atomic.Atomic[i64]) {
    for a.Load(atomic.Acquire) == 0 {
        a.Wait(io, 0) else {
            return
        }
    }
    _ = seen.FetchAdd(1, atomic.Relaxed)
}

// WkRun: two tasks wait on the word, main yields until both are parked,
// then stores 1 and wakes them with Atomic.Wake, which takes no Io.
WkRun(io: Io): int {
    a := atomic.Atomic[u32].New(0)
    seen := atomic.Atomic[i64].New(0)
    scope s := io.Scope() {
        s.go WkWaiter(io, &a, &seen)
        s.go WkWaiter(io, &a, &seen)
        for _ in 0..64 {
            io.Yield() else {}
        }
        a.Store(1, atomic.Release)
        a.Wake(true)
    }
    return int(seen.Load(atomic.SeqCst))
}

WkWake(io: Io, w: &runtime.Waker) {
    for _ in 0..64 {
        io.Yield() else {}
    }
    w.Wake()
}

// WkWaitTask waits on the Waker as a task of the run: it records 1 when the
// Wait returns, -1 when it is Canceled (the run deadlocked: no wake
// reached it).
WkWaitTask(io: Io, w: &runtime.Waker, got: &atomic.Atomic[i64]) {
    w.Wait(io) else {
        got.Store(-1, atomic.SeqCst)
        return
    }
    got.Store(1, atomic.SeqCst)
}

// WkWaker: one task of the run waits on a Waker, parked (the other yields
// 64 times first), and the other then wakes it with Waker.Wake, which
// takes no Io. Only std.sim's word-wake listener can reach the parked
// task: without it the run deadlocks once the waker ends, and the Wait is
// Canceled (removing Sim.New's ListenWake turns this red).
WkWaker(io: Io): int {
    w := runtime.Waker.New()
    got := atomic.Atomic[i64].New(0)
    scope s := io.Scope() {
        s.go WkWaitTask(io, &w, &got)
        s.go WkWake(io, &w)
    }
    return int(got.Load(atomic.SeqCst))
}

test "SIM-Wake an Atomic.Wake with no Io wakes two tasks parked through a Sim's Io, for 8 seeds" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    for seed in 1..9 {
        var sm := Sim.New(&ar, Trunc[u64](seed)) else {
            Assert(false)
            break
        }
        Assert(WkRun(sm.Io()) == 2)
        sm.Free()
    }
    ar.Free()
}

test "SIM-Wake a Waker woken by a task wakes another task parked on it through a Sim's Io, for 8 seeds" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    for seed in 1..9 {
        var sm := Sim.New(&ar, Trunc[u64](seed)) else {
            Assert(false)
            break
        }
        Assert(WkWaker(sm.Io()) == 1)
        sm.Free()
    }
    ar.Free()
}
