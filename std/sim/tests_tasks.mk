// Why: the seeded scheduler is what makes a concurrent run reproducible,
// so one seed must give one interleaving and different seeds must differ.
// What: blocks on one interleaving per seed, the network and the clock
// between tasks, the deadlock rule, races between Io calls, and change
// points; helpers such as SimWorker.
// How: several tasks under one Sim run one at a time, in an order drawn
// from the seed; each block digests the order and makes and frees its Sim.

import std.core
import std.fs
import std.net
import std.raw
import std.time

// Several tasks under one Sim run one at a time in an order drawn
// from the seed. A root task, spawned by main, spawns four workers that
// send ten values each through a channel of capacity 2, yielding and
// sleeping between sends; the root receives the 40 values. The digest of
// the order they arrive in is the interleaving.
SimWorker(io: Io, ch: &Chan[int], i: int) {
    for j in 0..10 {
        ch.Send(io, i * 100 + j) else {
            return
        }
        if (i + j) % 3 == 0 {
            io.Sleep(core.DurationOf(Cast[i64](10 * (i + 1)) * 1000)) else {
                return
            }
        } else {
            io.Yield() else {
                return
            }
        }
    }
}

SimRoot(io: Io, ch: &Chan[int], out: &raw.Atomic[u64]) {
    var d: u64 = FnvSeed
    var n := 0
    scope s := io.Scope() {
        for i in 0..4 {
            s.go SimWorker(io, ch, i)
        }
        for _ in 0..40 {
            v := ch.Recv(io) else {
                break
            }
            d = FnvU64(d, Cast[u64](v))
            n += 1
        }
    }
    if n == 40 {
        out.Store(d)
    }
}

SimInterleave(a: &Arena, seed: u64): u64 {
    ch := Chan[int].New(a.Alloc(), 2) else {
        return 0
    }
    var s := Sim.New(a, seed) else {
        ch.Free(a.Alloc())
        return 0
    }
    io := s.Io()
    out := raw.Atomic[u64].New(0)
    scope sc := io.Scope() {
        sc.go SimRoot(io, &ch, &out)
    }
    s.Free()
    ch.Free(a.Alloc())
    return out.Load()
}

test "SIM: one seed gives one interleaving of several tasks, another seed another" (sys: &System) {
    first := SimInterleave(sys.arena, 21)
    Assert(first != 0)
    for _ in 0..20 {
        Assert(SimInterleave(sys.arena, 21) == first)
    }
    var differ := 0
    for k in 0..8 {
        d := SimInterleave(sys.arena, Cast[u64](22 + k))
        Assert(d != 0)
        if d != first {
            differ += 1
        }
    }
    Assert(differ > 0)
}

// Over the simulated network: a server task and a client task, in an
// order the seed picks. The client may dial before the server listens; it
// then sleeps 1 ms and dials again. The server's accept and receives wait
// for the client (the baton passes), and the client's Stream.Close ends
// the server's stream.
SimSrv(io: Io, out: &raw.Atomic[u64]) {
    l := net.Listen(io, "127.0.0.1:7100") else {
        return
    }
    c := l.Accept(io) else {
        l.Close()
        return
    }
    var buf: [64]u8 = [0; 64]
    var sum: u64 = 0
    for {
        k := c.Read(io, mut buf[..]) else {
            break
        }
        if k == 0 {
            break
        }
        for i in 0..k {
            sum += u64(buf[i])
        }
    }
    c.Close()
    l.Close()
    out.Store(sum)
}

SimCli(io: Io, tries: &raw.Atomic[u64]) {
    for _ in 0..100 {
        _ = tries.Add(1)
        c := net.Dial(io, "127.0.0.1:7100") else {
            io.Sleep(core.DurationOf(1_000_000)) else {
                return
            }
            continue
        }
        var msg: [300]u8 = [3; 300]
        c.WriteAll(io, msg[..]) else {
        }
        c.Close()
        return
    }
}

test "SIM: a server task and a client task meet over the simulated network" (sys: &System) {
    var early := 0
    for seed in 0..16 {
        var s := try Sim.New(sys.arena, Cast[u64](seed))
        onerr s.Free()
        io := s.Io()
        sum := raw.Atomic[u64].New(0)
        tries := raw.Atomic[u64].New(0)
        scope sc := io.Scope() {
            sc.go SimSrv(io, &sum)
            sc.go SimCli(io, &tries)
        }
        s.Free()
        Assert(sum.Load() == 900)
        if tries.Load() > 1 {
            early += 1
        }
    }
    // Some seeds run the client first: its first dial is Refused.
    Assert(early > 0 && early < 16)
}

