// Why: Cell, Mutex, Guard and Chan are the prelude's shared state; a lost
// wakeup, a lost update or a starved waiter must show as a failing block.
// What: test blocks for Cell, Mutex (fairness, cancellation, the lock word's
// S bit and layout), Guard and Chan (Send, Recv, TrySend, Close, capacity).
// How: TestSTDSYNC_CoreTestBlocks (std/std-sync_blocks_test.go) runs them in
// child processes with the counting heap, dev and release, at 1 and 8
// carriers over seeds. A block that must panic ends its name with the
// "[exit N: text]" tag. std.core cannot import std.atomic, so the shared
// counters are raw.Atomic words.

import std.raw

linear struct SyncTok {
    s: int
    q: int
}

SyncTake(t: SyncTok): int {
    SyncTok(s, q) := t
    return s * 100000 + q
}

SyncRand(x: u64): u64 {
    var y := x
    y ^= y << 13
    y ^= y >> 7
    y ^= y << 17
    return y
}

SyncMs(n: i64): Duration {
    return DurationOf(n * 1_000_000)
}

test "Cell: New, Load, and Store through a shared reference" {
    c := Cell[int].New(1)
    r := &c
    Assert(r.Load() == 1)
    r.Store(2)
    Assert(c.Load() == 2)
    p := Cell[u8].New(255)
    (&p).Store(p.Load() - 1)
    Assert(p.Load() == 254)
}

test "Mutex: Lock, Mut, Ref, Store, Unlock and Into" (sys: &System) {
    m := Mutex[int].New(3)
    var g := m.Lock(sys.io) else {
        Assert(false)
        return
    }
    *g.Mut() += 4
    Assert(*g.Ref() == 7)
    g.Store(11)
    Assert(*g.Ref() == 11)
    g.Unlock()
    var h := m.Lock(sys.io) else {
        Assert(false)
        return
    }
    Assert(*h.Ref() == 11)
    h.Unlock()
    Assert(m.Into() == 11)
}

SyncLocker(m: &Mutex[int], io: Io, seed: int) {
    var x: u64 = Cast[u64](seed * 7919 + 1)
    for _ in 0..2000 {
        var g := m.Lock(io) else {
            return
        }
        v := *g.Ref()
        x = SyncRand(x)
        if x % 4 == 0 {
            // A yield inside the critical section: another task that runs
            // meanwhile must wait, so a lost update would show in the count.
            io.Yield() else {}
        }
        *g.Mut() = v + 1
        g.Unlock()
    }
}

test "Mutex under 8 tasks counts every critical section once" (sys: &System) {
    m := Mutex[int].New(0)
    scope s := sys.io.Scope() {
        for k in 0..8 {
            s.go SyncLocker(&m, sys.io, k)
        }
    }
    Assert(m.Into() == 16000)
}

SyncLockOr(m: &Mutex[int], io: Io): int {
    var g := m.Lock(io) else {
        return -1
    }
    *g.Mut() += 1
    v := *g.Ref()
    g.Unlock()
    return v
}

test "Lock in a cancelled scope is Canceled even when free; NoCancel takes it" (sys: &System) {
    m := Mutex[int].New(0)
    var a := 0
    var b := 0
    scope s := sys.io.Scope() {
        s.Cancel()
        a = SyncLockOr(&m, sys.io)
        b = SyncLockOr(&m, sys.io.NoCancel())
    }
    Assert(a == -1)
    Assert(b == 1)
    Assert(m.Into() == 1)
}

// SyncCancelOr is 1 when SyncCancel (Mutex.Lock's cancellation point) is
// Canceled, else 0.
SyncCancelOr(io: Io): int {
    SyncCancel(io) else {
        return 1
    }
    return 0
}

// Cancellation follows the scope tree: a scope opened from a NoCancel Io is
// cancelled by its own Cancel, so a Lock of a free mutex under it with the
// masked Io is Canceled; the cancelled scope around the NoCancel call does
// not cancel it.
test "a scope opened from a NoCancel Io cancels Lock under it, even when free; the scope around the mask does not" (sys: &System) {
    m := Mutex[int].New(0)
    var outer := 0
    var outer_check := 9
    var own := 0
    var own_check := 9
    scope s := sys.io.Scope() {
        s.Cancel()
        q := sys.io.NoCancel()
        outer = SyncLockOr(&m, q)
        outer_check = SyncCancelOr(q)
        scope b := q.Scope() {
            b.Cancel()
            own = SyncLockOr(&m, q)
            own_check = SyncCancelOr(q)
        }
    }
    Assert(outer == 1)
    Assert(outer_check == 0)
    Assert(own == -1)
    Assert(own_check == 1)
    Assert(m.Into() == 1)
}

