// Why: a scope deadline must end every kind of wait under it, exactly once
// and with the right cause, and a NoCancel boundary must keep it out.
// What: the RT8-Dl-*, RT8-Mask-* and RT8-Net-* blocks, with helpers such as
// DlSlept, DlWord, DlCanceller, DlDialOut and DlNotRun.
// How: kind 1 blocks, run by the scheduler harness, and at 1 and 16
// carriers by std/rt_deadline_test.go. Bounds are loose (a lost deadline
// shows as a 10 s wait against a 5 s bound, or the watchdog), so the blocks
// are not timing tests.

import std.core
import std.raw
import std.rt
import std.sys

const DlMs: i64 = 1_000_000
const DlLong: i64 = 10_000_000_000
const DlBound: i64 = 5_000_000_000

// DlSlept sleeps 10 s; out is 1 when the sleep was Canceled.
DlSlept(io: Io, out: &raw.Atomic[u32]) {
    io.Sleep(core.DurationOf(DlLong)) else {
        out.Store(1)
        return
    }
}

// DlWord waits cancellably on a word nobody changes; out 1 on Canceled.
DlWord(io: Io, w: &raw.Atomic[u32], out: &raw.Atomic[u32]) {
    for {
        if WaitWord(WordOf(w), 0, Cancellable(io)) == core.SysErr.Canceled {
            out.Store(1)
            return
        }
    }
}

// DlLock locks a mutex another holds; out 1 on Canceled.
DlLock(io: Io, m: &Mutex[bool], out: &raw.Atomic[u32]) {
    var g := m.Lock(io) else {
        out.Store(1)
        return
    }
    g.Unlock()
}

// DlRecv receives from an empty channel; out 1 on Canceled.
DlRecv(io: Io, c: &Chan[int], out: &raw.Atomic[u32]) {
    _ = c.Recv(io) else {
        out.Store(1)
        return
    }
}

// DlSend sends to a full channel; out 1 on Canceled.
DlSend(io: Io, c: &Chan[int], out: &raw.Atomic[u32]) {
    c.Send(io, 7) else {
        out.Store(1)
        return
    }
}

// DlAccept accepts on a listener nobody dials; out 1 on Canceled.
DlAccept(io: Io, l: uint, out: &raw.Atomic[u32]) {
    a := NativeAccept(io, l)
    if a.err == core.SysErr.Canceled {
        out.Store(1)
        return
    }
    if a.err == core.SysErr.None {
        rt.Abandon(Trunc[uint](a.value))
    }
}

// DlRead reads from a connected socket whose peer sends nothing; out 1 on
// Canceled.
DlRead(io: Io, s: uint, out: &raw.Atomic[u32]) {
    var buf: [16]u8 = [0; 16]
    r := NativeRecv(io, s, mut buf[..])
    if r.err == core.SysErr.Canceled {
        out.Store(1)
    }
}

test "RT8-Dl-Sleep a deadline cancels the body's sleep and a task's, and Expired says why" (sys: &System) {
    out := raw.Atomic[u32].New(0)
    var own := false
    var exp := false
    t0 := BenchNow()
    scope s := sys.io.Scope() {
        Assert(!s.Expired())
        s.CancelAfter(core.DurationOf(30 * DlMs))
        s.go DlSlept(sys.io, &out)
        sys.io.Sleep(core.DurationOf(DlLong)) else {
            own = true
        }
        exp = s.Expired()
    }
    Assert(BenchNow() - t0 < DlBound)
    Assert(own && out.Load() == 1 && exp)
    Assert(core.CancelQuiet())
}

