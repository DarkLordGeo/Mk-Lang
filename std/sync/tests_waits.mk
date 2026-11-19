// Why: Semaphore, Notify, Barrier and Once each promise invariants (no lost
// wakeup, no lost permit, init once) that break only under races and cancels.
// What: the Wait-Sem, Wait-Notify, Wait-Barrier, Wait-Once and Wait-SIM blocks;
// each block's comment names the invariant (S1, N2, B3, O1...) it checks.
// How: run by `mk test --std=std.sync -run Wait-` from
// std/std-sync_waitblocks_test.go, dev and release, at 1 and 8 carriers and
// several MK_TEST_SEED jitter seeds.

// The SIM blocks run each case under std.sim for many seeds: one seed is
// one interleaving at the Io calls, and a lost wakeup there is the Sim's
// deadlock answer, Canceled, which the blocks count as a failure.

import std.atomic
import std.sim
import std.time
import std.core

WtMs(n: i64): time.Duration {
    return time.Millis(n)
}

// ---- Semaphore

test "Wait-Sem counts, TryAcquire, zero and Release" (sys: &System) {
    s := Semaphore.New(3)
    Assert(s.TryAcquire(2))
    Assert(!s.TryAcquire(2))
    Assert(s.TryAcquire(1))
    Assert(!s.TryAcquire(1))
    Assert(s.TryAcquire(0))
    s.Release(3)
    s.Acquire(sys.io, 3).Must()
    s.Acquire(sys.io, 0).Must()
    s.Release(1)
    Assert(s.w.Load() == SemOne)
    s.Release(SemMax - 1)
    Assert((s.w.Load() >> 3) == SemMax)
    Assert(s.TryAcquire(SemMax))
    Assert(s.w.Load() == 0)
    // A request above the most a Semaphore holds never fits, and checking
    // it overflows nothing.
    Assert(!s.TryAcquire(SemMax + 1))
}

// A property test against a model: one task, 100,000 steps of TryAcquire
// or Release of 0 to 7 permits chosen by splitmix64, against a plain
// count. With nobody waiting the semaphore is the count exactly: every
// TryAcquire succeeds when the model holds n, and w is the count with no
// flag after every step.
WtMix(x: u64): u64 {
    var z := x +% 0x9E37_79B9_7F4A_7C15
    z = (z ^ (z >> 30)) *% 0xBF58_476D_1CE4_E5B9
    z = (z ^ (z >> 27)) *% 0x94D0_49BB_1331_11EB
    return z ^ (z >> 31)
}

test "Wait-Sem TryAcquire and Release match a counter over 100,000 steps" (sys: &System) {
    s := Semaphore.New(10)
    var model := 10
    var seed: u64 = 7
    for _ in 0..100000 {
        seed = WtMix(seed)
        n := Cast[int](seed & 7)
        if (seed & 8) != 0 {
            got := s.TryAcquire(n)
            Assert(got == (model >= n))
            if got {
                model -= n
            }
        } else if model < 1000 {
            s.Release(n)
            model += n
        }
        Assert(s.w.Load() == model * SemOne)
    }
}

WtSemTry(s: &Semaphore, io: Io, n: int): int {
    s.Acquire(io, n) else {
        return 1
    }
    return 0
}

test "Wait-Sem a cancelled scope is Canceled even with permits free" (sys: &System) {
    s := Semaphore.New(5)
    var a := 0
    var b := 0
    scope sc := sys.io.Scope() {
        sc.Cancel()
        a = WtSemTry(&s, sys.io, 1)
        b = WtSemTry(&s, sys.io.NoCancel(), 1)
    }
    Assert(a == 1 && b == 0)
    Assert(s.w.Load() == 4 * SemOne)
}

// Records the order in which acquirers got their permits.
WtSemTake(s: &Semaphore, io: Io, n: int, order: &Chan[int]) {
    s.Acquire(io, n) else {
        order.Send(io.NoCancel(), -n) else {}
        return
    }
    order.Send(io, n) else {}
}

// S3: a large request queued first, which has waited more than 1 ms when
// the first permit comes, starves at its first lost retry and is then
// served before smaller ones queued after it, though each small one would
// fit sooner; nothing barges past it.
test "Wait-Sem a starving large request is not overtaken" (sys: &System) {
    s := Semaphore.New(0)
    order := try Chan[int].New(sys.arena.Alloc(), 8)
    scope sc := sys.io.Scope() {
        sc.go WtSemTake(&s, sys.io, 5, &order)
        sys.io.Sleep(WtMs(5)) else {}
        sc.go WtSemTake(&s, sys.io, 1, &order)
        sys.io.Sleep(WtMs(5)) else {}
        sc.go WtSemTake(&s, sys.io, 2, &order)
        sys.io.Sleep(WtMs(5)) else {}
        // The first permit wakes 5 to retry; it loses (1 of 5) after 15 ms,
        // so it starves: S set, the permit given to it, the count 0.
        s.Release(1)
        for (s.w.Load() & SemS) == 0 {
            sys.io.Sleep(WtMs(1)) else {}
        }
        Assert(!s.TryAcquire(1))
        // One at a time: 1 and 2 would fit, but 5 is first (S2: S set,
        // count 0, the permits are owed to the head).
        for _ in 0..3 {
            s.Release(1)
            Assert(!s.TryAcquire(1))
            Assert((s.w.Load() >> 3) == 0)
            sys.io.Sleep(WtMs(1)) else {}
        }
        s.Release(1)
        Assert(order.Recv(sys.io).Must() == 5)
        s.Release(1)
        Assert(order.Recv(sys.io).Must() == 1)
        s.Release(2)
        Assert(order.Recv(sys.io).Must() == 2)
    }
    Assert(s.w.Load() == 0)
    order.Free(sys.arena.Alloc())
}

