// Why: std.net's Stream.SendHandles and RecvHandles pass descriptors
// between processes over a Unix-domain socket, and a child started with
// named handles must find them.
//
// What: PassMax, UnixSendFds, UnixRecvFds, PassKind, PassReady and
// HandlesValue, with PassHandlesSeal, the start-up check behind
// HandlesValue's verdict.
//
// How: SCM_RIGHTS through sendmsg and recvmsg, for std.rt's SendFds and
// RecvFds (rt/passed.mk). Every call is MSG_DONTWAIT, so it never blocks a
// carrier; std.rt waits between tries. Darwin's struct msghdr is 48 bytes
// (msg_name at 0, msg_namelen u32 at 8, msg_iov at 16, msg_iovlen i32 at
// 24, msg_control at 32, msg_controllen u32 at 40, msg_flags i32 at 44) and
// its struct cmsghdr 12 (cmsg_len u32, cmsg_level i32 at 4, cmsg_type i32
// at 8), with data and each next header at 4-byte alignment
// (__DARWIN_ALIGN32). Darwin has no MSG_CMSG_CLOEXEC, so each received
// descriptor is made close-on-exec by fcntl right after recvmsg, and no
// MSG_NOSIGNAL in every release, so a send relies on SO_NOSIGPIPE
// (unix.mk) and std.sys's ignored SIGPIPE (fault.mk).

import std.core
import std.raw
import std.sys.lc

/// PassMax is the most descriptors one message carries, each way: a
/// SendHandles names at most this many, and a RecvHandles makes room for
/// this many.
export const PassMax: int = 64

const PassSolSocket: u32 = 0xFFFF
const PassScmRights: u32 = 1
const PassMsgDontWait: i32 = 0x80
const PassMsgCtrunc: u32 = 0x20
const PassSoType: i32 = 0x1008
const PassSoAcceptConn: i32 = 0x2
// PassEmsgsize is Darwin's EMSGSIZE.
const PassEmsgsize: i64 = 40
const PassEbadf: i64 = 9
// PassCmsgBytes is sizeof(struct cmsghdr), already 4-byte aligned.
const PassCmsgBytes: int = 12
// PassCtlBytes is the control buffer: a cmsghdr and PassMax descriptors of
// 4 bytes, rounded up to 8.
const PassCtlBytes: int = 272

// PassMsg fills the msghdr at m (48 bytes) for one iovec at v (16 bytes)
// over the n bytes at p, and the control buffer ctl of cn bytes (none when
// cn is 0).
PassMsg(m: *u8, v: *u8, p: *u8, n: int, ctl: *u8, cn: int) {
    unsafe "m holds 48 bytes and v 16, both in the caller's frame" {
        raw.Fill(m, 0, 48)
        UxPut64(v, 0, u64(raw.Addr(p)))
        UxPut64(v, 8, Trunc[u64](n))
        UxPut64(m, 16, u64(raw.Addr(v)))
        UxPut32(m, 24, 1)
        if cn > 0 {
            UxPut64(m, 32, u64(raw.Addr(ctl)))
            UxPut32(m, 40, Trunc[u32](cn))
        }
    }
}

/// UnixSendFds sends data with the descriptors fds attached to its first
/// byte (SCM_RIGHTS), without waiting: value the bytes sent, at least 1,
/// for the caller to send the rest of as plain bytes; -1 when the socket's
/// buffer is full and nothing was sent (EAGAIN: the caller waits and asks
/// again). The caller gives at least one byte and 1 to PassMax
/// descriptors; anything else is Other before any call. A peer gone is
/// EPIPE, reported as Reset (no SIGPIPE: see the header). O(1): one system
/// call; allocates nothing.
export UnixSendFds(fd: uint, data: []u8, fds: []uint): OsResult {
    n := fds.Len()
    if data.Len() == 0 || n == 0 || n > PassMax {
        return FailWith(core.SysErr.Other)
    }
    var m: [6]u64 = [0; 6]
    var v: [2]u64 = [0; 2]
    var ctl: [34]u64 = [0; 34]
    unsafe "m, v and ctl are this frame's; data is the caller's for the call" {
        c := raw.PtrCast[u64, u8](raw.DataMut(mut ctl[..]))
        UxPut32(c, 0, Trunc[u32](PassCmsgBytes + 4 * n))
        UxPut32(c, 4, PassSolSocket)
        UxPut32(c, 8, PassScmRights)
        for i, x in fds {
            UxPut32(c, PassCmsgBytes + 4 * i, Trunc[u32](x))
        }
        mp := raw.PtrCast[u64, u8](raw.DataMut(mut m[..]))
        PassMsg(mp, raw.PtrCast[u64, u8](raw.DataMut(mut v[..])), raw.Data(data), data.Len(), c,
            PassCmsgBytes + 4 * n)
        for {
            r := sendmsg(Trunc[i32](fd), mp, PassMsgDontWait)
            if r >= 0 {
                return Ok(i64(r))
            }
            e := Errno()
            if e == UxEagain {
                return Ok(-1)
            }
            if e != UxEintr {
                return SockFail(e)
            }
        }
    }
}

