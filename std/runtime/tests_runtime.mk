// Why: std.runtime's doors must report the truth about the runtime.
// What: test blocks of std.runtime, run by `mk test --std=std.runtime` at
// the default carriers and with MK_CARRIERS=1.
// How: checks each door's result, and runs Blocking, Waker, Dedicated,
// Thread and Join under cancels, deadlines and many tasks at once.

import std.atomic
import std.core
import std.raw
import std.rt
import std.sys

test "Runtime Processors and Carriers are at least 1" {
    Assert(Processors() >= 1)
    Assert(Carriers() >= 1)
    Assert(Carriers() <= 256)
}

test "Runtime StackBounds holds the stack pointer, StackRemaining is the distance to lo" (sys: &System) {
    b := StackBounds()
    sp := rt.StackPointer()
    Assert(b.lo > 0 && b.lo < sp && sp < b.hi)
    r := StackRemaining()
    Assert(r > 0 && r <= sp - b.lo)
    // A test body runs as Main, on a Large slot (16 MiB); a spawned task
    // has the default 1 MiB.
    Assert(r < 16 << 20)
    scope s := sys.io.Scope() {
        s.go SmallStack()
    }
}

SmallStack() {
    b := StackBounds()
    sp := rt.StackPointer()
    Assert(b.lo < sp && sp < b.hi)
    Assert(b.hi - b.lo < 1 << 20)
    Assert(b.hi - b.lo > 1 << 19)
    Assert(StackRemaining() < 1 << 20)
}

// Deep recurses, checking what remains before each call, as an
// interpreter would, and stops with its own answer before the guard page.
Deep(n: int, min: uint): int {
    if StackRemaining() < min {
        return n
    }
    var pad: [64]u64 = [0; 64]
    pad[n % 64] = Trunc[u64](n)
    return Deep(n + 1, min) + Trunc[int](pad[n % 64]) - n
}

test "Runtime a recursion that checks StackRemaining ends before the guard page" (sys: &System) {
    scope s := sys.io.Scope() {
        s.go DeepTask()
    }
}

DeepTask() {
    depth := Deep(0, 64 << 10)
    // 1 MiB, less 64 KiB kept, at roughly 0.6 KiB a frame: hundreds deep.
    Assert(depth > 100)
    Assert(StackRemaining() > 64 << 10)
}

// Bump runs on the blocking pool: it sleeps the pool thread briefly, as a
// blocking foreign call would, and adds to the state.
Bump(x: int): int {
    // A pool thread has no task slot: f runs off the carriers.
    Assert(raw.IsNull(rt.CurrentSlot()))
    sys.SleepNanos(1_000_000)
    return x + 1
}

test "Runtime Blocking runs f off the carriers and returns its state" (sys: &System) {
    got := atomic.Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        for _ in 0..8 {
            s.go BlockingTask(sys.io, &got)
        }
    }
    Assert(got.Load(atomic.SeqCst) == 8 * 42)
    v := Blocking(sys.io, 1, Bump) else {
        Assert(false)
        0
    }
    Assert(v == 2)
}

BlockingTask(io: Io, got: &atomic.Atomic[i64]) {
    v := Blocking(io, 41, Bump) else {
        return
    }
    _ = got.FetchAdd(i64(v), atomic.Relaxed)
}

test "Runtime Blocking on a cancelled scope is Canceled before f runs" (sys: &System) {
    ran := atomic.Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        s.Cancel()
        s.go CanceledTask(sys.io, &ran)
    }
    Assert(ran.Load(atomic.SeqCst) == 1)
}

CanceledTask(io: Io, ran: &atomic.Atomic[i64]) {
    _ = Blocking(io, 0, Bump) else {
        _ = ran.FetchAdd(1, atomic.Relaxed)
        return
    }
}

// WakeLater wakes w after the waiter has had time to park.
WakeLater(io: Io, w: &Waker) {
    for _ in 0..64 {
        io.Yield() else {}
    }
    w.Wake()
}

