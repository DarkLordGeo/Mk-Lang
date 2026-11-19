// Why: std.net's ListenUnix and DialUnix need Unix-domain stream sockets on
// both targets, linked only when a program uses them.
//
// What: UnixListen and UnixDial (std-internal), with their jobs, UnixWrap
// and IsUnix.
//
// How: the calls go through the Io's call entry (on Windows the connect
// slot's use is linked only then), and std.sim and a Recorder's Tape answer
// Unsupported. The socket is wrapped as a runtime socket handle of family
// sys.Unix, so the accept, recv, send, shutdown and close entries serve it
// as a TCP socket. SockAddress on it answers Unsupported (its address is a
// path); SockOption's NoDelay and KeepAlive answer Unsupported, the buffer
// sizes, Linger and ReuseAddr go to the OS (rt/call.mk).

import std.core
import std.raw
import std.sys

/// UnixListen makes a Unix-domain stream socket listening at path with the
/// given backlog (0 or less: the OS's largest); value is its runtime socket
/// handle, whose accepts give runtime socket handles.
/// Errors: InvalidAddress for an empty path, one holding a NUL or one of
/// 108 bytes or more (sun_path), before any call; AddrInUse when path
/// exists (a stale socket file is the caller's to remove; this call removes
/// only the file its own bind made, when it then fails); Unsupported where
/// the target has no
/// Unix sockets (Windows before 10 1803, WSL's drive); else the bind's.
/// Checked inline (core.IoRouteChecked): Canceled under a cancelled scope,
/// as net.Listen; O(1) system calls; allocates one HandleRec on the runtime
/// heap.
export UnixListen(io: Io, path: []u8, backlog: int): core.IoResult {
    var a := CallOf(core.IoOpUnixListen, core.IoRouteChecked, 0, UnixListenJob)
    a.p = raw.Data(path)
    a.pn = path.Len()
    a.w.a = i64(backlog)
    r := Run(io, mut a)
    w := UnixWrap(r)
    if r.err == core.SysErr.None && w.err != core.SysErr.None && core.IsNative(io) {
        // No Listener holds the file this call's bind made.
        sys.UnixUnbind(path)
    }
    return w
}

UnixListenJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        Set(a, sys.UnixListen(In(a), Trunc[int]((*a).w.a)))
    }
}

/// UnixDial connects to the Unix-domain listener at path; value is the
/// connection's runtime socket handle. While the listener's backlog is
/// full it waits by Io.Sleep between tries (1 ms doubling to 50 ms), so a
/// cancel or a deadline ends the wait with Canceled and nothing held
/// (repeatable). Errors: InvalidAddress as UnixListen;
/// Refused when nothing listens at path (Linux NotFound when path does not
/// exist); Unsupported as UnixListen; Canceled (each try is checked inline,
/// so a cancelled scope's dial is Canceled before it connects, as net.Dial).
/// O(1) system calls per try; allocates one HandleRec on the runtime heap.
export UnixDial(io: Io, path: []u8): core.IoResult {
    var wait := LockFirstWait
    for {
        var a := CallOf(core.IoOpUnixDial, core.IoRouteChecked, 0, UnixDialJob)
        a.p = raw.Data(path)
        a.pn = path.Len()
        r := Run(io, mut a)
        if r.err != core.SysErr.None || r.value >= 0 {
            return UnixWrap(r)
        }
        io.Sleep(core.DurationOf(wait)) else {
            return core.IoResult(value: 0, err: core.SysErr.Canceled, os: 0)
        }
        wait = if wait >= LockMaxWait / 2 { LockMaxWait } else { wait * 2 }
    }
}

UnixDialJob(a: *core.IoCallArgs) {
    Set(a, sys.UnixConnect(In(a)))
}

// UnixWrap wraps a call's OS socket as a runtime socket handle of family
// sys.Unix; the socket is closed when the record cannot be made.
UnixWrap(r: core.IoResult): core.IoResult {
    if r.err != core.SysErr.None {
        return r
    }
    s := Trunc[uint](r.value)
    h := NewSocketHandle(s, sys.Unix)
    if h == 0 {
        _ = sys.SocketShutdownClose(s)
        return core.IoResult(value: 0, err: core.SysErr.Other, os: 0)
    }
    return core.IoResult(value: Trunc[i64](h), err: core.SysErr.None, os: 0)
}

// IsUnix: the runtime handle h is a Unix-domain socket.
IsUnix(h: uint): bool {
    r := Rec(h)
    unsafe "h is the caller's live handle" {
        return (*r).kind == KindSocket && (*r).family == sys.Unix
    }
}