// On an Io with no OS (InertIo: its sleep entry, the check, fails with
// Other), the check fails with something other than Canceled: not a
// cancellation, so Lock takes the free mutex, masked or not, and
// in a cancelled scope too, which an InertIo does not see.
test "a cancellation check that fails with another error than Canceled is not a cancellation" (sys: &System) {
    inert := InertIoValue()
    m := Mutex[int].New(0)
    Assert(SyncCancelOr(inert) == 0)
    Assert(SyncCancelOr(inert.NoCancel()) == 0)
    Assert(SyncLockOr(&m, inert) == 1)
    Assert(SyncLockOr(&m, inert.NoCancel()) == 2)
    var inside := 9
    scope s := sys.io.Scope() {
        s.Cancel()
        inside = SyncLockOr(&m, inert)
    }
    Assert(inside == 3)
    Assert(m.Into() == 3)
}

SyncParkedLocker(m: &Mutex[int], io: Io, res: &raw.Atomic[i64]) {
    res.Store(i64(SyncLockOr(m, io)))
}

test "a Lock parked on a held mutex is Canceled and leaves it free" (sys: &System) {
    m := Mutex[int].New(0)
    res := raw.Atomic[i64].New(7)
    var g := m.Lock(sys.io) else {
        Assert(false)
        return
    }
    scope s := sys.io.Scope() {
        s.go SyncParkedLocker(&m, sys.io, &res)
        sys.io.Sleep(SyncMs(10)) else {}
        s.Cancel()
    }
    g.Unlock()
    Assert(res.Load() == -1)
    // The cancelled waiter left the queue: the mutex is free, with no cell.
    Assert(MutexWordAt(raw.Of(&m)).Load() == 0)
    Assert(SyncLockOr(&m, sys.io) == 1)
}

test "capacity 3: FIFO, then Close drains the buffer, then Closed" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 3) else {
        Assert(false)
        return
    }
    for i in 1..4 {
        c.Send(sys.io, i * 10) else {
            Assert(false)
        }
    }
    x := c.Recv(sys.io) else {
        Assert(false)
        return
    }
    Assert(x == 10)
    c.Send(sys.io, 40) else {
        Assert(false)
    }
    c.Close()
    c.Close()
    var sum := 0
    var n := 0
    for {
        y := c.Recv(sys.io) else {
            break
        }
        sum += y
        n += 1
    }
    Assert(n == 3)
    Assert(sum == 90)
    // Send after Close is Closed and keeps x.
    t := SyncTok(s: 1, q: 2)
    var kept := 0
    d := Chan[SyncTok].New(a, 1) else {
        Assert(false)
        _ = SyncTake(t)
        return
    }
    d.Close()
    d.Send(sys.io, t) else {
        kept = SyncTake(t)
    }
    Assert(kept == 100002)
    d.FreeEmpty(a)
    c.Free(a)
}

SyncSendRange(c: &Chan[int], io: Io, n: int) {
    for i in 1..n + 1 {
        c.Send(io, i) else {
            return
        }
    }
}

test "capacity 0 hands every value from task to task" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 0) else {
        Assert(false)
        return
    }
    var sum := 0
    scope s := sys.io.Scope() {
        s.go SyncSendRange(&c, sys.io, 1000)
        for _ in 0..1000 {
            x := c.Recv(sys.io) else {
                Assert(false)
                return
            }
            sum += x
        }
    }
    Assert(sum == 500500)
    c.Free(a)
}

SyncLateReceiver(c: &Chan[int], io: Io, flag: &raw.Atomic[u32], got: &raw.Atomic[i64]) {
    io.Sleep(SyncMs(10)) else {}
    flag.Store(1)
    x := c.Recv(io) else {
        return
    }
    got.Store(i64(x))
}

