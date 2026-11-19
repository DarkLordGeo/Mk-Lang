// Why: kernels without io_uring, or with it refused, still need a poller;
// epoll is the fallback, and the backend MK_IO=epoll forces.
//
// What: the epoll backend: EpollInit, EpollAdd and EpollDel, EpollSubmit
// (Try, Record), EpollCancel, EpollReap and the descriptor table (Desc).
//
// How: descriptors are registered once, edge-triggered. An operation is
// tried at once; only EAGAIN records it on its descriptor's wait list, and
// the handler (on whichever thread reaps the edge) retries it, so a
// completion is final and the waiter never retries. No edge is lost: each
// direction counts its edges (seq), and a submitter records its op only if
// seq is unchanged after the EAGAIN, under the descriptor's lock; otherwise
// it tries again. The descriptor records are a table indexed by fd (MaxFds
// records of 64 bytes, one cache line each), mapped read-write with
// MAP_NORESERVE once, so untouched records cost nothing.

import std.core
import std.raw

const EpollCloExec: uint = 0x80000
const EfdNonBlock: uint = 0x800
const EpollCtlAdd: uint = 1
const EpollCtlDel: uint = 2
const EpollIn: u32 = 0x001
const EpollOut: u32 = 0x004
const EpollErr: u32 = 0x008
const EpollHup: u32 = 0x010
const EpollRdHup: u32 = 0x2000
const EpollEt: u32 = 0x8000_0000
const Eagain: int = -11
const Eintr4: int = -4
const Einprogress: int = -115
const Econnaborted: int = -103
const Enotconn: int = -107
const MsgNoSignal: uint = 0x4000
const SockNonBlockCloExec: uint = 0x80800
// MaxWaitMs is epoll_pwait's largest timeout (an int of milliseconds).
const MaxWaitMs: i64 = 0x7FFF_FFFF

// MaxFds bounds the descriptors epoll can serve (a larger fd is refused
// by PollAttach, so it goes to the pool, which serves it correctly).
const MaxFds: int = 1_048_576
const DescBytes: int = 64
// A record: lock at 0, read seq at 4, write seq at 8, then the read list
// (head 16, tail 24) and the write list (head 32, tail 40).
const DLock: int = 0
const DSeqRead: int = 4
const DSeqWrite: int = 8
const DHeadRead: int = 16
const DHeadWrite: int = 32

// EvBytes and EvData: struct epoll_event is packed on x86_64 (12 bytes,
// data at 4) and naturally aligned on aarch64 (16 bytes, data at 8).
EvBytes(): int {
    return if Target.arch == Arch.Arm64 { 16 } else { 12 }
}

// EventWords is the size of EpollReap's event buffer in u64 words, and
// EvMax how many epoll_events the kernel may write into it: derived from
// the layout, so the maxevents argument can never exceed the buffer on
// either architecture (64 events of 16 bytes do not fit 768 bytes on
// aarch64). x86_64: 85 events, aarch64: 64.
const EventWords: int = 128

EvMax(): int {
    return EventWords * 8 / EvBytes()
}

EvData(): int {
    return if Target.arch == Arch.Arm64 { 8 } else { 4 }
}

// EpollInit makes the set, the descriptor table and the wake eventfd
// (registered level-triggered with data MaxFds, which no descriptor has).
EpollInit(): bool {
    pp := PP()
    unsafe "system call; the state page" {
        ep := Syscall(Nrs().epoll_create1, EpollCloExec, 0, 0, 0, 0, 0)
        if IsErr(ep) {
            return false
        }
        ev := Syscall(Nrs().eventfd2, 0, EpollCloExec | EfdNonBlock, 0, 0, 0, 0)
        if IsErr(ev) {
            _ = Syscall(Nrs().close, Trunc[uint](ep), 0, 0, 0, 0, 0)
            return false
        }
        d := Map(raw.Null[u8](), MaxFds * DescBytes, ProtRW, MapPrivate | MapAnonymous | MapNoReserve)
        if raw.IsNull(d) {
            _ = Syscall(Nrs().close, Trunc[uint](ev), 0, 0, 0, 0, 0)
            _ = Syscall(Nrs().close, Trunc[uint](ep), 0, 0, 0, 0, 0)
            return false
        }
        var e: [2]u64 = [0; 2]
        ep8 := raw.PtrCast[u64, u8](raw.Data(e[..]))
        Put32(ep8, 0, EpollIn)
        Put64(ep8, EvData(), u64(MaxFds))
        if IsErr(Syscall(Nrs().epoll_ctl, Trunc[uint](ep), EpollCtlAdd, Trunc[uint](ev), raw.Addr(ep8), 0, 0)) {
            Unmap(d, MaxFds * DescBytes)
            _ = Syscall(Nrs().close, Trunc[uint](ev), 0, 0, 0, 0, 0)
            _ = Syscall(Nrs().close, Trunc[uint](ep), 0, 0, 0, 0, 0)
            return false
        }
        (*pp).epfd = Trunc[uint](ep)
        (*pp).descs = d
        // Published before PollInit returns (see PollInit's edge).
        Poll.evfd.Store(Trunc[u64](ev))
    }
    return true
}

