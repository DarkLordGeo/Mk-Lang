// Why: std.net's ListenUnix and DialUnix, and handle passing between
// processes, need Unix-domain stream sockets on Linux.
//
// What: UnixListen, UnixUnbind, UnixConnect, the handle-passing calls
// UnixSendFds, UnixRecvFds, PassKind and PassReady, and Unix, AcceptUnixArea
// and PassMax; reached through std.rt's call entry (rt/unix.mk).
//
// How: a socket made here is a runtime socket as SocketOpen's TCP sockets
// are: close-on-exec, non-blocking and registered on epoll, blocking on
// io_uring, so the accept, recv and send entries serve it unchanged. Path
// names only: a path with a NUL (an abstract name) is refused, and a path
// takes at most 107 bytes and its NUL (sun_path). Errors are SockFail's, and
// EAFNOSUPPORT (97), EPROTONOSUPPORT (93) and EOPNOTSUPP (95, WSL's drives at
// bind) are Unsupported. Every path that made a descriptor closes it on
// every error path.

import std.core
import std.raw

/// Unix is the SockAddr family recorded for a Unix-domain socket's runtime
/// handle (its address is a path, which SockAddr does not hold).
export const Unix: u16 = 1

/// AcceptUnixArea is the address area an accept on a Unix listener lends in
/// op.buf (Windows' AcceptEx needs it); Linux's accept ignores it: accept4
/// cuts the peer's address to the 32 bytes of op.os, and a Unix peer
/// decodes to the zero address.
export const AcceptUnixArea: int = 256

// AfUnix is AF_UNIX, the socket call's domain and sun_family.
const AfUnix: uint = 1
// UnixPathMax is sun_path's size.
const UnixPathMax: int = 108
// UnixAddrBytes is the encoded sockaddr_un's buffer: 2 + 108, rounded up.
const UnixAddrBytes: int = 112
const UnixEagain: i64 = 11
const UnixEintr: i64 = 4
const FGetFl: uint = 3

// UnixFail is SockFail with the address family's absence as Unsupported.
UnixFail(errno: i64): OsResult {
    if errno == 97 || errno == 93 || errno == 95 {
        return OsResult(value: 0, err: core.SysErr.Unsupported, os: errno)
    }
    return SockFail(errno)
}

// UnixAddr writes path as a sockaddr_un at p (UnixAddrBytes bytes) and
// returns its length (the family, the path and its NUL), or 0 when path is
// empty, holds a NUL or does not fit sun_path.
UnixAddr(path: []u8, p: *u8): int {
    n := path.Len()
    if n == 0 || n >= UnixPathMax {
        return 0
    }
    unsafe "p holds UnixAddrBytes bytes" {
        raw.Fill(p, 0, UnixAddrBytes)
        Put16(p, 0, Trunc[u16](AfUnix))
        for i, x in path {
            if x == 0 {
                return 0
            }
            raw.Write(raw.Offset(p, 2 + i), x)
        }
    }
    return 2 + n + 1
}

// UnixSocket makes a close-on-exec stream socket, non-blocking when nb.
UnixSocket(nb: bool): OsResult {
    ty := if nb { SockStream | SockCloExec | SockNonBlock } else { SockStream | SockCloExec }
    unsafe "a system call that passes only values" {
        r := Syscall(Nrs().socket, AfUnix, ty, 0, 0, 0, 0)
        return if IsErr(r) { UnixFail(i64(-r)) } else { Ok(i64(r)) }
    }
}

// UnixClose closes fd on an error path and returns r.
UnixClose(fd: uint, r: OsResult): OsResult {
    unsafe "a system call that passes only values" {
        _ = Syscall(Nrs().close, fd, 0, 0, 0, 0, 0)
    }
    return r
}