/// UnixRecvFds receives at most buf.Len() bytes (at least 1) and the
/// descriptors that came with them, without waiting: value the bytes, 0 at
/// the end of the stream, -1 when nothing is queued (EAGAIN: the caller
/// waits for input and asks again); os the count of descriptors written to
/// fds (which has room for PassMax), each made close-on-exec. A message
/// whose descriptors did not fit the control buffer (MSG_CTRUNC; those
/// that did not fit never reach this process as descriptors) closes the
/// ones that did too, and is Other with os 40 (EMSGSIZE); its bytes are consumed. O(1)
/// system calls besides one fcntl or close per descriptor; allocates
/// nothing.
export UnixRecvFds(fd: uint, buf: mut []u8, fds: mut []uint): OsResult {
    if buf.Len() == 0 || fds.Len() < PassMax {
        return FailWith(core.SysErr.Other)
    }
    var m: [6]u64 = [0; 6]
    var v: [2]u64 = [0; 2]
    var ctl: [34]u64 = [0; 34]
    unsafe "m, v and ctl are this frame's; buf is the caller's for the call" {
        c := raw.PtrCast[u64, u8](raw.DataMut(mut ctl[..]))
        mp := raw.PtrCast[u64, u8](raw.DataMut(mut m[..]))
        PassMsg(mp, raw.PtrCast[u64, u8](raw.DataMut(mut v[..])), raw.DataMut(buf), buf.Len(), c,
            PassCtlBytes)
        var r := 0
        for {
            r = recvmsg(Trunc[i32](fd), mp, PassMsgDontWait)
            if r >= 0 {
                break
            }
            e := Errno()
            if e == UxEagain {
                return Ok(-1)
            }
            if e != UxEintr {
                return SockFail(e)
            }
        }
        // Every SCM_RIGHTS header the kernel wrote, in order (a stream
        // socket gives one per call; the loop does not rely on it).
        var k := 0
        clen := Trunc[int](u64(UxGet32(mp, 40)))
        var off := 0
        for off + PassCmsgBytes <= clen {
            l := Trunc[int](u64(UxGet32(c, off)))
            if l < PassCmsgBytes || off + l > clen {
                break
            }
            if UxGet32(c, off + 4) == PassSolSocket && UxGet32(c, off + 8) == PassScmRights {
                for j in 0..(l - PassCmsgBytes) / 4 {
                    d := Trunc[i32](UxGet32(c, off + PassCmsgBytes + 4 * j))
                    if k < PassMax {
                        _ = fcntl(d, UxFSetFd, UxFdCloExec)
                        fds[k] = uint(Trunc[u32](d))
                        k += 1
                    } else {
                        _ = lc.CloseFd(d)
                    }
                }
            }
            off += (l + 3) / 4 * 4
        }
        if UxGet32(mp, 44) & PassMsgCtrunc != 0 {
            for j in 0..k {
                _ = lc.CloseFd(Trunc[i32](fds[j]))
            }
            return OsResult(value: 0, err: core.SysErr.Other, os: PassEmsgsize)
        }
        return OsResult(value: i64(r), err: core.SysErr.None, os: i64(k))
    }
}