// S4: the head waiter is given part of its request, then cancelled; its
// permits go to the waiter behind it, and none is lost.
test "Wait-Sem a cancelled waiter passes its partial permits on" (sys: &System) {
    s := Semaphore.New(0)
    order := try Chan[int].New(sys.arena.Alloc(), 8)
    scope outer := sys.io.Scope() {
        scope inner := sys.io.Scope() {
            inner.go WtSemTake(&s, sys.io, 10, &order)
            sys.io.Sleep(WtMs(5)) else {}
            outer.go WtSemTake(&s, sys.io, 3, &order)
            sys.io.Sleep(WtMs(5)) else {}
            s.Release(4)
            sys.io.Sleep(WtMs(2)) else {}
            inner.Cancel()
        }
        // 10 was woken to retry by the Release, lost it after 10 ms, so it
        // starved and was given the 4; cancelled, it gave them back: 3 to
        // the second waiter (woken to retry when S cleared), 1 to the
        // count.
    }
    // The two arrive in either order: a woken task may run after the
    // canceller's.
    a := order.Recv(sys.io).Must()
    b := order.Recv(sys.io).Must()
    Assert((a == -10 && b == 3) || (a == 3 && b == -10))
    Assert(s.w.Load() == SemOne)
    order.Free(sys.arena.Alloc())
}

// S5 when the first waiter leaves in normal mode: 5 queues, then 1; a
// Release of 2 wakes 5 to retry, which it loses within 1 ms (yields, not
// sleeps), so it goes back to the front without starving; then 5 is
// cancelled. 1 fits in the count that held 5 back, so the cancel must
// wake it. If a slow run starves 5 instead, handoff mode serves 1; either
// way 1 finishes and one permit is left.
test "Wait-Sem a cancelled first waiter wakes the next in normal mode" (sys: &System) {
    s := Semaphore.New(0)
    order := try Chan[int].New(sys.arena.Alloc(), 8)
    scope outer := sys.io.Scope() {
        scope inner := sys.io.Scope() {
            inner.go WtSemTake(&s, sys.io, 5, &order)
            for (s.w.Load() & SemQ) == 0 {
                sys.io.Yield() else {}
            }
            outer.go WtSemTake(&s, sys.io, 1, &order)
            for _ in 0..200 {
                sys.io.Yield() else {}
            }
            s.Release(2)
            for _ in 0..200 {
                sys.io.Yield() else {}
            }
            inner.Cancel()
        }
    }
    a := order.Recv(sys.io).Must()
    b := order.Recv(sys.io).Must()
    Assert((a == -5 && b == 1) || (a == 1 && b == -5))
    Assert(s.w.Load() == SemOne)
    order.Free(sys.arena.Alloc())
}

// Sixteen tasks take 1 to 3 of 4 permits, yielding while they hold them;
// the number in use never passes 4, and at the end all 4 are free.
WtSemWorker(s: &Semaphore, io: Io, k: int, inuse: &atomic.AtomicInt, bad: &atomic.AtomicInt) {
    for i in 0..400 {
        n := 1 + (k + i) % 3
        s.Acquire(io, n) else {
            _ = bad.Add(1)
            return
        }
        if inuse.Add(i64(n)) > 4 {
            _ = bad.Add(1)
        }
        if i % 3 == 0 {
            io.Yield() else {}
        }
        _ = inuse.Add(-i64(n))
        s.Release(n)
    }
}

test "Wait-Sem 16 tasks never hold more than 4 permits" (sys: &System) {
    s := Semaphore.New(4)
    inuse := atomic.AtomicInt.New(0)
    bad := atomic.AtomicInt.New(0)
    scope sc := sys.io.Scope() {
        for k in 0..16 {
            sc.go WtSemWorker(&s, sys.io, k, &inuse, &bad)
        }
    }
    Assert(bad.Load() == 0)
    Assert(s.w.Load() == 4 * SemOne)
}

// S3 under churn: eight tasks take 1 of 4 permits and yield while they
// hold it, so a permit is nearly always free for a barger; a task that
// wants all 4 would starve without handoff mode. It is served 20 times,
// then stops the others. A starved request shows as the child's timeout.
WtSemSmall(s: &Semaphore, io: Io, stop: &atomic.AtomicInt, inuse: &atomic.AtomicInt, bad: &atomic.AtomicInt) {
    for stop.Load() == 0 {
        s.Acquire(io, 1) else {
            _ = bad.Add(1)
            return
        }
        if inuse.Add(1) > 4 {
            _ = bad.Add(1)
        }
        io.Yield() else {}
        _ = inuse.Add(-1)
        s.Release(1)
    }
}

WtSemBig(s: &Semaphore, io: Io, stop: &atomic.AtomicInt, inuse: &atomic.AtomicInt, bad: &atomic.AtomicInt) {
    for _ in 0..20 {
        s.Acquire(io, 4) else {
            _ = bad.Add(1)
            break
        }
        if inuse.Add(4) > 4 {
            _ = bad.Add(1)
        }
        _ = inuse.Add(-4)
        s.Release(4)
    }
    stop.Store(1)
}

