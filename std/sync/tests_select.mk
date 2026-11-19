// Why: Select's watch cells and wake hand-offs (std/core/sync_chan.mk) can
// leak a cell, lose a wake or strand a value beside parked Selects.
// What: the Conc-Select, Select-Cells, Select-Poll, Select-PassOn and
// Select-Pool blocks.
// How: native tasks at 1 and 8 carriers and seeded std.sim schedules, run by
// `mk test --std=std.sync` (std/std-missing_blocks_test.go).

import std.atomic
import std.core
import std.raw
import std.sim

SelMs(n: i64): core.Duration {
    return core.DurationOf(n * 1_000_000)
}

test "Conc-Select the ready field's index, in byte order of the names" (sys: &System) {
    a := sys.heap.Alloc()
    c1 := Chan[int].New(a, 1).Must()
    c2 := Chan[int].New(a, 1).Must()
    c2.Send(sys.io, 5).Must()
    Assert(Select(sys.io, (x: &c1, y: &c2)).Must() == 1)
    // Byte order: a is field 0 whatever the written order.
    Assert(Select(sys.io, (z: &c1, a: &c2)).Must() == 0)
    Assert(c2.Recv(sys.io).Must() == 5)
    c1.Send(sys.io, 6).Must()
    Assert(Select(sys.io, (x: &c1, y: &c2)).Must() == 0)
    Assert(c1.Recv(sys.io).Must() == 6)
    c2.Close()
    Assert(Select(sys.io, (x: &c1, y: &c2)).Must() == 1)
    c1.Free(a)
    c2.Free(a)
}

SelSendLater(c: &Chan[int], io: Io, v: int, ms: i64) {
    io.Sleep(SelMs(ms)) else {}
    c.Send(io, v) else {}
}

SelCloseLater(c: &Chan[int], io: Io, ms: i64) {
    io.Sleep(SelMs(ms)) else {}
    c.Close()
}

test "Conc-Select wakes on a later Send, a parked sender and a Close" (sys: &System) {
    a := sys.heap.Alloc()
    c1 := Chan[int].New(a, 4).Must()
    c0 := Chan[int].New(a, 0).Must()
    scope s := sys.io.Scope() {
        s.go SelSendLater(&c1, sys.io, 7, 20)
        Assert(Select(sys.io, (p: &c0, q: &c1)).Must() == 1)
        Assert(c1.Recv(sys.io).Must() == 7)
        // capacity 0: ready once a sender is parked
        s.go SelSendLater(&c0, sys.io, 8, 20)
        Assert(Select(sys.io, (p: &c0, q: &c1)).Must() == 0)
        Assert(c0.Recv(sys.io).Must() == 8)
        s.go SelCloseLater(&c1, sys.io, 20)
        Assert(Select(sys.io, (p: &c0, q: &c1)).Must() == 1)
    }
    c0.Free(a)
    c1.Free(a)
}

SelParked(c: &Chan[int], io: Io, res: &atomic.AtomicInt) {
    i := Select(io, (only: c)) else {
        res.Store(-1)
        return
    }
    res.Store(i64(i))
}

test "Conc-Select a parked Select is Canceled" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 1).Must()
    res := atomic.AtomicInt.New(5)
    scope s := sys.io.Scope() {
        s.go SelParked(&c, sys.io, &res)
        sys.io.Sleep(SelMs(10)) else {}
        s.Cancel()
    }
    Assert(res.Load() == -1)
    // The registration is undone: a later Select still works.
    c.Send(sys.io, 1).Must()
    Assert(Select(sys.io, (only: &c)).Must() == 0)
    _ = c.Recv(sys.io).Must()
    c.Free(a)
}

test "Conc-Select in a cancelled scope is Canceled even when ready" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 1).Must()
    c.Send(sys.io, 1).Must()
    var got := 0
    scope s := sys.io.Scope() {
        s.Cancel()
        got = SelTry(&c, sys.io)
    }
    Assert(got == -1)
    _ = c.Recv(sys.io).Must()
    c.Free(a)
}

SelTry(c: &Chan[int], io: Io): int {
    i := Select(io, (only: c)) else {
        return -1
    }
    return i
}

SelProducer(c: &Chan[int], io: Io, base: int, n: int) {
    for i in 0..n {
        c.Send(io, base + i) else {
            return
        }
    }
}