/// PassKind says what the descriptor fd, passed to this process, is, as
/// std.net types it: value 0 for anything but a socket (a file, a pipe, a
/// device), 1 for a stream socket that is not listening, 2 for a listening
/// one, 3 for any other socket (datagram, seqpacket, or a family other
/// than Unix, IPv4 and IPv6); os a stream socket's family (Unix, Inet4 or
/// Inet6) with its bound port in bits 16 and up (TCP only). A value naming
/// nothing is EBADF (fstat's answer). O(1) system calls; allocates nothing.
export PassKind(fd: uint): OsResult {
    var st: [18]u64 = [0; 18]
    if StatFd(fd, mut st) != 0 {
        return Fail(PassEbadf)
    }
    if HandleKind(fd) != 4 {
        return Ok(0)
    }
    h := Trunc[i32](fd)
    var ty: [1]i32 = [0]
    var acc: [1]i32 = [0]
    var sa: [16]u64 = [0; 16]
    var n: [1]u32 = [4]
    unsafe "getsockopt and getsockname write into ty, acc, sa and n, this frame's, of the sizes n gives" {
        if getsockopt(h, UxSolSocket, PassSoType, raw.PtrCast[i32, u8](raw.DataMut(mut ty[..])), raw.DataMut(mut n[..])) != 0 {
            return SockFail(Errno())
        }
        if ty[0] != UxSockStream {
            return Ok(3)
        }
        n[0] = 4
        if getsockopt(h, UxSolSocket, PassSoAcceptConn, raw.PtrCast[i32, u8](raw.DataMut(mut acc[..])), raw.DataMut(mut n[..])) != 0 {
            return SockFail(Errno())
        }
        kind: i64 = if acc[0] != 0 { 2 } else { 1 }
        n[0] = 128
        p := raw.PtrCast[u64, u8](raw.DataMut(mut sa[..]))
        if getsockname(h, p, raw.DataMut(mut n[..])) != 0 {
            return SockFail(Errno())
        }
        // sa_len at 0, sa_family at 1, and for IPv4 and IPv6 the port at 2
        // in network order.
        family: u8 = raw.Read(raw.Offset(p, 1))
        if family == Trunc[u8](UxAfUnix) {
            return OsResult(value: kind, err: core.SysErr.None, os: i64(Unix))
        }
        port := (i64(raw.Read(raw.Offset(p, 2))) << 8) | i64(raw.Read(raw.Offset(p, 3)))
        if family == Trunc[u8](AfInet) {
            return OsResult(value: kind, err: core.SysErr.None, os: i64(Inet4) | (port << 16))
        }
        if family == Trunc[u8](AfInet6) {
            return OsResult(value: kind, err: core.SysErr.None, os: i64(Inet6) | (port << 16))
        }
        return Ok(3)
    }
}

/// PassReady readies a descriptor passed to this process: close-on-exec,
/// and a socket also for the poller as SocketOpen's are: non-blocking (the
/// flag belongs to the open file, which the sender shares) and
/// PollAttach'ed. O(1) system calls; allocates nothing.
export PassReady(fd: uint, socket: bool): OsResult {
    e := UnixReady(Trunc[i32](fd), socket)
    if e != 0 {
        return Fail(e)
    }
    if socket && !PollAttach(fd) {
        return Fail(24)
    }
    return Ok(0)
}

// PassSealState is PassHandlesSeal's verdict on MK_HANDLES.
struct PassSealState {
    handles: raw.Atomic[u32]
}

// PassSeal.handles: PassUnsealed until PassHandlesSeal runs, then
// PassSealNone, PassSealGood or PassSealRefused.
var PassSeal: PassSealState = PassSealState(handles: raw.Atomic[u32].New(0))

const PassUnsealed: u32 = 0
const PassSealNone: u32 = 1
const PassSealGood: u32 = 2
const PassSealRefused: u32 = 3

// PassHandlesSeal is InheritSeal's MK_HANDLES half, for proc.mk to call at
// start-up before it touches any descriptor: the descriptors MK_HANDLES
// names are checked (PassHandlesCheck, against inherit, MK_INHERIT's
// descriptor or -1), the verdict is kept for HandlesValue, and a list that
// passes is made close-on-exec, so a program this process starts gets
// none of them; a list that fails is left as it is.
// O(bytes of the environment) and one system call per descriptor;
// allocates nothing.
PassHandlesSeal(sp: *u8, inherit: i64) {
    var hs: [64]uint = [0; 64]
    n := PassHandlesCheck(sp, inherit, mut hs[..])
    // Edge: written once by start-up's only thread, before any task or
    // carrier exists; HandlesValue's Load comes after.
    PassSeal.handles.Store(if n == -1 { PassSealNone } else if n < 0 { PassSealRefused } else { PassSealGood })
    for i in 0..n {
        unsafe "fcntl passes no memory" {
            _ = fcntl(Trunc[i32](hs[i]), UxFSetFd, UxFdCloExec)
        }
    }
}