test "RT8-Dl-AwaitPoints a deadline ends each kind of cancellable wait: sleep, word, Mutex, Chan recv and send, accept, read" (sys: &System) {
    w := raw.Atomic[u32].New(0)
    m := Mutex[bool].New(false)
    empty := Chan[int].New(sys.arena.Alloc(), 1) else {
        Assert(false)
        return
    }
    full := Chan[int].New(sys.arena.Alloc(), 1) else {
        Assert(false)
        return
    }
    full.Send(sys.io, 1) else {
        Assert(false)
    }
    l := NListen(sys.io)
    lh := Trunc[uint](l.value)
    var ab: [32]u8 = [0; 32]
    d := NativeDial(sys.io, NAddr(mut ab, l.os))
    Assert(d.err == core.SysErr.None)
    a := NativeAccept(sys.io, lh)
    Assert(a.err == core.SysErr.None)
    o0 := raw.Atomic[u32].New(0)
    o1 := raw.Atomic[u32].New(0)
    o2 := raw.Atomic[u32].New(0)
    o3 := raw.Atomic[u32].New(0)
    o4 := raw.Atomic[u32].New(0)
    o5 := raw.Atomic[u32].New(0)
    o6 := raw.Atomic[u32].New(0)
    var g := m.Lock(sys.io) else {
        Assert(false)
        return
    }
    t0 := BenchNow()
    var exp := false
    scope s := sys.io.Scope() {
        s.CancelAfter(core.DurationOf(40 * DlMs))
        s.go DlSlept(sys.io, &o0)
        s.go DlWord(sys.io, &w, &o1)
        s.go DlLock(sys.io, &m, &o2)
        s.go DlRecv(sys.io, &empty, &o3)
        s.go DlSend(sys.io, &full, &o4)
        s.go DlAccept(sys.io, lh, &o5)
        s.go DlRead(sys.io, Trunc[uint](a.value), &o6)
        sys.io.Sleep(core.DurationOf(DlLong)) else {
            exp = s.Expired()
        }
    }
    g.Unlock()
    Assert(BenchNow() - t0 < DlBound)
    Assert(exp)
    Assert(o0.Load() == 1 && o1.Load() == 1 && o2.Load() == 1 && o3.Load() == 1)
    Assert(o4.Load() == 1 && o5.Load() == 1 && o6.Load() == 1)
    rt.Abandon(Trunc[uint](a.value))
    rt.Abandon(Trunc[uint](d.value))
    rt.Abandon(lh)
    empty.Free(sys.arena.Alloc())
    full.Free(sys.arena.Alloc())
}

test "RT8-Dl-Earliest only the earliest deadline counts, whichever is set first" (sys: &System) {
    for k in 0..2 {
        t0 := BenchNow()
        var cut := false
        scope s := sys.io.Scope() {
            if k == 0 {
                s.CancelAfter(core.DurationOf(DlLong * 6))
                s.CancelAfter(core.DurationOf(20 * DlMs))
            } else {
                s.CancelAfter(core.DurationOf(20 * DlMs))
                s.CancelAfter(core.DurationOf(DlLong * 6))
            }
            sys.io.Sleep(core.DurationOf(DlLong)) else {
                cut = true
            }
        }
        Assert(cut && BenchNow() - t0 < DlBound)
    }
}

test "RT8-Dl-Zero a zero or negative deadline cancels at once and expires" (sys: &System) {
    for k in 0..2 {
        var cut := false
        var exp := false
        scope s := sys.io.Scope() {
            s.CancelAfter(core.DurationOf(if k == 0 { 0 } else { -5 * DlMs }))
            exp = s.Expired()
            sys.io.Sleep(core.DurationOf(0)) else {
                cut = true
            }
            match s.Remaining() {
                some(r) {
                    Assert(r.Nanos() == 0)
                }
                none {
                    Assert(false)
                }
            }
        }
        Assert(cut && exp)
    }
}

test "RT8-Dl-CancelFirst a scope cancelled first never expires, before or after its deadline is set" (sys: &System) {
    for k in 0..2 {
        var exp := true
        // k 1 arms the deadline before the cancel: 500 ms is far past any
        // stall of this task between the two statements on a loaded
        // machine, which a short deadline would lose to.
        d: i64 = if k == 0 { 5 * DlMs } else { 500 * DlMs }
        scope outer := sys.io.Scope() {
            scope s := sys.io.Scope() {
                if k == 0 {
                    s.Cancel()
                    s.CancelAfter(core.DurationOf(d))
                } else {
                    s.CancelAfter(core.DurationOf(d))
                    s.Cancel()
                }
                // Past the deadline: a timer left armed would fire now.
                sys.io.NoCancel().Sleep(core.DurationOf(d + 25 * DlMs)) else {
                    Assert(false)
                }
                exp = s.Expired()
            }
            Assert(!outer.Expired())
        }
        // One assert line per leg, so a failure's line names its k.
        if k == 0 {
            Assert(!exp)
        } else {
            Assert(!exp)
        }
    }
}

test "RT8-Dl-Ancestor a deadline cancels nested scopes, which do not expire, and an ancestor's cancel disarms a nested deadline" (sys: &System) {
    inner := raw.Atomic[u32].New(0)
    var iexp := true
    var oexp := false
    scope o := sys.io.Scope() {
        o.CancelAfter(core.DurationOf(20 * DlMs))
        scope i := sys.io.Scope() {
            i.CancelAfter(core.DurationOf(DlLong))
            i.go DlSlept(sys.io, &inner)
            DlSlept(sys.io, &inner)
            iexp = i.Expired()
        }
        oexp = o.Expired()
    }
    Assert(inner.Load() == 1 && !iexp && oexp)
    // An ancestor's Cancel reaches a scope whose own deadline is armed: the
    // deadline is disarmed and never fires.
    var e2 := true
    scope p := sys.io.Scope() {
        scope q := sys.io.Scope() {
            q.CancelAfter(core.DurationOf(5 * DlMs))
            p.Cancel()
            sys.io.NoCancel().Sleep(core.DurationOf(20 * DlMs)) else {
                Assert(false)
            }
            e2 = q.Expired()
        }
    }
    Assert(!e2)
}