test "Runtime a Waker wakes a waiting task, and a Wake before the Wait is kept" (sys: &System) {
    w := Waker.New()
    scope s := sys.io.Scope() {
        s.go WakeLater(sys.io, &w)
        w.Wait(sys.io) else {
            Assert(false)
        }
    }
    w.Wake()
    w.Wait(sys.io) else {
        Assert(false)
    }
}

// ForeignWake is a thread sys.ThreadStart made, not a carrier: it wakes the
// Waker at arg, then ends.
ForeignWake(arg: *u8) {
    sys.SleepNanos(20_000_000)
    unsafe "arg is the Waker of the test below, which waits for this wake before it returns" {
        (*raw.PtrCast[u8, Waker](arg)).Wake()
    }
}

test "Runtime a Waker is woken from a foreign thread" (env: &System) {
    w := Waker.New()
    var arg := raw.Null[u8]()
    unsafe "the thread gets w's address; the test waits for its wake before w goes out of scope" {
        arg = raw.PtrCast[Waker, u8](raw.Of(&w))
    }
    r := sys.ThreadStart(ForeignWake, arg, 1 << 16)
    Assert(r.err == core.SysErr.None)
    w.Wait(env.io) else {
        Assert(false)
    }
}

test "Runtime PanicInfo is a plain record of C ABI fields" {
    p := PanicInfo(code: 16, message: "suspended in an extern call", file: "a.mk", line: 3, column: 7)
    Assert(p.code == 16 && p.line == 3 && p.column == 7 && p.file == "a.mk")
}

// OffTaskWait runs on a thread sys.ThreadStart made, which is no task: an
// Atomic.Wait there through the native Io (at arg) is panic 16,
// as task.WaitWord off a task stack is; the process ends with exit 101.
var OffTaskWord: atomic.Atomic[u32] = atomic.Atomic[u32].New(0)

OffTaskWait(arg: *u8) {
    unsafe "arg is the test's Io, in a frame that never returns before the panic" {
        io := raw.Read(raw.PtrCast[u8, Io](arg))
        OffTaskWord.Wait(io, 0) else {}
    }
}

test "Runtime Atomic.Wait off a task is panic 16 [exit 101: suspend in callback]" (env: &System) {
    io := env.io
    var arg := raw.Null[u8]()
    unsafe "the thread reads io once; this frame waits for the panic" {
        arg = raw.PtrCast[Io, u8](raw.Of(&io))
    }
    r := sys.ThreadStart(OffTaskWait, arg, 1 << 16)
    Assert(r.err == core.SysErr.None)
    for _ in 0..1000 {
        sys.SleepNanos(10_000_000)
    }
}

// SlowBump holds a pool thread 100 ms, as a blocking foreign call would.
SlowBump(x: int): int {
    sys.SleepNanos(100_000_000)
    return x + 1
}

SlowTask(io: Io, ran: &atomic.Atomic[i64], cut: &atomic.Atomic[i64]) {
    v := Blocking(io, 1, SlowBump) else {
        _ = cut.FetchAdd(1, atomic.Relaxed)
        return
    }
    Assert(v == 2)
    _ = ran.FetchAdd(1, atomic.Relaxed)
}

test "Runtime a cancel takes a queued Blocking job out unrun, and one that runs finishes" (sys: &System) {
    ran := atomic.Atomic[i64].New(0)
    cut := atomic.Atomic[i64].New(0)
    scope s := sys.io.Scope() {
        // More jobs than the pool's 64 threads, so some wait in its queue.
        for _ in 0..160 {
            s.go SlowTask(sys.io, &ran, &cut)
        }
        sys.io.Sleep(core.DurationOf(30_000_000)) else {}
        s.Cancel()
    }
    Assert(ran.Load(atomic.SeqCst) + cut.Load(atomic.SeqCst) == 160)
    Assert(ran.Load(atomic.SeqCst) >= 1)
    Assert(cut.Load(atomic.SeqCst) >= 1)
}

// GrowList is a Blocking f over a linear state: it hands the list back.
GrowList(xs: HeapList[int]): HeapList[int] {
    return xs
}