test "Wait-Sem a request for every permit is served among small ones that churn" (sys: &System) {
    s := Semaphore.New(4)
    stop := atomic.AtomicInt.New(0)
    inuse := atomic.AtomicInt.New(0)
    bad := atomic.AtomicInt.New(0)
    scope sc := sys.io.Scope() {
        for _ in 0..8 {
            sc.go WtSemSmall(&s, sys.io, &stop, &inuse, &bad)
        }
        sc.go WtSemBig(&s, sys.io, &stop, &inuse, &bad)
    }
    Assert(bad.Load() == 0)
    Assert(s.w.Load() == 4 * SemOne)
}

// Cancellation at every await point: acquirers in a scope cancelled after
// a varying delay, while releasers keep the permits moving. Every permit
// comes back (S4).
WtSemChurn(s: &Semaphore, io: Io, k: int) {
    for i in 0..100000 {
        n := 1 + (k + i) % 4
        s.Acquire(io, n) else {
            return
        }
        io.Yield() else {}
        s.Release(n)
    }
}

test "Wait-Sem cancel at every await point keeps every permit" (sys: &System) {
    s := Semaphore.New(5)
    for round in 0..40 {
        scope sc := sys.io.Scope() {
            for k in 0..8 {
                sc.go WtSemChurn(&s, sys.io, k + round)
            }
            sys.io.Sleep(time.Millis(i64(round % 4))) else {}
            sc.Cancel()
        }
        Assert(s.w.Load() == 5 * SemOne)
    }
}

// SIM: acquirers racing releases and TryAcquire under seeded schedules.
// A waits for 3 and B for 1 (each gives its permits back after a yield);
// a releaser gives 1 four times; a prober takes 1 by TryAcquire when it
// can and gives it back. Whatever the order, both acquirers finish and the
// 4 permits are free at the end (S4), and no TryAcquire succeeds in
// handoff mode (S2: the prober records a success with S set).
//
// The cancel-racing cases under the Sim are the blocks at the end of this
// file; the native blocks above cancel at every await point.
WtSimAcquire(s: &Semaphore, io: Io, n: int, got: &atomic.AtomicInt) {
    io.Yield() else {}
    s.Acquire(io, n) else {
        _ = got.Add(1000)
        return
    }
    _ = got.Add(i64(n))
    io.Yield() else {}
    s.Release(n)
}

WtSimRelease(s: &Semaphore, io: Io) {
    for _ in 0..4 {
        io.Yield() else {}
        s.Release(1)
    }
}

WtSimProbe(s: &Semaphore, io: Io, bad: &atomic.AtomicInt) {
    for _ in 0..6 {
        io.Yield() else {}
        if s.TryAcquire(1) {
            if (s.w.Load() & SemS) != 0 {
                _ = bad.Add(1)
            }
            io.Yield() else {}
            s.Release(1)
        }
    }
}

test "Wait-SIM Sem: acquirers racing releases, seeds 1 to 300" (sys: &System) {
    for seed in 1..301 {
        var sm := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := sm.Io()
        s := Semaphore.New(0)
        a := atomic.AtomicInt.New(0)
        b := atomic.AtomicInt.New(0)
        bad := atomic.AtomicInt.New(0)
        scope sc := io.Scope() {
            sc.go WtSimAcquire(&s, io, 3, &a)
            sc.go WtSimAcquire(&s, io, 1, &b)
            sc.go WtSimRelease(&s, io)
            sc.go WtSimProbe(&s, io, &bad)
        }
        sm.Free()
        Assert(a.Load() == 3 && b.Load() == 1 && bad.Load() == 0)
        Assert(s.w.Load() == 4 * SemOne)
    }
}

// SIM, handoff mode. The block above never reaches it: its
// tasks only yield, a yield or a now moves the Sim's clock 1 us, and a
// waiter starves only after SemStarveNanos (1 ms). Here the releaser and
// the prober first sleep 2 ms, which the Sim's clock jumps to once A (3)
// and B (1) are queued, so the first retry wake of A finds it waited
// 1 ms and, losing the retry (the count holds 1 at most, or the prober
// barged first), switches to handoff mode: S set, the count given to A,
// the rest by SemGiveLocked's partial grants until A has 3, then S
// clears. The prober counts the probes it made with S set (seen); none of
// them may succeed (S2, bad), and at least half the seeds must reach S
// (all 300 did on 2026-11-08), so the block fails if handoff mode goes
// unreached again. Mutation check (a std copy, 2026-11-08): with Release's
// handoff branch taken out, this block fails and the one above passes.
// The count printed shows only when the block fails.
WtSimSleepRelease(s: &Semaphore, io: Io) {
    io.Sleep(WtMs(2)) else {}
    for _ in 0..4 {
        io.Yield() else {}
        s.Release(1)
    }
}

WtSimSleepProbe(s: &Semaphore, io: Io, seen: &atomic.AtomicInt, bad: &atomic.AtomicInt) {
    io.Sleep(WtMs(2)) else {}
    for _ in 0..16 {
        io.Yield() else {}
        // Under the Sim no other task runs between these lines (the baton
        // moves only at an Io call), so S read here is S at the Cas.
        handoff := (s.w.Load() & SemS) != 0
        if handoff {
            _ = seen.Add(1)
        }
        if s.TryAcquire(1) {
            if handoff || (s.w.Load() & SemS) != 0 {
                _ = bad.Add(1)
            }
            io.Yield() else {}
            s.Release(1)
        }
    }
}