test "RT8-Dl-Remaining none without a deadline, the least over the chain, and none past a NoCancel boundary" (sys: &System) {
    scope s := sys.io.Scope() {
        Assert(s.Remaining().IsNone())
        s.CancelAfter(core.DurationOf(3_600_000_000_000))
        scope i := sys.io.Scope() {
            match i.Remaining() {
                some(r) {
                    Assert(r.Nanos() <= 3_600_000_000_000 && r.Nanos() > 3_500_000_000_000)
                }
                none {
                    Assert(false)
                }
            }
            i.CancelAfter(core.DurationOf(7_200_000_000_000))
            match i.Remaining() {
                some(r) {
                    Assert(r.Nanos() <= 3_600_000_000_000)
                }
                none {
                    Assert(false)
                }
            }
            q := sys.io.NoCancel()
            scope b := q.Scope() {
                Assert(b.Remaining().IsNone())
            }
        }
    }
}

// DlQuick is a task that ends at once.
DlQuick(io: Io, n: &raw.Atomic[u32]) {
    _ = n.Add(1)
}

test "RT8-Dl-ExitWindow a deadline firing as its scope exits, 20,000 rounds: Join waits it out" (sys: &System) {
    n := raw.Atomic[u32].New(0)
    for round in 0..20_000 {
        scope s := sys.io.Scope() {
            s.go DlQuick(sys.io, &n)
            // 0 to 49 us: before, during or after the task's end and the
            // body's exit.
            s.CancelAfter(core.DurationOf(Trunc[i64](round % 50) * 1_000))
            t0 := BenchNow()
            for BenchNow() - t0 < Trunc[i64](round % 37) * 1_000 {
            }
        }
    }
    Assert(n.Load() == 20_000)
    Assert(core.CancelQuiet())
}

// DlCanceller cancels s once start is set.
DlCanceller(s: &Scope, start: &raw.Atomic[u32]) {
    for start.Load() == 0 {
    }
    s.Cancel()
}

test "RT8-Dl-RaceCancel a Cancel racing the deadline: exactly one cause, stable while the scope lives" (sys: &System) {
    var expired := 0
    for round in 0..2_000 {
        start := raw.Atomic[u32].New(0)
        var a := false
        var b := false
        scope s := sys.io.Scope() {
            s.CancelAfter(core.DurationOf(Trunc[i64](round % 40) * 1_000))
            s.go DlCanceller(&s, &start)
            start.Store(1)
            sys.io.Sleep(core.DurationOf(DlLong)) else {}
            a = s.Expired()
            YieldNow()
            b = s.Expired()
        }
        Assert(a == b)
        if a {
            expired += 1
        }
    }
    // Both causes happen over the rounds is likely, not certain: not asserted.
    Assert(expired >= 0)
}

test "RT8-Mask-Before a NoCancel Io ignores the deadline of the scopes enclosing the call" (sys: &System) {
    var masked := true
    var exp := false
    scope s := sys.io.Scope() {
        s.CancelAfter(core.DurationOf(10 * DlMs))
        q := sys.io.NoCancel()
        q.Sleep(core.DurationOf(40 * DlMs)) else {
            masked = false
        }
        exp = s.Expired()
        // The unmasked Io is cancelled.
        var cut := false
        sys.io.Sleep(core.DurationOf(0)) else {
            cut = true
        }
        Assert(cut)
    }
    Assert(masked && exp)
}

test "RT8-Mask-After a scope opened from a NoCancel Io is a boundary its own deadline cancels" (sys: &System) {
    out := raw.Atomic[u32].New(0)
    var bexp := false
    var mine := false
    var outer_ok := true
    t0 := BenchNow()
    scope s := sys.io.Scope() {
        s.Cancel()
        q := sys.io.NoCancel()
        scope b := q.Scope() {
            // Not cancelled by s: the boundary copies no flag.
            q.Sleep(core.DurationOf(1_000)) else {
                outer_ok = false
            }
            b.CancelAfter(core.DurationOf(20 * DlMs))
            b.go DlSlept(q, &out)
            q.Sleep(core.DurationOf(DlLong)) else {
                mine = true
            }
            bexp = b.Expired()
        }
        // Back in s: q is masked again for s's own cancel.
        q.Sleep(core.DurationOf(1_000)) else {
            outer_ok = false
        }
    }
    Assert(BenchNow() - t0 < DlBound)
    Assert(outer_ok && mine && bexp && out.Load() == 1)
    Assert(core.CancelQuiet())
}