// SimStuck receives from a channel nobody sends on.
SimStuck(io: Io, ch: &Chan[int], res: &raw.Atomic[u64]) {
    _ = ch.Recv(io) else e {
        match e {
            Canceled {
                _ = res.Add(1)
            }
            Closed {
            }
        }
        return
    }
}

test "SIM: tasks that all wait on each other are a deadlock, and each wait answers Canceled" (sys: &System) {
    var s := try Sim.New(sys.arena, 3)
    onerr s.Free()
    io := s.Io()
    a := sys.heap.Alloc()
    c1 := try Chan[int].New(a, 0)
    c2 := Chan[int].New(a, 0) else {
        c1.Free(a)
        s.Free()
        fail OutOfMemory
    }
    res := raw.Atomic[u64].New(0)
    scope sc := io.Scope() {
        sc.go SimStuck(io, &c1, &res)
        sc.go SimStuck(io, &c2, &res)
    }
    s.Free()
    c1.Free(a)
    c2.Free(a)
    Assert(res.Load() == 2)
}

// SimParked runs SimStuck in a scope of its own and joins it: while the
// worker is blocked, this task is away (in Join), so a choice waits in the
// deadlock rule's grace with the worker parked for real.
SimParked(io: Io, ch: &Chan[int], res: &raw.Atomic[u64]) {
    scope s := io.Scope() {
        s.go SimStuck(io, ch, res)
    }
}

test "SIM: Restart or Free while a task is parked ends its wait with Canceled" (sys: &System) {
    a := sys.heap.Alloc()
    ch := try Chan[int].New(a, 0)
    res := raw.Atomic[u64].New(0)
    for round in 0..4 {
        var s := Sim.New(sys.arena, Cast[u64](round)) else {
            ch.Free(a)
            fail OutOfMemory
        }
        io := s.Io()
        scope sc := io.Scope() {
            sc.go SimParked(io, &ch, &res)
            if round >= 2 {
                sys.io.Sleep(time.Millis(20)) else {
                }
            }
            _ = s.Restart()
        }
        s.Free()
    }
    for round in 0..4 {
        var s := Sim.New(sys.arena, Cast[u64](round)) else {
            ch.Free(a)
            fail OutOfMemory
        }
        io := s.Io()
        scope sc := io.Scope() {
            sc.go SimParked(io, &ch, &res)
            if round >= 2 {
                sys.io.Sleep(time.Millis(20)) else {
                }
            }
            s.Free()
        }
    }
    ch.Free(a)
    Assert(res.Load() == 8)
}

// SimRecvCanceled: a receive on ch answered Canceled.
SimRecvCanceled(ch: &Chan[int], io: Io): bool {
    _ = ch.Recv(io) else e {
        return match e {
            Canceled { true }
            Closed { false }
        }
    }
    return false
}

test "SIM: main waiting while the tasks deadlock is woken with Canceled" (sys: &System) {
    var s := try Sim.New(sys.arena, 4)
    onerr s.Free()
    io := s.Io()
    a := sys.heap.Alloc()
    c1 := try Chan[int].New(a, 0)
    c3 := Chan[int].New(a, 0) else {
        c1.Free(a)
        s.Free()
        fail OutOfMemory
    }
    res := raw.Atomic[u64].New(0)
    var main_canceled := false
    scope sc := io.Scope() {
        sc.go SimParked(io, &c1, &res)
        // The worker blocks and its parent joins, so the deadlock rule
        // waits out its grace; main's wait starts inside it.
        sys.io.Sleep(time.Millis(20)) else {
        }
        main_canceled = SimRecvCanceled(&c3, io)
    }
    s.Free()
    c1.Free(a)
    c3.Free(a)
    Assert(main_canceled && res.Load() == 1)
}

// Main spawns a collector and four workers directly (no root
// task). The births are announced, and main's Join is the leaf whose note
// lets the first choice happen, so the interleaving is the seed's alone.
SimCollect(io: Io, ch: &Chan[int], out: &raw.Atomic[u64]) {
    var d: u64 = FnvSeed
    var n := 0
    for _ in 0..40 {
        v := ch.Recv(io) else {
            break
        }
        d = FnvU64(d, Cast[u64](v))
        n += 1
    }
    if n == 40 {
        out.Store(d)
    }
}