test "Wait-SIM Sem handoff mode is reached and nothing barges in it, seeds 1 to 300" (sys: &System) {
    var reached := 0
    for seed in 1..301 {
        var sm := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := sm.Io()
        s := Semaphore.New(0)
        a := atomic.AtomicInt.New(0)
        b := atomic.AtomicInt.New(0)
        seen := atomic.AtomicInt.New(0)
        bad := atomic.AtomicInt.New(0)
        scope sc := io.Scope() {
            sc.go WtSimAcquire(&s, io, 3, &a)
            sc.go WtSimAcquire(&s, io, 1, &b)
            sc.go WtSimSleepRelease(&s, io)
            sc.go WtSimSleepProbe(&s, io, &seen, &bad)
        }
        sm.Free()
        Assert(a.Load() == 3 && b.Load() == 1 && bad.Load() == 0)
        // S4 and the end of handoff mode: every permit free, no flag left.
        Assert(s.w.Load() == 4 * SemOne)
        if seen.Load() > 0 {
            reached += 1
        }
    }
    var out := sys.Stdout()
    Print(mut out, "Sem: handoff mode seen by the prober in {r} of 300 seeds\n", (r: reached)) else {}
    out.Flush() else {}
    Assert(reached >= 150)
}

// ---- Notify

test "Wait-Notify a permit is stored once and consumed once" (sys: &System) {
    n := Notify.New()
    n.NotifyOne()
    n.NotifyOne()
    n.Wait(sys.io).Must()
    Assert(n.w.Load() == NotifyEmpty)
    // NotifyAll with nobody waiting stores nothing.
    n.NotifyAll()
    Assert(n.w.Load() == NotifyEmpty)
}

WtNotifyWait(n: &Notify, io: Io, woke: &atomic.AtomicInt) {
    n.Wait(io) else {
        _ = woke.Add(1000)
        return
    }
    _ = woke.Add(1)
}

test "Wait-Notify NotifyOne wakes one, NotifyAll wakes the rest" (sys: &System) {
    n := Notify.New()
    woke := atomic.AtomicInt.New(0)
    scope sc := sys.io.Scope() {
        for _ in 0..5 {
            sc.go WtNotifyWait(&n, sys.io, &woke)
        }
        sys.io.Sleep(WtMs(10)) else {}
        n.NotifyOne()
        sys.io.Sleep(WtMs(10)) else {}
        Assert(woke.Load() == 1)
        n.NotifyAll()
    }
    Assert(woke.Load() == 5)
    Assert(n.w.Load() == NotifyEmpty)
}

test "Wait-Notify a cancelled waiter consumes nothing" (sys: &System) {
    n := Notify.New()
    woke := atomic.AtomicInt.New(0)
    scope sc := sys.io.Scope() {
        sc.go WtNotifyWait(&n, sys.io, &woke)
        sys.io.Sleep(WtMs(5)) else {}
        sc.Cancel()
    }
    Assert(woke.Load() == 1000)
    Assert(n.w.Load() == NotifyEmpty)
    n.NotifyOne()
    Assert(n.w.Load() == NotifyPermit)
    n.Wait(sys.io).Must()
}

// The condition-variable pattern without a lost wakeup: the producer
// publishes, then notifies; the consumer checks, then waits.
WtProduce(ready: &atomic.AtomicInt, n: &Notify, io: Io, count: int) {
    for i in 0..count {
        _ = ready.Add(1)
        n.NotifyOne()
        if i % 2 == 0 {
            io.Yield() else {}
        }
    }
}

WtConsume(ready: &atomic.AtomicInt, n: &Notify, io: Io, count: int, bad: &atomic.AtomicInt) {
    for ready.Load() < i64(count) {
        n.Wait(io) else {
            _ = bad.Add(1)
            return
        }
    }
}

test "Wait-Notify producer and consumer, 20,000 notifications" (sys: &System) {
    n := Notify.New()
    ready := atomic.AtomicInt.New(0)
    bad := atomic.AtomicInt.New(0)
    scope sc := sys.io.Scope() {
        sc.go WtConsume(&ready, &n, sys.io, 20000, &bad)
        sc.go WtProduce(&ready, &n, sys.io, 20000)
    }
    Assert(bad.Load() == 0)
}

// Cancellation at every await point (N3): four takers wait in a loop in a
// scope cancelled after 0 to 980 us (1,000 rounds), while a giver outside
// it sends one notification at a time, the next only once the last was
// taken. A popped waiter returns ok even if cancelled meanwhile, and a
// cancelled one consumes nothing, so every notification sent was taken,
// or it is the stored permit; the word never stays at waiting. A Wait
// that returned Canceled after it was popped (a lost notification) fails
// this block at 2 and 8 carriers (checked with that defect planted).
WtNotifyTaker(n: &Notify, io: Io, got: &atomic.AtomicInt) {
    for {
        n.Wait(io) else {
            return
        }
        _ = got.Add(1)
    }
}

WtNotifyGiver(n: &Notify, io: Io, got: &atomic.AtomicInt, sent: &atomic.AtomicInt, stop: &atomic.AtomicInt) {
    for stop.Load() == 0 {
        if got.Load() == sent.Load() {
            // Counted before the notify, so got never passes sent.
            _ = sent.Add(1)
            n.NotifyOne()
        } else {
            io.Yield() else {}
        }
    }
}