/// UnixListen makes a Unix-domain stream socket bound to path and listening
/// (backlog <= 0 asks for the maximum), ready for the poller; value is the
/// descriptor. InvalidAddress for an empty path, one with a NUL or one of
/// 108 bytes or more, before any call; EADDRINUSE when path exists (the
/// caller removes a stale socket file; this call removes only the file its
/// own bind made, when the listen after it fails). A file system
/// without sockets (WSL's drive) answers bind's error.
/// O(1) system calls; allocates nothing.
export UnixListen(path: []u8, backlog: int): OsResult {
    var sa: [14]u64 = [0; 14]
    var n := 0
    unsafe "sa holds UnixAddrBytes bytes" {
        n = UnixAddr(path, raw.PtrCast[u64, u8](raw.DataMut(mut sa[..])))
    }
    if n == 0 {
        return FailWith(core.SysErr.InvalidAddress)
    }
    epoll := !IsUring()
    s := UnixSocket(epoll)
    if s.err != core.SysErr.None {
        return s
    }
    fd := Trunc[uint](s.value)
    b := if backlog > 0 && backlog < 0x7FFF_FFFF { Trunc[uint](backlog) } else { SoMaxConn }
    unsafe "system calls; sa is the encoded address" {
        r := Syscall(Nrs().bind, fd, raw.Addr(raw.Data(sa[..])), Trunc[uint](n), 0, 0, 0)
        if IsErr(r) {
            return UnixClose(fd, UnixFail(i64(-r)))
        }
        r2 := Syscall(Nrs().listen, fd, b, 0, 0, 0, 0)
        if IsErr(r2) {
            failed := UnixClose(fd, UnixFail(i64(-r2)))
            UnixUnbind(path)
            return failed
        }
    }
    if epoll && !EpollAdd(fd) {
        r := UnixClose(fd, Fail(24))
        UnixUnbind(path)
        return r
    }
    return Ok(s.value)
}

/// UnixUnbind removes the socket file a UnixListen's bind made at path,
/// when a later step failed and no Listener holds it, so the next listen
/// at path is not EADDRINUSE. Only after this process's own successful
/// bind there: the file is then this process's. An encoding UnixListen
/// refuses removes nothing. O(1) system calls; allocates nothing.
export UnixUnbind(path: []u8) {
    var sa: [14]u64 = [0; 14]
    unsafe "sa holds UnixAddrBytes bytes; sun_path starts at byte 2 and ends in a NUL (UnixAddr refuses 108 bytes)" {
        p := raw.PtrCast[u64, u8](raw.DataMut(mut sa[..]))
        if UnixAddr(path, p) == 0 {
            return
        }
        _ = Syscall(Nrs().unlinkat, AtFdcwd, raw.Addr(p) + 2, 0, 0, 0, 0)
    }
}

/// UnixConnect connects a new Unix-domain stream socket to path, without
/// waiting: value is the descriptor, ready for the poller, or -1 with no
/// descriptor held when the listener's backlog is full (EAGAIN; the caller
/// sleeps and asks again, rt.UnixDial). InvalidAddress as UnixListen;
/// ECONNREFUSED when nothing listens at path, ENOENT when path does not
/// exist. The connect is non-blocking, so a carrier never waits in it; on
/// io_uring the connected socket is then made blocking, as the ring's
/// sockets are. O(1) system calls; allocates nothing.
export UnixConnect(path: []u8): OsResult {
    var sa: [14]u64 = [0; 14]
    var n := 0
    unsafe "sa holds UnixAddrBytes bytes" {
        n = UnixAddr(path, raw.PtrCast[u64, u8](raw.DataMut(mut sa[..])))
    }
    if n == 0 {
        return FailWith(core.SysErr.InvalidAddress)
    }
    s := UnixSocket(true)
    if s.err != core.SysErr.None {
        return s
    }
    fd := Trunc[uint](s.value)
    unsafe "system calls; sa is the encoded address" {
        r := Syscall(Nrs().connect, fd, raw.Addr(raw.Data(sa[..])), Trunc[uint](n), 0, 0, 0)
        if IsErr(r) {
            e := i64(-r)
            // A Unix connect is never in progress: EAGAIN is a full
            // backlog, and EINTR left no connection behind (the socket is
            // closed either way).
            if e == UnixEagain || e == UnixEintr {
                return UnixClose(fd, Ok(-1))
            }
            return UnixClose(fd, UnixFail(e))
        }
        if IsUring() {
            fl := Syscall(Nrs().fcntl, fd, FGetFl, 0, 0, 0, 0)
            if IsErr(fl) {
                return UnixClose(fd, Fail(i64(-fl)))
            }
            r2 := Syscall(Nrs().fcntl, fd, FSetFl, Trunc[uint](fl) & ~ONonBlock, 0, 0, 0)
            if IsErr(r2) {
                return UnixClose(fd, Fail(i64(-r2)))
            }
        } else if !EpollAdd(fd) {
            return UnixClose(fd, Fail(24))
        }
    }
    return Ok(s.value)
}

