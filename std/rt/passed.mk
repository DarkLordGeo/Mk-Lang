// Why: processes hand OS handles to each other (a listener to a worker, a
// file to a child), and the runtime must adopt them as its own records.
//
// What: SendFds and RecvFds behind std.net's Stream.SendHandles and
// RecvHandles (SCM_RIGHTS on Linux; Windows and Nutshell answer
// Unsupported, std.sys), InheritedFds for proc.Inherited's adoption of the
// handles a Command named (MK_HANDLES), Passed, and PassShared, the mark that
// keeps a shared socket alive.
//
// How: native only: a simulated Io's handles are no OS handles, so every
// call here answers Unsupported under one. The sends and receives run
// inline on the calling task (each system call is MSG_DONTWAIT): a receive
// waits for input on the poller (IoOpReady, as TermWait does), a send whose
// socket buffer is full sleeps between tries (UnixDial's 1 ms doubling to
// 50 ms), so a cancel or a deadline ends either wait with Canceled and
// nothing sent or taken.

import std.core
import std.raw
import std.sys

/// Passed is a handle passed to this process, beside a Unix stream's bytes
/// or inherited from its parent, on its way to the module whose type wraps
/// it (net.Passed.Adopt, fs.File.Adopt). std-internal: std.rt is, so no
/// program builds one, and the exported functions that take one are std's
/// in effect. h is its runtime handle (the receiver's to close), kind
/// PassFile, PassStream or PassListener, family a socket's (sys.Unix,
/// sys.Inet4 or sys.Inet6; 0 for a file), port a TCP listener's bound
/// port (else 0). Plain.
export struct Passed {
    export h: uint
    export kind: u32
    export family: u16
    export port: int
}

/// Passed.kind: anything but a socket (a file, a pipe end, a device).
export const PassFile: u32 = 0
/// Passed.kind: a stream socket that is not listening.
export const PassStream: u32 = 1
/// Passed.kind: a listening stream socket.
export const PassListener: u32 = 2

/// PassNative: h is a runtime handle on an OS handle (not 0, not a
/// simulated Io's record), which a send or a spawn may pass on. O(1);
/// allocates nothing.
export PassNative(h: uint): bool {
    if h == 0 {
        return false
    }
    unsafe "h is a runtime handle: every handle a safe program holds is a live record" {
        return (*Rec(h)).kind != KindForeign
    }
}

/// PassShared marks the socket behind h as shared with another process
/// (passed on, or adopted from one): its close then skips the shutdown,
/// which would end every process's copy of the connection, and only
/// releases this one (sys.SocketAbortClose), so the peer sees the end when
/// the last copy closes. Anything but a socket is left as it is. O(1);
/// allocates nothing.
export PassShared(h: uint) {
    unsafe "h is the caller's live handle; only its owner's task writes its record" {
        r := Rec(h)
        if (*r).kind == KindSocket {
            (*r).abort = true
        }
    }
}

/// SendFds sends data on the Unix stream h with the handles hs attached
/// to its first byte: value the bytes sent with them, at least 1 (the
/// caller sends the rest as plain bytes). Each socket among hs is then
/// marked shared (PassShared). Errors: Unsupported under a simulated Io,
/// for a handle that is not a native Unix stream socket, and on Windows
/// and Nutshell (no SCM_RIGHTS); Other for an empty data, no handles, more
/// than sys.PassMax, or a handle among hs that is not native; Canceled
/// when the scope is cancelled, at entry or while the buffer is full
/// (nothing sent); else the OS's (EPIPE, ECONNRESET for a peer gone).
/// O(1) system calls per try; allocates nothing.
export SendFds(io: Io, h: uint, data: []u8, hs: []uint): core.IoResult {
    if !core.IsNative(io) || !PassNative(h) || !IsUnix(h) {
        return Fail(core.SysErr.Unsupported)
    }
    if data.Len() == 0 || hs.Len() == 0 || hs.Len() > sys.PassMax {
        return Fail(core.SysErr.Other)
    }
    core.SyncCancel(io) else {
        return Fail(core.SysErr.Canceled)
    }
    var fds: [64]uint = [0; 64]
    for i, x in hs {
        if !PassNative(x) {
            return Fail(core.SysErr.Other)
        }
        unsafe "x is the caller's live handle" {
            fds[i] = (*Rec(x)).os
        }
    }
    var os: uint = 0
    unsafe "h is the caller's live handle" {
        os = (*Rec(h)).os
    }
    var wait := LockFirstWait
    for {
        r := sys.UnixSendFds(os, data, fds[0..hs.Len()])
        if r.err != core.SysErr.None {
            return IoOf(r)
        }
        if r.value >= 0 {
            for x in hs {
                PassShared(x)
            }
            return IoOf(r)
        }
        io.Sleep(core.DurationOf(wait)) else {
            return Fail(core.SysErr.Canceled)
        }
        wait = if wait >= LockMaxWait / 2 { LockMaxWait } else { wait * 2 }
    }
}