SimDirect(a: &Arena, seed: u64): u64 {
    ch := Chan[int].New(a.Alloc(), 2) else {
        return 0
    }
    var s := Sim.New(a, seed) else {
        ch.Free(a.Alloc())
        return 0
    }
    io := s.Io()
    out := raw.Atomic[u64].New(0)
    scope sc := io.Scope() {
        sc.go SimCollect(io, &ch, &out)
        for i in 0..4 {
            sc.go SimWorker(io, &ch, i)
        }
    }
    s.Free()
    ch.Free(a.Alloc())
    return out.Load()
}

test "SIM tasks main spawns directly run in one order per seed, another seed another" (sys: &System) {
    first := SimDirect(sys.arena, 21)
    Assert(first != 0)
    for _ in 0..20 {
        Assert(SimDirect(sys.arena, 21) == first)
    }
    var differ := 0
    for k in 0..8 {
        d := SimDirect(sys.arena, Cast[u64](22 + k))
        Assert(d != 0)
        if d != first {
            differ += 1
        }
    }
    Assert(differ > 0)
}

test "SIM a deadlock with a joining task is called at once, not after a grace wait" (sys: &System) {
    a := sys.heap.Alloc()
    ch := try Chan[int].New(a, 0)
    res := raw.Atomic[u64].New(0)
    t0 := time.Now(sys.io)
    for round in 0..10 {
        var s := Sim.New(sys.arena, Cast[u64](round)) else {
            ch.Free(a)
            fail OutOfMemory
        }
        io := s.Io()
        scope sc := io.Scope() {
            sc.go SimParked(io, &ch, &res)
        }
        s.Free()
    }
    ms := time.Now(sys.io).Since(t0).Nanos() / 1_000_000
    ch.Free(a)
    // Each round's worker is Canceled by the deadlock rule; the old
    // 250 ms grace made ten rounds take at least 2.5 s.
    Assert(res.Load() == 10)
    Assert(ms < 1_000)
}

// The overdue tasks: a sleeper whose deadline the clock has passed runs
// at the next choice, ahead of a demoted spinner.
OverdueSleeper(io: Io, ch: &Chan[int]) {
    io.Sleep(time.Millis(1)) else {
        return
    }
    ch.Send(io, 1) else {
    }
}

OverdueSpinner(io: Io, ch: &Chan[int]) {
    start := time.Now(io)
    for time.Now(io).Since(start).Nanos() < 5_000_000 {
        io.Yield() else {
            return
        }
    }
    ch.Send(io, 2) else {
    }
}

OverdueRoot(io: Io, ch: &Chan[int], first: &raw.Atomic[u64]) {
    scope s := io.Scope() {
        s.go OverdueSleeper(io, ch)
        s.go OverdueSpinner(io, ch)
        v := ch.Recv(io) else {
            return
        }
        first.Store(Cast[u64](v))
        _ = ch.Recv(io) else {
            return
        }
    }
}

OverdueFlag(io: Io, flag: &raw.Atomic[u64]) {
    io.Sleep(time.Millis(1)) else {
        return
    }
    flag.Store(1)
}

OverdueWait(io: Io, flag: &raw.Atomic[u64], seen: &raw.Atomic[u64]) {
    var k: u64 = 0
    for k < 1_000_000 && flag.Load() == 0 {
        io.Yield() else {
            return
        }
        k += 1
    }
    if flag.Load() == 1 {
        seen.Store(k + 1)
    }
}

test "SIM an overdue sleeper runs at the next choice, so a yield loop waiting for it ends" (sys: &System) {
    a := sys.heap.Alloc()
    for seed in 0..50 {
        ch := try Chan[int].New(a, 4)
        first := raw.Atomic[u64].New(0)
        var s := Sim.New(sys.arena, Cast[u64](seed)) else {
            ch.Free(a)
            fail OutOfMemory
        }
        io := s.Io()
        scope sc := io.Scope() {
            sc.go OverdueRoot(io, &ch, &first)
        }
        s.Free()
        ch.Free(a)
        // The 1 ms sleeper reports before the 5 ms spinner on every seed.
        Assert(first.Load() == 1)
    }
    for seed in 0..20 {
        flag := raw.Atomic[u64].New(0)
        seen := raw.Atomic[u64].New(0)
        var s := try Sim.New(sys.arena, Cast[u64](seed))
        io := s.Io()
        scope sc := io.Scope() {
            sc.go OverdueFlag(io, &flag)
            sc.go OverdueWait(io, &flag, &seen)
        }
        s.Free()
        // Each yield costs 1 us, so about 1,000 yields reach the deadline.
        Assert(seen.Load() > 0 && seen.Load() < 10_000)
    }
}

