// Why: std.net's ListenUnix and DialUnix work on Windows too, since
// Windows 10 1803 has AF_UNIX stream sockets.
//
// What: UnixListen, UnixUnbind and UnixConnect, with the Unix family tag
// and AcceptUnixArea, reached through std.rt's call entry (rt/unix.mk).
//
// How: a socket made here is overlapped, not inheritable and attached to the port, as
// SocketOpen's TCP sockets are, so the accept, recv and send entries serve
// it; an accept on a Unix listener takes its address area out of line
// (poll_iocp.mk AcceptArea: AcceptEx wants 126-byte slots for AF_UNIX,
// measured on this host, and op.os has 96). A path takes at most 107 bytes
// and its NUL (sun_path); the bytes go to Winsock as they are.
// Errors: WSAEAFNOSUPPORT (10047, before 1803) is Unsupported, the rest
// MapError's. Each path that made a socket closes it on every error path.

import std.core
import std.raw

/// Unix is the SockAddr family recorded for a Unix-domain socket's runtime
/// handle (its address is a path, which SockAddr does not hold).
export const Unix: u16 = 1

/// AcceptUnixArea is the address area an accept on a Unix listener lends in
/// op.buf: AcceptEx's two slots of 128 bytes (poll_iocp.mk AcceptArea).
export const AcceptUnixArea: int = 256

// AfUnixOs is AF_UNIX.
const AfUnixOs: u16 = 1
// UnixPathMax is sun_path's size.
const UnixPathMax: int = 108
// UnixAddrBytes is the encoded sockaddr_un's buffer: 2 + 108, rounded up.
const UnixAddrBytes: int = 112
const WsaeNoBufs: u32 = 10055
const WsaeAfNoSupport: u32 = 10047

// UnixWsaFail is WsaFail with the address family's absence as Unsupported.
UnixWsaFail(): OsResult {
    e := WsaError()
    if e == WsaeAfNoSupport {
        return OsResult(value: 0, err: core.SysErr.Unsupported, os: i64(e))
    }
    return Fail(e)
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
        Put16(p, 0, AfUnixOs)
        for i, x in path {
            if x == 0 {
                return 0
            }
            raw.Write(raw.Offset(p, 2 + i), x)
        }
    }
    return 2 + n + 1
}

// UnixClose closes s on an error path and returns r.
UnixClose(s: uint, r: OsResult): OsResult {
    _ = CloseSocketRaw(s)
    return r
}

/// UnixListen makes a Unix-domain stream socket bound to path and listening
/// (backlog <= 0 asks for the maximum), attached to the port; value is the
/// SOCKET. InvalidAddress for an empty path, one with a NUL or one of 108
/// bytes or more, before any call; WSAEADDRINUSE when path exists (the
/// caller removes a stale socket file; this call removes only the file its
/// own bind made, when a step after it fails); Unsupported
/// before Windows 10 1803. O(1) system calls; allocates nothing.
export UnixListen(path: []u8, backlog: int): OsResult {
    var sa: [14]u64 = [0; 14]
    var n := 0
    unsafe "sa holds UnixAddrBytes bytes" {
        n = UnixAddr(path, raw.PtrCast[u64, u8](raw.DataMut(mut sa[..])))
    }
    if n == 0 {
        return FailWith(core.SysErr.InvalidAddress)
    }
    if !WinsockReady() {
        return FailWith(core.SysErr.Other)
    }
    s := NewSocket(AfUnixOs)
    if s == InvalidSocket {
        return UnixWsaFail()
    }
    b := if backlog > 0 && backlog < 0x7FFF_FFFF { Trunc[i32](backlog) } else { SoMaxConn }
    unsafe "sa is the encoded address" {
        if BindRaw(s, raw.PtrCast[u64, u8](raw.Data(sa[..])), Trunc[i32](n)) != 0 {
            return UnixClose(s, UnixWsaFail())
        }
    }
    if ListenRaw(s, b) != 0 {
        r := UnixClose(s, UnixWsaFail())
        UnixUnbind(path)
        return r
    }
    if !AttachSocket(s) {
        r := UnixClose(s, LastError())
        UnixUnbind(path)
        return r
    }
    return Ok(Trunc[i64](s))
}

/// UnixUnbind removes the socket file a UnixListen's bind made at path,
/// when a later step failed and no Listener holds it, so the next listen
/// at path is not WSAEADDRINUSE. Only after this process's own successful
/// bind there: the file is then this process's. A path that is not UTF-8
/// removes nothing. O(1) system calls; allocates nothing past Remove's.
export UnixUnbind(path: []u8) {
    p := str.From(path) else {
        return
    }
    _ = Remove(p)
}

/// UnixConnect connects a new Unix-domain stream socket to path: value is
/// the SOCKET, attached to the port, or -1 with no socket held when the
/// listener's backlog is full (WSAENOBUFS; the caller sleeps and asks
/// again, rt.UnixDial). InvalidAddress as UnixListen; WSAECONNREFUSED when
/// nothing listens at path or path does not exist. An AF_UNIX connect
/// completes or fails at once (measured: a full backlog answers WSAENOBUFS
/// in no measurable time), so this plain connect does not hold the carrier.
/// O(1) system calls; allocates nothing.
export UnixConnect(path: []u8): OsResult {
    var sa: [14]u64 = [0; 14]
    var n := 0
    unsafe "sa holds UnixAddrBytes bytes" {
        n = UnixAddr(path, raw.PtrCast[u64, u8](raw.DataMut(mut sa[..])))
    }
    if n == 0 {
        return FailWith(core.SysErr.InvalidAddress)
    }
    if !WinsockReady() {
        return FailWith(core.SysErr.Other)
    }
    s := NewSocket(AfUnixOs)
    if s == InvalidSocket {
        return UnixWsaFail()
    }
    unsafe "sa is the encoded address" {
        if ConnectRaw(s, raw.PtrCast[u64, u8](raw.Data(sa[..])), Trunc[i32](n)) != 0 {
            e := WsaError()
            if e == WsaeNoBufs {
                return UnixClose(s, Ok(-1))
            }
            return UnixClose(s, if e == WsaeAfNoSupport {
                OsResult(value: 0, err: core.SysErr.Unsupported, os: i64(e))
            } else {
                Fail(e)
            })
        }
    }
    if !AttachSocket(s) {
        return UnixClose(s, LastError())
    }
    return Ok(Trunc[i64](s))
}