test "Wait-Notify cancel at every await point loses no notification" (sys: &System) {
    for round in 0..1000 {
        n := Notify.New()
        got := atomic.AtomicInt.New(0)
        sent := atomic.AtomicInt.New(0)
        stop := atomic.AtomicInt.New(0)
        scope outer := sys.io.Scope() {
            outer.go WtNotifyGiver(&n, sys.io, &got, &sent, &stop)
            scope sc := sys.io.Scope() {
                for _ in 0..4 {
                    sc.go WtNotifyTaker(&n, sys.io, &got)
                }
                sys.io.Sleep(core.DurationOf(i64(round % 50) * 20000)) else {}
                sc.Cancel()
            }
            stop.Store(1)
        }
        g := got.Load()
        k := sent.Load()
        if g == k {
            Assert(n.w.Load() == NotifyEmpty)
        } else {
            Assert(g == k - 1 && n.w.Load() == NotifyPermit)
        }
        // Still usable: the stored permit, or a new one, is taken at once.
        n.NotifyOne()
        n.Wait(sys.io).Must()
        Assert(n.w.Load() == NotifyEmpty)
    }
}

// NotifyAll racing three waiters: one that queued before it is woken by
// it; one that comes after is woken by the notifier's NotifyOne loop (a
// permit stays one, so it notifies until every waiter is out). A lost
// wakeup is the Sim's Canceled, which the waiter counts as 1000.
WtSimNotifier(n: &Notify, io: Io, woke: &atomic.AtomicInt) {
    io.Yield() else {}
    n.NotifyAll()
    for woke.Load() < 3 {
        n.NotifyOne()
        io.Yield() else {}
    }
}

test "Wait-SIM Notify: notify racing a wait, seeds 1 to 300" (sys: &System) {
    for seed in 1..301 {
        var sm := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := sm.Io()
        n := Notify.New()
        ready := atomic.AtomicInt.New(0)
        bad := atomic.AtomicInt.New(0)
        woke := atomic.AtomicInt.New(0)
        scope sc := io.Scope() {
            sc.go WtConsume(&ready, &n, io, 30, &bad)
            sc.go WtProduce(&ready, &n, io, 30)
        }
        scope sc2 := io.Scope() {
            for _ in 0..3 {
                sc2.go WtNotifyWait(&n, io, &woke)
            }
            sc2.go WtSimNotifier(&n, io, &woke)
        }
        sm.Free()
        Assert(bad.Load() == 0)
        Assert(woke.Load() == 3)
    }
}

// ---- Barrier

WtBarWorker(b: &Barrier, io: Io, rounds: int, arrived: &atomic.AtomicInt, leaders: &atomic.AtomicInt, bad: &atomic.AtomicInt, n: int) {
    for r in 0..rounds {
        _ = arrived.Add(1)
        if r % 2 == 0 {
            io.Yield() else {}
        }
        leader := b.Wait(io) else {
            _ = bad.Add(1)
            return
        }
        if leader {
            _ = leaders.Add(1)
        }
        // B2: every arrival of round r happened before any return of it.
        if arrived.Load() < i64(n * (r + 1)) {
            _ = bad.Add(1)
        }
    }
}

test "Wait-Barrier one leader per round, reused 500 times by 8 tasks" (sys: &System) {
    b := Barrier.New(8)
    arrived := atomic.AtomicInt.New(0)
    leaders := atomic.AtomicInt.New(0)
    bad := atomic.AtomicInt.New(0)
    scope sc := sys.io.Scope() {
        for _ in 0..8 {
            sc.go WtBarWorker(&b, sys.io, 500, &arrived, &leaders, &bad, 8)
        }
    }
    Assert(bad.Load() == 0)
    Assert(leaders.Load() == 500)
}

test "Wait-Barrier of one: every Wait leads" (sys: &System) {
    b := Barrier.New(1)
    Assert(b.Wait(sys.io).Must())
    Assert(b.Wait(sys.io).Must())
}

// The cancellation point's fast test is inline at std.sync's waits too
// (core.CancelClear): while no scope is cancelled, Acquire, Notify.Wait on a
// stored permit, a Barrier of one, LockRead and a Select on a ready channel
// check for cancellation without entering core's CancelPoint.
// std/std-sync_inline_test.go runs this block over a scratch std in which
// CancelPoint panics when it is entered while that test would have answered.
test "Wait-Fast with no scope cancelled, the waits check for cancellation without entering CancelPoint" (sys: &System) {
    Assert(core.CancelClear(sys.io))
    s := Semaphore.New(1)
    s.Acquire(sys.io, 1).Must()
    s.Release(1)
    n := Notify.New()
    n.NotifyOne()
    n.Wait(sys.io).Must()
    b := Barrier.New(1)
    Assert(b.Wait(sys.io).Must())
    l := RwLock[int].New(4)
    r := l.LockRead(sys.io).Must()
    Assert(*r.Ref() == 4)
    r.Unlock()
    Assert(l.Into() == 4)
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 1).Must()
    c.Send(sys.io, 5).Must()
    Assert(Select(sys.io, (x: &c)).Must() == 0)
    Assert(c.Recv(sys.io).Must() == 5)
    c.Free(a)
}

WtBarOnce(b: &Barrier, io: Io, out: &atomic.AtomicInt) {
    leader := b.Wait(io) else {
        out.Store(-1)
        return
    }
    if leader {
        out.Store(2)
    } else {
        out.Store(1)
    }
}