// Handle passing: SCM_RIGHTS over a Unix-domain stream socket, for
// std.net's Stream.SendHandles and RecvHandles through std.rt's SendFds and
// RecvFds (rt/passed.mk). Every call is MSG_DONTWAIT, so it never blocks a
// carrier on either backend; std.rt waits between tries.

/// PassMax is the most descriptors one message carries, each way: a
/// SendHandles names at most this many, and a RecvHandles makes room for
/// this many (the kernel's own limit, SCM_MAX_FD, is 253).
export const PassMax: int = 64

const ScmRights: u32 = 1
const MsgCmsgCloexec: uint = 0x4000_0000
const MsgCtrunc: u32 = 8
const SoType: uint = 3
const SoAcceptConn: uint = 30
const UnixEmsgsize: i64 = 90
// PassCtlBytes is the control buffer: a cmsghdr (16 bytes) and PassMax
// descriptors of 4 bytes.
const PassCtlBytes: int = 272

// NrSendmsg and NrRecvmsg are the two calls' numbers, which nr.mk's table
// does not hold: x86_64 46 and 47, arm64 211 and 212.
NrSendmsg(): uint {
    return if Target.arch == Arch.Arm64 { 211 } else { 46 }
}

NrRecvmsg(): uint {
    return if Target.arch == Arch.Arm64 { 212 } else { 47 }
}

// PassMsg fills the msghdr at m (56 bytes) for one iovec at v (16 bytes)
// over the n bytes at p, and the control buffer ctl of cn bytes (none when
// cn is 0).
PassMsg(m: *u8, v: *u8, p: *u8, n: int, ctl: *u8, cn: int) {
    unsafe "m holds 56 bytes and v 16, both in the caller's frame" {
        raw.Fill(m, 0, 56)
        Put64(v, 0, u64(raw.Addr(p)))
        Put64(v, 8, Trunc[u64](n))
        Put64(m, 16, u64(raw.Addr(v)))
        Put64(m, 24, 1)
        if cn > 0 {
            Put64(m, 32, u64(raw.Addr(ctl)))
            Put64(m, 40, Trunc[u64](cn))
        }
    }
}

