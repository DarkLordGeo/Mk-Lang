// Why: each poller operation is tried at once and, when it would wait,
// recorded against its descriptor's edge count, so no edge kqueue reports
// between the try and the record is lost.
//
// What: the per-operation half of the kqueue backend (poll.mk): Try and its
// kinds (reads, writes, accept, connect, datagrams, OpReady), Record,
// InjectEdge, KqSubmit, KqCancel and Ready.
//
// How: Linux epoll's design (std/sys/linux/poll_epoll.mk), on the 64-byte
// descriptor records of poll.mk: a try that answers EAGAIN appends the op to
// its direction's wait list under the record's lock, unless the edge count
// moved since the try, in which case it tries again.

import std.core
import std.raw
import std.sys.lc

// IsWriteDir: connect, send, write and sendto wait for writability.
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

// Clip bounds one call's length (MaxIo: Darwin refuses a count over
// INT_MAX).
Clip(n: int): uint {
    if n <= 0 {
        return 0
    }
    return if n > MaxIo { Trunc[uint](MaxIo) } else { Trunc[uint](n) }
}

// Try makes op's non-blocking call once (EINTR repeated at once). true: op
// finished, res set; false: EAGAIN, it must wait for an edge.
Try(op: *Op): bool {
    unsafe "op is owned by this thread: being submitted, or taken off its wait list" {
        fd := (*op).h
        s := Trunc[i32](fd)
        o := OsArea(op)
        k := (*op).kind
        for {
            var r := 0
            if k == OpRecv {
                r = recvfrom(s, (*op).buf, Clip((*op).len), 0, raw.Null[u8](), raw.Null[u32]())
            } else if k == OpSend {
                // SocketOpen's and accepted sockets have SO_NOSIGPIPE, and
                // the process ignores SIGPIPE (fault.mk): a closed peer is
                // EPIPE.
                r = sendto(s, (*op).buf, Clip((*op).len), 0, raw.Null[u8](), 0)
            } else if k == OpRead {
                // pos >= 0 is pread (a pipe or socket answers ESPIPE).
                r = ReadCall(fd, (*op).buf, Trunc[int](Clip((*op).len)), (*op).pos)
            } else if k == OpWrite {
                r = WriteCall(fd, (*op).buf, Trunc[int](Clip((*op).len)), (*op).pos)
            } else if k == OpAccept {
                Put32(o, OsAddrN, 32)
                r = int(accept(s, raw.Offset(o, OsAddr), raw.PtrCast[u8, u32](raw.Offset(o, OsAddrN))))
                if r >= 0 {
                    if AcceptDone(op, Trunc[uint](r)) {
                        return true
                    }
                    continue
                }
            } else if k == OpConnect {
                return ConnectCheck(op)
            } else if k == OpReady {
                return ReadyTry(op)
            } else if k == OpRecvFrom {
                r = RecvDgram(op)
                if r >= 0 {
                    DgramDone(op, r, (Get32(o, OsMsg + 44) & Trunc[u32](MsgTrunc)) != 0)
                    return true
                }
            } else if k == OpSendTo {
                n := EncodeAddr(&(*op).addr, raw.Offset(o, OsAddr))
                if n == 0 {
                    (*op).res = FailWith(core.SysErr.InvalidAddress)
                    return true
                }
                r = sendto(s, (*op).buf, Clip((*op).len), 0, raw.Offset(o, OsAddr), Trunc[u32](n))
            }
            if r >= 0 {
                (*op).res = Ok(i64(r))
                return true
            }
            e := Errno()
            if e == Eintr || (k == OpAccept && e == Econnaborted) {
                continue
            }
            if e == Eagain {
                return false
            }
            // An error by kind and handle.
            (*op).res = IoFail(fd, k, e)
            return true
        }
    }
}

