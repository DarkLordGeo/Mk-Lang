// Why: a cancel must end a pool job blocked in its OS call, without the
// interrupt reaching another task's job, even when it lands too early.
// What: the RT-F13-* blocks (PoolRepeat, PoolCancelNoBleed, PoolCancelEarly,
// PoolCancelBusy, EnvAbsent), run by std/rt-sched_harness_test.go and mk test.
// How: the source is a read that never gets data: on Windows the standard
// input, which the harness keeps open and empty (MK_TEST_STDIN open; mk test
// gives the null device, which cannot block, and then the blocks check only
// what they can); on Linux a blocking socket whose peer never writes (no
// O_NONBLOCK, so the pool thread blocks in the kernel).

import std.core
import std.raw
import std.rt
import std.sys

// FSrc is a blocking read source: h a runtime handle read through the pool
// (0 when this process has none), and on Linux the sockets behind it.
struct FSrc {
    h: uint
    l: uint
    peer: uint
}

// FSource makes the blocking source; stdin_open is what the harness says.
FSource(io: Io, stdin_open: bool): FSrc {
    var f := FSrc(h: 0, l: 0, peer: 0)
    if Target.os == Os.Windows {
        unsafe "the RtState page: stdin's runtime handle lives for the process" {
            h := (*rt.Proc()).stdin.h
            if sys.HandleKind((*rt.Rec(h)).os) == 3 {
                f.h = h
            }
        }
        // Under the harness the pipe must be there, or the blocks would
        // pass without their scenario.
        Assert(f.h != 0 || !stdin_open)
        return f
    }
    l := NListen(io)
    f.l = Trunc[uint](l.value)
    c := sys.SocketOpen(sys.Inet4, false)
    Assert(c.err == core.SysErr.None)
    fd := Trunc[uint](c.value)
    acc := raw.Atomic[u64].New(0)
    scope s := io.Scope() {
        s.go NAcceptInto(io, f.l, &acc)
        var op := sys.NewOp(sys.OpConnect, fd)
        op.addr = sys.SockAddr(family: sys.Inet4, port: Trunc[u16](l.os), addr: [127, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0],
            scope_id: 0)
        unsafe "op is in this frame until Await returns" {
            Assert(Await(raw.OfMut(mut op), false) == core.SysErr.None)
        }
        Assert(op.res.err == core.SysErr.None)
    }
    Assert(acc.Load() != 0)
    f.peer = Trunc[uint](acc.Load())
    f.h = rt.NewHandle(fd, false)
    Assert(f.h != 0)
    return f
}

// FClose closes what FSource made (Windows' stdin stays).
FClose(f: &FSrc) {
    if f.l != 0 {
        rt.Abandon(f.h)
        rt.Abandon(f.peer)
        rt.Abandon(f.l)
    }
}

// FBlockedRead reads the source through the pool (NativeRead, JobRead) and
// records the result: 1 Canceled, 2 data or end of input, 3 any other
// error; then 4 more if a later sleep in the cancelled scope also returned
// Canceled (level-triggered). at becomes 1 just before the read.
FBlockedRead(io: Io, h: uint, at: &raw.Atomic[u32], res: &raw.Atomic[u32]) {
    var b: [16]u8 = [0; 16]
    at.Store(1)
    // OnPool, not NativeRead: NativeRead makes a read of a pipe or a
    // socket off the poller a wait job, which takes a thread beyond the
    // pool's bound, and these blocks need the read on one of the
    // pmax threads (with MK_TEST_POOL=1, the job queued behind it).
    var op := sys.NewOp(sys.OpRead, 0)
    unsafe "h is a live runtime handle; b is in this frame until OnPool returns" {
        op.h = (*rt.Rec(h)).os
        op.buf = raw.DataMut(mut b[..])
    }
    op.len = 16
    op.pos = -1
    r := OnPool(io, JobRead, mut op)
    var v: u32 = 3
    if r.err == core.SysErr.Canceled {
        v = 1
    } else if r.err == core.SysErr.None {
        v = 2
    }
    io.Sleep(core.DurationOf(0)) else {
        v += 4
    }
    res.Store(v)
}

// FQuick runs one quick pool job and stores its value, or 0x100 + its
// error's kind when it failed (Canceled is the bleed the test forbids).
FQuick(io: Io, pos: i64, out: &raw.Atomic[u64]) {
    var op := sys.NewOp(0, 0)
    op.pos = pos
    unsafe "op is in this frame until Blocking returns" {
        e := Blocking(PQuick, raw.OfMut(mut op), true)
        if e != core.SysErr.None {
            out.Store(0x100)
            return
        }
    }
    if op.res.err != core.SysErr.None {
        out.Store(0x200)
        return
    }
    out.Store(Trunc[u64](op.res.value))
}