/// RecvFds receives at most buf.Len() bytes (at least 1) on the Unix stream
/// h and the handles that came with them, each adopted (Adopt) into out
/// (room for sys.PassMax): value the bytes, 0 at the end of the stream; os
/// the count of out written. While nothing is queued it waits for input on
/// the poller, so a cancel or a deadline ends the wait with Canceled and
/// nothing taken (repeatable). Errors: Unsupported as SendFds, and
/// for a handle that came with the bytes and is a socket but no stream
/// socket (datagram, seqpacket: the bytes are consumed, and every handle
/// that came with them closed); Other with os 90 for more than PassMax
/// handles in one message (the same); Other with no code when the heap
/// cannot give a record (the same); Canceled; else the OS's. O(1) system
/// calls per try besides the handles' adoption; allocates one runtime
/// record per handle received.
export RecvFds(io: Io, h: uint, buf: mut []u8, out: mut []Passed): core.IoResult {
    if !core.IsNative(io) || !PassNative(h) || !IsUnix(h) {
        return Fail(core.SysErr.Unsupported)
    }
    if buf.Len() == 0 || out.Len() < sys.PassMax {
        return Fail(core.SysErr.Other)
    }
    core.SyncCancel(io) else {
        return Fail(core.SysErr.Canceled)
    }
    var os: uint = 0
    unsafe "h is the caller's live handle" {
        os = (*Rec(h)).os
    }
    var fds: [64]uint = [0; 64]
    for {
        r := sys.UnixRecvFds(os, buf, mut fds[..])
        if r.err != core.SysErr.None {
            return IoOf(r)
        }
        if r.value >= 0 {
            n := Trunc[int](r.os)
            a := AdoptAll(fds[0..n], out, true)
            if a.err != core.SysErr.None {
                return a
            }
            return core.IoResult(value: r.value, err: core.SysErr.None, os: r.os)
        }
        w := TermWait(io, h)
        if w.err != core.SysErr.None {
            return w
        }
    }
}

// AdoptAll adopts each of fds into out, in order; on the first failure it
// closes every runtime handle already made and answers that failure. With
// received (descriptors recvmsg just installed, this process's alone) it
// also closes every descriptor not yet adopted, the failing one included
// unless the OS could not say what it is (Adopt's typed). Without it
// (inherited handles, InheritedFds) nothing not yet adopted is touched.
AdoptAll(fds: []uint, out: mut []Passed, received: bool): core.IoResult {
    for i, d in fds {
        (r, typed) := Adopt(d, mut out[i])
        if r.err != core.SysErr.None {
            for j in 0..i {
                _ = CloseRuntimeHandle(out[j].h)
            }
            if received {
                for j in i..fds.Len() {
                    if j != i || typed {
                        _ = sys.Close(fds[j])
                    }
                }
            }
            return IoOf(r)
        }
    }
    return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
}

