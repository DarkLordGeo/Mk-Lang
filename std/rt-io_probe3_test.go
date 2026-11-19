// Why: every fixed defect of std.sys's poller, sockets and signals keeps
// the case that failed before its fix.
// What: rtioProbe3, the Mk source of std/rtioprobe/probe3.mk, one case per
// defect (Handoff, SemHuge, HugeWait, WakeSteal, DoneRace, SigRace...).
// How: std/rt-io_poll_test.go writes it next to probe.mk and probe2.mk; each
// case returns 0, or the number of the step that failed.

package std

// rtioProbe3 is std/rtioprobe/probe3.mk of the copy.
const rtioProbe3 = `// A probe of std.sys, third file (std/rt-io_probe3_test.go).

import std.core
import std.raw
import std.sys

const MaxI64: i64 = 0x7FFF_FFFF_FFFF_FFFF

// Case3 is what a case's threads share with Main, in a page of its own.
struct Case3 {
    stop: raw.Atomic[u32]
    exited: raw.Atomic[u32]
    returned: raw.Atomic[u32]
    bad: raw.Atomic[u32]
    flag: raw.Atomic[u32]
    t0: raw.Atomic[u64]
    t1: raw.Atomic[u64]
    ops: [4]raw.Atomic[u64]
    sem: sys.Sem
    tid: raw.Atomic[u64]
    res: raw.Atomic[u64]
}

NewPage(): *u8 {
    p := sys.Reserve(4096, 4096)
    if raw.IsNull(p) || !sys.Commit(p, 4096) {
        return raw.Null[u8]()
    }
    return p
}

Sh(p: *u8): *Case3 {
    unsafe "p is a committed page holding a Case3" {
        return raw.PtrCast[u8, Case3](p)
    }
}

// Spin yields for ms milliseconds.
Spin(ms: i64) {
    t := sys.Now() + ms * 1_000_000
    for sys.Now() < t {
        sys.ThreadYield()
    }
}

// Mark marks every op of out[..n] delivered (Cas 1 to 3); false when one
// was delivered twice.
Mark(out: []*sys.Op, n: int): bool {
    var ok := true
    for i in 0..n {
        unsafe "a delivered op" {
            if !(*out[i]).state.Cas(1, 3) {
                ok = false
            }
        }
    }
    return ok
}

// Stop sets stop and posts PollWake until every one of n threads exited.
StopAll(g: *Case3, n: u32): bool {
    unsafe "g is the case's page" {
        (*g).stop.Store(1)
        deadline := sys.Now() + 10_000_000_000
        for (*g).exited.Load() != n {
            sys.PollWake()
            sys.ThreadYield()
            if sys.Now() > deadline {
                return false
            }
        }
    }
    return true
}

// SemPoster posts the case's semaphore 50 ms after it starts.
SemPoster(arg: *u8) {
    g := Sh(arg)
    Spin(50)
    unsafe "g is the case's page" {
        sys.SemPost(&(*g).sem, 1)
        _ = (*g).exited.Add(1)
    }
}

// SemHuge: SemWait with a timeout whose deadline is
// past i64's largest value waits for the post (it overflowed: a panic, or
// a false at once).
export SemHuge(): u32 {
    page := NewPage()
    if raw.IsNull(page) {
        return 10
    }
    g := Sh(page)
    var code: u32 = 0
    unsafe "g is the case's page" {
        if !sys.SemInit(mut (*g).sem) {
            return 11
        }
        if !Ok(sys.ThreadStart(SemPoster, page, 262144)) {
            return 12
        }
        t0 := sys.Now()
        if !sys.SemWait(&(*g).sem, MaxI64 - 1) {
            code = 1
        } else if sys.Now() - t0 < 40_000_000 {
            code = 2
        }
        for (*g).exited.Load() != 1 {
            sys.ThreadYield()
        }
    }
    sys.Release(page, 4096)
    return code
}

// HugeHolder waits in PollWait(-1) until stop, so Main's wait is a parker
// on Linux.
HugeHolder(arg: *u8) {
    g := Sh(arg)
    var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    unsafe "g is the case's page" {
        for (*g).stop.Load() == 0 {
            _ = sys.PollWait(-1, mut out[..])
        }
        _ = (*g).exited.Add(1)
    }
}

// HugeWaker posts PollWake every 20 ms from 50 ms on, until Main's wait
// has returned.
HugeWaker(arg: *u8) {
    g := Sh(arg)
    Spin(50)
    unsafe "g is the case's page" {
        for (*g).flag.Load() == 0 {
            sys.PollWake()
            Spin(20)
        }
        _ = (*g).exited.Add(1)
    }
}

// HugeWait: PollWait with the largest timeout
// returns only on a wake. Windows armed a 100 ns timer for it (a return at
// once); a Linux parker overflowed in SemWait. A holder thread blocks in
// the kernel first, so Main parks on Linux.
export HugeWait(): u32 {
    page := NewPage()
    if raw.IsNull(page) {
        return 10
    }
    g := Sh(page)
    var code: u32 = 0
    if !Ok(sys.ThreadStart(HugeHolder, page, 262144)) {
        return 11
    }
    Spin(30)
    if !Ok(sys.ThreadStart(HugeWaker, page, 262144)) {
        return 12
    }
    var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    t0 := sys.Now()
    _ = sys.PollWait(MaxI64, mut out[..])
    took := sys.Now() - t0
    unsafe "g is the case's page" {
        (*g).flag.Store(1)
    }
    if took < 40_000_000 {
        code = 1
    }
    unsafe "g is the case's page" {
        // The waker exits on flag; the holder on stop.
        for (*g).exited.Load() != 1 {
            sys.ThreadYield()
        }
        (*g).exited.Store(0)
    }
    if !StopAll(g, 1) {
        return 3
    }
    sys.Release(page, 4096)
    return code
}

// OnceThread does one PollWait(-1), marks what it got, counts its return,
// then waits for stop without polling again.
OnceThread(arg: *u8) {
    g := Sh(arg)
    var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    n := sys.PollWait(-1, mut out[..])
    unsafe "g is the case's page" {
        if !Mark(out[..], n) {
            (*g).bad.Store(1)
        }
        _ = (*g).returned.Add(1)
        for (*g).stop.Load() == 0 {
            sys.ThreadYield()
        }
        _ = (*g).exited.Add(1)
    }
}

// ParkCount is PollTestPark's callback for Handoff: it only counts (the
// hook's own counter).
ParkCount(arg: *u8) {
}

// WaitState waits up to ms for op's state to be 3.
WaitState(op: *sys.Op, ms: i64): bool {
    deadline := sys.Now() + ms * 1_000_000
    unsafe "the probe's op" {
        for (*op).state.Load() != 3 {
            if sys.Now() > deadline {
                return false
            }
            sys.ThreadYield()
        }
    }
    return true
}

// Handoff: one thread blocks in the kernel and two park; each
// polls once and never again. Recv A completes: the holder returns it and
// hands the kernel wait to a parker. A parker that reaped nothing and
// returned left the other asleep with the kernel wait free, so recv B,
// completed next, was never delivered (step 4). Windows has no parkers: 0
// by the same steps.
export Handoff(): u32 {
    p := MakePair()
    if p.code != 0 {
        _ = ClosePair(&p)
        return p.code
    }
    page := NewPage()
    if raw.IsNull(page) {
        return 10
    }
    g := Sh(page)
    var code: u32 = 0
    var abuf: [4]u8 = [0; 4]
    var bbuf: [4]u8 = [0; 4]
    var one: [1]u8 = [9]
    var ra := sys.NewOp(sys.OpRecv, p.s)
    ra.buf = raw.DataMut(mut abuf[..])
    ra.len = 4
    var rb := sys.NewOp(sys.OpRecv, p.s)
    rb.buf = raw.DataMut(mut bbuf[..])
    rb.len = 4
    var sa := sys.NewOp(sys.OpSend, p.c)
    sa.buf = raw.DataMut(mut one[..])
    sa.len = 1
    var sb := sys.NewOp(sys.OpSend, p.c)
    sb.buf = raw.DataMut(mut one[..])
    sb.len = 1
    // Wakes left by earlier cases (a PollWake whose waiter had gone) would
    // make the first holder return at once: blocking waits drain them.
    var drain: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    for _ in 0..3 {
        _ = sys.PollWait(1_000_000, mut drain[..])
    }
    base := sys.PollTestPark(ParkCount, page, true)
    for _ in 0..3 {
        if !Ok(sys.ThreadStart(OnceThread, page, 262144)) {
            return 11
        }
    }
    // Two parked (Linux), then let them reach their semaphore wait.
    deadline := sys.Now() + 5_000_000_000
    for Backend() != 1 && sys.PollTestPark(ParkCount, page, true) < base + 2 {
        if sys.Now() > deadline {
            code = 1
            break
        }
        sys.ThreadYield()
    }
    _ = sys.PollTestPark(ParkCount, page, false)
    Spin(50)
    if code == 0 {
        Start(raw.OfMut(mut ra))
        Send1(raw.OfMut(mut sa))
        if !WaitState(raw.OfMut(mut ra), 5_000) {
            code = 2
        }
    }
    if code == 0 {
        // Let the hand-off settle (the old code returned a parker here).
        Spin(100)
        Start(raw.OfMut(mut rb))
        Send1(raw.OfMut(mut sb))
        if !WaitState(raw.OfMut(mut rb), 2_000) {
            code = 4
        } else if !Ok(rb.res) || rb.res.value != 1 || bbuf[0] != 9 {
            code = 5
        }
    }
    if !StopAll(g, 3) {
        return 6
    }
    unsafe "g is the case's page" {
        if code == 0 && (*g).bad.Load() != 0 {
            code = 7
        }
    }
    // Ops a failed run left pending (and the sends) are reaped by Main.
    var ops: [4]*sys.Op = [raw.OfMut(mut ra), raw.OfMut(mut rb), raw.OfMut(mut sa), raw.OfMut(mut sb)]
    var left: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    var nl := 0
    for op in ops {
        unsafe "the probe's ops" {
            if (*op).state.Load() == 1 {
                left[nl] = op
                nl += 1
            }
        }
    }
    if nl > 0 && Reap(left[..nl], 5_000) != 0 && code == 0 {
        code = 8
    }
    sys.Release(page, 4096)
    c := ClosePair(&p)
    if code != 0 {
        return code
    }
    return c
}

// Send1 sends op's byte without a completion on Linux (a direct write:
// io_uring's sockets block, epoll's take one byte at once), so that only
// the recvs use up the threads' single polls; Windows sends through the
// port (a send completes inline there, skip-on-success).
Send1(op: *sys.Op) {
    unsafe "the probe's op" {
        if Backend() == 1 {
            Start(op)
            return
        }
        (*op).state.Store(3)
        (*op).res = sys.WriteSync((*op).h, raw.Slice((*op).buf, (*op).len), -1)
    }
}

// StealHolder waits in PollWait(-1) until stop and counts its returns.
StealHolder(arg: *u8) {
    g := Sh(arg)
    var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    unsafe "g is the case's page" {
        for (*g).stop.Load() == 0 {
            _ = sys.PollWait(-1, mut out[..])
            _ = (*g).returned.Add(1)
        }
        _ = (*g).exited.Add(1)
    }
}

// StealBusy polls without blocking until stop, as a busy carrier's PollNow
// does.
StealBusy(arg: *u8) {
    g := Sh(arg)
    var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    unsafe "g is the case's page" {
        for (*g).stop.Load() == 0 {
            _ = sys.PollWait(0, mut out[..])
        }
        _ = (*g).exited.Add(1)
    }
}

// WakeSteal: a thread blocks in the kernel while two
// others poll without blocking as fast as they can; each of rounds
// PollWakes must make the blocked thread return, within 200 ms (step 1000
// plus the round). A non-blocking reap that takes the wake (epoll drained
// the eventfd; io_uring reaped the wake's CQE) leaves it asleep.
export WakeSteal(rounds: int): u32 {
    page := NewPage()
    if raw.IsNull(page) {
        return 10
    }
    g := Sh(page)
    var code: u32 = 0
    if !Ok(sys.ThreadStart(StealHolder, page, 262144)) {
        return 11
    }
    Spin(30)
    for _ in 0..2 {
        if !Ok(sys.ThreadStart(StealBusy, page, 262144)) {
            return 12
        }
    }
    Spin(50)
    unsafe "g is the case's page" {
        for r in 0..rounds {
            before := (*g).returned.Load()
            t0 := sys.Now()
            sys.PollWake()
            for (*g).returned.Load() == before && sys.Now() - t0 < 200_000_000 {
                sys.ThreadYield()
            }
            if (*g).returned.Load() == before {
                code = 1_000 + Trunc[u32](r)
                break
            }
            // Let the holder block again.
            Spin(1)
        }
    }
    if !StopAll(g, 3) {
        return 2
    }
    sys.Release(page, 4096)
    return code
}

// DoneReaper polls until stop: an out of one slot and a 1 ms wait on odd
// threads (the overflow path pushes to the done list), non-blocking polls
// on even ones (the done list's lock-free check races the pushes).
DoneReaper(arg: *u8) {
    g := Sh(arg)
    var out: [3]*sys.Op = [raw.Null[sys.Op](); 3]
    unsafe "g is the case's page" {
        me := (*g).tid.Add(1)
        for (*g).stop.Load() == 0 {
            var n := 0
            if me % 2 == 1 {
                n = sys.PollWait(1_000_000, mut out[..1])
            } else {
                n = sys.PollWait(0, mut out[..])
            }
            if !Mark(out[..], n) {
                (*g).bad.Store(1)
            }
        }
        _ = (*g).exited.Add(1)
    }
}

// DoneRace: 8 threads reap while 32 recvs complete at once,
// over rounds: every op is delivered exactly once within 2 s (the done
// list's fast path is an atomic count, never a racing plain read).
export DoneRace(rounds: int): u32 {
    var pairs: [16]Pair = [Pair(l: 0, c: 0, s: 0, code: 0); 16]
    for i in 0..16 {
        pairs[i] = MakePair()
        if pairs[i].code != 0 {
            return pairs[i].code
        }
    }
    page := NewPage()
    if raw.IsNull(page) {
        return 10
    }
    g := Sh(page)
    for _ in 0..8 {
        if !Ok(sys.ThreadStart(DoneReaper, page, 262144)) {
            return 11
        }
    }
    var code: u32 = 0
    var rbuf: [32]u8 = [0; 32]
    var sbuf: [1]u8 = [5]
    // 64 ops in pages of their own: rcv at i, snd at 32 + i.
    opp := sys.Reserve(65536, 4096)
    if raw.IsNull(opp) || !sys.Commit(opp, 65536) {
        return 12
    }
    for r in 0..rounds {
        for i in 0..32 {
            var a := pairs[i / 2].s
            var b := pairs[i / 2].c
            if i % 2 == 1 {
                a = pairs[i / 2].c
                b = pairs[i / 2].s
            }
            unsafe "the op pages hold 64 ops of at most 1 KiB" {
                rcv := OpAt(opp, i)
                raw.Write(rcv, sys.NewOp(sys.OpRecv, a))
                (*rcv).buf = raw.Offset(raw.DataMut(mut rbuf[..]), i)
                (*rcv).len = 1
                snd := OpAt(opp, 32 + i)
                raw.Write(snd, sys.NewOp(sys.OpSend, b))
                (*snd).buf = raw.DataMut(mut sbuf[..])
                (*snd).len = 1
            }
        }
        for i in 0..64 {
            Start(OpAt(opp, i))
        }
        for i in 0..64 {
            if !WaitState(OpAt(opp, i), 2_000) {
                code = 1
            }
        }
        if code != 0 {
            // Release the ops only after every one came back.
            _ = StopAll(g, 8)
            for i in 0..64 {
                var one: [1]*sys.Op = [OpAt(opp, i)]
                _ = Reap(one[..], 5_000)
            }
            return 100 + Trunc[u32](r)
        }
    }
    sys.Release(opp, 65536)
    if !StopAll(g, 8) {
        return 2
    }
    unsafe "g is the case's page" {
        if (*g).bad.Load() != 0 {
            code = 3
        }
    }
    sys.Release(page, 4096)
    for i in 0..16 {
        _ = ClosePair(&pairs[i])
    }
    return code
}

// OpAt is op i of a page of ops (1 KiB apart).
OpAt(p: *u8, i: int): *sys.Op {
    unsafe "p holds 64 KiB" {
        return raw.PtrCast[u8, sys.Op](raw.Offset(p, i * 1024))
    }
}

// SockEpipe: after a socket's own write shutdown, a
// send fails with Reset on every backend and target (Windows said Closed,
// 10058), and so does a write of it through OpWrite where the backend
// writes sockets (io_uring said Closed: OpWrite's EPIPE went through the
// file mapping).
export SockEpipe(): u32 {
    p := MakePair()
    if p.code != 0 {
        _ = ClosePair(&p)
        return p.code
    }
    var code: u32 = 0
    if !Ok(sys.SocketShutdown(p.c, 1)) {
        code = 1
    }
    var b: [1]u8 = [1]
    var snd := sys.NewOp(sys.OpSend, p.c)
    snd.buf = raw.DataMut(mut b[..])
    snd.len = 1
    Start(raw.OfMut(mut snd))
    var one: [1]*sys.Op = [raw.OfMut(mut snd)]
    if Reap(one[..], 5_000) != 0 {
        code = 2
    } else if code == 0 && snd.res.err != core.SysErr.Reset {
        code = 1_000 + Trunc[u32](snd.res.os)
    }
    if code == 0 && Backend() != 1 {
        var wr := sys.NewOp(sys.OpWrite, p.c)
        wr.buf = raw.DataMut(mut b[..])
        wr.len = 1
        Start(raw.OfMut(mut wr))
        var w1: [1]*sys.Op = [raw.OfMut(mut wr)]
        if Reap(w1[..], 5_000) != 0 {
            code = 3
        } else if wr.res.err != core.SysErr.Reset {
            code = 2_000 + Trunc[u32](wr.res.os)
        }
    }
    c := ClosePair(&p)
    if code != 0 {
        return code
    }
    return c
}

// KindSock: HandleKind of a socket is 4 on both targets
// (Windows said 3, a pipe), and on Windows a piped standard output stays 3
// (the test runs the probe with its output on a pipe).
export KindSock(): u32 {
    s := sys.SocketOpen(sys.Inet4, true)
    if !Ok(s) {
        return 10
    }
    h := Trunc[uint](s.value)
    k := sys.HandleKind(h)
    _ = sys.SocketShutdownClose(h)
    if k != 4 {
        return 100 + k
    }
    if Backend() == 1 && sys.HandleKind(sys.StdHandle(1)) != 3 {
        return 200 + sys.HandleKind(sys.StdHandle(1))
    }
    return 0
}

// PosRead: a positioned read leaves the
// stream position alone on both targets (Windows moved it to the end);
// Windows: a positioned synchronous read of an overlapped handle on the
// port returns its bytes and leaves no packet; a stream-position read
// through the poller of an overlapped file is refused (it read offset 0).
export PosRead(): u32 {
    var data: [8]u8 = [97, 98, 99, 100, 101, 102, 103, 104]
    if !WriteNew("rtio_pos.tmp", data[..]) {
        return 1
    }
    r := sys.OpenFile("rtio_pos.tmp", 0, false)
    if !Ok(r) {
        return 2
    }
    h := Trunc[uint](r.value)
    var a: [4]u8 = [0; 4]
    var b: [4]u8 = [0; 4]
    x := sys.ReadSync(h, mut a[..], 4)
    y := sys.ReadSync(h, mut b[..], -1)
    _ = sys.Close(h)
    if !Ok(x) || x.value != 4 || a[0] != 101 {
        return 3
    }
    if !Ok(y) || y.value != 4 || b[0] != 97 {
        return 4
    }
    var code: u32 = 0
    if Backend() == 1 {
        o := sys.OpenFile("rtio_pos.tmp", 0, true)
        if !Ok(o) {
            return 5
        }
        oh := Trunc[uint](o.value)
        if !sys.PollAttach(oh) {
            code = 6
        }
        var c: [4]u8 = [0; 4]
        z := sys.ReadSync(oh, mut c[..], 2)
        if code == 0 && (!Ok(z) || z.value != 4 || c[0] != 99) {
            code = 7
        }
        var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
        if code == 0 && sys.PollWait(100_000_000, mut out[..]) != 0 {
            code = 8
        }
        var rd := sys.NewOp(sys.OpRead, oh)
        rd.buf = raw.DataMut(mut c[..])
        rd.len = 4
        rd.pos = -1
        if code == 0 {
            Start(raw.OfMut(mut rd))
            var one: [1]*sys.Op = [raw.OfMut(mut rd)]
            if Reap(one[..], 5_000) != 0 {
                code = 9
            } else if rd.res.err != core.SysErr.Other || rd.res.os != 87 {
                code = 10
            }
        }
        sys.PollDetach(oh)
        _ = sys.Close(oh)
    }
    if !Ok(sys.Remove("rtio_pos.tmp")) && code == 0 {
        code = 11
    }
    return code
}

// PosCost is the mean cost in nanoseconds of a positioned 4 KiB ReadSync
// of a cached file, over n reads (logged, not judged: the price of
// PosIo's pointer save and event on Windows).
export PosCost(n: int): i64 {
    var data: [4096]u8 = [1; 4096]
    if !WriteNew("rtio_cost.tmp", data[..]) {
        return -1
    }
    r := sys.OpenFile("rtio_cost.tmp", 0, false)
    if !Ok(r) {
        return -2
    }
    h := Trunc[uint](r.value)
    var buf: [4096]u8 = [0; 4096]
    t0 := sys.Now()
    for _ in 0..n {
        _ = sys.ReadSync(h, mut buf[..], 0)
    }
    took := sys.Now() - t0
    _ = sys.Close(h)
    _ = sys.Remove("rtio_cost.tmp")
    return took / i64(n)
}

// Inet6: an IPv6 wildcard listener accepts an IPv4 peer on
// both targets (dual-stack: Windows refused it), and an IPv6 peer on ::1,
// whose address decodes as ::1 with a port and scope 0. A machine with no
// IPv6 answers 0 (nothing to compare).
export Inet6(): u32 {
    ls := sys.SocketOpen(sys.Inet6, true)
    if !Ok(ls) {
        return 0
    }
    l := Trunc[uint](ls.value)
    var wild := sys.SockAddr(family: sys.Inet6, port: 0, addr: [0; 16], scope_id: 0)
    if !Ok(sys.SocketListen(l, &wild, 16)) {
        _ = sys.SocketShutdownClose(l)
        return 0
    }
    var bound := wild
    if !Ok(sys.SocketName(l, mut bound)) || bound.port == 0 {
        _ = sys.SocketShutdownClose(l)
        return 1
    }
    var code: u32 = 0
    for v6 in [false, true] {
        var fam := sys.Inet4
        var to := Loopback(bound.port)
        if v6 {
            fam = sys.Inet6
            to = sys.SockAddr(family: sys.Inet6, port: bound.port, addr: [0; 16], scope_id: 0)
            to.addr[15] = 1
        }
        cs := sys.SocketOpen(fam, true)
        if !Ok(cs) {
            code = 2
            break
        }
        c := Trunc[uint](cs.value)
        var acc := sys.NewOp(sys.OpAccept, l)
        acc.addr.family = sys.Inet6
        var con := sys.NewOp(sys.OpConnect, c)
        con.addr = to
        Start(raw.OfMut(mut acc))
        Start(raw.OfMut(mut con))
        var ops: [2]*sys.Op = [raw.OfMut(mut acc), raw.OfMut(mut con)]
        if Reap(ops[..], 10_000) != 0 {
            code = 3
        } else if !Ok(acc.res) || !Ok(con.res) {
            code = 4
            if !v6 {
                code = 5
            }
        } else {
            _ = sys.SocketShutdownClose(Trunc[uint](acc.res.value))
            if v6 && (acc.addr.family != sys.Inet6 || acc.addr.addr[15] != 1 || acc.addr.addr[0] != 0 ||
                acc.addr.port == 0 || acc.addr.scope_id != 0) {
                code = 6
            }
        }
        _ = sys.SocketShutdownClose(c)
        if code != 0 {
            break
        }
    }
    _ = sys.SocketShutdownClose(l)
    return code
}

// SigWindow is test point 1's callback: the installer marks that it is in
// the window and stays there 50 ms.
SigWindow(arg: *u8) {
    g := Sh(arg)
    unsafe "g is the case's page" {
        (*g).flag.Store(1)
    }
    Spin(50)
}

SigInstaller(arg: *u8) {
    g := Sh(arg)
    ok := sys.SignalWatch(2)
    unsafe "g is the case's page" {
        (*g).t1.Store(Trunc[u64](sys.Now()))
        if !ok {
            (*g).bad.Store(1)
        }
        _ = (*g).exited.Add(1)
    }
}

// SigRace: while one thread is inside the install window of
// SignalWatch (test point 1 holds it there 50 ms), a second SignalWatch of
// the same signal waits for the outcome instead of returning true at once.
// Hangup: it never fires on Windows, and no test sends it on Linux.
export SigRace(): u32 {
    page := NewPage()
    if raw.IsNull(page) {
        return 10
    }
    g := Sh(page)
    _ = sys.SysTestHook(1, SigWindow, page, true)
    if !Ok(sys.ThreadStart(SigInstaller, page, 262144)) {
        return 11
    }
    var code: u32 = 0
    unsafe "g is the case's page" {
        deadline := sys.Now() + 5_000_000_000
        for (*g).flag.Load() == 0 {
            if sys.Now() > deadline {
                code = 1
                break
            }
            sys.ThreadYield()
        }
        if code == 0 {
            ok := sys.SignalWatch(2)
            t := sys.Now()
            for (*g).exited.Load() == 0 {
                sys.ThreadYield()
            }
            if !ok || (*g).bad.Load() != 0 {
                code = 2
            } else if t < Trunc[i64]((*g).t1.Load()) - 5_000_000 {
                // Returned more than 5 ms before the installer did.
                code = 3
            }
        } else {
            for (*g).exited.Load() == 0 {
                sys.ThreadYield()
            }
        }
    }
    _ = sys.SysTestHook(1, SigWindow, page, false)
    sys.Release(page, 4096)
    return code
}

// Reader is Interrupt's thread: one interruptible read with no data; it
// records the result's error and os code.
Reader(arg: *u8) {
    g := Sh(arg)
    var b: [8]u8 = [0; 8]
    unsafe "g is the case's page" {
        h := Trunc[uint]((*g).ops[0].Load())
        (*g).flag.Store(1)
        r := sys.ReadOnce(h, mut b[..], -1)
        (*g).t1.Store(Trunc[u64](sys.Now()))
        if r.err == core.SysErr.Canceled {
            (*g).res.Store(1)
        } else {
            (*g).res.Store(100 + Trunc[u64](r.os))
        }
        _ = (*g).exited.Add(1)
    }
}

// Interrupt: ThreadInterrupt ends
// a blocked ReadOnce of another thread with Canceled within 50 ms (on
// Linux a blocking socket with no data, os 4; on Windows a standard input
// the test keeps open and empty, os 995). Before, no such call existed,
// and ReadSync retried EINTR forever.
export Interrupt(): u32 {
    page := NewPage()
    if raw.IsNull(page) {
        return 10
    }
    g := Sh(page)
    var p := Pair(l: 0, c: 0, s: 0, code: 0)
    var h := sys.StdHandle(0)
    if Backend() != 1 {
        p = BlockingPair()
        if p.code != 0 {
            _ = ClosePair(&p)
            return p.code
        }
        h = p.c
    }
    var code: u32 = 0
    unsafe "g is the case's page" {
        (*g).ops[0].Store(u64(h))
        t := sys.ThreadStart(Reader, page, 262144)
        if !Ok(t) {
            return 11
        }
        for (*g).flag.Load() == 0 {
            sys.ThreadYield()
        }
        Spin(50)
        t0 := sys.Now()
        // Re-send until the read returns (an interrupt before the call
        // blocks interrupts nothing: 4.11's early window).
        deadline := t0 + 2_000_000_000
        for (*g).exited.Load() == 0 {
            if !sys.ThreadInterrupt(Trunc[uint](t.value)) {
                code = 1
                break
            }
            Spin(5)
            if sys.Now() > deadline {
                code = 2
                break
            }
        }
        if code == 0 {
            if (*g).res.Load() != 1 {
                code = 1_000 + Trunc[u32]((*g).res.Load())
            } else if Trunc[i64]((*g).t1.Load()) - t0 > 50_000_000 {
                code = 3
            }
        }
    }
    if p.l != 0 {
        _ = ClosePair(&p)
    }
    if code != 2 {
        sys.Release(page, 4096)
    }
    return code
}

// BlockingPair is MakePair with a blocking client socket (Linux: no
// O_NONBLOCK on either backend), whose reads block in the kernel.
BlockingPair(): Pair {
    var p := Pair(l: 0, c: 0, s: 0, code: 0)
    l := sys.SocketOpen(sys.Inet4, true)
    if !Ok(l) {
        p.code = 101
        return p
    }
    p.l = Trunc[uint](l.value)
    a := Loopback(0)
    if !Ok(sys.SocketListen(p.l, &a, 16)) {
        p.code = 102
        return p
    }
    var bound := Loopback(0)
    if !Ok(sys.SocketName(p.l, mut bound)) {
        p.code = 103
        return p
    }
    c := sys.SocketOpen(sys.Inet4, false)
    if !Ok(c) {
        p.code = 104
        return p
    }
    p.c = Trunc[uint](c.value)
    var acc := sys.NewOp(sys.OpAccept, p.l)
    acc.addr.family = sys.Inet4
    var con := sys.NewOp(sys.OpConnect, p.c)
    con.addr = bound
    Start(raw.OfMut(mut acc))
    Start(raw.OfMut(mut con))
    var ops: [2]*sys.Op = [raw.OfMut(mut acc), raw.OfMut(mut con)]
    if Reap(ops[..], 10_000) != 0 || !Ok(acc.res) || !Ok(con.res) {
        p.code = 105
        return p
    }
    p.s = Trunc[uint](acc.res.value)
    return p
}

// ResubReaper polls without blocking until stop (it meets the CQEs of
// ResubRace's resubmissions).
ResubReaper(arg: *u8) {
    g := Sh(arg)
    var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    unsafe "g is the case's page" {
        for (*g).stop.Load() == 0 {
            n := sys.PollWait(0, mut out[..])
            if !Mark(out[..], n) {
                (*g).bad.Store(1)
            }
        }
        _ = (*g).exited.Add(1)
    }
}

// ResubWindow is test point 2's callback, inside Resubmit's window: it
// cancels the op on odd rounds and waits 20 ms, so the reaper thread meets
// the new CQE while the resubmitter still holds the op.
ResubWindow(arg: *u8) {
    g := Sh(arg)
    unsafe "g is the case's page; ops[0] the op being resubmitted" {
        op := raw.FromAddr[sys.Op](Trunc[uint]((*g).ops[0].Load()))
        if (*g).flag.Load() % 2 == 1 {
            (*op).cancel.Store(1)
            sys.PollCancel(op)
        }
    }
    Spin(20)
}

// ResubRace (io_uring only): the remove of an empty
// directory goes through Resubmit (EISDIR, then the AT_REMOVEDIR step);
// inside its window (test point 2) another thread reaps the new CQE and,
// on odd rounds, the op is cancelled. The op is handed out exactly once
// (the reaper leaves it to the resubmitter), as data or Canceled, and the
// directory is gone unless the cancel won. The harness makes rtio_rd0 to
// rtio_rd<n-1>. Other backends: 0.
export ResubRace(n: int): u32 {
    if Backend() != 2 {
        return 0
    }
    page := NewPage()
    if raw.IsNull(page) {
        return 10
    }
    g := Sh(page)
    // Two reapers: the one that meets the EISDIR CQE resubmits and stops
    // in the window; the other meets the new CQE there.
    for _ in 0..2 {
        if !Ok(sys.ThreadStart(ResubReaper, page, 262144)) {
            return 11
        }
    }
    _ = sys.SysTestHook(2, ResubWindow, page, true)
    var code: u32 = 0
    var name: [16]u8 = [114, 116, 105, 111, 95, 114, 100, 0, 0, 0, 0, 0, 0, 0, 0, 0]
    for i in 0..n {
        var k := 7
        if i >= 10 {
            name[k] = Trunc[u8](48 + i / 10)
            k += 1
        }
        name[k] = Trunc[u8](48 + i % 10)
        k += 1
        var path := ""
        unsafe "name holds k ASCII bytes and outlives the op" {
            path = raw.Str(raw.Data(name[..k]), k)
        }
        var scratch: [64]u8 = [0; 64]
        var rm := sys.NewOp(sys.OpRemove, 0)
        rm.path = path
        rm.buf = raw.DataMut(mut scratch[..])
        rm.len = 64
        unsafe "g is the case's page" {
            (*g).flag.Store(Trunc[u32](i))
            (*g).ops[0].Store(u64(raw.Addr(raw.OfMut(mut rm))))
        }
        Start(raw.OfMut(mut rm))
        if !WaitState(raw.OfMut(mut rm), 5_000) {
            code = 1
            break
        }
        if rm.res.err == core.SysErr.Canceled {
            continue
        }
        if !Ok(rm.res) {
            code = 1_000 + Trunc[u32](rm.res.os)
            break
        }
        if sys.Remove(path).err != core.SysErr.NotFound {
            code = 2
            break
        }
    }
    ran := sys.SysTestHook(2, ResubWindow, page, false)
    if !StopAll(g, 2) {
        return 3
    }
    unsafe "g is the case's page" {
        if code == 0 && (*g).bad.Load() != 0 {
            code = 4
        }
    }
    if code == 0 && ran < Trunc[u64](n) {
        code = 5
    }
    sys.Release(page, 4096)
    return code
}

// GenOnce submits a recv on its own frame and returns the op's result:
// the frame is at the same address on every call from the same caller.
GenOnce(p: &Pair, cancel: bool): (res: sys.OsResult, at: u64) {
    var b: [4]u8 = [0; 4]
    var one: [1]u8 = [3]
    var rcv := sys.NewOp(sys.OpRecv, p.s)
    rcv.buf = raw.DataMut(mut b[..])
    rcv.len = 4
    Start(raw.OfMut(mut rcv))
    if cancel {
        sys.PollCancel(raw.OfMut(mut rcv))
        Spin(20)
    }
    var snd := sys.NewOp(sys.OpSend, p.c)
    snd.buf = raw.DataMut(mut one[..])
    snd.len = 1
    Start(raw.OfMut(mut snd))
    var ops: [2]*sys.Op = [raw.OfMut(mut rcv), raw.OfMut(mut snd)]
    at := u64(raw.Addr(raw.OfMut(mut rcv)))
    if Reap(ops[..], 5_000) != 0 {
        return (res: sys.OsResult(value: -1, err: core.SysErr.Other, os: -1), at: at)
    }
    return (res: rcv.res, at: at)
}

// GenStale (io_uring only): a recv completes; a
// new recv on the same frame (the same address) is then cancelled by a
// cancel that names the first one's user_data (test point 4), as a cancel
// processed late would. The 16-bit generation in user_data makes it miss:
// the new recv delivers its byte, not Canceled. Other backends: 0.
export GenStale(): u32 {
    if Backend() != 2 {
        return 0
    }
    p := MakePair()
    if p.code != 0 {
        _ = ClosePair(&p)
        return p.code
    }
    var code: u32 = 0
    _ = sys.SysTestHook(4, ParkCount, raw.Null[u8](), true)
    a := GenOnce(&p, false)
    b := GenOnce(&p, true)
    _ = sys.SysTestHook(4, ParkCount, raw.Null[u8](), false)
    if a.at != b.at {
        // The two recvs were not at one address: nothing to check.
        code = 4
    } else if !Ok(a.res) || a.res.value != 1 {
        code = 1
    } else if b.res.err == core.SysErr.Canceled {
        code = 2
    } else if !Ok(b.res) || b.res.value != 1 {
        code = 3
    }
    c := ClosePair(&p)
    if code != 0 {
        return code
    }
    return c
}

// HrDirty (Windows): with slot 0 dirty and still pending (test
// point 3), a 1 ms PollWait still ends within about the timer's precision
// (the next slot), not the 15.6 ms tick: the mean of 10 waits is under
// 5 ms. Linux: 0.
export HrDirty(): u32 {
    if Backend() != 1 {
        return 0
    }
    var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    _ = sys.SysTestHook(3, ParkCount, raw.Null[u8](), true)
    t0 := sys.Now()
    for _ in 0..10 {
        _ = sys.PollWait(1_000_000, mut out[..])
    }
    mean := (sys.Now() - t0) / 10
    ran := sys.SysTestHook(3, ParkCount, raw.Null[u8](), false)
    if ran < 10 {
        return 1
    }
    if mean > 5_000_000 {
        return 2
    }
    return 0
}

// Resolve: Windows resolves localhost to a loopback
// address with the port asked, and an .invalid name is InvalidAddress;
// Linux answers InvalidAddress for both (no resolver without libc).
export Resolve(): u32 {
    var a := sys.SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0)
    r := sys.ResolveHost("localhost", 8080, mut a)
    local := a.port == 8080 && ((a.family == sys.Inet4 && a.addr[0] == 127) || (a.family == sys.Inet6 && a.addr[15] == 1))
    x := sys.ResolveHost("example.invalid", 80, mut a)
    if Backend() != 1 {
        if r.err != core.SysErr.InvalidAddress || x.err != core.SysErr.InvalidAddress {
            return 1
        }
        return 0
    }
    if !Ok(r) {
        return 1_000 + Trunc[u32](r.os)
    }
    if !local {
        return 3
    }
    if x.err != core.SysErr.InvalidAddress {
        return 2_000 + Trunc[u32](x.os)
    }
    return 0
}
`