// FInCall waits until the read task's job is in its call (a pool thread
// took it), at most 2 s; the thread may not be blocked in the kernel yet,
// which is the early window, also covered.
FInCall(at: &raw.Atomic[u32]) {
    t0 := BenchNow()
    for at.Load() == 0 && BenchNow() - t0 < 2_000_000_000 {
        YieldNow()
    }
    for FPhase() != JobInCall && BenchNow() - t0 < 2_000_000_000 {
        YieldNow()
    }
}

// The task of the read under test, published by FTrack (tests only).
var FReader: raw.Atomic[u64] = raw.Atomic[u64].New(0)

// CurrentJobOf is the read task's record (FReader).
CurrentJobOf(st: *TaskState): *Task {
    unsafe "the read task is parked in Blocking while FInCall runs" {
        // Edge: FTrack's store before the task reached its read.
        return raw.FromAddr[Task](Trunc[uint](FReader.Load()))
    }
}

// FTrackedRead is FBlockedRead that first publishes its task (FReader).
FTrackedRead(io: Io, h: uint, at: &raw.Atomic[u32], res: &raw.Atomic[u32]) {
    unsafe "only the running task's address is read; nothing is dereferenced" {
        // Edge: before at's store, which FInCall reads first.
        FReader.Store(Trunc[u64](raw.Addr(Current())))
    }
    FEarlyMine()
    FBlockedRead(io, h, at, res)
}

// FPhase is the read task's job phase (JobInCall while its call runs).
FPhase(): u32 {
    unsafe "the TaskState page; the read task is parked in Blocking" {
        st := TS()
        rt.Lock(&(*st).plock)
        var ph: u32 = 0
        j := raw.PtrCast[u8, PoolJob]((*CurrentJobOf(st)).job)
        if !raw.IsNull(j) {
            ph = (*j).phase
        }
        rt.Unlock(&(*st).plock)
        return ph
    }
}

// FCount is a job that reports Canceled on its first call and the value 9
// on its second: what a call hit by another job's interrupt looks like
// (the late window). op.pos counts the calls.
FCount(op: *sys.Op) {
    unsafe "op is the caller's" {
        (*op).pos += 1
        if (*op).pos == 1 {
            (*op).res = sys.OsResult(value: 0, err: core.SysErr.Canceled, os: 4)
        } else {
            (*op).res = sys.OsResult(value: 9, err: core.SysErr.None, os: 0)
        }
    }
}

// FRepeat runs FCount through the pool, uncancelled: the value and the
// number of calls.
FRepeat(): i64 {
    var op := sys.NewOp(0, 0)
    op.pos = 0
    unsafe "op is in this frame until Blocking returns" {
        e := Blocking(FCount, raw.OfMut(mut op), true)
        Assert(e == core.SysErr.None)
    }
    Assert(op.res.err == core.SysErr.None)
    return op.res.value * 100 + op.pos
}

// FEarlyArm arms the pool's early gate; FEarlyHeld and FEarlyRelease drive
// it (PoolHook.early: 1 armed, 2 held, 3 released).
FEarlyArm() {
    // The reader names itself (FEarlyMine) before its read is queued.
    PoolHook.early_task.Store(0)
    PoolHook.early.Store(1)
}

// FEarlyMine names the calling task as the one whose job the early gate
// holds.
FEarlyMine() {
    unsafe "only the running task's address is read; nothing is dereferenced" {
        PoolHook.early_task.Store(u64(raw.Addr(Current())))
    }
}

FEarlyHeld(): bool {
    t0 := BenchNow()
    for PoolHook.early.Load() != 2 && BenchNow() - t0 < 2_000_000_000 {
        YieldNow()
    }
    return PoolHook.early.Load() == 2
}

FEarlyRelease() {
    _ = PoolHook.early.Cas(2, 3)
    PoolHook.early.Store(0)
}

// FNote writes a line "<what> <n>" to standard error (a measurement the
// harness logs with -v).
FNote(what: str, n: i64) {
    var b: [64]u8 = [0; 64]
    w := what.AsBytes()
    var k := 0
    for i in 0..w.Len() {
        if k < 40 {
            b[k] = w[i]
            k += 1
        }
    }
    b[k] = 32
    k += 1
    var d: [20]u8 = [0; 20]
    var m := 0
    var x := n
    if x < 0 {
        x = 0
    }
    for {
        d[m] = Trunc[u8](48 + x % 10)
        m += 1
        x = x / 10
        if x == 0 {
            break
        }
    }
    for i in 0..m {
        b[k] = d[m - 1 - i]
        k += 1
    }
    b[k] = 10
    k += 1
    rt.WriteErr(b[..k])
}