// DlMaskedLate opens a scope from q, a masked Io taken in scopes that
// have ended, and sleeps on q under a 20 ms deadline; out 1 when the sleep
// was Canceled and the scope Expired.
DlMaskedLate(q: Io, out: &raw.Atomic[u32]) {
    var cut := false
    var exp := false
    scope b := q.Scope() {
        b.CancelAfter(core.DurationOf(20 * DlMs))
        q.Sleep(core.DurationOf(DlLong)) else {
            cut = true
        }
        exp = b.Expired()
    }
    if cut && exp {
        out.Store(1)
    }
}

// DlMaskedFrom receives a masked Io on c and runs DlMaskedLate with it.
DlMaskedFrom(io: Io, c: &Chan[Io], out: &raw.Atomic[u32]) {
    q := c.Recv(io) else {
        return
    }
    DlMaskedLate(q, out)
}

test "RT8-Mask-Late a scope opened from a NoCancel Io after the scopes it was taken in ended is a boundary its own deadline cancels" (sys: &System) {
    // Taken two levels down, used at level 0 (Main's chain) and at level 1.
    var q := sys.io
    scope a := sys.io.Scope() {
        scope a2 := sys.io.Scope() {
            q = sys.io.NoCancel()
            Assert(!a2.Expired())
        }
        Assert(!a.Expired())
    }
    t0 := BenchNow()
    out := raw.Atomic[u32].New(0)
    DlMaskedLate(q, &out)
    Assert(out.Load() == 1)
    out.Store(0)
    scope s := sys.io.Scope() {
        DlMaskedLate(q, &out)
        Assert(!s.Expired())
    }
    Assert(out.Load() == 1)
    // Sent over a Chan to a task one level shallower than the NoCancel call.
    c := Chan[Io].New(sys.arena.Alloc(), 1) else {
        Assert(false)
        return
    }
    out.Store(0)
    scope s2 := sys.io.Scope() {
        s2.go DlMaskedFrom(sys.io, &c, &out)
        scope d := sys.io.Scope() {
            scope d2 := sys.io.Scope() {
                c.Send(sys.io, sys.io.NoCancel()) else {
                    Assert(false)
                }
                Assert(!d2.Expired())
            }
            Assert(!d.Expired())
        }
    }
    Assert(out.Load() == 1)
    Assert(BenchNow() - t0 < DlBound)
    Assert(core.CancelQuiet())
}

test "RT8-Mask-Nested a scope opened from a NoCancel Io under a boundary of that Io is linked: the boundary's deadline reaches it" (sys: &System) {
    out := raw.Atomic[u32].New(0)
    var exp := false
    var mine := false
    var left: i64 = -1
    t0 := BenchNow()
    scope s := sys.io.Scope() {
        s.Cancel()
        q := sys.io.NoCancel()
        scope b := q.Scope() {
            b.CancelAfter(core.DurationOf(20 * DlMs))
            scope n := q.Scope() {
                match n.Remaining() {
                    some(r) {
                        left = r.Nanos()
                    }
                    none {}
                }
                n.go DlSlept(q, &out)
                q.Sleep(core.DurationOf(DlLong)) else {
                    mine = true
                }
            }
            exp = b.Expired()
        }
    }
    Assert(BenchNow() - t0 < DlBound)
    Assert(out.Load() == 1 && mine && exp)
    Assert(left >= 0 && left <= 20 * DlMs)
    Assert(core.CancelQuiet())
}

test "RT8-Dl-ZeroAfterArmed a zero deadline after an armed one expires the scope and leaves no time remaining" (sys: &System) {
    for k in 0..2 {
        var exp := false
        var left: i64 = -1
        var inner: i64 = -1
        scope s := sys.io.Scope() {
            s.CancelAfter(core.DurationOf(DlLong))
            s.CancelAfter(core.DurationOf(if k == 0 { 0 } else { -5 * DlMs }))
            exp = s.Expired()
            match s.Remaining() {
                some(r) {
                    left = r.Nanos()
                }
                none {}
            }
            scope n := sys.io.Scope() {
                match n.Remaining() {
                    some(r) {
                        inner = r.Nanos()
                    }
                    none {}
                }
            }
        }
        Assert(exp && left == 0 && inner == 0)
    }
}