// On Canceled the caller still owns its state: a linear HeapList comes
// back on the error edge, to be freed; on success it is consumed and f's
// result is the caller's (std/std-rt_blocking_test.go checks that a use
// after success is refused).
test "Runtime Blocking's Canceled leaves a linear state with the caller" (sys: &System) {
    var got := 0
    xs := HeapList[int].New(sys.heap.Alloc())
    scope s := sys.io.Scope() {
        s.Cancel()
        ys := Blocking(sys.io, xs, GrowList) else {
            xs.Free(sys.heap.Alloc())
            got = 1
            return
        }
        ys.Free(sys.heap.Alloc())
    }
    Assert(got == 1)
}

test "Runtime Blocking over a linear state returns f's result" (sys: &System) {
    var xs := HeapList[int].New(sys.heap.Alloc())
    xs.Push(sys.heap.Alloc(), 7) else {
        Assert(false)
    }
    ys := Blocking(sys.io, xs, GrowList) else {
        xs.Free(sys.heap.Alloc())
        Assert(false)
        return
    }
    Assert(ys.Len() == 1 && ys[0] == 7)
    ys.Free(sys.heap.Alloc())
}

// FgRandom is a foreign table's random entry: it counts its calls in the
// u64 its Foreign's ctx points at and fills buf with 7s.
FgRandom(io: Io, buf: mut []u8): core.IoResult {
    unsafe "ctx is the test's counter, live until the Foreign is released" {
        p := raw.PtrCast[u8, u64](ForeignContext(io))
        raw.Write(p, raw.Read(p) + 1)
    }
    for i in 0..buf.Len() {
        buf[i] = 7
    }
    return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
}

test "Runtime ForeignIo routes an Io's entries to a program's own table, and its Io is Canceled after Release" {
    var count: u64 = 0
    var t := core.InertIo
    t.random = FgRandom
    unsafe "count and t live until Release below" {
        fg := ForeignIo(raw.PtrCast[u64, u8](raw.OfMut(mut count)), &t)
        io := fg.Io()
        var buf: [4]u8 = [0; 4]
        io.Random(mut buf[..]) else {
            Assert(false)
        }
        Assert(buf[0] == 7 && buf[3] == 7)
        io.Random(mut buf[..]) else {
            Assert(false)
        }
        Assert(count == 2)
        Assert(raw.Addr(ForeignContext(io)) == raw.Addr(raw.OfMut(mut count)))
        fg.Release()
        var cut := false
        io.Random(mut buf[..]) else {
            cut = true
        }
        Assert(cut)
        Assert(count == 2)
        Assert(raw.IsNull(ForeignContext(io)))
    }
}

test "Runtime 300 Foreigns made and released in turn reuse the registry's slots" {
    var count: u64 = 0
    var t := core.InertIo
    t.random = FgRandom
    unsafe "count and t outlive every Foreign below" {
        for _ in 0..300 {
            fg := ForeignIo(raw.PtrCast[u64, u8](raw.OfMut(mut count)), &t)
            var b: [1]u8 = [0]
            fg.Io().Random(mut b[..]) else {
                Assert(false)
            }
            fg.Release()
        }
    }
    Assert(count == 300)
}

// JoinWork is a thread's whole life for the join test: it sleeps, then
// stores 1 in the word at arg.
JoinWork(arg: *u8) {
    sys.SleepNanos(30_000_000)
    unsafe "arg is the test's word, live until the join returns" {
        (*raw.PtrCast[u8, atomic.Atomic[u32]](arg)).Store(1, atomic.Release)
    }
}

// JoinOn is Blocking's f for the join: sys.ThreadJoin blocks a pool thread,
// not a carrier.
JoinOn(h: i64): i64 {
    sys.ThreadJoin(h)
    return h
}

test "Runtime sys.ThreadJoin returns once the thread has ended, through the blocking pool" (env: &System) {
    done := atomic.Atomic[u32].New(0)
    for _ in 0..4 {
        done.Store(0, atomic.Relaxed)
        var arg := raw.Null[u8]()
        unsafe "the thread gets done's address; the join below outlives its use" {
            arg = raw.PtrCast[atomic.Atomic[u32], u8](raw.Of(&done))
        }
        r := sys.ThreadStartJoin(JoinWork, arg, 1 << 16)
        Assert(r.err == core.SysErr.None)
        _ = Blocking(env.io, r.value, JoinOn) else {
            Assert(false)
            0
        }
        Assert(done.Load(atomic.Acquire) == 1)
    }
}