// Desc is fd's record.
Desc(fd: uint): *u8 {
    unsafe "fd < MaxFds, checked by every caller" {
        return raw.Offset((*PP()).descs, Trunc[int](fd) * DescBytes)
    }
}

DescLock(d: *u8): *raw.Atomic[u32] {
    unsafe "d is a live descriptor record, and DLock is its lock word's offset" {
        return raw.PtrCast[u8, raw.Atomic[u32]](raw.Offset(d, DLock))
    }
}

// EpollAdd registers fd edge-triggered for both directions and clears its
// record. A regular file (EPERM) or an fd beyond the table is refused.
EpollAdd(fd: uint): bool {
    if fd >= Trunc[uint](MaxFds) {
        return false
    }
    d := Desc(fd)
    SpinLock(DescLock(d))
    for off in [DHeadRead, DHeadRead + 8, DHeadWrite, DHeadWrite + 8] {
        Put64(d, off, 0)
    }
    SpinUnlock(DescLock(d))
    var e: [2]u64 = [0; 2]
    unsafe "system call; e is an epoll_event" {
        ep8 := raw.PtrCast[u64, u8](raw.Data(e[..]))
        Put32(ep8, 0, EpollIn | EpollOut | EpollRdHup | EpollEt)
        Put64(ep8, EvData(), u64(fd))
        r := Syscall(Nrs().epoll_ctl, (*PP()).epfd, EpollCtlAdd, fd, raw.Addr(ep8), 0, 0)
        return !IsErr(r) || r == -17
    }
}

// EpollDel removes fd from the set and clears its record (no op may be
// pending on it: the contract's no-close-while-pending rule).
EpollDel(fd: uint) {
    if fd >= Trunc[uint](MaxFds) {
        return
    }
    var e: [2]u64 = [0; 2]
    unsafe "system call; e is an epoll_event in this frame, which the kernel ignores for a delete" {
        _ = Syscall(Nrs().epoll_ctl, (*PP()).epfd, EpollCtlDel, fd, raw.Addr(raw.Data(e[..])), 0, 0)
    }
    d := Desc(fd)
    SpinLock(DescLock(d))
    for off in [DHeadRead, DHeadRead + 8, DHeadWrite, DHeadWrite + 8] {
        Put64(d, off, 0)
    }
    SpinUnlock(DescLock(d))
}

// IsWriteDir: connect, send and write wait for writability.
IsWriteDir(kind: u32): bool {
    return kind == OpConnect || kind == OpSend || kind == OpWrite || kind == OpSendTo
}

SeqOff(kind: u32): int {
    return if IsWriteDir(kind) { DSeqWrite } else { DSeqRead }
}

HeadOff(kind: u32): int {
    return if IsWriteDir(kind) { DHeadWrite } else { DHeadRead }
}

// Append puts op at the tail of a list (head at off, tail at off + 8); the
// caller holds the record's lock.
Append(d: *u8, off: int, op: *Op) {
    SetNext(op, raw.Null[Op]())
    tail := Get64(d, off + 8)
    if tail == 0 {
        Put64(d, off, u64(raw.Addr(op)))
    } else {
        unsafe "tail is the address of the list's last op" {
            SetNext(raw.FromAddr[Op](Trunc[uint](tail)), op)
        }
    }
    Put64(d, off + 8, u64(raw.Addr(op)))
}