test "Send waits for a receiver" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 0) else {
        Assert(false)
        return
    }
    flag := raw.Atomic[u32].New(0)
    got := raw.Atomic[i64].New(0)
    var seen: u32 = 0
    scope s := sys.io.Scope() {
        s.go SyncLateReceiver(&c, sys.io, &flag, &got)
        c.Send(sys.io, 7) else {
            Assert(false)
        }
        // Acquire edge: the receiver stored the flag before its Recv took
        // the value, and this Send returned only after that.
        seen = flag.Load()
    }
    Assert(seen == 1)
    Assert(got.Load() == 7)
    c.Free(a)
}

SyncSendKeeps(c: &Chan[SyncTok], io: Io, kept: &raw.Atomic[i64]) {
    t := SyncTok(s: 4, q: 2)
    c.Send(io, t) else {
        kept.Store(i64(SyncTake(t)))
        return
    }
}

test "Send canceled keeps x" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[SyncTok].New(a, 0) else {
        Assert(false)
        return
    }
    kept := raw.Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        s.go SyncSendKeeps(&c, sys.io, &kept)
        sys.io.Sleep(SyncMs(10)) else {}
        s.Cancel()
    }
    Assert(kept.Load() == 400002)
    Assert(raw.IsNull(ChanMetaGet(c.st).sq.head))
    c.FreeEmpty(a)
}

SyncRecvRes(c: &Chan[int], io: Io, res: &raw.Atomic[i64]) {
    x := c.Recv(io) else {
        res.Store(-1)
        return
    }
    res.Store(i64(x))
}

SyncSendRes(c: &Chan[int], io: Io, res: &raw.Atomic[i64]) {
    c.Send(io, 9) else {
        res.Store(-1)
        return
    }
    res.Store(1)
}

test "Recv canceled before a sender leaves no cell in the queue" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 0) else {
        Assert(false)
        return
    }
    r := raw.Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        s.go SyncRecvRes(&c, sys.io, &r)
        sys.io.Sleep(SyncMs(10)) else {}
        s.Cancel()
    }
    Assert(r.Load() == -1)
    Assert(raw.IsNull(ChanMetaGet(c.st).rq.head))
    // With a stale cell in rq this Send would hand its value to it and
    // succeed; with none it waits, then is cancelled.
    w := raw.Atomic[i64].New(0)
    scope t := sys.io.Scope() {
        t.go SyncSendRes(&c, sys.io, &w)
        sys.io.Sleep(SyncMs(10)) else {}
        t.Cancel()
    }
    Assert(w.Load() == -1)
    c.Free(a)
}

SyncClosedCount(c: &Chan[int], io: Io, n: &raw.Atomic[i64]) {
    _ = c.Recv(io) else {
        _ = n.Add(1)
        return
    }
}

test "Close wakes parked receivers with Closed" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 0) else {
        Assert(false)
        return
    }
    n := raw.Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..3 {
            s.go SyncClosedCount(&c, sys.io, &n)
        }
        sys.io.Sleep(SyncMs(10)) else {}
        c.Close()
    }
    Assert(n.Load() == 3)
    c.Free(a)
}

// The counters of the conservation test: received count and id sum, kept
// count and id sum.
struct SyncCount {
    n: raw.Atomic[i64]
    sum: raw.Atomic[i64]
    kept: raw.Atomic[i64]
    kept_sum: raw.Atomic[i64]
}

SyncSender(c: &Chan[SyncTok], io: Io, id: int, seed: int, m: int, k: &SyncCount) {
    var x: u64 = Cast[u64](seed * 7919 + id + 1)
    for q in 0..m {
        t := SyncTok(s: id, q: q)
        c.Send(io, t) else {
            _ = k.kept_sum.Add(i64(SyncTake(t)))
            _ = k.kept.Add(1)
            for r in q + 1..m {
                _ = k.kept_sum.Add(i64(id * 100000 + r))
                _ = k.kept.Add(1)
            }
            return
        }
        x = SyncRand(x)
        if x % 4 == 0 {
            io.Yield() else {}
        }
    }
}

