// Why: std.net and std.proc pass handles between processes, and Windows
// offers a different route than Linux's SCM_RIGHTS.
//
// What: UnixSendFds and UnixRecvFds, which answer Unsupported, and
// PassKind and PassReady, which type and ready a handle a Command names.
//
// How: a Windows AF_UNIX socket carries no ancillary data (Microsoft's
// AF_UNIX documents none), so std.net's Stream.SendHandles and RecvHandles
// are Linux's only. A handle a Command names reaches the child on its
// handle list instead (child.mk).

import std.core
import std.raw

// Winsock's SO_TYPE and SO_ACCEPTCONN (winsock2.h: 0x1008 and 0x0002).
const SoTypeWin: i32 = 0x1008
const SoAcceptConnWin: i32 = 0x0002

/// UnixSendFds has std/sys/linux's contract; Windows answers Unsupported
/// (no SCM_RIGHTS on AF_UNIX). O(1); allocates nothing.
export UnixSendFds(fd: uint, data: []u8, fds: []uint): OsResult {
    return FailWith(core.SysErr.Unsupported)
}

/// UnixRecvFds has std/sys/linux's contract; Windows answers Unsupported
/// (no SCM_RIGHTS on AF_UNIX). O(1); allocates nothing.
export UnixRecvFds(fd: uint, buf: mut []u8, fds: mut []uint): OsResult {
    return FailWith(core.SysErr.Unsupported)
}

/// PassKind has std/sys/linux's contract for a handle a Command named:
/// 0 for a file, a pipe end or a character device, 2 for a
/// listening stream socket, the one kind of socket a Command passes on
/// Windows (a connected stream's overlapped I/O would complete on the
/// parent's port, so std.proc refuses one), and 3 for any other socket,
/// which std.rt refuses (Unsupported); os the listener's family and port
/// as Linux gives them. A value that names no open handle is
/// ERROR_INVALID_HANDLE (6), told apart by GetHandleInformation, which
/// answers for any live handle; a live handle that is none of these (an
/// event, a thread, a completion port) is Unsupported. In neither case is
/// the handle touched. O(1) system calls; allocates nothing.
export PassKind(fd: uint): OsResult {
    var flags: u32 = 0
    unsafe "a kernel32 call; flags is this frame's" {
        if GetHandleInformation(fd, raw.OfMut(mut flags)) == 0 {
            return Fail(6)
        }
    }
    k := HandleKind(fd)
    if k == 0 {
        return FailWith(core.SysErr.Unsupported)
    }
    if k != 4 {
        return Ok(0)
    }
    if !WinsockReady() {
        return FailWith(core.SysErr.Unsupported)
    }
    var ty: [1]u32 = [0]
    var acc: [1]u32 = [0]
    var sz: i32 = 4
    unsafe "Winsock; ty, acc and sz are this frame's, 4 bytes each" {
        if GetSockOpt(fd, SolSocket, SoTypeWin, raw.PtrCast[u32, u8](raw.DataMut(mut ty[..])), raw.OfMut(mut sz)) != 0 {
            return WsaFail()
        }
        sz = 4
        if GetSockOpt(fd, SolSocket, SoAcceptConnWin, raw.PtrCast[u32, u8](raw.DataMut(mut acc[..])),
            raw.OfMut(mut sz)) != 0 {
            return WsaFail()
        }
    }
    if ty[0] != 1 || (acc[0] & 0xFF) == 0 {
        return Ok(3)
    }
    var a := SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0)
    n := SocketName(fd, mut a)
    if n.err != core.SysErr.None {
        return n
    }
    return OsResult(value: 2, err: core.SysErr.None, os: i64(a.family) | (i64(a.port) << 16))
}

/// PassReady has std/sys/linux's contract: the handle is made not
/// inheritable (InheritSeal did so at start-up already); a listener stays
/// on the parent's completion port, so its accepts take the shared-port
/// path (SharedPort). O(1): one system call; allocates nothing.
export PassReady(fd: uint, socket: bool): OsResult {
    unsafe "a kernel32 call that passes no memory, only values and handles" {
        _ = SetHandleInformation(fd, HandleFlagInherit, 0)
    }
    return Ok(0)
}