// A joined Linux thread's 64 KiB alternate signal
// stack is unmapped by the join. MappedBytes sums the sizes of the
// process's mappings in /proc/self/maps (Linux; 0 elsewhere), read with a
// positional read per 64 KiB.
MappedBytes(): u64 {
    if Target.os != Os.Linux {
        return 0
    }
    o := sys.OpenFile("/proc/self/maps", 0, false)
    if o.err != core.SysErr.None {
        return 0
    }
    var buf: [65536]u8 = [0; 65536]
    var total: u64 = 0
    var lo: u64 = 0
    var cur: u64 = 0
    var field := 0
    var pos: i64 = 0
    for {
        r := sys.ReadSync(Trunc[uint](o.value), mut buf[..], pos)
        if r.err != core.SysErr.None || r.value <= 0 {
            break
        }
        n := Trunc[int](r.value)
        pos += r.value
        for i in 0..n {
            c := buf[i]
            if c == 10 {
                field = 0
                cur = 0
            } else if field == 0 && c == 45 {
                lo = cur
                cur = 0
                field = 1
            } else if field == 1 && c == 32 {
                total += cur - lo
                field = 2
            } else if field < 2 {
                d: u64 = if c >= 97 { u64(c) - 87 } else { u64(c) - 48 }
                cur = cur * 16 + d
            }
        }
    }
    _ = sys.Close(Trunc[uint](o.value))
    return total
}

JoinQuick(arg: *u8) {
}

test "Runtime 128 joined threads give back their stacks and alternate signal stacks" (env: &System) {
    // Four joins first, so the blocking pool's threads (their stacks and
    // alternate stacks) exist before the first sample.
    for _ in 0..4 {
        r := sys.ThreadStartJoin(JoinQuick, raw.Null[u8](), 1 << 16)
        Assert(r.err == core.SysErr.None)
        _ = Blocking(env.io, r.value, JoinOn) else {
            Assert(false)
            0
        }
    }
    before := MappedBytes()
    for _ in 0..128 {
        r := sys.ThreadStartJoin(JoinQuick, raw.Null[u8](), 1 << 16)
        Assert(r.err == core.SysErr.None)
        _ = Blocking(env.io, r.value, JoinOn) else {
            Assert(false)
            0
        }
    }
    after := MappedBytes()
    // A leak of the alternate stacks would be 128 x 64 KiB = 8 MiB.
    Assert(after < before + (4 << 20))
}

// Dedicated and Thread. Each test
// block runs in a child process of its own, so the globals below are one
// block's. Run at the default carriers and at MK_CARRIERS=1 and 16.

// Tid and NowNs are std.sys's, for test blocks whose sys is the System.
Tid(): u64 {
    return sys.ThreadId()
}

NowNs(): i64 {
    return sys.Now()
}

// The dedicated carrier's thread id, as the first of its tasks saw it, and
// the number of its tasks' checks that saw another.
var PinTid: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)
var PinBad: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)
var PinRuns: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)
// Shared tasks that ran on the dedicated carrier's thread.
var PinStolen: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)

// PinCheck records the thread a dedicated task runs on: the first sets
// PinTid, every other must match it.
PinCheck() {
    id := sys.ThreadId()
    r := PinTid.CompareExchange(0, id, atomic.SeqCst, atomic.SeqCst)
    if !r.ok && r.value != id {
        _ = PinBad.FetchAdd(1, atomic.SeqCst)
    }
    _ = PinRuns.FetchAdd(1, atomic.SeqCst)
}

// PinWorker checks its thread around yields and short sleeps (its own
// heap's timers) n times.
PinWorker(io: Io, n: int) {
    for k in 0..n {
        PinCheck()
        if k % 4 == 0 {
            io.Sleep(core.DurationOf(20_000)) else {}
        } else {
            io.Yield() else {}
        }
    }
    PinCheck()
}