// Each consumer selects over both channels, then receives from the one it
// chose; Recv may then wait (another consumer took the value), which is
// allowed. Every value arrives exactly once.
SelConsumer(c1: &Chan[int], c2: &Chan[int], io: Io, sum: &atomic.AtomicInt, got: &atomic.AtomicInt) {
    var closed1 := false
    var closed2 := false
    for !(closed1 && closed2) {
        var i := 0
        if closed1 {
            i = 1
        } else if !closed2 {
            i = Select(io, (a: c1, b: c2)) else {
                return
            }
        }
        if i == 0 {
            v := c1.Recv(io) else {
                closed1 = true
                continue
            }
            _ = sum.Add(i64(v))
            _ = got.Add(1)
        } else {
            v := c2.Recv(io) else {
                closed2 = true
                continue
            }
            _ = sum.Add(i64(v))
            _ = got.Add(1)
        }
    }
}

test "Conc-Select 4 producers, 4 selecting consumers, every value once" (sys: &System) {
    a := sys.heap.Alloc()
    c1 := Chan[int].New(a, 2).Must()
    c2 := Chan[int].New(a, 0).Must()
    sum := atomic.AtomicInt.New(0)
    got := atomic.AtomicInt.New(0)
    scope s := sys.io.Scope() {
        for _ in 0..4 {
            s.go SelConsumer(&c1, &c2, sys.io, &sum, &got)
        }
        scope p := sys.io.Scope() {
            p.go SelProducer(&c1, sys.io, 0, 1000)
            p.go SelProducer(&c1, sys.io, 1000, 1000)
            p.go SelProducer(&c2, sys.io, 2000, 1000)
            p.go SelProducer(&c2, sys.io, 3000, 1000)
        }
        c1.Close()
        c2.Close()
    }
    Assert(got.Load() == 4000)
    // 0 + 1 + ... + 3999
    Assert(sum.Load() == 7998000)
    c1.Free(a)
    c2.Free(a)
}

// ---- Close wakes a Select parked under a std.sim Io ----
//
// The waker's Io must carry a Sim Io's run token (slot << 48 | token << 1):
// rebuilt from the table address alone, it would make SimWake ignore the
// wake and the deadlock rule answer the parked Select Canceled. Task A parks in Select on an empty channel; task B
// sleeps 1 ms of simulated time and closes it. Every seed must give 0.

SimSelOne(c: &Chan[int], io: Io, res: &atomic.AtomicInt) {
    i := Select(io, (only: c)) else {
        res.Store(-1)
        return
    }
    res.Store(i64(i))
}

SimCloseAfter(c: &Chan[int], io: Io) {
    io.Sleep(SelMs(1)) else {}
    c.Close()
}

test "Conc-Select a Close wakes a Select parked under a Sim" (sys: &System) {
    a := sys.heap.Alloc()
    for seed in 1..65 {
        var s := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := s.Io()
        c := Chan[int].New(a, 0).Must()
        res := atomic.AtomicInt.New(5)
        scope sc := io.Scope() {
            sc.go SimSelOne(&c, io, &res)
            sc.go SimCloseAfter(&c, io)
        }
        s.Free()
        c.Free(a)
        Assert(res.Load() == 0)
    }
}

// Two Selects parked under one Sim on the same channel: one Close wakes
// both.
test "Conc-Select one Close wakes two parked Selects, under a Sim" (sys: &System) {
    a := sys.heap.Alloc()
    for seed in 1..33 {
        var s := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := s.Io()
        c := Chan[int].New(a, 0).Must()
        r1 := atomic.AtomicInt.New(5)
        r2 := atomic.AtomicInt.New(5)
        scope sc := io.Scope() {
            sc.go SimSelOne(&c, io, &r1)
            sc.go SimSelOne(&c, io, &r2)
            sc.go SimCloseAfter(&c, io)
        }
        s.Free()
        c.Free(a)
        Assert(r1.Load() == 0 && r2.Load() == 0)
    }
}