// DlPipeRead reads the pipe end h; out 1 when the read was Canceled.
DlPipeRead(io: Io, h: uint, out: &raw.Atomic[u32]) {
    var b: [16]u8 = [0; 16]
    if NativeRead(io, h, mut b[..], -1).err == core.SysErr.Canceled {
        out.Store(1)
    }
}

// DlFlood sends 64 MiB on s, which nobody reads; out 1 when a send was
// Canceled (the socket's buffers fill long before).
DlFlood(io: Io, s: uint, out: &raw.Atomic[u32]) {
    var buf: [65536]u8 = [7; 65536]
    for _ in 0..1024 {
        w := NativeSend(io, s, buf[..])
        if w.err == core.SysErr.Canceled {
            out.Store(1)
            return
        }
        if w.err != core.SysErr.None {
            return
        }
    }
}

// DlChildWait waits for the child h; out 1 when the wait was Canceled.
DlChildWait(io: Io, h: uint, job: uint, out: &raw.Atomic[u32]) {
    if rt.ChildWait(io, h, job).err == core.SysErr.Canceled {
        out.Store(1)
    }
}

test "RT8-Dl-AwaitPointsMore a deadline fired by a carrier's timer ends a pipe read, a send on a full socket and a child's wait; each is repeatable" (sys: &System) {
    io := sys.io
    // A pipe nobody writes yet.
    var e: [2]uint = [0; 2]
    Assert(rt.PipeOpen(io, mut e).err == core.SysErr.None)
    // A connection whose peer never reads.
    l := NListen(io)
    lh := Trunc[uint](l.value)
    var ab: [32]u8 = [0; 32]
    d := NativeDial(io, NAddr(mut ab, l.os))
    Assert(d.err == core.SysErr.None)
    a := NativeAccept(io, lh)
    Assert(a.err == core.SysErr.None)
    // A child that runs for 100 s.
    var cs := CSpec(core.IoStdNull, core.IoStdNull, core.IoStdNull)
    c := CLong(io, mut cs)
    Assert(c.err == core.SysErr.None)
    ch := Trunc[uint](c.value)
    o0 := raw.Atomic[u32].New(0)
    o1 := raw.Atomic[u32].New(0)
    o2 := raw.Atomic[u32].New(0)
    o3 := raw.Atomic[u32].New(0)
    t0 := BenchNow()
    var exp := false
    scope s := io.Scope() {
        s.CancelAfter(core.DurationOf(40 * DlMs))
        s.go DlPipeRead(io, e[0], &o0)
        s.go DlFlood(io, Trunc[uint](d.value), &o1)
        s.go DlChildWait(io, ch, cs.job, &o2)
        // The body itself waits for the child too.
        if rt.ChildWait(io, ch, cs.job).err == core.SysErr.Canceled {
            o3.Store(1)
        }
        exp = s.Expired()
    }
    Assert(BenchNow() - t0 < DlBound)
    Assert(exp && o0.Load() == 1 && o1.Load() == 1 && o2.Load() == 1 && o3.Load() == 1)
    Assert(core.CancelQuiet())
    // Repeatable: the pipe read gets bytes written after it was
    // cut, and the child, still running, answers a later wait.
    Assert(NativeWrite(io, e[1], "late".AsBytes(), -1).value == 4)
    var b: [16]u8 = [0; 16]
    Assert(NativeRead(io, e[0], mut b[..], -1).value == 4 && b[0] == 108)
    Assert(rt.ChildSignal(io, ch, false, cs.pid, cs.job, 9).err == core.SysErr.None)
    v := rt.ChildWait(io, ch, cs.job).value
    Assert(v == 1 || (!PWin() && v == -9))
    PClose(io, ch)
    PClose(io, e[0])
    PClose(io, e[1])
    rt.Abandon(Trunc[uint](d.value))
    rt.Abandon(Trunc[uint](a.value))
    rt.Abandon(lh)
}

// DlHoleNet is TEST-NET-1 (RFC 5737), which is never routed: on a host with
// a default route a dial to it is sent and never answered. It is the hole
// on Windows. There the OS resets a dial to a full loopback backlog at
// once, but Mk's dial does not return the reset for about 2 s (measured on
// Windows 11), so a full backlog is a hole there only for
// as long as that lasts; TEST-NET-1 depends on the host's route instead.
// On Linux the hole is a loopback listener whose accept queue is full: the
// kernel drops a SYN that finds the queue full (tcp_conn_request), so the
// dial waits for its first retransmission, 1 s later, and no host network
// is involved.
const DlHoleNet: str = "192.0.2.1:9"