// FListed: how many cancelled jobs are on the pool's list.
FListed(): u32 {
    unsafe "the TaskState page Start mapped, which lives until exit" {
        return (*TS()).pcancel_n.Load()
    }
}

// FRepeatTask runs FRepeat n times on a task and counts the wrong answers.
FRepeatTask(io: Io, n: int, bad: &raw.Atomic[u32]) {
    for _ in 0..n {
        if FRepeat() != 902 {
            _ = bad.Add(1)
        }
    }
}

test "RT-F13-PoolRepeat a call that reports Canceled for a job nobody cancelled is repeated" (sys: &System) {
    // The late window: an interrupt meant for another job is not
    // this job's result. The calls run on a task: a program that spawns no
    // task runs a blocking job on the calling thread (rt.UsesTasks), where
    // no other job's interrupt exists and a Canceled is the call's own.
    bad := raw.Atomic[u32].New(0)
    scope s := sys.io.Scope() {
        s.go FRepeatTask(sys.io, 100, &bad)
    }
    Assert(bad.Load() == 0)
}

// FSpin waits ns on the clock (the pool thread then blocks in its call).
FSpin(ns: i64) {
    t0 := BenchNow()
    for BenchNow() - t0 < ns {
    }
}

test "RT-F13-PoolCancelNoBleed [env MK_TEST_POOL=1] a pool read in its call is interrupted by its cancel; the job queued behind it never sees Canceled" (sys: &System) {
    stdin := sys.Env("MK_TEST_STDIN") else { "" }
    src := FSource(sys.io, stdin == "open")
    if src.h == 0 {
        // mk test on Windows: no blocking source (the null device). The
        // uncancelled-repeat rule is still checked by RT-F13-PoolRepeat.
        return
    }
    var worst: i64 = 0
    var over50 := 0
    var fast := 0
    for round in 0..200 {
        at := raw.Atomic[u32].New(0)
        res := raw.Atomic[u32].New(0)
        bres := raw.Atomic[u64].New(0)
        var t1: i64 = 0
        scope outer := sys.io.Scope() {
            scope s := sys.io.Scope() {
                s.go FTrackedRead(sys.io, src.h, &at, &res)
                FInCall(&at)
                // B's job queues behind A's on the one pool thread (dev);
                // in release B gets a thread of its own.
                outer.go FQuick(sys.io, i64(round), &bres)
                // Odd rounds: the thread is blocked in the kernel when the
                // cancel comes, so the first interrupt ends the call. Even
                // rounds: at once, often before the call (the early window).
                if round % 2 == 1 {
                    FSpin(2_000_000)
                }
                t1 = BenchNow()
                s.Cancel()
            }
            d := BenchNow() - t1
            if d > worst {
                worst = d
            }
            if d >= 50_000_000 {
                over50 += 1
            }
            if round % 2 == 1 && d < ReinterruptNs / 2 {
                fast += 1
            }
        }
        // A: Canceled, and the next operation in its scope too.
        Assert(res.Load() == 5)
        // B: its own value, never Canceled or Other (3 * round).
        Assert(bres.Load() == Trunc[u64](3 * round))
    }
    Assert(FListed() == 0)
    FNote("f13-noble-worst-ns", worst)
    FNote("f13-noble-over-50ms", i64(over50))
    FNote("f13-noble-incall-fast-of-100", i64(fast))
    // The first interrupt ends a call that is blocked: most such cancels
    // take far less than one re-interrupt period (a lost first interrupt
    // would make each take about ReinterruptNs).
    Assert(fast >= 50)
    // The 50 ms bound, allowing for a loaded machine: at most 5 of 200 rounds
    // over it, and none near the watchdog.
    Assert(over50 <= 5 && worst < 1_000_000_000)
    FClose(&src)
}