SyncReceiver(c: &Chan[SyncTok], io: Io, seed: int, k: &SyncCount) {
    var x: u64 = Cast[u64](seed + 77)
    for {
        t := c.Recv(io) else {
            return
        }
        _ = k.sum.Add(i64(SyncTake(t)))
        _ = k.n.Add(1)
        x = SyncRand(x)
        if x % 3 == 0 {
            io.Yield() else {}
        }
    }
}

SyncConserve(sys: &System, capacity: int, seed: int) {
    a := sys.heap.Alloc()
    c := Chan[SyncTok].New(a, capacity) else {
        Assert(false)
        return
    }
    k := SyncCount(n: raw.Atomic[i64].New(0), sum: raw.Atomic[i64].New(0),
        kept: raw.Atomic[i64].New(0), kept_sum: raw.Atomic[i64].New(0))
    m := 10000
    scope r := sys.io.Scope() {
        for _ in 0..8 {
            r.go SyncReceiver(&c, sys.io, seed, &k)
        }
        scope s := sys.io.Scope() {
            for id in 0..8 {
                s.go SyncSender(&c, sys.io, id, seed, m, &k)
            }
            for _ in 0..(seed % 50) * 40 {
                sys.io.Yield() else {}
            }
            s.Cancel()
        }
        c.Close()
    }
    var ids: i64 = 0
    for id in 0..8 {
        for q in 0..m {
            ids += i64(id * 100000 + q)
        }
    }
    Assert(k.n.Load() + k.kept.Load() == i64(8 * m))
    Assert(k.sum.Load() + k.kept_sum.Load() == ids)
    c.FreeEmpty(a)
}

test "Values are conserved: 8 senders, 8 receivers, 10,000 linear tokens each, cancelled" (sys: &System) {
    for seed in 1..5 {
        SyncConserve(sys, 0, seed * 13)
        SyncConserve(sys, 3, seed * 17)
    }
}

// A clock that reads 0. IoTable.now promises no origin; a Lock that took
// start == 0 to mean "not read yet" would re-read such a clock forever and
// never queue, so on one carrier the holder would never run again.
// The table is a copy on this test's stack, where MakeIo asks for a
// global. That is sound here: the scope joins every task that holds zio
// before tab goes, and Mutex never keeps a table past the call (only Chan
// stores one, in ChanState.tab, and no channel sees zio).
SyncZeroNow(io: Io): IoResult {
    return IoResult(value: 0, err: SysErr.None, os: 0)
}

SyncHolder(m: &Mutex[int], io: Io) {
    var g := m.Lock(io) else {
        return
    }
    for _ in 0..20 {
        io.Yield() else {}
    }
    *g.Mut() += 1
    g.Unlock()
}

test "Lock queues under a clock that reads 0" (sys: &System) {
    var tab := *IoTab(sys.io)
    tab.now = SyncZeroNow
    zio := MakeIo(raw.OfMut(mut tab), 0)
    m := Mutex[int].New(0)
    scope s := sys.io.Scope() {
        s.go SyncHolder(&m, zio)
        s.go SyncHolder(&m, zio)
        s.go SyncHolder(&m, zio)
    }
    Assert(m.Into() == 3)
}

struct SyncZ {
}

// The tail index at a capacity above MaxInt / 2. A zero-size T needs no
// ring, so such a channel is allocatable; forming head + used would
// overflow there. head and used are set directly to the edge.
test "Send at capacity MaxInt computes the tail slot without overflow" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[SyncZ].New(a, 9223372036854775807) else {
        Assert(false)
        return
    }
    var m := ChanMetaGet(c.st)
    m.head = m.cap - 1
    m.used = m.cap - 2
    ChanMetaPut(c.st, m)
    c.Send(sys.io, SyncZ()) else {
        Assert(false)
    }
    Assert(ChanMetaGet(c.st).used == m.cap - 1)
    _ = c.Recv(sys.io) else {
        Assert(false)
        return
    }
    m2 := ChanMetaGet(c.st)
    Assert(m2.head == 0)
    Assert(m2.used == m.cap - 2)
    // Back to empty, so Free does not walk 2^63 zero-size slots.
    var m3 := m2
    m3.used = 0
    ChanMetaPut(c.st, m3)
    c.Free(a)
}

