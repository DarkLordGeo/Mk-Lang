// Why: a non-blocking PollWait must never keep a PollWake from the thread
// that waits next; on IOCP a zero-timeout GetQueuedCompletionStatusEx
// dequeued the wake packet and dropped it.
// What: rtioProbe7, the Mk source of std/rtioprobe/probe7.mk (WakeKeep,
// WakeLate, PushAll, RenameDir).
// How: std/rt-io_pollwake_test.go writes it next to the earlier probe files; each
// case returns 0, or the number of the step that failed.

package std

// It uses the earlier files' helpers (NewPage, Sh, Spin, StealBusy, StopAll,
// Ok, MakePair, OpAt, ParkCount).

// rtioProbe7 is std/rtioprobe/probe7.mk of the copy.
const rtioProbe7 = `// A probe of std.sys, seventh file (std/rt-io_probe7_test.go).

import std.core
import std.raw
import std.sys

// WakeKeep: with no other thread polling, a PollWake followed by
// non-blocking PollWaits must still end the next blocking wait at once,
// and only that one: a second wait lasts its whole timeout (the wake is
// kept, not copied). On IOCP the zero-timeout call took the wake packet
// and dropped it, so the blocking wait slept its 300 ms (step 10 + round,
// plus 100 x its milliseconds). On io_uring this is UringReap's token-free
// branch (the stray wake and wake_pending); on epoll the undrained
// eventfd. Every backend gives the same answer. Rounds use 1, 2 and 3
// non-blocking calls.
export WakeKeep(): u32 {
    var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    // Drain whatever start left (none expected): a short blocking wait.
    _ = sys.PollWait(1_000_000, mut out[..])
    for round in 0..6 {
        sys.PollWake()
        // io_uring posts the eventfd read's CQE from task work, which runs
        // when this thread next enters the kernel: the yields in Spin do,
        // so the CQE is on the ring before the non-blocking polls (the
        // token-free branch of UringReap, not only the holder's wait).
        Spin(1)
        polls := 1 + round % 3
        for _ in 0..polls {
            if sys.PollWait(0, mut out[..]) != 0 {
                return 1
            }
        }
        t0 := sys.Now()
        n := sys.PollWait(300_000_000, mut out[..])
        d := sys.Now() - t0
        if n != 0 {
            return 2
        }
        if d >= 150_000_000 {
            return 10 + Trunc[u32](round) + 100 * Trunc[u32](d / 1_000_000)
        }
        // The wake is used up: a 50 ms wait lasts 50 ms (by Now) and
        // returns nothing.
        t1 := sys.Now()
        m := sys.PollWait(50_000_000, mut out[..])
        e := sys.Now() - t1
        if m != 0 {
            return 3
        }
        if e < 50_000_000 {
            return 20 + Trunc[u32](round) + 100 * Trunc[u32](e / 1_000)
        }
    }
    return 0
}
// ErrIdx is e's place in core.SysErr (None 0 to Other 15).
ErrIdx(e: core.SysErr): u32 {
    var all: [16]core.SysErr = [core.SysErr.None, core.SysErr.NotFound, core.SysErr.Exists, core.SysErr.Denied,
        core.SysErr.IsDirectory, core.SysErr.NotDirectory, core.SysErr.NoSpace, core.SysErr.InvalidPath,
        core.SysErr.Canceled, core.SysErr.Closed, core.SysErr.Refused, core.SysErr.Reset, core.SysErr.AddrInUse,
        core.SysErr.Unreachable, core.SysErr.InvalidAddress, core.SysErr.Other]
    for i in 0..16 {
        if all[i] == e {
            return Trunc[u32](i)
        }
    }
    return 99
}

// RenameDir: Rename where a directory is the source or the destination
// must give one answer on both targets (4.12, RT-14). The Go test makes
// rtio_rfile (a file), rtio_rdir (an empty directory) and rtio_rfull (a
// directory holding one file) before the run. The answer is three SysErr
// places, a hundred apart (100,000,000 above them when wrong): a file over
// the empty directory, the empty directory over the full one, the empty
// directory over the file, the empty directory over a second empty one.
// Windows answered Denied (ERROR_ACCESS_DENIED) for the first, Linux
// IsDirectory (EISDIR): 103150500 before RenameFail.
var RenameOs0: raw.Atomic[u64] = raw.Atomic[u64].New(0)
var RenameOs1: raw.Atomic[u64] = raw.Atomic[u64].New(0)
var RenameOs2: raw.Atomic[u64] = raw.Atomic[u64].New(0)
var RenameOs3: raw.Atomic[u64] = raw.Atomic[u64].New(0)

// RenameDirOs is the OS code of RenameDir's i-th call (logged; one thread
// stores and loads them, so the order needs no edge).
export RenameDirOs(i: int): u64 {
    if i == 0 {
        return RenameOs0.Load()
    }
    if i == 1 {
        return RenameOs1.Load()
    }
    if i == 2 {
        return RenameOs2.Load()
    }
    return RenameOs3.Load()
}

export RenameDir(): u32 {
    a := sys.Rename("rtio_rfile", "rtio_rdir")
    b := sys.Rename("rtio_rdir", "rtio_rfull")
    c := sys.Rename("rtio_rdir", "rtio_rfile")
    d := sys.Rename("rtio_rdir", "rtio_rdir2")
    RenameOs3.Store(Trunc[u64](d.os))
    RenameOs0.Store(Trunc[u64](a.os))
    RenameOs1.Store(Trunc[u64](b.os))
    RenameOs2.Store(Trunc[u64](c.os))
    got := ErrIdx(a.err) * 1_000_000 + ErrIdx(b.err) * 10_000 + ErrIdx(c.err) * 100 + ErrIdx(d.err)
    // IsDirectory, Other (ENOTEMPTY, ERROR_DIR_NOT_EMPTY: SysErr has no
    // place of its own for it), NotDirectory, None (an empty directory is
    // replaced on both).
    if got == 4_150_500 {
        return 0
    }
    return 100_000_000 + got
}

// Cpus is sys.CpuCount, which the Go test compares with Go's answer and
// with nproc.
export Cpus(): int {
    return sys.CpuCount()
}
// LateHolder is an idle carrier that is late to its wait: it raises flag,
// spins 3 ms outside the kernel (between its last check and its wait),
// then waits up to 1 s and counts its return.
LateHolder(arg: *u8) {
    g := Sh(arg)
    var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    unsafe "g is the case's page" {
        for (*g).stop.Load() == 0 {
            (*g).flag.Store(1)
            Spin(3)
            _ = sys.PollWait(1_000_000_000, mut out[..])
            (*g).flag.Store(0)
            _ = (*g).returned.Add(1)
        }
        _ = (*g).exited.Add(1)
    }
}

// WakeLate (C-RTS-C5's interleaving, threaded): the wake is posted while
// the holder is not yet in the kernel and two busy threads poll without
// blocking (StealBusy); each of rounds wakes must end the holder's next
// wait within 300 ms (step 1,000 plus the round). On IOCP the busy
// threads dequeued the queued packet and dropped it, so the holder slept
// its whole second.
export WakeLate(rounds: int): u32 {
    page := NewPage()
    if raw.IsNull(page) {
        return 10
    }
    g := Sh(page)
    var code: u32 = 0
    if !Ok(sys.ThreadStart(LateHolder, page, 262144)) {
        return 11
    }
    for _ in 0..2 {
        if !Ok(sys.ThreadStart(StealBusy, page, 262144)) {
            return 12
        }
    }
    unsafe "g is the case's page" {
        for r in 0..rounds {
            // The holder is in its spin: flag 1, and its count steady.
            t0 := sys.Now()
            for (*g).flag.Load() == 0 && sys.Now() - t0 < 2_000_000_000 {
                sys.ThreadYield()
            }
            before := (*g).returned.Load()
            sys.PollWake()
            t1 := sys.Now()
            for (*g).returned.Load() == before && sys.Now() - t1 < 300_000_000 {
                sys.ThreadYield()
            }
            if (*g).returned.Load() == before {
                code = 1_000 + Trunc[u32](r)
                break
            }
        }
    }
    if !StopAll(g, 3) {
        return 2
    }
    sys.Release(page, 4096)
    return code
}
// Mine counts how many of out[..n] are among ops[..4] (OpAt of p), and
// marks each in got.
Mine(p: *u8, out: []*sys.Op, n: int, got: mut []bool): int {
    var k := 0
    for j in 0..n {
        for i in 0..4 {
            if raw.Same(out[j], OpAt(p, i)) && !got[i] {
                got[i] = true
                k += 1
            }
        }
    }
    return k
}

// PushAll: an SQE that an earlier push left published and unconsumed (an
// EBUSY or EAGAIN; test point 9 makes Push return before its enter) is
// submitted by the next push. Four 1-byte recvs of data that is already
// there: three pushed with point 9 on, the fourth with it off; within
// 200 ms of non-blocking polls all four must be back (step 10 + how many
// were). Push entered with 1, so the fourth push submitted only the head
// SQE: 1 of 4, and the rest waited for a blocking wait. On epoll and IOCP
// the recvs complete inside PollSubmit (the point is io_uring's).
export PushAll(): u32 {
    pr := MakePair()
    if pr.code != 0 {
        return pr.code
    }
    opp := sys.Reserve(65536, 4096)
    if raw.IsNull(opp) || !sys.Commit(opp, 65536) {
        return 1
    }
    var code: u32 = 0
    var four: [4]u8 = [1, 2, 3, 4]
    var rbuf: [4]u8 = [0; 4]
    var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    var got: [4]bool = [false; 4]
    var back := 0
    unsafe "the op page holds 5 ops of at most 1 KiB" {
        // The data first: one send, waited for.
        snd := OpAt(opp, 4)
        raw.Write(snd, sys.NewOp(sys.OpSend, pr.s))
        (*snd).buf = raw.DataMut(mut four[..])
        (*snd).len = 4
        if sys.PollSubmit(snd) {
            t0 := sys.Now()
            var sent := false
            for !sent && sys.Now() - t0 < 2_000_000_000 {
                n := sys.PollWait(10_000_000, mut out[..])
                for j in 0..n {
                    if raw.Same(out[j], snd) {
                        sent = true
                    }
                }
            }
            if !sent {
                return 2
            }
        }
        if !Ok((*snd).res) || (*snd).res.value != 4 {
            return 3
        }
        Spin(10)
        for i in 0..4 {
            raw.Write(OpAt(opp, i), sys.NewOp(sys.OpRecv, pr.c))
            (*OpAt(opp, i)).buf = raw.Offset(raw.DataMut(mut rbuf[..]), i)
            (*OpAt(opp, i)).len = 1
        }
        _ = sys.SysTestHook(9, ParkCount, raw.Null[u8](), true)
        for i in 0..3 {
            if !sys.PollSubmit(OpAt(opp, i)) {
                got[i] = true
                back += 1
            }
        }
        _ = sys.SysTestHook(9, ParkCount, raw.Null[u8](), false)
        if !sys.PollSubmit(OpAt(opp, 3)) {
            got[3] = true
            back += 1
        }
        t1 := sys.Now()
        for back < 4 && sys.Now() - t1 < 200_000_000 {
            n := sys.PollWait(0, mut out[..])
            back += Mine(opp, out[..], n, mut got[..])
        }
        if back < 4 {
            code = 10 + Trunc[u32](back)
        }
        // Every op comes back before its page goes: blocking waits now
        // (they enter the kernel with whatever is unsubmitted).
        t2 := sys.Now()
        for back < 4 && sys.Now() - t2 < 5_000_000_000 {
            n := sys.PollWait(10_000_000, mut out[..])
            back += Mine(opp, out[..], n, mut got[..])
        }
        if back < 4 {
            // An op still in the kernel: its page must stay.
            return 20 + Trunc[u32](back)
        }
        var sum: u32 = 0
        for i in 0..4 {
            if !Ok((*OpAt(opp, i)).res) || (*OpAt(opp, i)).res.value != 1 {
                code = 30 + Trunc[u32](i)
            }
            sum += u32(rbuf[i])
        }
        if code == 0 && sum != 10 {
            code = 40
        }
    }
    sys.Release(opp, 65536)
    _ = sys.Close(pr.c)
    _ = sys.Close(pr.s)
    _ = sys.Close(pr.l)
    return code
}
`
