// Why: stack guards, stack bounds, Raise and the cancel after a resubmission
// need Mk code that drives std.sys directly.
// What: rtioProbe4, the Mk source of std/rtioprobe/probe4.mk.
// How: std/rt-io_stackguard_test.go writes it next to probe.mk to probe3.mk, whose
// helpers it uses; each case returns 0, or the number of the step that
// failed, except GuardKind and GuardMaps, which report what the kernel did.

package std

// rtioProbe4 is std/rtioprobe/probe4.mk of the copy.
const rtioProbe4 = `// A probe of std.sys, fourth file (std/rt-io_probe4_test.go).

import std.core
import std.raw
import std.sys

const GSlot: int = 16384

// GuardKind: 1 when GuardStack installs a light guard in a fresh
// read-write slot here, 0 when it is refused (Windows always; Linux before
// 6.13). The harness compares it with the kernel's version.
export GuardKind(): u32 {
    base := sys.Reserve(GSlot, GSlot)
    if raw.IsNull(base) {
        return 2
    }
    var k: u32 = 3
    if sys.CommitStack(base, GSlot) {
        k = 0
        if sys.GuardStack(base) {
            k = 1
        }
    }
    sys.Release(base, GSlot)
    return k
}

// GuardCheck: eight adjacent slots prepared as std/rt/region.mk does
// (CommitStack over the whole slot, then GuardStack on its lowest page,
// else GuardPage). Every slot gets the same answer; a refused GuardStack
// leaves the page read-write with its contents; the page above the guard
// is read-write in either form.
export GuardCheck(): u32 {
    base := sys.Reserve(8 * GSlot, GSlot)
    if raw.IsNull(base) {
        return 10
    }
    var code: u32 = 0
    var first: u32 = 2
    unsafe "the slots are this case's reservation" {
        for i in 0..8 {
            s := raw.Offset(base, i * GSlot)
            if !sys.CommitStack(s, GSlot) {
                code = 11
                break
            }
            mark: u8 = Trunc[u8](i + 1)
            raw.Write(s, mark)
            var gi: u32 = 0
            if sys.GuardStack(s) {
                gi = 1
            }
            if first == 2 {
                first = gi
            } else if first != gi {
                code = 12
                break
            }
            if gi == 0 {
                if raw.Read(s) != mark {
                    code = 13
                    break
                }
                if !sys.GuardPage(s) {
                    code = 14
                    break
                }
            }
            above := raw.Offset(s, 4096)
            raw.Write(above, mark)
            if raw.Read(above) != mark {
                code = 15
                break
            }
        }
    }
    sys.Release(base, 8 * GSlot)
    return code
}

// Maps is the number of lines of /proc/self/maps (the process's VMAs) on
// Linux; -1 elsewhere or when it cannot be read.
Maps(): i64 {
    if Target.os != Os.Linux {
        return -1
    }
    r := sys.OpenFile("/proc/self/maps", 0, false)
    if !Ok(r) {
        return -1
    }
    h := Trunc[uint](r.value)
    var buf: [4096]u8 = [0; 4096]
    var n: i64 = 0
    for {
        x := sys.ReadSync(h, mut buf[..], -1)
        if !Ok(x) {
            n = -1
            break
        }
        if x.value == 0 {
            break
        }
        for i in 0..Trunc[int](x.value) {
            if buf[i] == 10 {
                n += 1
            }
        }
    }
    _ = sys.Close(h)
    return n
}

// GuardMaps prepares eight adjacent slots as GuardCheck does and returns
// how many mappings that added (light guards: at most 1, the slots merge
// into one; the PROT_NONE form: two per slot), -1 where it is not counted.
export GuardMaps(): i64 {
    base := sys.Reserve(8 * GSlot, GSlot)
    if raw.IsNull(base) {
        return -2
    }
    before := Maps()
    unsafe "the slots are this case's reservation" {
        for i in 0..8 {
            s := raw.Offset(base, i * GSlot)
            if !sys.CommitStack(s, GSlot) {
                sys.Release(base, 8 * GSlot)
                return -3
            }
            if !sys.GuardStack(s) && !sys.GuardPage(s) {
                sys.Release(base, 8 * GSlot)
                return -4
            }
        }
    }
    after := Maps()
    sys.Release(base, 8 * GSlot)
    if before < 0 || after < 0 {
        return -1
    }
    return after - before
}

// BoundsThread checks ThreadStackBounds on a thread ThreadStart made: a
// local lies in [lo, hi), and hi - lo is the stack asked for (t0) on
// Linux, at least that on Windows (the reservation is rounded up).
BoundsThread(arg: *u8) {
    g := Sh(arg)
    (lo, hi) := sys.ThreadStackBounds()
    var local: u64 = 0
    at := raw.Addr(raw.Of(&local))
    unsafe "g is the case's page" {
        want := Trunc[uint]((*g).t0.Load())
        if lo == 0 || hi <= lo || at < lo || at >= hi {
            _ = (*g).bad.Add(1)
        } else if hi - lo < want || (Target.os == Os.Linux && hi - lo != want) {
            _ = (*g).bad.Add(1)
        }
        _ = (*g).exited.Add(1)
    }
}

// StackBounds (rt-sched's request; 3.3 sys_lo, sys_hi): Main's thread is
// (0, 0) on Linux (not made by ThreadStart) and its own stack on Windows;
// 16 threads started at once each find their own stack; on Linux 600
// threads one after another, more than the table's 512 entries, still
// do (an entry is given back when its thread ends).
export StackBounds(): u32 {
    (lo, hi) := sys.ThreadStackBounds()
    if Target.os == Os.Linux && (lo != 0 || hi != 0) {
        return 1
    }
    if Target.os != Os.Linux && (lo == 0 || hi <= lo) {
        return 2
    }
    page := NewPage()
    if raw.IsNull(page) {
        return 10
    }
    g := Sh(page)
    unsafe "g is the case's page; it is never released (threads may still be ending)" {
        (*g).t0.Store(262144)
        for _ in 0..16 {
            if !Ok(sys.ThreadStart(BoundsThread, page, 262144)) {
                return 11
            }
        }
        if !WaitWord(raw.Of(&(*g).exited), 16, 10_000) {
            return 3
        }
        if (*g).bad.Load() != 0 {
            return 4
        }
        if Target.os == Os.Linux {
            (*g).t0.Store(65536)
            for i in 0..600 {
                if !Ok(sys.ThreadStart(BoundsThread, page, 65536)) {
                    return 12
                }
                if !WaitWord(raw.Of(&(*g).exited), Trunc[u32](17 + i), 10_000) {
                    return 5
                }
            }
            if (*g).bad.Load() != 0 {
                return 6
            }
        }
    }
    return 0
}

// CountReaches waits up to 2 s for SignalCount(sig) to reach n.
CountReaches(sig: u32, n: u64): bool {
    deadline := sys.Now() + 2_000_000_000
    for sys.SignalCount(sig) < n {
        if sys.Now() > deadline {
            return false
        }
        sys.ThreadYield()
    }
    return true
}

// RaiseCase: Raise of a
// signal that is not watched is refused (and the process lives); a watched
// Interrupt and Terminate are delivered once each (Linux by kill, Windows
// by the console handler on the calling thread); Hangup and an unknown
// number are refused.
export RaiseCase(): u32 {
    if sys.Raise(1) {
        return 1
    }
    if !sys.SignalWatch(0) {
        return 2
    }
    c := sys.SignalCount(0)
    if !sys.Raise(0) {
        return 3
    }
    if !CountReaches(0, c + 1) {
        return 4
    }
    if sys.Raise(2) {
        return 5
    }
    if sys.Raise(1) {
        return 6
    }
    if !sys.SignalWatch(1) {
        return 7
    }
    tc := sys.SignalCount(1)
    if !sys.Raise(1) {
        return 8
    }
    if !CountReaches(1, tc + 1) {
        return 9
    }
    if sys.Raise(3) {
        return 10
    }
    Spin(20)
    if sys.SignalCount(0) != c + 1 || sys.SignalCount(1) != tc + 1 {
        return 11
    }
    return 0
}

// ResubWin is test point 2's callback, inside the resubmission's window:
// it requests the cancel and calls PollCancel, which (point 4 on io_uring;
// no AcceptEx yet on IOCP) reaches nothing, as a cancel that ran just
// before the resubmission would; then it turns point 4 off, so the
// resubmitter's own cancel names the new submission.
ResubWin(arg: *u8) {
    g := Sh(arg)
    unsafe "g is the case's page; ops[0] the op being resubmitted" {
        op := raw.FromAddr[sys.Op](Trunc[uint]((*g).ops[0].Load()))
        (*op).cancel.Store(1)
        sys.PollCancel(op)
        _ = sys.SysTestHook(4, ParkCount, raw.Null[u8](), false)
        _ = (*g).flag.Add(1)
    }
}

// ResubCancel (the cancel check after a resubmission). io_uring: a recv's CQE
// is made to report EINTR (test point 5), so Resubmit sends a recv that waits
// (its byte was taken); in the window (point 2) a cancel is requested and
// PollCancel names the first submission (point 4), finding nothing.
// Resubmit's check of op.cancel must cancel the new submission: the recv ends
// Canceled within 2 s. IOCP: the accepted connection is made to look reset
// (point 5), so ReissueAccept issues AcceptEx again; in its window, before
// the AcceptEx, the cancel's CancelIoEx finds nothing; the check after the
// AcceptEx must cancel it. epoll has no resubmission: 0.
export ResubCancel(): u32 {
    b := Backend()
    if b == 3 {
        return 0
    }
    page := NewPage()
    if raw.IsNull(page) {
        return 10
    }
    g := Sh(page)
    var code: u32 = 0
    if b == 2 {
        code = ResubCancelUring(page, g)
    } else {
        code = ResubCancelIocp(page, g)
    }
    _ = sys.SysTestHook(5, ParkCount, raw.Null[u8](), false)
    _ = sys.SysTestHook(2, ResubWin, page, false)
    _ = sys.SysTestHook(4, ParkCount, raw.Null[u8](), false)
    unsafe "g is the case's page" {
        if code == 0 && (*g).flag.Load() != 1 {
            code = 20 + (*g).flag.Load()
        }
    }
    sys.Release(page, 4096)
    return code
}

ResubCancelUring(page: *u8, g: *Case3): u32 {
    p := MakePair()
    if p.code != 0 {
        _ = ClosePair(&p)
        return p.code
    }
    var code: u32 = 0
    var one: [1]u8 = [5]
    var snd := sys.NewOp(sys.OpSend, p.c)
    snd.buf = raw.DataMut(mut one[..])
    snd.len = 1
    Start(raw.OfMut(mut snd))
    var so: [1]*sys.Op = [raw.OfMut(mut snd)]
    if Reap(so[..], 5_000) != 0 || !Ok(snd.res) {
        _ = ClosePair(&p)
        return 11
    }
    var buf: [4]u8 = [0; 4]
    var rcv := sys.NewOp(sys.OpRecv, p.s)
    rcv.buf = raw.DataMut(mut buf[..])
    rcv.len = 4
    unsafe "g is the case's page" {
        (*g).ops[0].Store(u64(raw.Addr(raw.OfMut(mut rcv))))
    }
    _ = sys.SysTestHook(4, ParkCount, raw.Null[u8](), true)
    _ = sys.SysTestHook(2, ResubWin, page, true)
    _ = sys.SysTestHook(5, ParkCount, raw.Null[u8](), true)
    Start(raw.OfMut(mut rcv))
    var ro: [1]*sys.Op = [raw.OfMut(mut rcv)]
    if Reap(ro[..], 2_000) != 0 {
        // The resubmitted recv was never cancelled: complete it with a
        // byte, so nothing is pending on this frame.
        code = 1
        _ = sys.SysTestHook(2, ResubWin, page, false)
        Start(raw.OfMut(mut snd))
        var both: [2]*sys.Op = [raw.OfMut(mut snd), raw.OfMut(mut rcv)]
        if Reap(both[..], 5_000) != 0 {
            code = 2
        }
    } else if rcv.res.err != core.SysErr.Canceled {
        code = 3
    }
    c := ClosePair(&p)
    if code != 0 {
        return code
    }
    return c
}

ResubCancelIocp(page: *u8, g: *Case3): u32 {
    l := sys.SocketOpen(sys.Inet4, true)
    if !Ok(l) {
        return 31
    }
    lh := Trunc[uint](l.value)
    a := Loopback(0)
    var bound := Loopback(0)
    if !Ok(sys.SocketListen(lh, &a, 16)) || !Ok(sys.SocketName(lh, mut bound)) {
        _ = sys.SocketShutdownClose(lh)
        return 32
    }
    c := sys.SocketOpen(sys.Inet4, true)
    if !Ok(c) {
        _ = sys.SocketShutdownClose(lh)
        return 33
    }
    ch := Trunc[uint](c.value)
    var code: u32 = 0
    var acc := sys.NewOp(sys.OpAccept, lh)
    acc.addr.family = sys.Inet4
    unsafe "g is the case's page" {
        (*g).ops[0].Store(u64(raw.Addr(raw.OfMut(mut acc))))
    }
    _ = sys.SysTestHook(2, ResubWin, page, true)
    _ = sys.SysTestHook(5, ParkCount, raw.Null[u8](), true)
    Start(raw.OfMut(mut acc))
    var con := sys.NewOp(sys.OpConnect, ch)
    con.addr = bound
    Start(raw.OfMut(mut con))
    var co: [1]*sys.Op = [raw.OfMut(mut con)]
    if Reap(co[..], 5_000) != 0 || !Ok(con.res) {
        code = 34
    }
    var ao: [1]*sys.Op = [raw.OfMut(mut acc)]
    if Reap(ao[..], 2_000) != 0 {
        // The reissued AcceptEx was never cancelled: connect a second
        // client, so nothing is pending on this frame.
        code = 1
        _ = sys.SysTestHook(2, ResubWin, page, false)
        c2 := sys.SocketOpen(sys.Inet4, true)
        if Ok(c2) {
            var con2 := sys.NewOp(sys.OpConnect, Trunc[uint](c2.value))
            con2.addr = bound
            Start(raw.OfMut(mut con2))
            var both: [2]*sys.Op = [raw.OfMut(mut con2), raw.OfMut(mut acc)]
            if Reap(both[..], 5_000) != 0 {
                code = 2
            }
            if Ok(acc.res) {
                _ = sys.SocketShutdownClose(Trunc[uint](acc.res.value))
            }
            _ = sys.SocketShutdownClose(Trunc[uint](c2.value))
        }
    } else if acc.res.err != core.SysErr.Canceled {
        code = 3
        if Ok(acc.res) {
            _ = sys.SocketShutdownClose(Trunc[uint](acc.res.value))
        }
    }
    _ = sys.SocketShutdownClose(ch)
    _ = sys.SocketShutdownClose(lh)
    return code
}
`