// PinNest spawns from the dedicated Io it was given: its tasks stay there.
PinNest(io: Io) {
    PinCheck()
    scope s := io.Scope() {
        for _ in 0..4 {
            s.go PinWorker(io, 6)
        }
    }
    PinCheck()
}

// SharedWorker runs on the shared carriers and counts a run on the
// dedicated carrier's thread.
SharedWorker(io: Io, n: int) {
    for _ in 0..n {
        t := PinTid.Load(atomic.SeqCst)
        if t != 0 && sys.ThreadId() == t {
            _ = PinStolen.FetchAdd(1, atomic.SeqCst)
        }
        io.Yield() else {}
    }
}

test "Runtime Dedicated: a dedicated carrier's tasks, and theirs, report one thread id, and no shared task runs there" (sys: &System) {
    d := Dedicated(sys.io)
    scope sh := sys.io.Scope() {
        for _ in 0..8 {
            sh.go SharedWorker(sys.io, 200)
        }
        scope s := d.Scope() {
            for _ in 0..16 {
                s.go PinWorker(d, 24)
            }
            s.go PinNest(d)
        }
    }
    Assert(PinBad.Load(atomic.SeqCst) == 0)
    Assert(PinRuns.Load(atomic.SeqCst) == 16 * 25 + 2 + 4 * 7)
    Assert(PinTid.Load(atomic.SeqCst) != 0)
    Assert(PinStolen.Load(atomic.SeqCst) == 0)
    // Main runs on the shared carriers, never on the dedicated thread.
    Assert(Tid() != PinTid.Load(atomic.SeqCst))
}

// PinSpin computes without suspending for about ms milliseconds.
PinSpin(ms: i64) {
    PinCheck()
    t0 := sys.Now()
    for sys.Now() - t0 < ms * 1_000_000 {
    }
    PinCheck()
}

// A scope's deadline armed by a task on a dedicated carrier fires while
// another task there computes without suspending: it goes to a heap the
// shared carriers fire (RT8-Dl-Busy's shape, HeapHere).
var PinExpired: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)

PinDeadline(io: Io, shared: Io) {
    scope s := shared.Scope() {
        s.CancelAfter(core.DurationOf(5_000_000))
        s.go SharedWait(shared)
    }
    _ = PinExpired.FetchAdd(1, atomic.SeqCst)
}

// SharedWait waits on the shared carriers until its scope is cancelled.
SharedWait(io: Io) {
    io.Sleep(core.DurationOf(60_000_000_000)) else {}
}

test "Runtime Dedicated: a deadline armed on a dedicated carrier fires while a task there computes" (sys: &System) {
    d := Dedicated(sys.io)
    t0 := NowNs()
    scope s := d.Scope() {
        s.go PinDeadline(d, sys.io)
        s.go PinSpin(300)
    }
    Assert(PinExpired.Load(atomic.SeqCst) == 1)
    Assert(PinBad.Load(atomic.SeqCst) == 0)
    // The deadline's scope ended within the spin, not after a minute.
    Assert(NowNs() - t0 < 30_000_000_000)
}

test "Runtime Dedicated of a Sim's or another non-native Io is that Io" (sys: &System) {
    unsafe "a ForeignIo over the inert table, released below" {
        f := ForeignIo(raw.Null[u8](), &InertTable)
        io := f.Io()
        d := Dedicated(io)
        Assert(core.IoFlags(d) == core.IoFlags(io))
        f.Release()
    }
}

// Dedicated of a NoCancel Io keeps the mask and pins: its tasks report one
// thread id. A pinned Io is no Foreign's (ForeignContext checks the table).
test "Runtime Dedicated of a NoCancel Io is dedicated and stays masked, and is no Foreign's" (sys: &System) {
    d := Dedicated(sys.io.NoCancel())
    Assert((core.IoFlags(d) & 1) != 0)
    unsafe "ForeignContext is unsafe only" {
        Assert(raw.IsNull(ForeignContext(d)))
    }
    scope s := d.Scope() {
        for _ in 0..8 {
            s.go PinWorker(d, 6)
        }
    }
    Assert(PinBad.Load(atomic.SeqCst) == 0)
    Assert(PinRuns.Load(atomic.SeqCst) == 8 * 7)
}