// B3: a cancelled arrival is taken back; the round still needs n.
test "Wait-Barrier a cancelled Wait leaves the round needing n" (sys: &System) {
    b := Barrier.New(3)
    c := atomic.AtomicInt.New(0)
    x := atomic.AtomicInt.New(0)
    y := atomic.AtomicInt.New(0)
    z := atomic.AtomicInt.New(0)
    scope sc := sys.io.Scope() {
        sc.go WtBarOnce(&b, sys.io, &c)
        sys.io.Sleep(WtMs(5)) else {}
        sc.Cancel()
    }
    Assert(c.Load() == -1)
    scope sc2 := sys.io.Scope() {
        sc2.go WtBarOnce(&b, sys.io, &x)
        sc2.go WtBarOnce(&b, sys.io, &y)
        sys.io.Sleep(WtMs(5)) else {}
        // Two arrived; the cancelled one does not count.
        Assert(x.Load() == 0 && y.Load() == 0)
        sc2.go WtBarOnce(&b, sys.io, &z)
    }
    Assert(x.Load() + y.Load() + z.Load() == 4)
}

// Cancellation at every await point (B3): four tasks reuse a barrier of 4
// in a scope cancelled after 0 to 3 ms. Every cancelled Wait takes its
// arrival back, so four fresh tasks then complete 20 rounds with one
// leader each and every arrival before every return; a stale arrival would
// complete a round early, which WtBarWorker counts as bad (or the last
// task would wait forever).
WtBarChurn(b: &Barrier, io: Io, k: int) {
    for i in 0..100000 {
        _ = b.Wait(io) else {
            return
        }
        if (k + i) % 3 == 0 {
            io.Yield() else {}
        }
    }
}

test "Wait-Barrier cancel at every await point leaves it usable" (sys: &System) {
    b := Barrier.New(4)
    for round in 0..40 {
        scope sc := sys.io.Scope() {
            for k in 0..4 {
                sc.go WtBarChurn(&b, sys.io, k + round)
            }
            sys.io.Sleep(time.Millis(i64(round % 4))) else {}
            sc.Cancel()
        }
        arrived := atomic.AtomicInt.New(0)
        leaders := atomic.AtomicInt.New(0)
        bad := atomic.AtomicInt.New(0)
        scope sc2 := sys.io.Scope() {
            for _ in 0..4 {
                sc2.go WtBarWorker(&b, sys.io, 20, &arrived, &leaders, &bad, 4)
            }
        }
        Assert(bad.Load() == 0 && leaders.Load() == 20)
    }
}

test "Wait-SIM Barrier: reuse by 3 and by 5 tasks, seeds 1 to 300" (sys: &System) {
    for seed in 1..301 {
        var sm := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := sm.Io()
        n := 3 + 2 * (seed % 2)
        b := Barrier.New(n)
        arrived := atomic.AtomicInt.New(0)
        leaders := atomic.AtomicInt.New(0)
        bad := atomic.AtomicInt.New(0)
        scope sc := io.Scope() {
            for _ in 0..n {
                sc.go WtBarWorker(&b, io, 6, &arrived, &leaders, &bad, n)
            }
        }
        sm.Free()
        Assert(bad.Load() == 0 && leaders.Load() == 6)
    }
}

// ---- Once

WtOnceGet(o: &Once[i64], io: Io, calls: &atomic.AtomicInt, sum: &atomic.AtomicInt) {
    io.Yield() else {}
    v := o.Get(io, fn(): i64 {
        _ = calls.Add(1)
        var x: i64 = 0
        for i in 0..20000 {
            x +%= i64(i)
        }
        return x
    }) else {
        _ = sum.Add(1_000_000_000_000)
        return
    }
    _ = sum.Add(*v)
}

test "Wait-Once init runs once across 16 tasks, every caller sees it" (sys: &System) {
    o := Once[i64].New()
    calls := atomic.AtomicInt.New(0)
    sum := atomic.AtomicInt.New(0)
    scope sc := sys.io.Scope() {
        for _ in 0..16 {
            sc.go WtOnceGet(&o, sys.io, &calls, &sum)
        }
    }
    Assert(calls.Load() == 1)
    Assert(sum.Load() == 16 * 199990000)
    Assert(o.Into().Must() == 199990000)
}

test "Wait-Once a finished Once answers in a cancelled scope; an empty one does not" (sys: &System) {
    o := Once[int].New()
    e := Once[int].New()
    Assert(*o.Get(sys.io, fn(): int { return 7 }).Must() == 7)
    var a := 0
    var b := 0
    scope sc := sys.io.Scope() {
        sc.Cancel()
        x := o.Get(sys.io, fn(): int { return 8 }) else {
            return
        }
        a = *x
        _ = e.Get(sys.io, fn(): int { return 9 }) else {
            b = 1
        }
    }
    Assert(a == 7 && b == 1)
    Assert(e.Into().IsNone())
}

