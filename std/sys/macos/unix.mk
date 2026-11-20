// Why: std.net's ListenUnix and DialUnix, and handle passing between
// processes (passfd.mk), need Unix-domain stream sockets on macOS.
//
// What: UnixListen, UnixUnbind, UnixConnect, Unix and AcceptUnixArea;
// reached through std.rt's call entry (rt/unix.mk); and the helpers
// passfd.mk shares (UnixClose, the byte accessors UxGet32, UxPut32,
// UxGet64, UxPut64).
//
// How: a socket made here is a runtime socket as SocketOpen's TCP sockets
// are: close-on-exec, non-blocking, SO_NOSIGPIPE, and readied for the
// poller (PollAttach), so the accept, recv and send entries serve it
// unchanged. Darwin has no SOCK_CLOEXEC or SOCK_NONBLOCK, so fcntl sets
// both right after socket(). Darwin's sockaddr_un is sun_len, sun_family
// (one byte each), then sun_path of 104 bytes; a path with a NUL is
// refused, and a path takes at most 103 bytes and its NUL. Errors are
// SockFail's, and EAFNOSUPPORT (47), EPROTONOSUPPORT (43), EOPNOTSUPP (102)
// and ENOTSUP (45) are Unsupported. Every path that made a descriptor
// closes it on every error path.

import std.core
import std.raw
import std.sys.lc

/// Unix is the SockAddr family recorded for a Unix-domain socket's runtime
/// handle (its address is a path, which SockAddr does not hold).
export const Unix: u16 = 1

/// AcceptUnixArea is the address area an accept on a Unix listener lends in
/// op.buf (Windows' AcceptEx needs it); macOS's accept ignores it, and a
/// Unix peer decodes to the zero address.
export const AcceptUnixArea: int = 256

// UxAfUnix is AF_UNIX, the socket call's domain and sun_family.
const UxAfUnix: i32 = 1
const UxSockStream: i32 = 1
// UxPathMax is sun_path's size.
const UxPathMax: int = 104
// UxAddrBytes is the encoded sockaddr_un's buffer: 2 + 104, rounded up.
const UxAddrBytes: int = 112
const UxSolSocket: i32 = 0xFFFF
const UxSoNoSigPipe: i32 = 0x1022
const UxSoMaxConn: i32 = 128
// fcntl's commands and flags (Darwin's <fcntl.h>).
const UxFGetFl: i32 = 3
const UxFSetFd: i32 = 2
const UxFSetFl: i32 = 4
const UxFdCloExec: i32 = 1
const UxONonBlock: i32 = 0x4
const UxEagain: i64 = 35
const UxEintr: i64 = 4

// UxGet32, UxPut32, UxGet64 and UxPut64 read and write a little-endian word
// at byte off of p; the caller's buffer holds it.
UxGet32(p: *u8, off: int): u32 {
    unsafe "the caller's buffer holds 4 bytes at off" {
        return raw.ReadUnaligned(raw.PtrCast[u8, u32](raw.Offset(p, off)))
    }
}

UxPut32(p: *u8, off: int, v: u32) {
    unsafe "the caller's buffer holds 4 bytes at off" {
        raw.Write(raw.PtrCast[u8, u32](raw.Offset(p, off)), v)
    }
}

UxGet64(p: *u8, off: int): u64 {
    unsafe "the caller's buffer holds 8 bytes at off" {
        return raw.ReadUnaligned(raw.PtrCast[u8, u64](raw.Offset(p, off)))
    }
}

UxPut64(p: *u8, off: int, v: u64) {
    unsafe "the caller's buffer holds 8 bytes at off" {
        raw.Write(raw.PtrCast[u8, u64](raw.Offset(p, off)), v)
    }
}

// UnixFail is SockFail with the address family's absence as Unsupported.
UnixFail(errno: i64): OsResult {
    if errno == 47 || errno == 43 || errno == 102 || errno == 45 {
        return OsResult(value: 0, err: core.SysErr.Unsupported, os: errno)
    }
    return SockFail(errno)
}

// UnixAddr writes path as a sockaddr_un at p (UxAddrBytes bytes) and
// returns its length (sun_len, sun_family, the path and its NUL), or 0
// when path is empty, holds a NUL or does not fit sun_path.
UnixAddr(path: []u8, p: *u8): int {
    n := path.Len()
    if n == 0 || n >= UxPathMax {
        return 0
    }
    unsafe "p holds UxAddrBytes bytes" {
        raw.Fill(p, 0, UxAddrBytes)
        raw.Write(p, Trunc[u8](2 + n + 1))
        raw.Write(raw.Offset(p, 1), Trunc[u8](UxAfUnix))
        for i, x in path {
            if x == 0 {
                return 0
            }
            raw.Write(raw.Offset(p, 2 + i), x)
        }
    }
    return 2 + n + 1
}

// UnixReady makes fd close-on-exec and, when nb, non-blocking; the errno
// of the fcntl that failed, or 0.
UnixReady(fd: i32, nb: bool): i64 {
    unsafe "fcntl passes no memory" {
        if fcntl(fd, UxFSetFd, UxFdCloExec) == -1 {
            return Errno()
        }
        if !nb {
            return 0
        }
        fl := fcntl(fd, UxFGetFl)
        if fl == -1 || fcntl(fd, UxFSetFl, fl | UxONonBlock) == -1 {
            return Errno()
        }
    }
    return 0
}