// The one state in which the word's S bit decides an unlock: a
// woken waiter in flight (W) while a starving cell is the queue's head. The
// schedule is built by hand on this task, through the lock's own paths, so
// no interleaving is left to chance: H, starving (it was woken before and
// lost), was requeued at the front, then X at the front after it (not
// starving); this task holds the lock. Its unlock pops X and sets W, and H
// becomes head with S set (MutexReleaseK). A barger then takes the free
// lock (the test task again, through Lock's slow path), and its unlock must
// hand the lock to H (state 2, L kept): the one-Cas fast path of
// MutexUnlockSlow may clear L only when S is clear. With S wrong (clear)
// that unlock would clear L and leave H queued, passing over a starving
// waiter (Q4). The cells are never parked on, so the wakes find nobody
// (wake never fails), and the test plays each cell's owner after.
SyncCell(io: Io, starving: bool): MutexWaiter {
    return MutexWaiter(next: raw.Null[MutexWaiter](), prev: raw.Null[MutexWaiter](),
        tail: raw.Null[MutexWaiter](), state: raw.Atomic[u32].New(0), io: io, starving: starving)
}

test "a starving head behind a woken waiter is handed the lock by the next unlock" (sys: &System) {
    m := Mutex[int].New(0)
    var g := m.Lock(sys.io) else {
        Assert(false)
        return
    }
    w := &m.word
    var xc := SyncCell(sys.io, false)
    var hc := SyncCell(sys.io, true)
    x := MWOf(mut xc)
    h := MWOf(mut hc)
    s := MutexTakeK(w, sys.io)
    Assert(s == (MutexL | MutexK))
    MutexReleaseK(w, MQPushFront(MQPushFront(MWAt(s & MutexQ), h), x), false)
    Assert(w.Load() == (MWAddr(x) | MutexL)) // head X, not starving: S clear
    g.Unlock()
    // Kept for after the handover check, so a wrong S fails on behaviour.
    woken := w.Load()
    Assert(MWStateAt(x).Load() == 1)
    var b := m.Lock(sys.io) else {
        Assert(false)
        return
    }
    Assert(w.Load() == (woken | MutexL))
    b.Unlock()
    Assert(MWStateAt(h).Load() == 2) // handed over within one unlock of becoming head
    Assert(woken == (MWAddr(h) | MutexW | MutexS))
    Assert(w.Load() == (MutexL | MutexW)) // L kept for H, queue empty, S clear, X still woken
    MutexClearW(w)                        // as X, the woken waiter
    MutexUnlockWord(w, sys.io)            // as H, which holds the lock
    Assert(w.Load() == 0)
    Assert(m.Into() == 0)
}

// The other side of the block above: the same schedule with H not starving, so S is
// clear when X is woken, and the barger's unlock takes the one-Cas fast
// path: it clears L and nothing else, H stays queued and is not woken (X,
// woken, will retry, Q3). X then leaves without the lock, and its
// MutexWakeIfFree passes the woken duty to H.
test "a head that is not starving behind a woken waiter: the unlock only clears L" (sys: &System) {
    m := Mutex[int].New(0)
    var g := m.Lock(sys.io) else {
        Assert(false)
        return
    }
    w := &m.word
    var xc := SyncCell(sys.io, false)
    var hc := SyncCell(sys.io, false)
    x := MWOf(mut xc)
    h := MWOf(mut hc)
    s := MutexTakeK(w, sys.io)
    MutexReleaseK(w, MQPushFront(MQPushFront(MWAt(s & MutexQ), h), x), false)
    g.Unlock()
    Assert(w.Load() == (MWAddr(h) | MutexW))
    Assert(MWStateAt(x).Load() == 1)
    var b := m.Lock(sys.io) else {
        Assert(false)
        return
    }
    b.Unlock()
    Assert(w.Load() == (MWAddr(h) | MutexW))
    Assert(MWStateAt(h).Load() == 0)
    MutexClearW(w) // as X
    MutexWakeIfFree(w, sys.io)
    Assert(MWStateAt(h).Load() == 1)
    Assert(w.Load() == MutexW)
    MutexClearW(w) // as H
    Assert(w.Load() == 0)
    Assert(m.Into() == 0)
}