// NapServer, NapClient and Napper: I/O latency is an event, so a 1 ms
// sleep beside a network exchange takes 1 ms of simulated time, and the
// sleep may end first.
NapServer(io: Io, port: str) {
    l := net.Listen(io, port) else {
        return
    }
    c := l.Accept(io) else {
        l.Close()
        return
    }
    var msg: [1]u8 = [7]
    c.WriteAll(io, msg[..]) else {
    }
    c.Close()
    l.Close()
}

NapClient(io: Io, port: str, order: &raw.Atomic[u64], got: &raw.Atomic[u64]) {
    for _ in 0..50 {
        c := net.Dial(io, port) else {
            io.Sleep(time.Millis(1)) else {
                return
            }
            continue
        }
        var buf: [1]u8 = [0]
        n := c.Read(io, mut buf[..]) else {
            c.Close()
            return
        }
        c.Close()
        if n == 1 && buf[0] == 7 {
            got.Store(order.Add(1) + 1)
        }
        return
    }
}

Napper(io: Io, order: &raw.Atomic[u64], nap: &raw.Atomic[u64], at: &raw.Atomic[u64]) {
    t0 := time.Now(io)
    io.Sleep(time.Millis(1)) else {
        return
    }
    nap.Store(Cast[u64](time.Now(io).Since(t0).Nanos()))
    at.Store(order.Add(1) + 1)
}

test "SIM a sleep beside network I/O fires on time, and may fire before the reply" (sys: &System) {
    var before := 0
    var after := 0
    for seed in 0..100 {
        order := raw.Atomic[u64].New(0)
        got := raw.Atomic[u64].New(0)
        nap := raw.Atomic[u64].New(0)
        at := raw.Atomic[u64].New(0)
        var s := try Sim.New(sys.arena, Cast[u64](seed))
        io := s.Io()
        scope sc := io.Scope() {
            sc.go NapServer(io, "127.0.0.1:7300")
            sc.go NapClient(io, "127.0.0.1:7300", &order, &got)
            sc.go Napper(io, &order, &nap, &at)
        }
        s.Free()
        Assert(got.Load() > 0 && at.Load() > 0)
        // 1 ms plus the few 1 us clock reads of the napper itself.
        Assert(nap.Load() >= 1_000_000 && nap.Load() < 1_050_000)
        if at.Load() < got.Load() {
            before += 1
        } else {
            after += 1
        }
    }
    Assert(before > 0 && after > 0)
}

// RaceIncr reads the counter file and writes it back plus one. Two such
// tasks lose an update for some seed, and a seed's result is the same
// every time.
RaceIncr(io: Io) {
    var b: [1]u8 = [0]
    var f := fs.Open(io, "c") else {
        return
    }
    n := f.ReadAt(io, 0, mut b[..]) else {
        f.Abandon()
        return
    }
    f.Close(io) else {
        f.Abandon()
    }
    if n != 1 {
        return
    }
    b[0] = b[0] + 1
    fs.WriteAll(io, "c", b[..]) else {
    }
}

RaceRun(a: &Arena, seed: u64): int {
    var s := Sim.New(a, seed) else {
        return -1
    }
    io := s.Io()
    var z: [1]u8 = [0]
    fs.WriteAll(io, "c", z[..]) else {
    }
    scope sc := io.Scope() {
        sc.go RaceIncr(io)
        sc.go RaceIncr(io)
    }
    var b: [1]u8 = [0]
    var f := fs.Open(io, "c") else {
        s.Free()
        return -1
    }
    n := f.ReadAt(io, 0, mut b[..]) else {
        f.Abandon()
        s.Free()
        return -1
    }
    f.Abandon()
    s.Free()
    if n != 1 {
        return -1
    }
    return int(b[0])
}

test "SIM a race between Io calls that do not park is reachable, one result per seed" (sys: &System) {
    var lost := 0
    var both := 0
    for seed in 0..200 {
        v := RaceRun(sys.arena, Cast[u64](seed))
        Assert(v == 1 || v == 2)
        Assert(RaceRun(sys.arena, Cast[u64](seed)) == v)
        if v == 1 {
            lost += 1
        } else {
            both += 1
        }
    }
    Assert(lost > 0 && both > 0)
}