/// UnixSendFds sends data with the descriptors fds attached to its first
/// byte (SCM_RIGHTS), without waiting: value the bytes sent, at least 1,
/// for the caller to send the rest of as plain bytes; -1 when the socket's
/// buffer is full and nothing was sent (EAGAIN: the caller waits and asks
/// again). The caller gives at least one byte and 1 to PassMax
/// descriptors; anything else is Other before any call. A peer gone is
/// EPIPE (no SIGPIPE: MSG_NOSIGNAL). O(1): one system call; allocates
/// nothing.
export UnixSendFds(fd: uint, data: []u8, fds: []uint): OsResult {
    n := fds.Len()
    if data.Len() == 0 || n == 0 || n > PassMax {
        return FailWith(core.SysErr.Other)
    }
    var m: [7]u64 = [0; 7]
    var v: [2]u64 = [0; 2]
    var ctl: [34]u64 = [0; 34]
    unsafe "m, v and ctl are this frame's; data is the caller's for the call" {
        c := raw.PtrCast[u64, u8](raw.DataMut(mut ctl[..]))
        Put64(c, 0, Trunc[u64](16 + 4 * n))
        Put32(c, 8, Trunc[u32](SolSocket))
        Put32(c, 12, ScmRights)
        for i, x in fds {
            Put32(c, 16 + 4 * i, Trunc[u32](x))
        }
        mp := raw.PtrCast[u64, u8](raw.DataMut(mut m[..]))
        PassMsg(mp, raw.PtrCast[u64, u8](raw.DataMut(mut v[..])), raw.Data(data), data.Len(), c,
            (16 + 4 * n + 7) / 8 * 8)
        for {
            r := Syscall(NrSendmsg(), fd, raw.Addr(mp), MsgDontWait | MsgNoSignal, 0, 0, 0)
            if !IsErr(r) {
                return Ok(i64(r))
            }
            e := i64(-r)
            if e == UnixEagain {
                return Ok(-1)
            }
            if e != UnixEintr {
                return SockFail(e)
            }
        }
    }
}

/// UnixRecvFds receives at most buf.Len() bytes (at least 1) and the
/// descriptors that came with them, without waiting: value the bytes, 0 at
/// the end of the stream, -1 when nothing is queued (EAGAIN: the caller
/// waits for input and asks again); os the count of descriptors written to
/// fds (which has room for PassMax), each close-on-exec
/// (MSG_CMSG_CLOEXEC). A message that carried more descriptors than
/// PassMax (MSG_CTRUNC: the kernel closed those that did not fit) closes
/// the ones that did too, and is Other with os 90 (EMSGSIZE); its bytes
/// are consumed. O(1) system calls besides those closes; allocates
/// nothing.
export UnixRecvFds(fd: uint, buf: mut []u8, fds: mut []uint): OsResult {
    if buf.Len() == 0 || fds.Len() < PassMax {
        return FailWith(core.SysErr.Other)
    }
    var m: [7]u64 = [0; 7]
    var v: [2]u64 = [0; 2]
    var ctl: [34]u64 = [0; 34]
    unsafe "m, v and ctl are this frame's; buf is the caller's for the call" {
        c := raw.PtrCast[u64, u8](raw.DataMut(mut ctl[..]))
        mp := raw.PtrCast[u64, u8](raw.DataMut(mut m[..]))
        PassMsg(mp, raw.PtrCast[u64, u8](raw.DataMut(mut v[..])), raw.DataMut(buf), buf.Len(), c,
            PassCtlBytes)
        var r := 0
        for {
            r = Syscall(NrRecvmsg(), fd, raw.Addr(mp), MsgDontWait | MsgCmsgCloexec, 0, 0, 0)
            if !IsErr(r) {
                break
            }
            e := i64(-r)
            if e == UnixEagain {
                return Ok(-1)
            }
            if e != UnixEintr {
                return SockFail(e)
            }
        }
        // Every SCM_RIGHTS header the kernel wrote, in order (a stream
        // socket gives one per call; the loop does not rely on it).
        var k := 0
        clen := Trunc[int](Get64(mp, 40))
        var off := 0
        for off + 16 <= clen {
            l := Trunc[int](Get64(c, off))
            if l < 16 || off + l > clen {
                break
            }
            if Get32(c, off + 8) == Trunc[u32](SolSocket) && Get32(c, off + 12) == ScmRights {
                for j in 0..(l - 16) / 4 {
                    d := uint(Get32(c, off + 16 + 4 * j))
                    if k < PassMax {
                        fds[k] = d
                        k += 1
                    } else {
                        _ = Syscall(Nrs().close, d, 0, 0, 0, 0, 0)
                    }
                }
            }
            off += (l + 7) / 8 * 8
        }
        if Get32(mp, 48) & MsgCtrunc != 0 {
            for j in 0..k {
                _ = Syscall(Nrs().close, fds[j], 0, 0, 0, 0, 0)
            }
            return OsResult(value: 0, err: core.SysErr.Other, os: UnixEmsgsize)
        }
        return OsResult(value: i64(r), err: core.SysErr.None, os: i64(k))
    }
}