// RecvDgram is one recvmsg of a datagram into op.buf: the msghdr at OsMsg
// names the sockaddr at OsAddr (32 bytes) and the iovec at OsIov. An empty
// op.buf receives into the byte at OsScratch instead: Darwin's recvmsg of
// no bytes returns 0 at once with no datagram taken and no address when
// nothing is queued, where it must wait; DgramDone cuts the count to 0. Darwin's
// struct msghdr: name at 0, namelen at 8, iov at 16, iovlen at 24, control
// at 32, controllen at 40, flags at 44 (48 bytes); struct iovec: base at 0,
// len at 8. The count, or -1 with errno set.
RecvDgram(op: *Op): int {
    unsafe "op is owned by this thread; its os area holds the msghdr, iovec and sockaddr" {
        o := OsArea(op)
        m := raw.Offset(o, OsMsg)
        v := raw.Offset(o, OsIov)
        raw.Fill(m, 0, 48)
        if (*op).len <= 0 {
            Put64(v, 0, u64(raw.Addr(raw.Offset(o, OsScratch))))
            Put64(v, 8, 1)
        } else {
            Put64(v, 0, u64(raw.Addr((*op).buf)))
            Put64(v, 8, u64(Clip((*op).len)))
        }
        Put64(m, 0, u64(raw.Addr(raw.Offset(o, OsAddr))))
        Put32(m, 8, 32)
        Put64(m, 16, u64(raw.Addr(v)))
        Put32(m, 24, 1)
        return recvmsg(Trunc[i32]((*op).h), m, 0)
    }
}

// SockSetup makes s close-on-exec and SIGPIPE-free (SO_NOSIGPIPE: Darwin
// has no MSG_NOSIGNAL), and non-blocking when nonblock: 0, or the errno.
SockSetup(s: i32, nonblock: bool): i64 {
    unsafe "fcntl and setsockopt; one is a 4-byte option value in this frame" {
        if fcntl(s, FSetFd, FdCloExec) == -1 {
            return Errno()
        }
        if nonblock {
            fl := fcntl(s, FGetFl)
            if fl == -1 {
                return Errno()
            }
            if fcntl(s, FSetFl, fl | ONonBlock) == -1 {
                return Errno()
            }
        }
        var one: [1]u32 = [1]
        if setsockopt(s, SolSocket, SoNoSigPipe, raw.PtrCast[u32, u8](raw.Data(one[..])), 4) == -1 {
            return Errno()
        }
    }
    return 0
}

// AcceptDone readies the new socket s (close-on-exec, non-blocking,
// SO_NOSIGPIPE: set here, never assumed from the listener), registers it
// and reads the peer's address; a socket that cannot be readied or
// registered is closed (no leak) and the accept fails. Darwin's accept can
// hand out a connection its peer reset while it was queued, with no
// address (length 0), and its setsockopt then answers EINVAL (a socket shut
// both ways takes no option): that one is closed and false asks the caller
// to accept again, as for ECONNABORTED. true: op finished, res set.
AcceptDone(op: *Op, s: uint): bool {
    unsafe "op is owned by this thread" {
        e := SockSetup(Trunc[i32](s), true)
        if e == Einval {
            _ = lc.CloseFd(Trunc[i32](s))
            return false
        }
        if e != 0 {
            _ = lc.CloseFd(Trunc[i32](s))
            (*op).res = SockFail(e)
            return true
        }
        if !KqAdd(s) {
            _ = lc.CloseFd(Trunc[i32](s))
            (*op).res = Fail(Emfile)
            return true
        }
        o := OsArea(op)
        if Get32(o, OsAddrN) == 0 || !DecodeAddr(raw.Offset(o, OsAddr), mut (*op).addr) {
            (*op).addr = SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0)
        }
        (*op).res = Ok(Trunc[i64](s))
        return true
    }
}

// ConnectCheck finishes a connect that answered EINPROGRESS: SO_ERROR
// reports a failure; a zero SO_ERROR is success only when the socket has a
// peer (an edge can arrive before the handshake ends), else it keeps
// waiting (Go's rule).
ConnectCheck(op: *Op): bool {
    unsafe "op is owned by this thread" {
        s := Trunc[i32]((*op).h)
        var err: [1]u32 = [0]
        var n: [1]u32 = [4]
        if getsockopt(s, SolSocket, SoError, raw.PtrCast[u32, u8](raw.DataMut(mut err[..])), raw.DataMut(mut n[..])) ==
            -1 {
            (*op).res = SockFail(Errno())
            return true
        }
        if err[0] != 0 {
            (*op).res = ConnectFail(i64(err[0]))
            return true
        }
        var sa: [4]u64 = [0; 4]
        var sn: [1]u32 = [32]
        if getpeername(s, raw.PtrCast[u64, u8](raw.DataMut(mut sa[..])), raw.DataMut(mut sn[..])) == -1 {
            e := Errno()
            if e == Enotconn {
                return false
            }
            (*op).res = SockFail(e)
            return true
        }
        (*op).res = Ok(0)
        return true
    }
}