// FedWorker receives one value: main feeds it over a channel after Io
// calls of its own, and the worker's receive is not a deadlock.
FedWorker(io: Io, ch: &Chan[int], res: &raw.Atomic[u64]) {
    v := ch.Recv(io) else {
        return
    }
    res.Store(Cast[u64](v))
}

test "SIM main writing files and then sending to its worker is not a deadlock" (sys: &System) {
    a := sys.heap.Alloc()
    for seed in 0..50 {
        ch := try Chan[int].New(a, 1)
        res := raw.Atomic[u64].New(0)
        var s := Sim.New(sys.arena, Cast[u64](seed)) else {
            ch.Free(a)
            fail OutOfMemory
        }
        io := s.Io()
        var z: [1]u8 = [0]
        scope sc := io.Scope() {
            sc.go FedWorker(io, &ch, &res)
            for _ in 0..10 {
                fs.WriteAll(io, "c", z[..]) else {
                }
            }
            ch.Send(io, 5) else {
            }
        }
        s.Free()
        ch.Free(a)
        Assert(res.Load() == 5)
    }
}

// ChangeCounter and ChangeWatcher: main's operations before the tasks use
// up no change point, and a Restart's run draws its own. The counter's 600
// counted operations are cut by a change point that hands the baton to
// the watcher, for most seeds.
ChangeCounter(io: Io, cnt: &raw.Atomic[u64]) {
    for k in 0..600 {
        l := net.Listen(io, "127.0.0.1:0") else {
            return
        }
        l.Close()
        cnt.Store(Cast[u64](k + 1))
    }
}

ChangeWatcher(io: Io, cnt: &raw.Atomic[u64], mid: &raw.Atomic[u64]) {
    for _ in 0..5_000 {
        c := cnt.Load()
        if c > 0 && c < 600 {
            mid.Store(1)
        }
        if c == 600 {
            return
        }
        io.Yield() else {
            return
        }
    }
}

// ChangeMid: the watcher saw the counter part way, in the run after `before` main operations and
// `restarts` Restarts.
ChangeMid(a: &Arena, seed: u64, before: int, restarts: int): bool {
    var s := Sim.New(a, seed) else {
        return false
    }
    var io := s.Io()
    for _ in 0..restarts {
        io = s.Restart()
    }
    for _ in 0..before {
        l := net.Listen(io, "127.0.0.1:0") else {
            break
        }
        l.Close()
    }
    cnt := raw.Atomic[u64].New(0)
    mid := raw.Atomic[u64].New(0)
    scope sc := io.Scope() {
        sc.go ChangeCounter(io, &cnt)
        sc.go ChangeWatcher(io, &cnt, &mid)
    }
    s.Free()
    return mid.Load() == 1
}

test "SIM change points survive main's operations and a Restart" (sys: &System) {
    var plain := 0
    var after_main := 0
    var after_restart := 0
    for seed in 0..40 {
        if ChangeMid(sys.arena, Cast[u64](seed), 0, 0) {
            plain += 1
        }
        if ChangeMid(sys.arena, Cast[u64](seed), 600, 0) {
            after_main += 1
        }
        if ChangeMid(sys.arena, Cast[u64](seed), 0, 1) {
            after_restart += 1
        }
    }
    Assert(plain >= 20 && after_main >= 20 && after_restart >= 20)
}

// The futex pair under a Recorder and a Replayer: a waiter sleeps until
// its waker's wake (through the inner Io for a Recorder, on the native
// table for a Replayer), so a task the waiter waits for gets to run; a
// wait that spun on the caller's carrier never let it.
// SendSeven sends 7 on ch.
SendSeven(io: Io, ch: &Chan[int]) {
    ch.Send(io, 7) else {
    }
}

// TapeRecv: main spawns SendSeven under io and receives its value; -1 when
// none came.
TapeRecv(a: &Alloc, io: Io): int {
    ch := Chan[int].New(a, 1) else {
        return -1
    }
    var got := -1
    scope sc := io.Scope() {
        sc.go SendSeven(io, &ch)
        got = ch.Recv(io) else {
            -1
        }
    }
    ch.Free(a)
    return got
}