test "RT-F13-PoolCancelEarly an interrupt that lands before the call blocks is sent again" (sys: &System) {
    stdin := sys.Env("MK_TEST_STDIN") else { "" }
    src := FSource(sys.io, stdin == "open")
    if src.h == 0 {
        return
    }
    // Up to 3 attempts of 20 rounds; every round of every attempt checks
    // the results, and the timing rule must hold in one attempt. A lost
    // re-interrupt blocks the read until the watchdog in any attempt, and a
    // re-send period over 50 ms fails all three, while a load spike (once
    // 2 of 20 rounds over 50 ms, worst 119 ms, while other tests ran)
    // passes the next attempt. Preemption only adds time.
    var worst: i64 = 0
    var over50 := 0
    var tries := 0
    for tries < 3 {
        worst = 0
        over50 = 0
        for _ in 0..20 {
            at := raw.Atomic[u32].New(0)
            res := raw.Atomic[u32].New(0)
            FEarlyArm()
            var t1: i64 = 0
            scope s := sys.io.Scope() {
                s.go FTrackedRead(sys.io, src.h, &at, &res)
                // The pool thread took the read and holds before its call.
                Assert(FEarlyHeld())
                Assert(FPhase() == JobInCall)
                // The interrupt goes now and interrupts nothing: the thread
                // is not in a call.
                s.Cancel()
                Assert(FListed() == 1)
                // Then the call starts and blocks; only a second interrupt
                // can end it.
                t1 = BenchNow()
                FEarlyRelease()
            }
            d := BenchNow() - t1
            if d > worst {
                worst = d
            }
            if d >= 50_000_000 {
                over50 += 1
            }
            Assert(res.Load() == 5)
        }
        Assert(FListed() == 0)
        tries += 1
        FNote("f13-early-worst-ns", worst)
        if over50 <= 1 && worst < 1_000_000_000 {
            break
        }
    }
    // The re-interrupt comes within ReinterruptNs or two of the call's
    // start, so within the 50 ms bound; a loaded machine may miss once.
    Assert(over50 <= 1 && worst < 1_000_000_000)
    FClose(&src)
}

// FCarriers is the carrier count this process runs with.
FCarriers(): u32 {
    unsafe "the TaskState page Start mapped, which lives until exit" {
        // Edge: none; set at start.
        return (*TS()).want.Load()
    }
}

test "RT-F13-PoolCancelBusy an early-window interrupt is sent again while the canceller keeps computing" (sys: &System) {
    // The canceller does not suspend after Cancel, so only another carrier
    // can re-send; carriers that went idle before the job was listed wait
    // with no timeout, and CancelRec wakes one (WakeOne). At 1 carrier no
    // other carrier exists and the re-send waits for the canceller to
    // suspend (no preemption): checked only from 2 carriers.
    stdin := sys.Env("MK_TEST_STDIN") else { "" }
    src := FSource(sys.io, stdin == "open")
    if src.h == 0 || FCarriers() < 2 {
        FClose(&src)
        return
    }
    for _ in 0..10 {
        at := raw.Atomic[u32].New(0)
        res := raw.Atomic[u32].New(0)
        FEarlyArm()
        var seen := false
        scope s := sys.io.Scope() {
            s.go FTrackedRead(sys.io, src.h, &at, &res)
            Assert(FEarlyHeld())
            // Let every other carrier settle into its untimed idle wait.
            FSpin(5_000_000)
            s.Cancel()
            FEarlyRelease()
            // Compute, never suspend, for up to 500 ms: the read must end
            // meanwhile (the 10 ms re-send, from another carrier).
            t0 := BenchNow()
            for BenchNow() - t0 < 500_000_000 {
                if res.Load() == 5 {
                    seen = true
                    break
                }
            }
        }
        Assert(seen)
    }
    FClose(&src)
}

// EnvDrop ignores open outside Windows (FSource's shape) and returns 7
// there; on Windows 7 unless open, when it is 8.
EnvDrop(io: Io, open: bool): int {
    return if Target.os == Os.Windows && open { 8 } else { 7 }
}

test "RT-F13-EnvAbsent an absent environment name is none, and the search stops at the end of the list" (sys: &System) {
    // A Linux release fault in the four FSource blocks was this lookup: a
    // compiler that dropped the exit test of System.Env's inlined loop let a
    // name not in the environment (the Linux harness sets no MK_TEST_STDIN)
    // walk past the end of the entries until the next unmapped heap page (a
    // 64 KiB-aligned fault). Pinned here directly, in dev and release, on
    // both targets.
    // The failing shape first: the result feeds a parameter that the
    // Linux half of the callee ignores (as FSource's stdin_open), so the
    // inlined loop survives only for its loads.
    Assert(EnvDrop(sys.io, (sys.Env("MK_RTS_NO_SUCH_VARIABLE") else { "" }) == "open") == 7)
    Assert(sys.Env("MK_RTS_NO_SUCH_VARIABLE").IsNone())
    Assert(sys.Env("").IsNone())
    var n := 0
    for _ in 0..1000 {
        if sys.Env("MK_RTS_NO_SUCH_VARIABLE").IsSome() {
            n += 1
        }
    }
    Assert(n == 0)
}