// No lost wake-up (a Send that lands at any point of a Select is seen)
// under seeded schedules: a Select task and a
// sending task, each yielding between steps, so the Sim's PCT choice puts
// the Send before the Select's scan, between its scan and its wait, or
// after it parks. Every seed must return 0 with the value buffered. As for
// the RwLock block, Sim switches only at Io calls; the instruction-level
// window is the native stress runs'.
SimSelYield(c: &Chan[int], io: Io, res: &atomic.AtomicInt) {
    io.Yield() else {}
    SimSelOne(c, io, res)
}

SimSendYield(c: &Chan[int], io: Io, n: int) {
    for _ in 0..n {
        io.Yield() else {}
    }
    c.Send(io, 9) else {}
}

test "Conc-Select under Sim schedules, seeds 1 to 256" (sys: &System) {
    a := sys.heap.Alloc()
    for seed in 1..257 {
        var s := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := s.Io()
        c := Chan[int].New(a, 1).Must()
        res := atomic.AtomicInt.New(5)
        scope sc := io.Scope() {
            sc.go SimSelYield(&c, io, &res)
            sc.go SimSendYield(&c, io, seed % 4)
        }
        s.Free()
        Assert(res.Load() == 0)
        Assert(c.Recv(sys.io).Must() == 9)
        c.Free(a)
    }
}

// ---- One watch cell per (Select, channel) ----

// A Select whose scope is cancelled while a Send serves its channel, and a
// second Select on the same channel that nobody cancels. Natively: the Sim
// cannot schedule this race, since a Select's watch cells park outside the
// Sim's choice points.
SelCancelLater(c: &Chan[int], io: Io, res: &atomic.AtomicInt, us: i64) {
    scope inner := io.Scope() {
        inner.go SelParked(c, io, res)
        io.Sleep(core.DurationOf(us * 1000)) else {}
        inner.Cancel()
    }
}

SelSendUs(c: &Chan[int], io: Io, v: int, us: i64) {
    io.Sleep(core.DurationOf(us * 1000)) else {}
    c.Send(io, v) else {}
}

// 200 rounds with the cancel and the Send due at the same microsecond: the
// cancelled Select is 0 or Canceled, the other
// is 0, the value stays buffered, and nothing hangs.
test "Conc-Select a cancel racing a serve, native, 200 rounds" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 1).Must()
    for round in 0..200 {
        r1 := atomic.AtomicInt.New(5)
        r2 := atomic.AtomicInt.New(5)
        us := i64(200 + (round % 5) * 50)
        scope s := sys.io.Scope() {
            s.go SelCancelLater(&c, sys.io, &r1, us)
            s.go SelParked(&c, sys.io, &r2)
            s.go SelSendUs(&c, sys.io, round, us)
        }
        Assert(r1.Load() == 0 || r1.Load() == -1)
        Assert(r2.Load() == 0)
        Assert(c.Recv(sys.io).Must() == round)
    }
    c.Free(a)
}

// SelScribble overwrites the stack below its caller, where a returned
// Select's park was, so a cell left linked would be garbage when the next
// Send walked it.
SelScribble(n: int): int {
    var b: [64]int = [n; 64]
    return if n == 0 { b[3] } else { SelScribble(n - 1) + b[n % 64] }
}

SelBoth(c1: &Chan[int], c2: &Chan[int], io: Io, res: &atomic.AtomicInt) {
    i := Select(io, (a: c1, b: c2)) else {
        res.Store(-1)
        return
    }
    res.Store(i64(i))
}

test "Conc-Select a Select woken by one channel leaves no cell on the other" (sys: &System) {
    a := sys.heap.Alloc()
    c1 := Chan[int].New(a, 1).Must()
    c2 := Chan[int].New(a, 1).Must()
    for round in 0..50 {
        res := atomic.AtomicInt.New(5)
        scope s := sys.io.Scope() {
            s.go SelBoth(&c1, &c2, sys.io, &res)
            s.go SelSendLater(&c1, sys.io, round, 1)
        }
        Assert(res.Load() == 0)
        Assert(SelScribble(40) != 7)
        c2.Send(sys.io, round).Must()
        Assert(Select(sys.io, (only: &c2)).Must() == 0)
        Assert(c2.Recv(sys.io).Must() == round)
        Assert(c1.Recv(sys.io).Must() == round)
    }
    c1.Close()
    c2.Close()
    c1.Free(a)
    c2.Free(a)
}