/// Adopt wraps the OS handle os, passed to this process, as a runtime
/// handle in p, typed by what the OS says it is (sys.PassKind): a socket
/// readied for the poller (sys.PassReady) and marked shared, a listener
/// inherited on Windows on its parent's completion port (sys.SharedPort),
/// anything else a file served by the blocking pool. os is not closed on
/// failure: Unsupported for a socket that is not a stream socket, or a
/// handle the OS cannot type, Other when the heap cannot give a record,
/// else the OS's. typed is false when sys.PassKind failed, so os names no
/// handle the OS could describe, and true otherwise. O(1) system calls;
/// allocates one runtime record.
Adopt(os: uint, p: mut Passed): (r: sys.OsResult, typed: bool) {
    k := sys.PassKind(os)
    if k.err != core.SysErr.None {
        return (r: k, typed: false)
    }
    kind := Trunc[u32](k.value)
    if kind > PassListener {
        return (r: sys.OsResult(value: 0, err: core.SysErr.Unsupported, os: 0), typed: true)
    }
    sock := kind != PassFile
    ready := sys.PassReady(os, sock)
    if ready.err != core.SysErr.None {
        return (r: ready, typed: true)
    }
    family := Trunc[u16](k.os & 0xFFFF)
    var h: uint = 0
    if !sock {
        h = NewHandle(os, false)
    } else if Target.os == Os.Windows && kind == PassListener {
        h = NewInheritedSocket(os, family, sys.SharedPort)
    } else {
        h = NewSocketHandle(os, family)
    }
    if h == 0 {
        if sock {
            sys.PollDetach(os)
        }
        return (r: sys.OsResult(value: 0, err: core.SysErr.Other, os: 0), typed: true)
    }
    if sock {
        PassShared(h)
    }
    *p = Passed(h: h, kind: kind, family: if sock { family } else { 0 }, port: Trunc[int](k.os >> 16))
    return (r: sys.OsResult(value: 0, err: core.SysErr.None, os: 0), typed: true)
}

// InheritState is InheritedFds' once word: 0 unasked, 1 taken.
struct InheritState {
    taken: raw.Atomic[u32]
}

var Handed: InheritState = InheritState(taken: raw.Atomic[u32].New(0))

/// InheritedFds adopts the handles a Command named for this process
/// (MK_HANDLES) into out (room for sys.PassMax), in the order the
/// Command named them: os the count, 0 when there are none. Only the
/// first call in a process adopts them; every later one, and a call under
/// a simulated Io, answers 0 (a Sim's process inherits nothing). Errors:
/// Other with os 22 (EINVAL) for an MK_HANDLES that failed start-up's
/// check (sys.HandlesValue: a list no Spawn wrote for this process, which
/// names a handle this process holds for another reason, a closed value
/// or one twice), with nothing adopted and no handle touched; the OS's for
/// a value that names no open handle (Linux EBADF, Windows
/// ERROR_INVALID_HANDLE), Unsupported for a socket that is not a stream
/// socket (on Windows anything but a listener) or a handle the OS cannot
/// type, Other with no code when the heap cannot give a record: on any of
/// these, the handles already adopted are closed and the rest are left
/// as they are. O(bytes of MK_HANDLES) besides the adoptions; allocates
/// one runtime record per handle.
export InheritedFds(io: Io, out: mut []Passed): core.IoResult {
    empty := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    if !core.IsNative(io) || out.Len() < sys.PassMax {
        return empty
    }
    // Edge: the Cas from 0 lets one caller take the inherited handles; the
    // Load only skips the Cas once they are taken, and orders nothing.
    if Handed.taken.Load() != 0 || !Handed.taken.Cas(0, 1) {
        return empty
    }
    var sp := raw.Null[u8]()
    unsafe "the RtState page Init mapped, which lives until exit; init_sp is set once at start" {
        sp = (*R()).init_sp
    }
    var fds: [64]uint = [0; 64]
    n := sys.HandlesValue(sp, mut fds[..])
    if n == -1 {
        return empty
    }
    if n < 0 {
        return core.IoResult(value: 0, err: core.SysErr.Other, os: 22)
    }
    a := AdoptAll(fds[0..n], out, false)
    if a.err != core.SysErr.None {
        return a
    }
    return core.IoResult(value: 0, err: core.SysErr.None, os: i64(n))
}