// DlBackLog listens on a loopback port with a backlog of 1 (an accept
// queue of two on Linux) and accepts nothing; value is the handle and os
// the port, as NativeListen's.
DlBackLog(): core.IoResult {
    var a := sys.SockAddr(family: sys.Inet4, port: 0, addr: [0; 16], scope_id: 0)
    Assert(rt.ParseAddr("127.0.0.1:0", true, mut a))
    s := sys.SocketOpen(a.family, true)
    Assert(s.err == core.SysErr.None)
    sh := Trunc[uint](s.value)
    Assert(sys.SocketListen(sh, &a, 1).err == core.SysErr.None)
    var bound := a
    Assert(sys.SocketName(sh, mut bound).err == core.SysErr.None)
    h := rt.NewSocketHandle(sh, a.family)
    Assert(h != 0)
    return core.IoResult(value: Trunc[i64](h), err: core.SysErr.None, os: i64(bound.port))
}

// DlNotRun ends a block that could not test what it tests: it writes
// "NOT RUN: <why>", then the OS's code and the milliseconds the answer
// took, to standard error, and exits 77. That is a failure in mk test and
// in every harness, never a pass.
DlNotRun(why: str, os: i64, took: i64): never {
    rt.WriteErr("RT8-Dl-DialHole NOT RUN: ".AsBytes())
    rt.WriteErr(why.AsBytes())
    rt.WriteErr("\n".AsBytes())
    FNote("os", os)
    FNote("ms", took / DlMs)
    sys.Exit(77)
}

// DlDialOut dials addr; out 1 when the dial was Canceled, 2 when it
// connected (the socket is closed), 3 on any other answer.
DlDialOut(io: Io, addr: str, out: &raw.Atomic[u32]) {
    d := NativeDial(io, addr)
    if d.err == core.SysErr.Canceled {
        out.Store(1)
    } else if d.err == core.SysErr.None {
        rt.Abandon(Trunc[uint](d.value))
        out.Store(2)
    } else {
        out.Store(3)
    }
}

test "RT8-Dl-DialHole a deadline ends a dial nobody answers (Linux: a loopback listener whose accept queue is full; Windows: TEST-NET-1), from the body and from a task, twice; then a dial connects. A dial that fails before the deadline (no route, for example) ends it with exit 77 and NOT RUN, never a pass (R7)" (sys: &System) {
    io := sys.io
    win := PWin()
    var port: i64 = 0
    var bl: uint = 0
    if !win {
        b := DlBackLog()
        bl = Trunc[uint](b.value)
        port = b.os
    }
    var hb: [32]u8 = [0; 32]
    hole := if win { DlHoleNet } else { NAddr(mut hb, port) }
    // 1. From the body, until the deadline cuts a dial. On Linux the first
    // two dials connect and fill the queue: they run under a 2 s deadline,
    // which a loaded machine does not reach, so a cut always finds the
    // queue full. Later dials run under 40 ms. A cut dial must have waited
    // at least 20 ms (a dial the host answers takes about 1 ms). Any other
    // answer before the deadline is a dial the OS did not leave pending (no
    // route, ENETUNREACH, for example): NOT RUN. An answer after it means
    // the deadline did not end the dial: a failure. A dial the OS leaves
    // pending and the deadline cancels is a pass whatever the network would
    // have answered later (on Windows a reset that Mk's dial returns only
    // after about 2 s).
    var kept: [8]uint = [0; 8]
    var nk := 0
    var cut := false
    for k in 0..8 {
        after := if !win && k < 2 { 2_000 * DlMs } else { 40 * DlMs }
        var d := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
        var exp := false
        t0 := BenchNow()
        scope s := io.Scope() {
            s.CancelAfter(core.DurationOf(after))
            d = NativeDial(io, hole)
            exp = s.Expired()
        }
        took := BenchNow() - t0
        Assert(took < DlBound)
        if d.err == core.SysErr.Canceled {
            Assert(exp && took >= 20 * DlMs)
            cut = true
            break
        }
        if d.err == core.SysErr.None && !win {
            kept[nk] = Trunc[uint](d.value)
            nk += 1
            continue
        }
        Assert(took < after)
        if d.err == core.SysErr.None {
            rt.Abandon(Trunc[uint](d.value))
        }
        DlNotRun("the dial was answered before the deadline, not left pending", d.os, took)
    }
    if !cut {
        DlNotRun("no dial stayed unanswered in 8 tries", 0, 0)
    }
    Assert(core.CancelQuiet())
    // 2. Again, from a task and from the body at once.
    o0 := raw.Atomic[u32].New(0)
    o1 := raw.Atomic[u32].New(0)
    var exp := false
    t0 := BenchNow()
    scope s := io.Scope() {
        s.CancelAfter(core.DurationOf(40 * DlMs))
        s.go DlDialOut(io, hole, &o0)
        DlDialOut(io, hole, &o1)
        exp = s.Expired()
    }
    took := BenchNow() - t0
    Assert(took >= 20 * DlMs && took < DlBound)
    Assert(exp && o0.Load() == 1 && o1.Load() == 1)
    Assert(core.CancelQuiet())
    // 3. Repeatable: the cut dials left nothing behind, and a dial
    // connects afterwards. On Linux one accept makes room in the same
    // listener's queue; on Windows the dial goes to a loopback listener.
    var lb: [32]u8 = [0; 32]
    var lh: uint = 0
    var acc: uint = 0
    if win {
        l := NListen(io)
        lh = Trunc[uint](l.value)
        port = l.os
    } else {
        a := NativeAccept(io, bl)
        Assert(a.err == core.SysErr.None)
        acc = Trunc[uint](a.value)
    }
    target := if win { NAddr(mut lb, port) } else { hole }
    var d := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    scope s := io.Scope() {
        s.CancelAfter(core.DurationOf(2_000 * DlMs))
        d = NativeDial(io, target)
    }
    Assert(d.err == core.SysErr.None)
    if win {
        a := NativeAccept(io, lh)
        Assert(a.err == core.SysErr.None)
        acc = Trunc[uint](a.value)
        rt.Abandon(lh)
    } else {
        rt.Abandon(bl)
    }
    rt.Abandon(Trunc[uint](d.value))
    rt.Abandon(acc)
    for i in 0..nk {
        rt.Abandon(kept[i])
    }
    Assert(core.CancelQuiet())
}