// Two senders on two channels under Sim schedules: the Select returns
// either index, and both values stay buffered.
test "Conc-Select two channels served in either order, under Sim schedules, seeds 1 to 128" (sys: &System) {
    a := sys.heap.Alloc()
    for seed in 1..129 {
        var s := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := s.Io()
        c1 := Chan[int].New(a, 1).Must()
        c2 := Chan[int].New(a, 1).Must()
        res := atomic.AtomicInt.New(5)
        scope sc := io.Scope() {
            sc.go SelBoth(&c1, &c2, io, &res)
            sc.go SimSendYield(&c1, io, seed % 3)
            sc.go SimSendYield(&c2, io, (seed / 3) % 3)
        }
        s.Free()
        Assert(res.Load() == 0 || res.Load() == 1)
        Assert(c1.Recv(sys.io).Must() == 9)
        Assert(c2.Recv(sys.io).Must() == 9)
        c1.Free(a)
        c2.Free(a)
    }
}

SelIdle(c1: &Chan[int], c2: &Chan[int], io: Io, got: &atomic.AtomicInt) {
    i := Select(io, (x: c1, y: c2)) else {
        return
    }
    if i == 0 {
        _ = got.Add(1)
    }
}

SelStream(c: &Chan[int], io: Io, n: int, sum: &atomic.AtomicInt) {
    for _ in 0..n {
        v := c.Recv(io) else {
            return
        }
        _ = sum.Add(i64(v))
    }
}

// 1,000 Selects parked on two idle channels while 10,000 values pass
// through a third, which none of them watches; then one Close wakes all
// 1,000 (999 wakes under the lock, the last after it).
test "Conc-Select 1,000 parked Selects, a stream beside them, one Close wakes all" (sys: &System) {
    a := sys.heap.Alloc()
    idle1 := Chan[int].New(a, 0).Must()
    idle2 := Chan[int].New(a, 0).Must()
    c := Chan[int].New(a, 64).Must()
    got := atomic.AtomicInt.New(0)
    sum := atomic.AtomicInt.New(0)
    scope s := sys.io.Scope() {
        for _ in 0..1000 {
            s.go SelIdle(&idle1, &idle2, sys.io, &got)
        }
        scope p := sys.io.Scope() {
            p.go SelStream(&c, sys.io, 10000, &sum)
            p.go SelProducer(&c, sys.io, 0, 10000)
        }
        Assert(sum.Load() == 49995000)
        sys.io.Sleep(SelMs(5)) else {}
        idle1.Close()
    }
    Assert(got.Load() == 1000)
    idle1.Free(a)
    idle2.Free(a)
    c.Free(a)
}

SelMany(c: &Chan[int], io: Io): int {
    i := Select(io, (f00: c, f01: c, f02: c, f03: c, f04: c, f05: c, f06: c, f07: c, f08: c, f09: c, f10: c, f11: c, f12: c, f13: c, f14: c, f15: c, f16: c, f17: c, f18: c, f19: c, f20: c, f21: c, f22: c, f23: c, f24: c, f25: c, f26: c, f27: c, f28: c, f29: c, f30: c, f31: c, f32: c, f33: c, f34: c, f35: c, f36: c, f37: c, f38: c, f39: c, f40: c, f41: c, f42: c, f43: c, f44: c, f45: c, f46: c, f47: c, f48: c, f49: c, f50: c, f51: c, f52: c, f53: c, f54: c, f55: c, f56: c, f57: c, f58: c, f59: c, f60: c, f61: c, f62: c, f63: c, f64: c)) else {
        return -1
    }
    return i
}

// More fields than core.SelCells: the Select polls, and still returns when
// a value arrives. Every field is the one channel, so any index may come
// back: a scan that passes f00 before the Send and reaches f01 after it
// returns 1 (a scan is not one atomic look).
test "Conc-Select a record of 65 fields polls and returns" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 1).Must()
    var r := -2
    scope s := sys.io.Scope() {
        s.go SelSendLater(&c, sys.io, 3, 5)
        r = SelMany(&c, sys.io)
    }
    Assert(r >= 0 && r < 65)
    Assert(c.Recv(sys.io).Must() == 3)
    c.Free(a)
}

// ---- A Select racing Close, Send and its own cancel ----