/// HandlesValue is MK_HANDLES, the handles a Command named for this
/// process: the descriptors 3 up to 3 + n - 1, in order, written to out
/// (room for PassMax), and the count; -1 when MK_HANDLES is not set; -2
/// when the list fails the check (it is not 3 to 3 + n - 1, or names more
/// than out has room for, a descriptor that is not open, one that is
/// close-on-exec, or MK_INHERIT's), so this process was not given those
/// descriptors by a Spawn and none is to be taken. Once start-up's seal
/// (PassHandlesSeal) ran, its verdict, made before the runtime opened a
/// descriptor of its own, is the answer; before it, or in a process where
/// it never runs, the check runs now: every descriptor the runtime opens is
/// close-on-exec, so none passes for a named one.
/// O(bytes of the environment); allocates nothing.
export HandlesValue(sp: *u8, out: mut []uint): int {
    // Edge: reads PassHandlesSeal's Store, which start-up's only thread
    // made before any task existed, so the verdict it published is seen
    // whole.
    s := PassSeal.handles.Load()
    if s == PassUnsealed {
        return PassHandlesCheck(sp, PassInheritValue(sp), out)
    }
    if s == PassSealNone {
        return -1
    }
    if s == PassSealRefused {
        return -2
    }
    return PassHandlesList(sp, out)
}

// PassInheritValue parses MK_INHERIT: the descriptor, -1 when it is not
// set, -2 when it is not a decimal of at most 9 digits.
PassInheritValue(sp: *u8): i64 {
    var buf: [24]u8 = [0; 24]
    n := GetEnvRaw(sp, "MK_INHERIT", mut buf[..])
    if n <= 0 {
        return -1
    }
    if n > 9 {
        return -2
    }
    var v: i64 = 0
    for i in 0..n {
        c := buf[i]
        if c < 48 || c > 57 {
            return -2
        }
        v = v * 10 + i64(c - 48)
    }
    return v
}

// PassHandlesList parses MK_HANDLES: the count of descriptors written to
// out, -1 when it is not set, -2 when it is not a list of at most
// out.Len() decimals of 3 and above.
PassHandlesList(sp: *u8, out: mut []uint): int {
    var buf: [1536]u8 = [0; 1536]
    n := GetEnvRaw(sp, "MK_HANDLES", mut buf[..])
    if n < 0 {
        return -1
    }
    if n > buf.Len() {
        return -2
    }
    return PassParseHandles(buf[0..n], 3, out)
}

// PassHandlesCheck is PassHandlesList, then a list of n is -2 unless it is
// exactly 3, 4, ..., 3 + n - 1 (as a spawn put them: no repeat, no gap, no
// other order), and each is open and not close-on-exec, and none is
// inherit (MK_INHERIT's descriptor, or -1). Nothing is changed.
// O(bytes of the environment) and one system call per descriptor;
// allocates nothing.
PassHandlesCheck(sp: *u8, inherit: i64, out: mut []uint): int {
    n := PassHandlesList(sp, out)
    if n < 0 {
        return n
    }
    for i in 0..n {
        d := out[i]
        if d != Trunc[uint](3 + i) || Trunc[i64](d) == inherit {
            return -2
        }
        unsafe "fcntl passes no memory" {
            f := fcntl(Trunc[i32](d), FGetFd)
            if f == -1 || (f & 1) != 0 {
                return -2
            }
        }
    }
    return n
}

// PassParseHandles parses a comma-separated list of decimal handle values,
// each no less than least and of at most 18 digits, into out: the count,
// or -2 when text is not such a list or holds more than out has room for.
// An empty text is the empty list. O(text's bytes); allocates nothing.
PassParseHandles(text: []u8, least: u64, out: mut []uint): int {
    var k := 0
    var v: u64 = 0
    var digits := 0
    for i in 0..text.Len() + 1 {
        if i == text.Len() || text[i] == 44 {
            if i == text.Len() && k == 0 && digits == 0 {
                return 0
            }
            if digits == 0 || v < least || k >= out.Len() {
                return -2
            }
            out[k] = Trunc[uint](v)
            k += 1
            v = 0
            digits = 0
            continue
        }
        c := text[i]
        if c < 48 || c > 57 || digits >= 18 {
            return -2
        }
        v = v * 10 + u64(c - 48)
        digits += 1
    }
    return k
}