// Lock's and Unlock's first steps are MutexLockSlow's and MutexUnlockSlow's
// first iterations, inline: while a woken waiter is on its way (W), with
// nobody queued and then with a non-starving head H queued, a free lock is
// taken by one Cas that sets only L, and released by one Cas that clears
// only L, so neither operation enters its slow path (a W left set for long,
// the woken task waiting for its carrier, sent every Lock and Unlock there).
// The schedule is built by hand on this task, as in the blocks above; H is
// never parked on. std-sync_inline_test.go runs this block over a scratch
// std in which both slow paths are a panic.
test "with W set, Lock takes a free lock and Unlock clears L, each with one Cas, off the slow paths" (sys: &System) {
    m := Mutex[int].New(0)
    w := &m.word
    MutexSetW(w) // as an unlock that woke a waiter, which has not run yet
    var g := m.Lock(sys.io) else {
        Assert(false)
        return
    }
    Assert(w.Load() == (MutexW | MutexL))
    *g.Mut() += 1
    g.Unlock()
    Assert(w.Load() == MutexW)
    var hc := SyncCell(sys.io, false)
    h := MWOf(mut hc)
    s := MutexTakeK(w, sys.io)
    MutexReleaseK(w, MQPushFront(MWAt(s & MutexQ), h), false)
    Assert(w.Load() == (MWAddr(h) | MutexW)) // head H, not starving: S clear
    var b := m.Lock(sys.io) else {
        Assert(false)
        return
    }
    Assert(w.Load() == (MWAddr(h) | MutexW | MutexL))
    *b.Mut() += 1
    b.Unlock()
    Assert(w.Load() == (MWAddr(h) | MutexW)) // H stays queued, not woken: W's waiter retries (Q3)
    Assert(MWStateAt(h).Load() == 0)
    MutexClearW(w) // as the woken waiter, which leaves and passes its duty on
    MutexWakeIfFree(w, sys.io)
    Assert(MWStateAt(h).Load() == 1)
    MutexClearW(w) // as H
    Assert(w.Load() == 0)
    Assert(m.Into() == 2)
}

// The cancellation point's fast test is inline (CancelClear): while no
// scope is cancelled, a native Lock, Send and Recv check for cancellation
// without entering CancelPoint. std-sync_inline_test.go runs this block over
// a scratch std in which CancelPoint panics when it is entered while that
// test would have answered.
test "with no scope cancelled, Lock, Send and Recv check for cancellation without entering CancelPoint" (sys: &System) {
    Assert(CancelClear(sys.io))
    m := Mutex[int].New(0)
    var g := m.Lock(sys.io) else {
        Assert(false)
        return
    }
    *g.Mut() += 1
    g.Unlock()
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 1) else {
        Assert(false)
        return
    }
    c.Send(sys.io, 3) else {
        Assert(false)
    }
    x := c.Recv(sys.io) else {
        Assert(false)
        return
    }
    Assert(x == 3)
    c.Free(a)
    Assert(m.Into() == 1)
}

// The word keeps L, K, W and S in the low 4 bits of a queued
// cell's address (MutexQ), which holds only because MutexWaiter is declared
// align(16) (mem_chan.mk); MutexLockSlow's run-time test of a cell's
// address did not trip on a std without it (its frames happened to be
// 16-aligned). This pins the layout fact itself: without align(16) the
// cell's natural alignment is 8, and the block fails.
test "MutexWaiter is 16-aligned, leaving the word's four flag bits free" {
    Assert(AlignOf[MutexWaiter]() == 16)
    Assert(SizeOf[MutexWaiter]() % 16 == 0)
    Assert((MutexL | MutexK | MutexW | MutexS) == 15)
    Assert((MutexQ & 15) == 0 && ~(MutexQ | 15) == 0)
}

// Guard.Store would drop the old value, so for a linear T it is E0701
// and the place is replaced through Mut, which hands the old value
// back to be consumed.
test "a linear T under a Guard is replaced through Mut, which gives the old value back" (sys: &System) {
    m := Mutex[SyncTok].New(SyncTok(s: 1, q: 1))
    var g := m.Lock(sys.io) else {
        _ = SyncTake(m.Into())
        return
    }
    old := Replace(g.Mut(), SyncTok(s: 2, q: 2))
    Assert(SyncTake(old) == 100001)
    g.Unlock()
    Assert(SyncTake(m.Into()) == 200002)
}