// DlReader receives one byte on s into got.
DlReader(io: Io, s: uint, got: &raw.Atomic[u32]) {
    var buf: [1]u8 = [0; 1]
    r := NativeRecv(io, s, mut buf[..])
    if r.err == core.SysErr.None && r.value == 1 {
        got.Store(Trunc[u32](buf[0]))
    }
}

// DlWriter sends n bytes on s and stores 1 in done when all went.
DlWriter(io: Io, s: uint, n: int, done: &raw.Atomic[u32]) {
    var buf: [65536]u8 = [7; 65536]
    var left := n
    for left > 0 {
        k := if left < 65536 { left } else { 65536 }
        if !NSendAll(io, s, buf[0..k]) {
            return
        }
        left -= k
    }
    done.Store(1)
}

test "RT8-Net-ReadWrite one task waits to read a socket while another waits to write it" (sys: &System) {
    io := sys.io
    l := NListen(io)
    lh := Trunc[uint](l.value)
    var ab: [32]u8 = [0; 32]
    d := NativeDial(io, NAddr(mut ab, l.os))
    Assert(d.err == core.SysErr.None)
    a := NativeAccept(io, lh)
    Assert(a.err == core.SysErr.None)
    c := Trunc[uint](d.value)
    p := Trunc[uint](a.value)
    got := raw.Atomic[u32].New(0)
    done := raw.Atomic[u32].New(0)
    // 64 MiB is far past any loopback socket's buffers, so the writer waits.
    total := 64 << 20
    scope s := io.Scope() {
        s.go DlReader(io, c, &got)
        s.go DlWriter(io, c, total, &done)
        io.Sleep(core.DurationOf(50 * DlMs)) else {}
        Assert(got.Load() == 0 && done.Load() == 0)
        // The peer's byte ends the read while the write still waits.
        var one: [1]u8 = [9; 1]
        Assert(NSendAll(io, p, one[..]))
        t0 := BenchNow()
        for got.Load() == 0 && BenchNow() - t0 < DlBound {
            YieldNow()
        }
        Assert(got.Load() == 9)
        // Then the peer drains, and the write completes.
        var buf: [65536]u8 = [0; 65536]
        var seen := 0
        for seen < total {
            r := NativeRecv(io, p, mut buf[..])
            if r.err != core.SysErr.None || r.value <= 0 {
                break
            }
            seen += Trunc[int](r.value)
        }
        Assert(seen == total)
    }
    Assert(done.Load() == 1)
    rt.Abandon(c)
    rt.Abandon(p)
    rt.Abandon(lh)
}