// No cell left behind, asserted directly. A Select that returned, served by c1 after
// both cells were linked, or cancelled while both were, leaves no cell on
// either channel (core.ChanHasWatcher reads xq under the channel's lock).
// The stack scribble of the block above cannot see a cell left behind:
// the Select ran on another task's stack. Mutation check: SelectPark's
// `if j < linked` made `if j < 1` fails the first Assert below.
test "Select-Cells a Select that returned leaves no watch cell on any channel" (sys: &System) {
    a := sys.heap.Alloc()
    c1 := Chan[int].New(a, 1).Must()
    c2 := Chan[int].New(a, 1).Must()
    for round in 0..50 {
        res := atomic.AtomicInt.New(5)
        scope s := sys.io.Scope() {
            s.go SelBoth(&c1, &c2, sys.io, &res)
            s.go SelSendLater(&c1, sys.io, round, 1)
        }
        Assert(!core.ChanHasWatcher(&c2))
        Assert(!core.ChanHasWatcher(&c1))
        Assert(res.Load() == 0)
        Assert(c1.Recv(sys.io).Must() == round)
        res2 := atomic.AtomicInt.New(5)
        scope inner := sys.io.Scope() {
            inner.go SelBoth(&c1, &c2, sys.io, &res2)
            sys.io.Sleep(SelMs(1)) else {}
            inner.Cancel()
        }
        Assert(res2.Load() == -1)
        Assert(!core.ChanHasWatcher(&c1))
        Assert(!core.ChanHasWatcher(&c2))
    }
    c1.Free(a)
    c2.Free(a)
}

SelManyInto(c: &Chan[int], io: Io, res: &atomic.AtomicInt) {
    res.Store(i64(SelMany(c, io)))
}

// The polling path's await point (io.Sleep, more than core.SelCells
// fields) under cancellation: a Select of 65 fields over an idle channel,
// its scope cancelled 0.1 to 4.1 ms after the spawn, returns Canceled.
test "Select-Poll a polling Select of 65 fields is cancelled" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 1).Must()
    for round in 0..25 {
        res := atomic.AtomicInt.New(5)
        scope inner := sys.io.Scope() {
            inner.go SelManyInto(&c, sys.io, &res)
            sys.io.Sleep(core.DurationOf(i64(round % 5) * 1_000_000 + 100_000)) else {}
            inner.Cancel()
        }
        Assert(res.Load() == -1)
        Assert(!core.ChanHasWatcher(&c))
    }
    c.Free(a)
}

SelManyCancelUs(c: &Chan[int], io: Io, res: &atomic.AtomicInt, us: i64) {
    scope inner := io.Scope() {
        inner.go SelManyInto(c, io, res)
        io.Sleep(core.DurationOf(us * 1000)) else {}
        inner.Cancel()
    }
}

// The race: the cancel and a Send due at the same microsecond, 200
// rounds, natively. The polling Select returns a field index or Canceled,
// the value stays buffered, and nothing hangs.
test "Select-Poll a cancel racing a serve of a polling Select, native, 200 rounds" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 1).Must()
    for round in 0..200 {
        res := atomic.AtomicInt.New(-5)
        us := i64(200 + (round % 5) * 400)
        scope s := sys.io.Scope() {
            s.go SelManyCancelUs(&c, sys.io, &res, us)
            s.go SelSendUs(&c, sys.io, round, us)
        }
        r := res.Load()
        Assert(r == -1 || (r >= 0 && r < 65))
        Assert(c.Recv(sys.io).Must() == round)
    }
    c.Free(a)
}

// ---- A Send fires one Select, which passes the wake on ----

// Two Selects parked on one channel whose callers never Recv, and one
// Send: the Send fires one; it returns 0 and passes the wake on, so the
// other returns 0 too, and the value stays buffered. Under Sim schedules
// (each task yields first, so the Send lands before, between or after
// the parks); without the pass the second Select is left parked, which
// the Sim answers Canceled (its deadlock rule).
test "Select-PassOn one Send wakes one Select, and the wake is passed on, under Sim schedules, seeds 1 to 128" (sys: &System) {
    a := sys.heap.Alloc()
    for seed in 1..129 {
        var s := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := s.Io()
        c := Chan[int].New(a, 1).Must()
        r1 := atomic.AtomicInt.New(5)
        r2 := atomic.AtomicInt.New(5)
        scope sc := io.Scope() {
            sc.go SimSelYield(&c, io, &r1)
            sc.go SimSelYield(&c, io, &r2)
            sc.go SimSendYield(&c, io, seed % 4)
        }
        s.Free()
        Assert(r1.Load() == 0 && r2.Load() == 0)
        Assert(c.Recv(sys.io).Must() == 9)
        c.Free(a)
    }
}