// Cancellation at every await point (O1, O3, O4): eight tasks Get an
// empty Once whose init runs 1,000,000 steps, in a scope cancelled after
// 0 to 3 ms. Each Get either returns the value or Canceled; a runner
// finishes init whatever the cancel (nothing unwinds), so afterwards the
// Once is empty (nobody started) or done, never left running or waiting,
// and a later Get returns the one value with init run at most once.
WtOnceLong(o: &Once[i64], io: Io, calls: &atomic.AtomicInt, bad: &atomic.AtomicInt) {
    v := o.Get(io, fn(): i64 {
        _ = calls.Add(1)
        var x: i64 = 0
        for i in 0..1000000 {
            x +%= i64(i)
        }
        return x
    }) else {
        return
    }
    if *v != 499999500000 {
        _ = bad.Add(1)
    }
}

test "Wait-Once cancel at every await point runs init at most once" (sys: &System) {
    for round in 0..40 {
        o := Once[i64].New()
        calls := atomic.AtomicInt.New(0)
        bad := atomic.AtomicInt.New(0)
        scope sc := sys.io.Scope() {
            for _ in 0..8 {
                sc.go WtOnceLong(&o, sys.io, &calls, &bad)
            }
            sys.io.Sleep(time.Millis(i64(round % 4))) else {}
            sc.Cancel()
        }
        st := o.state.Load()
        Assert(st == OnceEmpty || st == OnceDone)
        Assert(calls.Load() <= 1 && (st == OnceDone) == (calls.Load() == 1))
        WtOnceLong(&o, sys.io, &calls, &bad)
        Assert(calls.Load() == 1 && bad.Load() == 0)
        Assert(o.state.Load() == OnceDone)
    }
}

test "Wait-SIM Once: init once under seeds 1 to 200" (sys: &System) {
    for seed in 1..201 {
        var sm := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := sm.Io()
        o := Once[i64].New()
        calls := atomic.AtomicInt.New(0)
        sum := atomic.AtomicInt.New(0)
        scope sc := io.Scope() {
            for _ in 0..4 {
                sc.go WtOnceGet(&o, io, &calls, &sum)
            }
        }
        sm.Free()
        Assert(calls.Load() == 1)
        Assert(sum.Load() == 4 * 199990000)
    }
}

// ---- Cancellation under the Sim
//
// A Sim honours a task's cancelled scope as the native Io does:
// a sleep, a yield or a wait in a cancelled scope answers Canceled, and a
// task blocked in a wait when its scope is cancelled is made runnable with
// Canceled at the Sim's next choice (std/sim/sched.mk CancelWaits). So the
// race of a cancel against a release, a notify or a barrier's last arrival
// is a seeded schedule here. The cancel is made by a task under the Sim
// (WtSimCancelAfter's yields are the Sim's choices), never by main,
// whose operations are not choices. Each block counts both outcomes and
// fails unless each is reached by some seed.

// Each Cancelled wrapper runs one waiter in a scope of its own and cancels
// that scope after k yields (a capturing literal cannot be spawned,
// so there is one wrapper per waiter).
// Takes 3 and keeps them, so the count at the end shows what it holds.
WtSimTake3(s: &Semaphore, io: Io, got: &atomic.AtomicInt) {
    s.Acquire(io, 3) else {
        _ = got.Add(1000)
        return
    }
    _ = got.Add(3)
}

WtSimSemCancelled(s: &Semaphore, io: Io, k: int, got: &atomic.AtomicInt) {
    scope inner := io.Scope() {
        inner.go WtSimTake3(s, io, got)
        for _ in 0..k {
            io.Yield() else {}
        }
        inner.Cancel()
    }
}

WtSimGiveOnes(s: &Semaphore, io: Io, j: int) {
    for _ in 0..j {
        io.Yield() else {}
    }
    for _ in 0..3 {
        io.Yield() else {}
        s.Release(1)
    }
}

test "Wait-SIM Sem-cancel: a cancel racing three releases keeps every permit, seeds 1 to 300" (sys: &System) {
    var took := 0
    var gave_up := 0
    for seed in 1..301 {
        var sm := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := sm.Io()
        s := Semaphore.New(0)
        got := atomic.AtomicInt.New(0)
        scope outer := io.Scope() {
            outer.go WtSimGiveOnes(&s, io, seed % 3)
            outer.go WtSimSemCancelled(&s, io, seed % 11, &got)
        }
        sm.Free()
        // S4: the waiter holds 3, or Canceled holding none, and the count
        // is the rest; nothing is lost and no flag is left.
        g := got.Load()
        Assert(g == 3 || g == 1000)
        if g == 3 {
            took += 1
            Assert(s.w.Load() == 0)
        } else {
            gave_up += 1
            Assert(s.w.Load() == 3 * SemOne)
        }
    }
    var out := sys.Stdout()
    Print(mut out, "Sem: took {t}, Canceled {c} of 300 seeds\n", (t: took, c: gave_up)) else {}
    out.Flush() else {}
    Assert(took > 0 && gave_up > 0)
}

WtSimNotifyCancelled(n: &Notify, io: Io, k: int, got: &atomic.AtomicInt) {
    scope inner := io.Scope() {
        inner.go WtNotifyWait(n, io, got)
        for _ in 0..k {
            io.Yield() else {}
        }
        inner.Cancel()
    }
}

WtSimNotifyAfter(n: &Notify, io: Io, j: int) {
    for _ in 0..j {
        io.Yield() else {}
    }
    n.NotifyOne()
}