test "RT8-Dl-ZeroAfterArm a deadline armed, then fired at once by a zero, while a carrier pops it, 20,000 rounds: Join waits for the popper" (sys: &System) {
    n := raw.Atomic[u32].New(0)
    for round in 0..20_000 {
        scope s := sys.io.Scope() {
            s.go DlQuick(sys.io, &n)
            s.CancelAfter(core.DurationOf(Trunc[i64](round % 5) * 1_000))
            t0 := BenchNow()
            for BenchNow() - t0 < Trunc[i64](round % 11) * 500 {
            }
            s.CancelAfter(core.DurationOf(0))
        }
    }
    Assert(n.Load() == 20_000)
    Assert(core.CancelQuiet())
}

test "RT8-Dl-Busy a deadline armed by a task that computes with no cancellation point fires on an idle carrier, so Expired turns true while it computes" (sys: &System) {
    var want: u32 = 0
    unsafe "the scheduler's state is set up before any test block runs" {
        want = (*TS()).want.Load()
    }
    // One carrier: the computing task holds it, and no other carrier can
    // fire the deadline until it reaches a schedule step.
    if want < 2 {
        return
    }
    for round in 0..20 {
        d := 2 * DlMs + i64(round) * 100_000
        // A round whose deadline fired before the loop's first check (the
        // task was preempted, or CancelAfter's wake took longer than d) saw
        // no transition while computing; it is tried again, at most 20
        // times, so the block cannot pass without seeing one.
        var seen := false
        for _ in 0..20 {
            t0 := BenchNow()
            var spins: u64 = 0
            var exp := false
            scope s := sys.io.Scope() {
                s.CancelAfter(core.DurationOf(d))
                // No cancellation point and no schedule step in this loop:
                // only another carrier, woken by the arming (OweArm,
                // WakeOne), can fire the deadline.
                for !s.Expired() {
                    spins +%= 1
                    if BenchNow() - t0 > DlBound {
                        break
                    }
                }
                exp = s.Expired()
            }
            took := BenchNow() - t0
            // One assert per half, so a failure's line names it: exp is
            // false when the loop gave up at DlBound before the deadline
            // fired.
            Assert(exp)
            Assert(took < DlBound)
            if spins > 0 {
                seen = true
                break
            }
            // Fired before the first check: never before its instant.
            Assert(took >= d)
        }
        Assert(seen)
    }
    Assert(core.CancelQuiet())
}

test "RT8-Dl-LimitHit a Canceled under a scope whose own deadline has passed is std's TimedOut whichever deadline fired first, and stays Canceled before that instant" (sys: &System) {
    var want: u32 = 0
    unsafe "the scheduler's state is set up before any test block runs" {
        want = (*TS()).want.Load()
    }
    // The scope's own deadline passes while the body computes with no
    // schedule step, then an ancestor's Cancel reaches the scope. At one
    // carrier nothing can fire the deadline first, so the scope does not
    // expire and only LimitPassed gives TimedOut; at more, an idle carrier
    // may fire it first (as in "RT8-Dl-Busy"), and Expired gives it.
    var passed := false
    var hit := false
    var other := false
    var exp := true
    scope o := sys.io.Scope() {
        scope i := sys.io.Scope() {
            i.CancelAfter(core.DurationOf(DlMs))
            t0 := BenchNow()
            for BenchNow() - t0 < 3 * DlMs {
            }
            o.Cancel()
            passed = LimitPassed(&i)
            hit = LimitHit(&i, true)
            // An error other than Canceled is TimedOut only when i expired.
            other = LimitHit(&i, false) == i.Expired()
            exp = i.Expired()
        }
    }
    Assert(passed && hit && other)
    if want < 2 {
        Assert(!exp)
    }
    // The ancestor's Cancel comes before the scope's own deadline: Canceled.
    scope p := sys.io.Scope() {
        scope q := sys.io.Scope() {
            q.CancelAfter(core.DurationOf(DlLong))
            p.Cancel()
            Assert(!LimitPassed(&q) && !LimitHit(&q, true))
        }
    }
    // The ancestor's deadline fires while the scope's own is far off:
    // Canceled; and a scope with no deadline of its own is never TimedOut.
    scope a := sys.io.Scope() {
        a.CancelAfter(core.DurationOf(DlMs))
        scope b := sys.io.Scope() {
            b.CancelAfter(core.DurationOf(DlLong))
            scope c := sys.io.Scope() {
                sys.io.NoCancel().Sleep(core.DurationOf(20 * DlMs)) else {
                    Assert(false)
                }
                Assert(a.Expired() && !b.Expired() && !c.Expired())
                Assert(!LimitPassed(&b) && !LimitHit(&b, true))
                Assert(!LimitPassed(&c) && !LimitHit(&c, true))
            }
        }
    }
    Assert(core.CancelQuiet())
}