// PingPong receives n values on inp and answers each with the next on
// out; sum is the sum of the values it got.
PingPong(io: Io, inp: &Chan[int], out: &Chan[int], n: int, sum: &raw.Atomic[u64]) {
    var t: u64 = 0
    for _ in 0..n {
        v := inp.Recv(io) else {
            return
        }
        t += Cast[u64](v)
        out.Send(io, v + 1) else {
            return
        }
    }
    sum.Store(t)
}

// TapePing plays 500 rounds of ping-pong between main and a task under io:
// true when main ends with 500 and the task's sum is 0 + 1 + ... + 499.
TapePing(a: &Alloc, io: Io): bool {
    ping := Chan[int].New(a, 1) else {
        return false
    }
    pong := Chan[int].New(a, 1) else {
        ping.Free(a)
        return false
    }
    sum := raw.Atomic[u64].New(0)
    var v := 0
    scope sc := io.Scope() {
        sc.go PingPong(io, &ping, &pong, 500, &sum)
        for _ in 0..500 {
            ping.Send(io, v) else {
                break
            }
            v = pong.Recv(io) else {
                break
            }
        }
    }
    pong.Free(a)
    ping.Free(a)
    return v == 500 && sum.Load() == 124_750
}

test "SIM under a Recorder over a Sim, main receiving from the task it spawned waits for it" (sys: &System) {
    a := sys.heap.Alloc()
    for seed in 0..20 {
        var s := try Sim.New(sys.arena, Cast[u64](seed))
        var rec := Recorder.New(s.Io()) else {
            s.Free()
            fail OutOfMemory
        }
        got := TapeRecv(a, rec.Io())
        rec.Free()
        s.Free()
        Assert(got == 7)
    }
}

test "SIM a replay of a run whose tasks meet on channels waits as the run did" (sys: &System) {
    a := sys.heap.Alloc()
    var rec := try Recorder.New(sys.io)
    defer rec.Free()
    Assert(TapeRecv(a, rec.Io()) == 7)
    Assert(TapePing(a, rec.Io()))
    var rp := try Replayer.New(try rec.Log(sys.arena))
    defer rp.Free()
    Assert(TapeRecv(a, rp.Io()) == 7)
    Assert(TapePing(a, rp.Io()))
    Assert(rp.Done() && !rp.Diverged())
}

// Starve waits on a channel nobody sends to, and counts a wait that ended
// with an error.
Starve(io: Io, ch: &Chan[int], ended: &raw.Atomic[u64]) {
    v := ch.Recv(io) else {
        -1
    }
    if v == -1 {
        _ = ended.Add(1)
    }
}

test "SIM under a Recorder over a Sim, tasks waiting on a channel nobody sends to are a deadlock, and each wait answers Canceled" (sys: &System) {
    a := sys.heap.Alloc()
    for seed in 0..10 {
        ch := try Chan[int].New(a, 1)
        ended := raw.Atomic[u64].New(0)
        var s := Sim.New(sys.arena, Cast[u64](seed)) else {
            ch.Free(a)
            fail OutOfMemory
        }
        var rec := Recorder.New(s.Io()) else {
            s.Free()
            ch.Free(a)
            fail OutOfMemory
        }
        io := rec.Io()
        scope sc := io.Scope() {
            sc.go Starve(io, &ch, &ended)
            sc.go Starve(io, &ch, &ended)
        }
        rec.Free()
        s.Free()
        ch.Free(a)
        Assert(ended.Load() == 2)
    }
}

// Spinner yields under io until a yield fails, then counts itself.
Spinner(io: Io, ended: &raw.Atomic[u64]) {
    for {
        io.Yield() else {
            _ = ended.Add(1)
            return
        }
    }
}

test "SIM a Recorder over a Sim freed while its tasks run: they see Canceled and end, and the scope's join returns" (sys: &System) {
    for seed in 0..10 {
        ended := raw.Atomic[u64].New(0)
        var s := try Sim.New(sys.arena, Cast[u64](seed))
        var rec := Recorder.New(s.Io()) else {
            s.Free()
            fail OutOfMemory
        }
        io := rec.Io()
        scope sc := io.Scope() {
            sc.go Spinner(io, &ended)
            sc.go Spinner(io, &ended)
            // A native sleep: main's note lets the tasks start and take
            // turns on the baton before the Recorder goes.
            sys.io.Sleep(core.DurationOf(20_000_000)) else {
            }
            rec.Free()
        }
        s.Free()
        Assert(ended.Load() == 2)
    }
}