// Thread's root and its children record what they saw.
var ThrTid: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)
var ThrBad: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)
var ThrSum: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)

ThrChild(io: Io, j: int, tid: u64) {
    for _ in 0..8 {
        if sys.ThreadId() != tid {
            _ = ThrBad.FetchAdd(1, atomic.SeqCst)
        }
        io.Yield() else {}
    }
    io.Sleep(core.DurationOf(100_000)) else {}
    if sys.ThreadId() != tid {
        _ = ThrBad.FetchAdd(1, atomic.SeqCst)
    }
    _ = ThrSum.FetchAdd(Trunc[u64](j), atomic.SeqCst)
}

// ThreadBody checks that it and the tasks it spawns from its Io stay on
// its thread, whose id ThrTid keeps (the last of several at once).
ThreadBody(io: Io) {
    tid := sys.ThreadId()
    ThrTid.Store(tid, atomic.SeqCst)
    scope s := io.Scope() {
        for j in 0..8 {
            s.go ThrChild(io, j, tid)
        }
    }
    io.Sleep(core.DurationOf(1_000_000)) else {}
    if sys.ThreadId() != tid {
        _ = ThrBad.FetchAdd(1, atomic.SeqCst)
    }
}

// JoinAll joins j, retrying a cancelled Join with a NoCancel Io.
JoinAll(j: Joined, io: Io) {
    j.Join(io) else {
        JoinAll(j, io.NoCancel())
    }
}

test "Runtime Thread runs f and its tasks on a carrier of its own, and Join waits for them" (sys: &System) {
    j := Thread(sys.io, ThreadBody)
    JoinAll(j, sys.io)
    Assert(ThrBad.Load(atomic.SeqCst) == 0)
    Assert(ThrSum.Load(atomic.SeqCst) == 28)
    Assert(ThrTid.Load(atomic.SeqCst) != 0 && ThrTid.Load(atomic.SeqCst) != Tid())
}

test "Runtime Thread: 32 threads one after another and 8 at once each join, so records are reused" (sys: &System) {
    for _ in 0..32 {
        JoinAll(Thread(sys.io, ThreadBody), sys.io)
    }
    var a := Thread(sys.io, ThreadBody)
    var b := Thread(sys.io, ThreadBody)
    var c := Thread(sys.io, ThreadBody)
    var d := Thread(sys.io, ThreadBody)
    var e := Thread(sys.io, ThreadBody)
    var f := Thread(sys.io, ThreadBody)
    var g := Thread(sys.io, ThreadBody)
    var h := Thread(sys.io, ThreadBody)
    JoinAll(h, sys.io)
    JoinAll(g, sys.io)
    JoinAll(f, sys.io)
    JoinAll(e, sys.io)
    JoinAll(d, sys.io)
    JoinAll(c, sys.io)
    JoinAll(b, sys.io)
    JoinAll(a, sys.io)
    Assert(ThrBad.Load(atomic.SeqCst) == 0)
    Assert(ThrSum.Load(atomic.SeqCst) == 40 * 28)
}

// Gate holds HoldBody until a test opens it.
var Gate: atomic.Atomic[u32] = atomic.Atomic[u32].New(0)
var Cancels: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)

HoldBody(io: Io) {
    for Gate.Load(atomic.SeqCst) == 0 {
        Gate.Wait(io, 0) else {}
    }
}

// TryJoin joins j in a scope that cancel cancels at once, or else whose
// deadline passes after 20 ms: Canceled, the Joined kept; then the gate
// opens and a NoCancel Join waits for the end.
TryJoin(j: Joined, io: Io, cancel: bool) {
    scope s := io.Scope() {
        if cancel {
            s.Cancel()
        } else {
            s.CancelAfter(core.DurationOf(20_000_000))
        }
        j.Join(io) else {
            _ = Cancels.FetchAdd(1, atomic.SeqCst)
            Gate.Store(1, atomic.SeqCst)
            Gate.Wake(true)
            JoinAll(j, io.NoCancel())
        }
    }
}