// ReadyTry is OpReady's try: poll for POLLIN with a zero timeout. true with
// res 0 when h is readable (or hung up, or in error: the read that follows
// says which); false to wait for kqueue's next read edge. POLLNVAL (a
// descriptor poll does not serve) ends the op with EBADF, never a wait
// that no edge would end; poll's own error ends the op with it.
ReadyTry(op: *Op): bool {
    var pfd: [1]u64 = [0]
    unsafe "pfd is a struct pollfd (fd, events, revents)" {
        q := raw.PtrCast[u64, u8](raw.DataMut(mut pfd[..]))
        Put32(q, 0, Trunc[u32]((*op).h))
        Put16(q, 4, PollIn)
        for {
            r := lc.PollFds(q, 1, 0)
            if r == -1 {
                e := Errno()
                if e == Eintr {
                    continue
                }
                (*op).res = Fail(e)
                return true
            }
            if r == 0 {
                return false
            }
            if (Get16(q, 6) & PollNval) != 0 {
                // Darwin's poll serves no device: a terminal's readiness is
                // TermReadyNow's (child.mk), a zero-timeout kqueue read.
                t := TermReadyNow((*op).h)
                if t == 0 {
                    return false
                }
                (*op).res = if t > 0 { Ok(0) } else { Fail(-t) }
                return true
            }
            (*op).res = Ok(0)
            return true
        }
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
            _ = KqReap(0, mut nothing[..0], 0)
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

// Attached reports whether op's descriptor is registered for the filter
// op waits on (read, or write for IsWriteDir).
Attached(op: *Op): bool {
    unsafe "op's descriptor record" {
        d := Desc((*op).h)
        need: u32 = if IsWriteDir((*op).kind) { 2 } else { 1 }
        SpinLock(DescLock(d))
        bits := Get32(d, DAttach)
        SpinUnlock(DescLock(d))
        return (bits & need) != 0
    }
}

// Unwatched reports whether op's descriptor is a character device attached
// with no kqueue filter for op's direction (KqAdd's bit 4).
Unwatched(op: *Op): bool {
    unsafe "op's descriptor record" {
        d := Desc((*op).h)
        SpinLock(DescLock(d))
        bits := Get32(d, DAttach)
        SpinUnlock(DescLock(d))
        return (bits & 4) != 0
    }
}

// KqSubmit tries op and records it when it must wait for an edge: true
// when it is pending, false when it finished with res set.
KqSubmit(op: *Op): bool {
    unsafe "op is the caller's operation" {
        fd := (*op).h
        if fd >= Trunc[uint](MaxFds) {
            (*op).res = FailWith(core.SysErr.Other)
            return false
        }
        // A connect is made whether or not the descriptor is attached, as
        // on Linux: a blocking socket (SocketOpen with async false, never
        // registered) connects in the call. Only a connect that would go on
        // in the background needs the write edge, so only that one is
        // refused on an unattached descriptor.
        if (*op).kind == OpConnect {
            attached := Attached(op)
            o := OsArea(op)
            n := EncodeAddr(&(*op).addr, raw.Offset(o, OsAddr))
            if n == 0 {
                (*op).res = FailWith(core.SysErr.InvalidAddress)
                return false
            }
            seq := ReadSeq(op)
            if connect(Trunc[i32](fd), raw.Offset(o, OsAddr), Trunc[u32](n)) == 0 {
                (*op).res = Ok(0)
                return false
            }
            e := Errno()
            // EINTR: the connect goes on in the background, as EINPROGRESS.
            if e != Einprogress && e != Eintr {
                (*op).res = ConnectFail(e)
                return false
            }
            if !attached {
                (*op).res = FailWith(core.SysErr.Other)
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
        if !Attached(op) {
            if Unwatched(op) && Try(op) {
                return false
            }
            (*op).res = FailWith(if Unwatched(op) { core.SysErr.Unsupported } else { core.SysErr.Other })
            return false
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

// KqCancel cancels op: an op still on its wait list is taken off and
// queued as Canceled on the done list (then PollWake, so a PollWait hands
// it out). An op not on the list is being retried by a handler, which sees
// cancel before recording it again, or has already completed.
KqCancel(op: *Op) {
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