// TrySend never waits and never drops x.
// SyncTryTok sends t without waiting and gives back what the channel left
// the caller: 0 when sent, else the token's SyncTake (full or Closed).
SyncTryTok(c: &Chan[SyncTok], t: SyncTok): int {
    r := c.TrySend(t) else {
        return SyncTake(t)
    }
    match r {
        some(back) {
            return SyncTake(back)
        }
        none {
            return 0
        }
    }
}

SyncRecvTok(c: &Chan[SyncTok], io: Io): int {
    t := c.Recv(io) else {
        return -1
    }
    return SyncTake(t)
}

test "TrySend: room takes x, a full ring gives x back, Closed keeps x" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[SyncTok].New(a, 2) else {
        Assert(false)
        return
    }
    Assert(SyncTryTok(&c, SyncTok(s: 1, q: 0)) == 0)
    Assert(SyncTryTok(&c, SyncTok(s: 1, q: 1)) == 0)
    // Full: x comes back whole.
    Assert(SyncTryTok(&c, SyncTok(s: 9, q: 9)) == 900009)
    // FIFO through Recv, then room again.
    Assert(SyncRecvTok(&c, sys.io) == 100000)
    Assert(SyncTryTok(&c, SyncTok(s: 2, q: 0)) == 0)
    Assert(SyncRecvTok(&c, sys.io) == 100001)
    Assert(SyncRecvTok(&c, sys.io) == 200000)
    // Closed: the caller keeps x.
    c.Close()
    var closed := false
    t := SyncTok(s: 3, q: 4)
    var kept := 0
    r := c.TrySend(t) else {
        closed = true
        kept = SyncTake(t)
        none
    }
    match r {
        some(back) {
            kept = -SyncTake(back)
        }
        none {}
    }
    Assert(closed && kept == 300004)
    c.FreeEmpty(a)
}

SyncRecvOne(c: &Chan[int], io: Io, got: &raw.Atomic[i64]) {
    x := c.Recv(io) else {
        return
    }
    got.Store(i64(x))
}

test "TrySend at capacity 0: no receiver gives x back; a parked receiver takes it" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 0) else {
        Assert(false)
        return
    }
    r := c.TrySend(5) else {
        Assert(false)
        return
    }
    Assert(r.Must() == 5)
    got := raw.Atomic[i64].New(0)
    var sent := false
    scope s := sys.io.Scope() {
        s.go SyncRecvOne(&c, sys.io, &got)
        // Until the receiver has parked its cell, TrySend gives 6 back;
        // then the parked receiver takes it. At most 10 s.
        for _ in 0..10000 {
            r2 := c.TrySend(6) else {
                Assert(false)
                return
            }
            if r2.IsNone() {
                sent = true
                break
            }
            Assert(r2.Must() == 6)
            sys.io.Sleep(SyncMs(1)) else {}
        }
    }
    Assert(sent)
    Assert(got.Load() == 6)
    c.Free(a)
}

SyncTryFlood(c: &Chan[int], n: int, sent: &raw.Atomic[i64], back: &raw.Atomic[i64]) {
    for i in 1..n + 1 {
        r := c.TrySend(i) else {
            return
        }
        match r {
            some(x) {
                _ = back.Add(i64(x))
            }
            none {
                _ = sent.Add(i64(i))
            }
        }
    }
}

SyncDrain(c: &Chan[int], io: Io, got: &raw.Atomic[i64]) {
    for {
        x := c.Recv(io) else {
            return
        }
        _ = got.Add(i64(x))
    }
}

test "TrySend under 4 senders and 2 receivers conserves every value: sent plus given back is all" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 4) else {
        Assert(false)
        return
    }
    sent := raw.Atomic[i64].New(0)
    back := raw.Atomic[i64].New(0)
    got := raw.Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        s.go SyncDrain(&c, sys.io, &got)
        s.go SyncDrain(&c, sys.io, &got)
        scope t := sys.io.Scope() {
            for _ in 0..4 {
                t.go SyncTryFlood(&c, 5000, &sent, &back)
            }
        }
        c.Close()
    }
    Assert(sent.Load() + back.Load() == 4 * 12502500)
    Assert(got.Load() == sent.Load())
    c.Free(a)
}