test "Runtime Join is Canceled by its scope's cancel, keeps the Joined, and a later Join waits" (sys: &System) {
    TryJoin(Thread(sys.io, HoldBody), sys.io, true)
    Assert(Cancels.Load(atomic.SeqCst) == 1)
}

test "Runtime Join is Canceled past its scope's deadline, keeps the Joined, and a later Join waits" (sys: &System) {
    t0 := NowNs()
    TryJoin(Thread(sys.io, HoldBody), sys.io, false)
    Assert(Cancels.Load(atomic.SeqCst) == 1)
    Assert(NowNs() - t0 >= 20_000_000)
}

// KeepIo keeps the two words of the Io a Thread's f was given.
var KeptTable: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)
var KeptFlags: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)

KeepIo(io: Io) {
    unsafe "an Io is two words: its table's address, then its flags" {
        p := raw.PtrCast[Io, u64](raw.Of(&io))
        KeptTable.Store(raw.Read(p), atomic.SeqCst)
        KeptFlags.Store(raw.Read(raw.Offset(p, 1)), atomic.SeqCst)
    }
}

Nothing() {
}

test "Runtime a spawn through the Io of a Thread that has ended panics [exit 101: has ended]" (sys: &System) {
    JoinAll(Thread(sys.io, KeepIo), sys.io)
    var stale := sys.io
    unsafe "the two words of the Io the ended Thread's f had" {
        p := raw.PtrCast[Io, u64](raw.OfMut(mut stale))
        raw.Write(p, KeptTable.Load(atomic.SeqCst))
        raw.Write(raw.Offset(p, 1), KeptFlags.Load(atomic.SeqCst))
    }
    scope s := stale.Scope() {
        s.go Nothing()
    }
}

test "Runtime Dedicated: 600 tasks at once overflow its deque into its own inbox, never the global queue" (sys: &System) {
    d := Dedicated(sys.io)
    scope sh := sys.io.Scope() {
        for _ in 0..4 {
            sh.go SharedWorker(sys.io, 400)
        }
        scope s := d.Scope() {
            for _ in 0..600 {
                s.go PinWorker(d, 2)
            }
        }
    }
    Assert(PinBad.Load(atomic.SeqCst) == 0)
    Assert(PinRuns.Load(atomic.SeqCst) == 600 * 3)
    Assert(PinStolen.Load(atomic.SeqCst) == 0)
}

// RanWith keeps the flags of the Io a Thread's f got.
var RanWith: atomic.Atomic[u64] = atomic.Atomic[u64].New(0)

RanHere(io: Io) {
    RanWith.Store(Trunc[u64](core.IoFlags(io)) | 1 << 63, atomic.SeqCst)
}

test "Runtime Thread with a non-native Io runs f on the calling task with that Io, and Join returns at once" (sys: &System) {
    unsafe "a ForeignIo over the inert table, released below" {
        f := ForeignIo(raw.Null[u8](), &InertTable)
        io := f.Io()
        j := Thread(io, RanHere)
        Assert(RanWith.Load(atomic.SeqCst) == (Trunc[u64](core.IoFlags(io)) | 1 << 63))
        JoinAll(j, io)
        f.Release()
    }
}

// A NoCancel copy of a Thread's Io is pinned too: tasks spawned from it run
// on the Thread's carrier (while the pin was in the mask-depth bits they
// ran on the shared carriers).
NoCancelBody(io: Io) {
    tid := sys.ThreadId()
    ThrTid.Store(tid, atomic.SeqCst)
    scope s := io.NoCancel().Scope() {
        for j in 0..8 {
            s.go ThrChild(io, j, tid)
        }
    }
}

test "Runtime a NoCancel copy of a Thread's Io spawns on the Thread's carrier" (sys: &System) {
    JoinAll(Thread(sys.io, NoCancelBody), sys.io)
    Assert(ThrBad.Load(atomic.SeqCst) == 0)
    Assert(ThrSum.Load(atomic.SeqCst) == 28)
}