// The same natively, 200 rounds, with four Selects and the Send due 0.2
// to 1 ms after the spawn.
test "Select-PassOn one Send wakes four Selects that never Recv, one by one, native, 200 rounds" (sys: &System) {
    a := sys.heap.Alloc()
    c := Chan[int].New(a, 1).Must()
    for round in 0..200 {
        r1 := atomic.AtomicInt.New(5)
        r2 := atomic.AtomicInt.New(5)
        r3 := atomic.AtomicInt.New(5)
        r4 := atomic.AtomicInt.New(5)
        scope s := sys.io.Scope() {
            s.go SelParked(&c, sys.io, &r1)
            s.go SelParked(&c, sys.io, &r2)
            s.go SelParked(&c, sys.io, &r3)
            s.go SelParked(&c, sys.io, &r4)
            s.go SelSendUs(&c, sys.io, round, i64(200 + (round % 5) * 200))
        }
        Assert(r1.Load() == 0 && r2.Load() == 0 && r3.Load() == 0 && r4.Load() == 0)
        Assert(c.Recv(sys.io).Must() == round)
    }
    c.Free(a)
}

SimSelPair(c1: &Chan[int], c2: &Chan[int], io: Io, res: &atomic.AtomicInt) {
    io.Yield() else {}
    SelBoth(c1, c2, io, res)
}

// A Select over (a: c1, b: c2) that c2 fires may find c1 ready too and
// return 0; it must pass c2's wake on to the Select parked on c2 alone.
// Under Sim schedules, both Sends yielding first; every seed: the pair
// returns 0 or 1, the single Select 0, both values buffered.
test "Select-PassOn a Select fired by one channel that returns another passes the wake on, under Sim schedules, seeds 1 to 256" (sys: &System) {
    a := sys.heap.Alloc()
    for seed in 1..257 {
        var s := try sim.Sim.New(sys.arena, Cast[u64](seed))
        io := s.Io()
        c1 := Chan[int].New(a, 1).Must()
        c2 := Chan[int].New(a, 1).Must()
        r1 := atomic.AtomicInt.New(5)
        r2 := atomic.AtomicInt.New(5)
        scope sc := io.Scope() {
            sc.go SimSelPair(&c1, &c2, io, &r1)
            sc.go SimSelYield(&c2, io, &r2)
            sc.go SimSendYield(&c2, io, seed % 3)
            sc.go SimSendYield(&c1, io, (seed / 3) % 4)
        }
        s.Free()
        Assert(r1.Load() == 0 || r1.Load() == 1)
        Assert(r2.Load() == 0)
        Assert(c1.Recv(sys.io).Must() == 9)
        Assert(c2.Recv(sys.io).Must() == 9)
        c1.Free(a)
        c2.Free(a)
    }
}

SelWorker(jobs: &Chan[int], quit: &Chan[int], io: Io, sum: &atomic.AtomicInt, got: &atomic.AtomicInt) {
    for {
        i := Select(io, (jobs: jobs, quit: quit)) else {
            return
        }
        if i != 0 {
            return
        }
        x := jobs.Recv(io) else {
            return
        }
        _ = sum.Add(i64(x))
        _ = got.Add(1)
    }
}