// Try makes op's non-blocking call once (EINTR retried at once). true: op
// finished, res set; false: EAGAIN, it must wait for an edge.
Try(op: *Op): bool {
    unsafe "op is owned by this thread: being submitted, or taken off its wait list" {
        fd := (*op).h
        o := OsArea(op)
        k := (*op).kind
        for {
            var r := 0
            if k == OpRecv {
                r = Syscall(Nrs().recvfrom, fd, raw.Addr((*op).buf), Clip((*op).len), 0, 0, 0)
            } else if k == OpSend {
                r = Syscall(Nrs().sendto, fd, raw.Addr((*op).buf), Clip((*op).len), MsgNoSignal, 0, 0)
            } else if k == OpRead {
                // pos >= 0 is pread, as io_uring's READ at an offset (a
                // pipe or socket answers ESPIPE on both backends).
                if (*op).pos >= 0 {
                    r = Syscall(Nrs().pread64, fd, raw.Addr((*op).buf), Clip((*op).len), Trunc[uint]((*op).pos), 0, 0)
                } else {
                    r = Syscall(Nrs().read, fd, raw.Addr((*op).buf), Clip((*op).len), 0, 0, 0)
                }
            } else if k == OpWrite {
                if (*op).pos >= 0 {
                    r = Syscall(Nrs().pwrite64, fd, raw.Addr((*op).buf), Clip((*op).len), Trunc[uint]((*op).pos), 0,
                        0)
                } else {
                    r = Syscall(Nrs().write, fd, raw.Addr((*op).buf), Clip((*op).len), 0, 0, 0)
                }
            } else if k == OpAccept {
                Put32(o, OsAddrN, 32)
                r = Syscall(Nrs().accept4, fd, raw.Addr(raw.Offset(o, OsAddr)), raw.Addr(raw.Offset(o, OsAddrN)),
                    SockNonBlockCloExec, 0, 0)
                if r == Econnaborted {
                    continue
                }
                if !IsErr(r) {
                    AcceptDone(op, Trunc[uint](r))
                    return true
                }
            } else if k == OpConnect {
                return ConnectCheck(op)
            } else if k == OpProcWait {
                // The pidfd turns readable when the child ends.
                return ProcTry(op)
            } else if k == OpReady {
                return ReadyTry(op)
            } else if k == OpRecvFrom {
                // MSG_TRUNC: the datagram's real length, so a cut one is
                // seen (DgramDone).
                Put32(o, OsAddrN, 32)
                r = Syscall(Nrs().recvfrom, fd, raw.Addr((*op).buf), Clip((*op).len), MsgTrunc,
                    raw.Addr(raw.Offset(o, OsAddr)), raw.Addr(raw.Offset(o, OsAddrN)))
                if !IsErr(r) {
                    DgramDone(op, r, false)
                    return true
                }
            } else if k == OpSendTo {
                n := EncodeAddr(&(*op).addr, raw.Offset(o, OsAddr))
                if n == 0 {
                    (*op).res = FailWith(core.SysErr.InvalidAddress)
                    return true
                }
                r = Syscall(Nrs().sendto, fd, raw.Addr((*op).buf), Clip((*op).len), MsgNoSignal,
                    raw.Addr(raw.Offset(o, OsAddr)), Trunc[uint](n))
            }
            if r == Eintr4 {
                continue
            }
            if r == Eagain {
                return false
            }
            // An error by kind and handle, as UringFinish maps it.
            (*op).res = if IsErr(r) { IoFail(fd, k, i64(-r)) } else { Ok(i64(r)) }
            return true
        }
    }
}

// Clip bounds one call's length (Linux moves at most 0x7FFFF000 bytes).
Clip(n: int): uint {
    if n <= 0 {
        return 0
    }
    return if n > MaxIo { Trunc[uint](MaxIo) } else { Trunc[uint](n) }
}

// AcceptDone registers the new socket and reads the peer's address; a
// socket that cannot be registered is closed (no leak) and the accept
// fails.
AcceptDone(op: *Op, s: uint) {
    unsafe "op is owned by this thread" {
        if !EpollAdd(s) {
            _ = Syscall(Nrs().close, s, 0, 0, 0, 0, 0)
            (*op).res = Fail(24)
            return
        }
        if !DecodeAddr(raw.Offset(OsArea(op), OsAddr), mut (*op).addr) {
            (*op).addr = SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0)
        }
        (*op).res = Ok(Trunc[i64](s))
    }
}