// UnixSocket makes a close-on-exec, non-blocking stream socket that
// raises no SIGPIPE.
UnixSocket(): OsResult {
    var fd: i32 = 0
    unsafe "socket passes only values" {
        fd = socket(UxAfUnix, UxSockStream, 0)
    }
    if fd == -1 {
        return UnixFail(Errno())
    }
    e := UnixReady(fd, true)
    if e != 0 {
        return UnixClose(Trunc[uint](i64(fd)), Fail(e))
    }
    var one: [1]i32 = [1]
    unsafe "one is an int option value of 4 bytes" {
        _ = setsockopt(fd, UxSolSocket, UxSoNoSigPipe, raw.PtrCast[i32, u8](raw.Data(one[..])), 4)
    }
    return Ok(i64(fd))
}

// UnixClose closes fd on an error path and returns r.
UnixClose(fd: uint, r: OsResult): OsResult {
    _ = lc.CloseFd(Trunc[i32](fd))
    return r
}

/// UnixListen makes a Unix-domain stream socket bound to path and listening
/// (backlog <= 0 asks for SOMAXCONN, 128), ready for the poller; value is
/// the descriptor. InvalidAddress for an empty path, one with a NUL or one
/// of 104 bytes or more, before any call; EADDRINUSE when path exists (the
/// caller removes a stale socket file; this call removes only the file its
/// own bind made, when a step after it fails). A file system without
/// sockets answers bind's error.
/// O(1) system calls; allocates nothing.
export UnixListen(path: []u8, backlog: int): OsResult {
    var sa: [14]u64 = [0; 14]
    var n := 0
    unsafe "sa holds UxAddrBytes bytes" {
        n = UnixAddr(path, raw.PtrCast[u64, u8](raw.DataMut(mut sa[..])))
    }
    if n == 0 {
        return FailWith(core.SysErr.InvalidAddress)
    }
    s := UnixSocket()
    if s.err != core.SysErr.None {
        return s
    }
    fd := Trunc[i32](s.value)
    b: i32 = if backlog > 0 && backlog < 0x7FFF_FFFF { Trunc[i32](backlog) } else { UxSoMaxConn }
    unsafe "bind reads the encoded address in sa; listen passes no memory" {
        if bind(fd, raw.PtrCast[u64, u8](raw.Data(sa[..])), Trunc[u32](n)) == -1 {
            return UnixClose(Trunc[uint](s.value), UnixFail(Errno()))
        }
        if listen(fd, b) == -1 {
            failed := UnixClose(Trunc[uint](s.value), UnixFail(Errno()))
            UnixUnbind(path)
            return failed
        }
    }
    if !PollAttach(Trunc[uint](s.value)) {
        r := UnixClose(Trunc[uint](s.value), Fail(24))
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
    unsafe "sa holds UxAddrBytes bytes; sun_path starts at byte 2 and ends in a NUL (UnixAddr refuses 104 bytes)" {
        p := raw.PtrCast[u64, u8](raw.DataMut(mut sa[..]))
        if UnixAddr(path, p) == 0 {
            return
        }
        _ = unlink(raw.Offset(p, 2))
    }
}

/// UnixConnect connects a new Unix-domain stream socket to path, without
/// waiting: value is the descriptor, ready for the poller, or -1 with no
/// descriptor held when the connect was interrupted or answered EAGAIN
/// (the caller sleeps and asks again, rt.UnixDial). InvalidAddress as
/// UnixListen; ECONNREFUSED when nothing listens at path (a listener
/// whose backlog is full may answer it too, which the caller cannot tell
/// apart); ENOENT when path does not exist. The connect is non-blocking,
/// so a carrier never waits in it; any other error, EINPROGRESS included,
/// closes the socket and is reported. O(1) system calls; allocates
/// nothing.
export UnixConnect(path: []u8): OsResult {
    var sa: [14]u64 = [0; 14]
    var n := 0
    unsafe "sa holds UxAddrBytes bytes" {
        n = UnixAddr(path, raw.PtrCast[u64, u8](raw.DataMut(mut sa[..])))
    }
    if n == 0 {
        return FailWith(core.SysErr.InvalidAddress)
    }
    s := UnixSocket()
    if s.err != core.SysErr.None {
        return s
    }
    fd := Trunc[uint](s.value)
    unsafe "connect reads the encoded address in sa" {
        if connect(Trunc[i32](s.value), raw.PtrCast[u64, u8](raw.Data(sa[..])), Trunc[u32](n)) == -1 {
            e := Errno()
            // EAGAIN and EINTR leave no connection behind (the socket is
            // closed either way): the caller tries again.
            if e == UxEagain || e == UxEintr {
                return UnixClose(fd, Ok(-1))
            }
            return UnixClose(fd, UnixFail(e))
        }
    }
    if !PollAttach(fd) {
        return UnixClose(fd, Fail(24))
    }
    return Ok(s.value)
}