// The worker pool of the selpool benchmark row, as a stress
// test: 64 workers each Select over (jobs, quit) then Recv; 20,000 jobs at
// capacity 8 (a buffering Send fires one) and at capacity 0 (a parking
// Send fires one); then jobs and quit are closed. Every value once.
test "Select-Pool 64 Select workers, 20,000 jobs, capacity 8 and 0, every value once" (sys: &System) {
    a := sys.heap.Alloc()
    for cap in [8, 0] {
        jobs := Chan[int].New(a, cap).Must()
        quit := Chan[int].New(a, 0).Must()
        sum := atomic.AtomicInt.New(0)
        got := atomic.AtomicInt.New(0)
        scope s := sys.io.Scope() {
            for _ in 0..64 {
                s.go SelWorker(&jobs, &quit, sys.io, &sum, &got)
            }
            for i in 0..20000 {
                jobs.Send(sys.io, i).Must()
            }
            jobs.Close()
            quit.Close()
        }
        Assert(got.Load() == 20000)
        Assert(sum.Load() == 199990000)
        Assert(!core.ChanHasWatcher(&jobs) && !core.ChanHasWatcher(&quit))
        jobs.Free(a)
        quit.Free(a)
    }
}

// SelLate is a scripted Selectable for the scan-order block: its Ready
// first makes the channel early ready (one Send on it), then reports
// itself ready. That plays the late-Send interleaving on one task: the scan
// reads early (empty), a Send makes early ready, then the scan reads this
// field (ready). It holds a raw pointer, since `impl Selectable for
// &SelLate` conflicts (E0704) with `impl[T] Selectable for &Chan[T]`.
struct SelLate {
    early: *Chan[int]
    io: Io
}

impl Selectable for SelLate {
    Ready(&self): bool {
        unsafe "early points at the block's channel, which outlives the Select" {
            c := self.early
            if !core.ChanReady(&*c) {
                (*c).Send(self.io, 41) else {}
            }
        }
        return true
    }
    Watch(&self, p: *core.SelPark, i: int): bool {
        return true
    }
    Unwatch(&self, p: *core.SelPark, i: int) {
    }
    Pass(&self, p: *core.SelPark) {
    }
}

test "Conc-Select a field made ready by a Send before the returned field became ready is returned first, though the scan read it empty" (sys: &System) {
    al := sys.heap.Alloc()
    ch := Chan[int].New(al, 1).Must()
    late := SelLate(early: raw.Of(&ch), io: sys.io)
    which := Select(sys.io, (a: &ch, b: late)).Must()
    // The scripted Send happened, whatever Select returned.
    Assert(core.ChanReady(&ch))
    Assert(which == 0)
    Assert(ch.Recv(sys.io).Must() == 41)
    ch.Free(al)
}

// The block below needs SelectScan's loop: on the first read a is empty,
// b is not ready (its first Ready) and c is ready, so the scan finds c; on
// the second read a is empty, then b's second Ready sends on a and reports
// b ready, so the second read finds b; only a third read sees a, which the
// Send made ready before b became ready. One read again would return 1, no
// read again 2.

// SelSecond is ready from its second Ready on, and that Ready first makes
// the channel early ready (one Send on it).
struct SelSecond {
    early: *Chan[int]
    calls: *atomic.AtomicInt
    io: Io
}

impl Selectable for SelSecond {
    Ready(&self): bool {
        var n: i64 = 0
        unsafe "early and calls point at the block's channel and counter, which outlive the Select" {
            n = (*self.calls).Add(1)
            if n >= 2 {
                c := self.early
                if !core.ChanReady(&*c) {
                    (*c).Send(self.io, 42) else {}
                }
            }
        }
        return n >= 2
    }
    Watch(&self, p: *core.SelPark, i: int): bool {
        return true
    }
    Unwatch(&self, p: *core.SelPark, i: int) {
    }
    Pass(&self, p: *core.SelPark) {
    }
}

test "Conc-Select a field made ready before the field found on the second read is returned first, which needs the reads again to loop (three fields)" (sys: &System) {
    al := sys.heap.Alloc()
    ch := Chan[int].New(al, 1).Must()
    last := Chan[int].New(al, 1).Must()
    last.Send(sys.io, 7).Must()
    calls := atomic.AtomicInt.New(0)
    mid := SelSecond(early: raw.Of(&ch), calls: raw.Of(&calls), io: sys.io)
    which := Select(sys.io, (a: &ch, b: mid, c: &last)).Must()
    // b's second Ready ran and sent on a, whatever Select returned.
    Assert(calls.Load() >= 2)
    Assert(core.ChanReady(&ch))
    Assert(which == 0)
    Assert(ch.Recv(sys.io).Must() == 42)
    Assert(last.Recv(sys.io).Must() == 7)
    ch.Free(al)
    last.Free(al)
}