// ConnectCheck finishes a connect that returned EINPROGRESS: SO_ERROR
// reports a failure; a zero SO_ERROR is success only when the socket has a
// peer (an edge can arrive before the handshake ends: an unconnected socket
// is writable at registration), else it keeps waiting (Go's rule).
ConnectCheck(op: *Op): bool {
    unsafe "op is owned by this thread" {
        fd := (*op).h
        var err: [2]u32 = [0, 4]
        r := Syscall(Nrs().getsockopt, fd, 1, 4, raw.Addr(raw.Data(err[..])),
            raw.Addr(raw.Offset(raw.Data(err[..]), 1)), 0)
        if IsErr(r) {
            (*op).res = SockFail(i64(-r))
            return true
        }
        if err[0] != 0 {
            (*op).res = ConnectFail(i64(err[0]))
            return true
        }
        var sa: [4]u64 = [0; 4]
        var n: [1]u32 = [32]
        p := Syscall(Nrs().getpeername, fd, raw.Addr(raw.Data(sa[..])), raw.Addr(raw.Data(n[..])), 0, 0, 0)
        if p == Enotconn {
            return false
        }
        if IsErr(p) {
            (*op).res = SockFail(i64(-p))
            return true
        }
        (*op).res = Ok(0)
        return true
    }
}

// Record is the second half of the protocol: under the lock, record op
// only if no edge was seen since seq was read; else the caller tries
// again. A cancel requested before the recording completes the op here.
// 0 recorded, 1 try again, 2 cancelled (res set).
Record(op: *Op, seq: u32): u32 {
    unsafe "op is owned by this thread" {
        d := Desc((*op).h)
        k := (*op).kind
        SpinLock(DescLock(d))
        if Get32(d, SeqOff(k)) != seq {
            SpinUnlock(DescLock(d))
            return 1
        }
        // Edge: a canceller stores cancel before its PollCancel takes this
        // lock, so a 0 read here under the lock is followed by a PollCancel
        // that finds the recorded op.
        if (*op).cancel.Load() == 1 {
            SpinUnlock(DescLock(d))
            (*op).res = FailWith(core.SysErr.Canceled)
            return 2
        }
        Append(d, HeadOff(k), op)
        SpinUnlock(DescLock(d))
        return 0
    }
}

// InjectEdge is PollTestHook's controlled schedule: make data arrive (the
// test's callback) and reap the edge now, inside the window between the
// failed try and the recording. Not reentrant: the reap may retry ops.
InjectEdge(op: *Op) {
    pp := PP()
    unsafe "PollInit ran, so PP() is the page it made, which lives until exit; its words are atomics" {
        // test_busy: the Cas admits one injection at a time (the reap below
        // may retry ops whose tries would inject again); injected is a
        // counter.
        if (*pp).test_on.Load() == 0 || !(*pp).test_busy.Cas(0, 1) {
            return
        }
        d := Desc((*op).h)
        off := SeqOff((*op).kind)
        before := Get32(d, off)
        f := (*pp).test_cb
        f((*pp).test_arg)
        // Reap until this descriptor's edge has really been consumed (the
        // data may reach the socket a moment after the send returns), at
        // most one second.
        var nothing: [1]*Op = [raw.Null[Op]()]
        deadline := Now() + 1_000_000_000
        for Get32(d, off) == before && Now() < deadline {
            _ = EpollReap(0, mut nothing[..0], 0)
        }
        if Get32(d, off) != before {
            // Edge: injected is a test counter; the Store(0) below frees
            // test_busy for the next Cas.
            _ = (*pp).injected.Add(1)
        }
        (*pp).test_busy.Store(0)
    }
}

ReadSeq(op: *Op): u32 {
    unsafe "op's descriptor record" {
        d := Desc((*op).h)
        SpinLock(DescLock(d))
        s := Get32(d, SeqOff((*op).kind))
        SpinUnlock(DescLock(d))
        return s
    }
}

