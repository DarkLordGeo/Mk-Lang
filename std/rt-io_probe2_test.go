// Why: more poller and socket cases than one probe file holds comfortably.
// What: rtioProbe2, the Mk source of std/rtioprobe/probe2.mk.
// How: std/rt-io_poll_test.go writes it next to probe.mk, whose helpers it
// uses (MakePair, ClosePair, Start, Reap, Await, Ok).

package std

// rtioProbe2 is std/rtioprobe/probe2.mk of the copy.
const rtioProbe2 = `// A probe of std.sys, second file (std/rt-io_probe2_test.go).

import std.core
import std.raw
import std.sys

// Gap is what ParkGap's two threads share with Main, in a page of its own.
struct Gap {
    claim: raw.Atomic[u32]
    gap: raw.Atomic[u32]
    parker: raw.Atomic[u64]
    cont: raw.Atomic[u32]
    away: raw.Atomic[u32]
    stop: raw.Atomic[u32]
    exited: raw.Atomic[u32]
    bad: raw.Atomic[u32]
}

// GapCb is PollTestPark's callback: the first thread that finds another
// blocking in the kernel records itself and waits, inside the window
// between its failed token Cas and its sleeper count, until Main lets it
// go on.
GapCb(arg: *u8) {
    unsafe "arg is the Gap page, live until both threads have exited" {
        g := raw.PtrCast[u8, Gap](arg)
        if !(*g).claim.Cas(0, 1) {
            return
        }
        (*g).parker.Store(sys.ThreadId())
        (*g).gap.Store(1)
        for (*g).cont.Load() == 0 && (*g).stop.Load() == 0 {
            sys.ThreadYield()
        }
    }
}

// GapThread waits forever in PollWait, marking what it is given. The
// thread that held the token when the parker entered the window (it is
// not the parker) stays away once it has returned, so nobody else is left
// to block in the kernel.
GapThread(arg: *u8) {
    unsafe "arg is the Gap page" {
        g := raw.PtrCast[u8, Gap](arg)
        me := sys.ThreadId()
        var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
        for (*g).stop.Load() == 0 {
            n := sys.PollWait(-1, mut out[..])
            for i in 0..n {
                if !(*out[i]).state.Cas(1, 3) {
                    (*g).bad.Store(1)
                }
            }
            if (*g).gap.Load() == 1 && (*g).parker.Load() != me && (*g).away.Load() == 0 {
                (*g).away.Store(1)
                for (*g).stop.Load() == 0 {
                    sys.ThreadYield()
                }
            }
        }
        _ = (*g).exited.Add(1)
    }
}

// WaitWord spins (yielding) until *w is at least v, up to ms milliseconds.
WaitWord(w: *raw.Atomic[u32], v: u32, ms: i64): bool {
    deadline := sys.Now() + ms * 1_000_000
    unsafe "w is a word of a live page" {
        for (*w).Load() < v {
            if sys.Now() > deadline {
                return false
            }
            sys.ThreadYield()
        }
    }
    return true
}

// ParkGap (Linux; RT-7's one-blocker rule under a controlled schedule):
// thread H blocks in the kernel, thread P finds the token taken and stops
// in the window before it counts itself a sleeper; Main wakes H, which
// releases the token, sees no sleeper, returns and stays away; then P goes
// on. A recv completed after that must still be delivered, by P: a
// PollWait that slept on the semaphore with the token free would leave it
// for good (step 3). Windows has no parked waiters: 0.
//
// The semaphore must hold no spare count when P parks, or P's wait returns
// at once and hides the gap: spare counts are left by posts whose parker
// had already timed out (ThreadStress makes many). So the test runs ParkGap
// before any case with more than one thread, and repeats the schedule.
export ParkGap(rounds: int): u32 {
    if Backend() == 1 {
        return 0
    }
    for _ in 0..rounds {
        c := ParkGapOnce()
        if c != 0 {
            return c
        }
    }
    return 0
}

ParkGapOnce(): u32 {
    p := MakePair()
    if p.code != 0 {
        _ = ClosePair(&p)
        return p.code
    }
    page := sys.Reserve(4096, 4096)
    if raw.IsNull(page) || !sys.Commit(page, 4096) {
        return 10
    }
    var code: u32 = 0
    var rbuf: [8]u8 = [0; 8]
    var sbuf: [1]u8 = [7]
    var rcv := sys.NewOp(sys.OpRecv, p.s)
    rcv.buf = raw.DataMut(mut rbuf[..])
    rcv.len = 8
    var snd := sys.NewOp(sys.OpSend, p.c)
    snd.buf = raw.DataMut(mut sbuf[..])
    snd.len = 1
    var started := false
    unsafe "page is the Gap record" {
        g := raw.PtrCast[u8, Gap](page)
        _ = sys.PollTestPark(GapCb, page, true)
        for _ in 0..2 {
            if !Ok(sys.ThreadStart(GapThread, page, 262144)) {
                return 11
            }
        }
        if !WaitWord(raw.Of(&(*g).gap), 1, 5_000) {
            code = 1
        }
        if code == 0 {
            sys.PollWake()
            if !WaitWord(raw.Of(&(*g).away), 1, 5_000) {
                code = 2
            }
        }
        (*g).cont.Store(1)
        if code == 0 {
            Start(raw.OfMut(mut rcv))
            Start(raw.OfMut(mut snd))
            started = true
            if !Await(raw.OfMut(mut rcv), 2_000) || !Await(raw.OfMut(mut snd), 2_000) {
                code = 3
            } else if !Ok(rcv.res) || rcv.res.value != 1 || rbuf[0] != 7 {
                code = 4
            }
        }
        n := sys.PollTestPark(GapCb, page, false)
        if code == 0 && n < 1 {
            code = 5
        }
        (*g).stop.Store(1)
        deadline := sys.Now() + 10_000_000_000
        for (*g).exited.Load() != 2 {
            sys.PollWake()
            sys.ThreadYield()
            if sys.Now() > deadline {
                return 6
            }
        }
        if code == 0 && (*g).bad.Load() != 0 {
            code = 7
        }
    }
    if started {
        // A failed run leaves the ops to Main: reap them before the frame
        // ends.
        var ops: [2]*sys.Op = [raw.OfMut(mut rcv), raw.OfMut(mut snd)]
        if Reap(ops[..], 5_000) != 0 && code == 0 {
            code = 8
        }
    }
    sys.Release(page, 4096)
    c := ClosePair(&p)
    if code != 0 {
        return code
    }
    return c
}

// FileFails runs the file calls' failure paths and one success: NotFound
// opens (synchronous and overlapped), a NUL in a path, a create, attach,
// detach and close, a remove, and a remove of what is gone.
export FileFails(): u32 {
    if sys.OpenFile("rtio_missing.tmp", 0, false).err != core.SysErr.NotFound {
        return 1
    }
    if sys.OpenFile("rtio_missing.tmp", 0, true).err != core.SysErr.NotFound {
        return 2
    }
    if sys.OpenFile("a\u{0}b", 0, false).err != core.SysErr.InvalidPath {
        return 3
    }
    r := sys.OpenFile("rtio_leak.tmp", 1, true)
    if !Ok(r) {
        return 4
    }
    h := Trunc[uint](r.value)
    if sys.PollAttach(h) {
        sys.PollDetach(h)
    }
    if !Ok(sys.Close(h)) {
        return 5
    }
    if !Ok(sys.Remove("rtio_leak.tmp")) {
        return 6
    }
    if sys.Remove("rtio_leak.tmp").err != core.SysErr.NotFound {
        return 7
    }
    return 0
}

// LeakRound runs every failure path once (and the successful pair
// lifecycle of Echo); refused adds a refused connect, which takes about
// 2 s on Windows (its SYN retries), so Windows runs it in two rounds only.
LeakRound(refused: bool): u32 {
    var c := AddrInUse()
    if c != 0 {
        return 100 + c
    }
    c = CancelAccept()
    if c != 0 {
        return 200 + c
    }
    c = CancelRecv()
    if c != 0 {
        return 300 + c
    }
    c = Echo()
    if c != 0 {
        return 400 + c
    }
    c = FileFails()
    if c != 0 {
        return 500 + c
    }
    if refused {
        c = Refused()
        if c != 0 {
            return 600 + c
        }
    }
    return 0
}

// Leaks: after a warm-up round (Windows opens handles of its own on the
// first Winsock calls), rounds of every failure path leave the process's
// handle count (Windows) or descriptor count (Linux) exactly where it was.
// A leak of one handle per round shows as a difference of rounds; the
// result is then 10,000 plus the difference.
export Leaks(rounds: int): u32 {
    c := LeakRound(true)
    if c != 0 {
        return c
    }
    before := sys.HandleCount()
    if before < 0 {
        return 1
    }
    for r in 0..rounds {
        c2 := LeakRound(Backend() != 1 || r < 2)
        if c2 != 0 {
            return c2
        }
    }
    after := sys.HandleCount()
    if after != before {
        var d := after - before
        if d < 0 {
            d = -d
        }
        if d > 9_999 {
            d = 9_999
        }
        return 10_000 + Trunc[u32](d)
    }
    return 0
}

// WaitRes is the mean length, in nanoseconds, of a 1 ms PollWait with
// nothing to complete, over n waits: the resolution timers through the
// poller get (logged by the test, not judged).
export WaitRes(n: int): i64 {
    var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    t0 := sys.Now()
    for _ in 0..n {
        _ = sys.PollWait(1_000_000, mut out[..])
    }
    return (sys.Now() - t0) / i64(n)
}

// WaitCost is the mean cost, in nanoseconds, of a PollWait with a finite
// timeout that returns at once (a PollWake posted just before it), over n
// waits: the fixed price of arming and releasing a timeout (logged, not
// judged).
export WaitCost(n: int): i64 {
    var out: [4]*sys.Op = [raw.Null[sys.Op](); 4]
    t0 := sys.Now()
    for _ in 0..n {
        sys.PollWake()
        _ = sys.PollWait(10_000_000, mut out[..])
    }
    return (sys.Now() - t0) / i64(n)
}

// Name fills buf with prefix copies of "./" then base (ASCII) and returns
// it as a str over buf.
Name(buf: mut []u8, dots: int, base: str): str from buf {
    var n := 0
    for _ in 0..dots {
        buf[n] = 46
        buf[n + 1] = 47
        n += 2
    }
    for x in base.AsBytes() {
        buf[n] = x
        n += 1
    }
    unsafe "buf holds n ASCII bytes and outlives the str" {
        return raw.Str(raw.Data(buf[..n]), n)
    }
}

// Long fills buf with a 250-byte ASCII file name: tag, then 'x's.
Long(buf: mut []u8, tag: u8): str {
    buf[0] = tag
    for i in 1..250 {
        buf[i] = 120
    }
    unsafe "buf holds 250 ASCII bytes and outlives the str" {
        return raw.Str(raw.Data(buf[..250]), 250)
    }
}

// WriteNew creates p holding data and closes it.
WriteNew(p: str, data: []u8): bool {
    r := sys.OpenFile(p, 1, false)
    if !Ok(r) {
        return false
    }
    h := Trunc[uint](r.value)
    w := sys.WriteSync(h, data, 0)
    return Ok(sys.Close(h)) && Ok(w) && w.value == i64(data.Len())
}

// ReadByte0 is the first byte of p (-1 on any failure).
ReadByte0(p: str): i64 {
    r := sys.OpenFile(p, 0, false)
    if !Ok(r) {
        return -1
    }
    h := Trunc[uint](r.value)
    var b: [4]u8 = [0; 4]
    x := sys.ReadSync(h, mut b[..], 0)
    _ = sys.Close(h)
    if !Ok(x) || x.value < 1 {
        return -1
    }
    return i64(b[0])
}

// Paths: the path calls the pool makes, with one answer on both targets.
// A 250-byte name (Windows: past MAX_PATH once the directory is added, so
// the \\?\ form) is created, renamed, read and removed; a 2,210-byte path
// of "./" steps (Windows: past the stack buffer) opens; a path over 4,096
// bytes is InvalidPath on Linux (the kernel's PATH_MAX) and opens on
// Windows (its limit is 32,767 units). Rename replaces a destination that
// is open elsewhere, whose handle keeps the old file (Windows: POSIX
// rename semantics, RT-14). A directory opens for reading; its reads (at
// an offset and at the stream position) and its size are IsDirectory;
// creating over it is IsDirectory. A missing source is NotFound.
export Paths(): u32 {
    var la: [256]u8 = [0; 256]
    var lb: [256]u8 = [0; 256]
    a := Long(mut la[..], 97)
    b := Long(mut lb[..], 98)
    var one: [1]u8 = [1]
    var two: [1]u8 = [2]
    if !WriteNew(a, one[..]) {
        return 1
    }
    if !Ok(sys.Rename(a, b)) {
        return 2
    }
    if ReadByte0(b) != 1 || sys.OpenFile(a, 0, false).err != core.SysErr.NotFound {
        return 3
    }
    var deep: [8400]u8 = [0; 8400]
    d := Name(mut deep[..], 1_100, "rtio_deep.tmp")
    if !WriteNew("rtio_deep.tmp", two[..]) || ReadByte0(d) != 2 {
        return 4
    }
    var deep2: [8400]u8 = [0; 8400]
    deeper := Name(mut deep2[..], 2_100, "rtio_deep.tmp")
    r := sys.OpenFile(deeper, 0, false)
    if Backend() == 1 {
        if !Ok(r) || !Ok(sys.Close(Trunc[uint](r.value))) {
            return 5
        }
    } else if r.err != core.SysErr.InvalidPath {
        return 6
    }
    if !Ok(sys.Remove(d)) || !Ok(sys.Remove(b)) {
        return 7
    }
    // Rename over an open destination.
    if !WriteNew("rtio_new.tmp", one[..]) || !WriteNew("rtio_old.tmp", two[..]) {
        return 8
    }
    o := sys.OpenFile("rtio_old.tmp", 0, false)
    if !Ok(o) {
        return 9
    }
    oh := Trunc[uint](o.value)
    rn := sys.Rename("rtio_new.tmp", "rtio_old.tmp")
    var got: [4]u8 = [0; 4]
    x := sys.ReadSync(oh, mut got[..], 0)
    _ = sys.Close(oh)
    if !Ok(rn) {
        return 1_000 + Trunc[u32](rn.os)
    }
    if !Ok(x) || got[0] != 2 || ReadByte0("rtio_old.tmp") != 1 {
        return 10
    }
    if !Ok(sys.Remove("rtio_old.tmp")) || sys.Rename("rtio_new.tmp", "rtio_x.tmp").err != core.SysErr.NotFound {
        return 11
    }
    // A directory.
    dr := sys.OpenFile(".", 0, false)
    if !Ok(dr) {
        return 12
    }
    dh := Trunc[uint](dr.value)
    dx := sys.ReadSync(dh, mut got[..], 0)
    ds := sys.ReadSync(dh, mut got[..], -1)
    dz := sys.FileSize(dh)
    _ = sys.Close(dh)
    if dx.err != core.SysErr.IsDirectory {
        return 2_000 + Trunc[u32](dx.os)
    }
    if ds.err != core.SysErr.IsDirectory {
        return 3_000 + Trunc[u32](ds.os)
    }
    if dz.err != core.SysErr.IsDirectory {
        return 4_000 + Trunc[u32](dz.os)
    }
    if sys.OpenFile(".", 1, false).err != core.SysErr.IsDirectory {
        return 13
    }
    return 0
}

// RemoveDir removes rtio_dir, an empty directory the test makes before
// each run: through the poller where the backend does removes (io_uring:
// unlinkat answers EISDIR and Resubmit sends the AT_REMOVEDIR step, the
// resubmission path), else through the pool's call. A second remove is
// NotFound.
export RemoveDir(): u32 {
    if sys.PollSupports(sys.OpRemove) {
        var scratch: [64]u8 = [0; 64]
        var rm := sys.NewOp(sys.OpRemove, 0)
        rm.path = "rtio_dir"
        rm.buf = raw.DataMut(mut scratch[..])
        rm.len = 64
        Start(raw.OfMut(mut rm))
        var one: [1]*sys.Op = [raw.OfMut(mut rm)]
        if Reap(one[..], 10_000) != 0 {
            return 1
        }
        if !Ok(rm.res) {
            return 100 + Trunc[u32](rm.res.os)
        }
    } else if !Ok(sys.Remove("rtio_dir")) {
        return 2
    }
    if sys.Remove("rtio_dir").err != core.SysErr.NotFound {
        return 3
    }
    return 0
}
`