/// PassKind says what the descriptor fd, passed to this process, is, as
/// std.net types it: value 0 for anything but a socket (a file, a pipe, a
/// device), 1 for a stream socket that is not listening, 2 for a listening
/// one, 3 for any other socket (datagram, seqpacket); os a stream socket's
/// family (Unix, Inet4 or Inet6) with its bound port in bits 16 and up
/// (TCP only). A value naming nothing is EBADF (fstat's answer). O(1)
/// system calls; allocates nothing.
export PassKind(fd: uint): OsResult {
    var st: [18]u64 = [0; 18]
    if !Stat(fd, mut st) {
        return Fail(9)
    }
    if HandleKind(fd) != 4 {
        return Ok(0)
    }
    var ty: [1]i32 = [0]
    var acc: [1]i32 = [0]
    var sa: [4]u64 = [0; 4]
    var n: [1]u32 = [4]
    unsafe "system calls; ty, acc, n and sa are this frame's" {
        r := Syscall(Nrs().getsockopt, fd, SolSocket, SoType, raw.Addr(raw.DataMut(mut ty[..])),
            raw.Addr(raw.DataMut(mut n[..])), 0)
        if IsErr(r) {
            return SockFail(i64(-r))
        }
        if ty[0] != 1 {
            return Ok(3)
        }
        n[0] = 4
        r2 := Syscall(Nrs().getsockopt, fd, SolSocket, SoAcceptConn, raw.Addr(raw.DataMut(mut acc[..])),
            raw.Addr(raw.DataMut(mut n[..])), 0)
        if IsErr(r2) {
            return SockFail(i64(-r2))
        }
        kind: i64 = if acc[0] != 0 { 2 } else { 1 }
        n[0] = 32
        r3 := Syscall(Nrs().getsockname, fd, raw.Addr(raw.DataMut(mut sa[..])), raw.Addr(raw.DataMut(mut n[..])), 0,
            0, 0)
        if IsErr(r3) {
            return SockFail(i64(-r3))
        }
        p := raw.PtrCast[u64, u8](raw.Data(sa[..]))
        if raw.Read(raw.PtrCast[u8, u16](p)) == Trunc[u16](AfUnix) {
            return OsResult(value: kind, err: core.SysErr.None, os: i64(Unix))
        }
        var a := SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0)
        if !DecodeAddr(p, mut a) {
            return Ok(3)
        }
        return OsResult(value: kind, err: core.SysErr.None, os: i64(a.family) | (i64(a.port) << 16))
    }
}

/// PassReady readies a descriptor passed to this process: close-on-exec,
/// and a socket also for the poller as SocketOpen's are, on the epoll
/// backend non-blocking and registered (io_uring needs neither; the flag
/// belongs to the open file, which the sender shares, and a non-blocking
/// socket serves either backend). O(1) system calls; allocates nothing.
export PassReady(fd: uint, socket: bool): OsResult {
    unsafe "a system call that passes no memory, only values and handles" {
        c := Syscall(Nrs().fcntl, fd, FSetFd, 1, 0, 0, 0)
        if IsErr(c) {
            return Result(c)
        }
        if !socket || IsUring() {
            return Ok(0)
        }
        fl := Syscall(Nrs().fcntl, fd, FGetFl, 0, 0, 0, 0)
        if IsErr(fl) {
            return Result(fl)
        }
        nb := Syscall(Nrs().fcntl, fd, FSetFl, Trunc[uint](fl) | ONonBlock, 0, 0, 0)
        if IsErr(nb) {
            return Result(nb)
        }
    }
    if !EpollAdd(fd) {
        return Fail(24)
    }
    return Ok(0)
}