// EpollSubmit tries op and records it when it must wait for an edge: true
// when it is pending, false when it finished with res set.
EpollSubmit(op: *Op): bool {
    unsafe "op is the caller's operation" {
        fd := (*op).h
        if fd >= Trunc[uint](MaxFds) {
            (*op).res = FailWith(core.SysErr.Other)
            return false
        }
        if (*op).kind == OpConnect {
            o := OsArea(op)
            n := EncodeAddr(&(*op).addr, raw.Offset(o, OsAddr))
            if n == 0 {
                (*op).res = FailWith(core.SysErr.InvalidAddress)
                return false
            }
            seq := ReadSeq(op)
            r := Syscall(Nrs().connect, fd, raw.Addr(raw.Offset(o, OsAddr)), Trunc[uint](n), 0, 0, 0)
            if r == 0 {
                (*op).res = Ok(0)
                return false
            }
            // EINTR: the connect goes on in the background, as EINPROGRESS.
            if r != Einprogress && r != Eintr4 {
                (*op).res = ConnectFail(i64(-r))
                return false
            }
            Put32(o, OsStage, 1)
            var s := seq
            for {
                rec := Record(op, s)
                if rec == 0 {
                    return true
                }
                if rec == 2 {
                    return false
                }
                s = ReadSeq(op)
                if ConnectCheck(op) {
                    return false
                }
            }
        }
        var first := true
        for {
            s := ReadSeq(op)
            if Try(op) {
                return false
            }
            if first {
                InjectEdge(op)
                first = false
            }
            rec := Record(op, s)
            if rec == 0 {
                return true
            }
            if rec == 2 {
                return false
            }
        }
    }
}

// EpollCancel cancels op: an op still on its wait list is taken off and
// queued as Canceled on the done list (then PollWake, so a PollWait hands it out).
// An op not on the list is being retried by a handler, which sees cancel
// before recording it again, or has already completed.
EpollCancel(op: *Op) {
    unsafe "op is pending (the one-owner rule): its record lives" {
        fd := (*op).h
        if fd >= Trunc[uint](MaxFds) {
            return
        }
        d := Desc(fd)
        off := HeadOff((*op).kind)
        var found := false
        SpinLock(DescLock(d))
        var prev: u64 = 0
        var cur := Get64(d, off)
        for cur != 0 {
            c := raw.FromAddr[Op](Trunc[uint](cur))
            nx := u64(raw.Addr(Next(c)))
            if raw.Same(c, op) {
                if prev == 0 {
                    Put64(d, off, nx)
                } else {
                    SetNext(raw.FromAddr[Op](Trunc[uint](prev)), Next(c))
                }
                if Get64(d, off + 8) == cur {
                    Put64(d, off + 8, prev)
                }
                found = true
                break
            }
            prev = cur
            cur = nx
        }
        SpinUnlock(DescLock(d))
        if found {
            (*op).res = FailWith(core.SysErr.Canceled)
            PushDone(op)
            PollWake()
        }
    }
}

// Ready handles one edge for one direction of d: count it, take the whole
// wait list, retry each op in order; a finished op is delivered, an op
// that still gets EAGAIN is recorded again by the seq rule.
Ready(d: *u8, seqoff: int, headoff: int, out: mut []*Op, k: int): int {
    var n := k
    SpinLock(DescLock(d))
    Put32(d, seqoff, Get32(d, seqoff) +% 1)
    var cur := Get64(d, headoff)
    Put64(d, headoff, 0)
    Put64(d, headoff + 8, 0)
    SpinUnlock(DescLock(d))
    for cur != 0 {
        var op := raw.Null[Op]()
        unsafe "cur is the address of an op this thread took off the list" {
            op = raw.FromAddr[Op](Trunc[uint](cur))
        }
        cur = u64(raw.Addr(Next(op)))
        var first := true
        for {
            s := ReadSeq(op)
            if Try(op) {
                n = Deliver(op, out, n)
                break
            }
            if first {
                InjectEdge(op)
                first = false
            }
            rec := Record(op, s)
            if rec == 0 {
                break
            }
            if rec == 2 {
                n = Deliver(op, out, n)
                break
            }
        }
    }
    return n
}