test "Wait-SIM Notify-cancel: a cancelled Wait racing NotifyOne loses no notification, seeds 1 to 300" (sys: &System) {
    var woke := 0
    var stored := 0
    for seed in 1..301 {
        var sm := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := sm.Io()
        n := Notify.New()
        got := atomic.AtomicInt.New(0)
        scope outer := io.Scope() {
            outer.go WtSimNotifyAfter(&n, io, seed % 7)
            outer.go WtSimNotifyCancelled(&n, io, (seed / 7) % 7, &got)
        }
        sm.Free()
        // N3: a Wait the NotifyOne reached returns ok and consumed it; a
        // cancelled one consumed nothing, so the notification is stored.
        g := got.Load()
        Assert(g == 1 || g == 1000)
        if g == 1 {
            woke += 1
            Assert(n.w.Load() == NotifyEmpty)
        } else {
            stored += 1
            Assert(n.w.Load() == NotifyPermit)
        }
    }
    var out := sys.Stdout()
    Print(mut out, "Notify: woken {w}, stored {s} of 300 seeds\n", (w: woke, s: stored)) else {}
    out.Flush() else {}
    Assert(woke > 0 && stored > 0)
}

// res: 1 for an ok Wait, 2 more when it led the round, 1000 for Canceled.
WtSimBarrierWait(b: &Barrier, io: Io, j: int, res: &atomic.AtomicInt) {
    for _ in 0..j {
        io.Yield() else {}
    }
    lead := b.Wait(io) else {
        _ = res.Add(1000)
        return
    }
    _ = res.Add(1)
    if lead {
        _ = res.Add(2)
    }
}

// A waits in a scope cancelled after k yields; when its arrival was taken
// back (B3), the round still needs two, so this task arrives instead (rw).
WtSimBarrierCancelled(b: &Barrier, io: Io, k: int, ra: &atomic.AtomicInt, rw: &atomic.AtomicInt) {
    scope inner := io.Scope() {
        inner.go WtSimBarrierWait(b, io, 0, ra)
        for _ in 0..k {
            io.Yield() else {}
        }
        inner.Cancel()
    }
    if ra.Load() == 1000 {
        WtSimBarrierWait(b, io, 0, rw)
    }
}

test "Wait-SIM Barrier-cancel: a cancel racing the last arrival leaves the barrier consistent, seeds 1 to 300" (sys: &System) {
    var completed := 0
    var taken_back := 0
    for seed in 1..301 {
        var sm := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := sm.Io()
        b := Barrier.New(2)
        ra := atomic.AtomicInt.New(0)
        rb := atomic.AtomicInt.New(0)
        rw := atomic.AtomicInt.New(0)
        scope outer := io.Scope() {
            outer.go WtSimBarrierWait(&b, io, seed % 5, &rb)
            outer.go WtSimBarrierCancelled(&b, io, (seed / 5) % 7, &ra, &rw)
        }
        // A second round with fresh tasks: the barrier is ready for it
        // whichever way the first went (no arrival left over).
        r2 := atomic.AtomicInt.New(0)
        scope again := io.Scope() {
            again.go WtSimBarrierWait(&b, io, 0, &r2)
            again.go WtSimBarrierWait(&b, io, 1, &r2)
        }
        sm.Free()
        a := ra.Load()
        // One round of B and one of A or the stand-in: both ok, one leader.
        var first := rb.Load()
        if a == 1000 {
            taken_back += 1
            first += rw.Load()
        } else {
            completed += 1
            Assert(rw.Load() == 0)
            first += a
        }
        Assert(first == 1 + 1 + 2)
        Assert(r2.Load() == 1 + 1 + 2)
    }
    var out := sys.Stdout()
    Print(mut out, "Barrier: completed {c}, taken back {t} of 300 seeds\n", (c: completed, t: taken_back)) else {}
    out.Flush() else {}
    Assert(completed > 0 && taken_back > 0)
}

// The cancel ends a wait that nothing else would end, at the Sim's next
// choice, not at the deadlock the run would reach later: the ticker keeps
// the run busy for 100 yields, and the waiter, blocked on a semaphore
// nobody releases, must return Canceled while the ticker still ticks
// (without CancelWaits it returns only when the ticker ends and the
// deadlock rule cancels every wait, at ticks 100).
WtSimTicker(ticks: &atomic.AtomicInt, io: Io) {
    for _ in 0..100 {
        io.Yield() else {}
        _ = ticks.Add(1)
    }
}

WtSimTakeTick(s: &Semaphore, io: Io, ticks: &atomic.AtomicInt, at: &atomic.AtomicInt) {
    s.Acquire(io, 1) else {
        at.Store(ticks.Load())
        return
    }
    at.Store(-1)
}

WtSimTickCancelled(s: &Semaphore, io: Io, k: int, ticks: &atomic.AtomicInt, at: &atomic.AtomicInt) {
    scope inner := io.Scope() {
        inner.go WtSimTakeTick(s, io, ticks, at)
        for _ in 0..k {
            io.Yield() else {}
        }
        inner.Cancel()
    }
}

test "Wait-SIM a cancel ends a blocked wait at the next choice, seeds 1 to 200" (sys: &System) {
    for seed in 1..201 {
        var sm := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := sm.Io()
        s := Semaphore.New(0)
        ticks := atomic.AtomicInt.New(0)
        at := atomic.AtomicInt.New(-2)
        scope outer := io.Scope() {
            outer.go WtSimTicker(&ticks, io)
            outer.go WtSimTickCancelled(&s, io, seed % 20, &ticks, &at)
        }
        sm.Free()
        Assert(at.Load() >= 0 && at.Load() < 100)
        Assert(s.w.Load() == 0)
    }
}