// EpollReap waits up to timeout_ns (0: not at all; negative: forever) for
// edges, handles them, and returns the completions in out[k..]. The wait
// is epoll_pwait2 (nanoseconds) where the kernel has it, else epoll_pwait
// with the timeout rounded up to whole milliseconds.
EpollReap(timeout_ns: i64, out: mut []*Op, k: int): int {
    pp := PP()
    var events: [EventWords]u64 = [0; EventWords]
    var got := 0
    max := Trunc[uint](EvMax())
    unsafe "system call; events holds EvMax() epoll_events of the target's layout" {
        evp := raw.Addr(raw.DataMut(mut events[..]))
        for {
            var r := 0
            // Test point 6: the epoll_pwait path of kernels before 5.11
            // (where every kernel takes epoll: io_uring needs 5.11), which
            // a newer kernel never reaches otherwise.
            // Edge: none; a cache of what the kernel answered, the same for
            // every thread.
            if (*pp).pwait2.Load() != 2 && !TestOn(6) {
                var ts: [2]u64 = [0; 2]
                var tsp: uint = 0
                if timeout_ns >= 0 {
                    Timespec(raw.PtrCast[u64, u8](raw.Data(ts[..])), timeout_ns)
                    tsp = raw.Addr(raw.Data(ts[..]))
                }
                r = Syscall(Nrs().epoll_pwait2, (*pp).epfd, evp, max, tsp, 0, 8)
                // A cache of the kernel's answer: racing threads store the
                // same value; no edge needed.
                if r == -38 {
                    (*pp).pwait2.Store(2)
                    continue
                }
            } else {
                // Rounded up to whole milliseconds and capped at the int
                // the kernel takes. The cap is tested before the rounding
                // add, which would overflow (a panic) for a timeout within
                // 1 ms of i64's largest value (EpollMs).
                ms: i64 = if timeout_ns >= MaxWaitMs * 1_000_000 {
                    MaxWaitMs
                } else if timeout_ns >= 0 {
                    (timeout_ns + 999_999) / 1_000_000
                } else {
                    -1
                }
                r = Syscall(Nrs().epoll_pwait, (*pp).epfd, evp, max, Trunc[uint](ms), 0, 8)
            }
            // An error (EINTR included) reaps no event.
            if !IsErr(r) {
                got = r
            }
            break
        }
    }
    var n := TakeDone(out, k)
    eb := EvBytes()
    for i in 0..got {
        unsafe "the i-th epoll_event the kernel wrote" {
            e := raw.Offset(raw.PtrCast[u64, u8](raw.Data(events[..])), i * eb)
            mask := Get32(e, 0)
            fd := Get64(e, EvData())
            if fd >= u64(MaxFds) {
                // The wake eventfd, level-triggered: only a wait that could
                // block (the token holder's) drains it. A non-blocking reap
                // leaves it readable, so a PollWake meant for the thread
                // blocked in the kernel still wakes it (Go's netpoll reads
                // netpollEventFd only when delay != 0, for the same reason).
                if timeout_ns != 0 {
                    var drain: u64 = 0
                    // Edge: the init's Store of evfd, before any wait.
                    _ = Syscall(Nrs().read, Trunc[uint](Poll.evfd.Load()), raw.Addr(raw.OfMut(mut drain)), 8, 0, 0, 0)
                }
                continue
            }
            d := Desc(Trunc[uint](fd))
            if (mask & (EpollIn | EpollRdHup | EpollHup | EpollErr)) != 0 {
                n = Ready(d, DSeqRead, DHeadRead, out, n)
            }
            if (mask & (EpollOut | EpollHup | EpollErr)) != 0 {
                n = Ready(d, DSeqWrite, DHeadWrite, out, n)
            }
        }
    }
    return n
}

// ReadyTry is OpReady's try: ppoll for POLLIN with a zero timeout. true
// with res 0 when h is readable (or hung up, or in error: the read that
// follows says which); false to wait for epoll's next edge. ppoll's own
// error ends the op with it.
ReadyTry(op: *Op): bool {
    var pfd: [1]u64 = [0]
    var ts: [2]u64 = [0, 0]
    unsafe "system call; pfd is a struct pollfd (fd, events, revents), ts a zero timespec" {
        q := raw.PtrCast[u64, u8](raw.DataMut(mut pfd[..]))
        Put32(q, 0, Trunc[u32]((*op).h))
        Put16(q, 4, 1)
        for {
            r := Syscall(Nrs().ppoll, raw.Addr(q), 1, raw.Addr(raw.Data(ts[..])), 0, 8, 0)
            if r == -4 {
                continue
            }
            if IsErr(r) {
                (*op).res = Result(r)
                return true
            }
            if r == 0 {
                return false
            }
            (*op).res = Ok(0)
            return true
        }
    }
}
