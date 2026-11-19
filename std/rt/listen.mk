// Why: std.net's ListenWith needs the backlog and address-reuse choices
// that net.Listen does not make, and a program should link that option path
// only when it uses it.
//
// What: ListenWith (std-internal), its job ListenJob, and Answer.
//
// How: the request goes through the Io's call entry (core.IoOpListen). A
// Sim answers the op as its listen; a Recorder's Tape and a Replayer answer
// Unsupported, as for every call op but the wall clock.

import std.core
import std.raw
import std.sys

/// ListenWith binds addr ("host:port", read as the table's listen reads
/// it) and listens. backlog <= 0 asks for the OS's largest queue; a
/// positive one is the queue asked for, which the OS may round up. reuse
/// lets the bind take a port still in TIME_WAIT (Linux SO_REUSEADDR, which
/// Linux honours only when the socket that left the entry chose reuse too),
/// and without it such a port is AddrInUse on Linux. Windows binds over
/// TIME_WAIT either way (measured by SD8-Listen-Reuse); without reuse it
/// sets SO_EXCLUSIVEADDRUSE, which keeps any other socket off the port
/// while the listener holds it. A port another socket holds is AddrInUse
/// either way. value is the listener's runtime socket handle, os the
/// bound port, so port 0 tells the caller which port the OS chose.
/// Errors: InvalidAddress for an address the parser refuses, before any
/// call; AddrInUse; Unsupported under a Recorder's Tape or a Replayer;
/// else the socket's, bind's or listen's. Checked inline
/// (core.IoRouteChecked): Canceled under a cancelled scope, as net.Listen.
/// O(1) system calls; allocates one HandleRec on the runtime heap.
export ListenWith(io: Io, addr: str, backlog: int, reuse: bool): core.IoResult {
    var a := CallOf(core.IoOpListen, core.IoRouteChecked, 0, ListenJob)
    a.p = raw.StrData(addr)
    a.pn = addr.Len()
    a.w.a = i64(backlog)
    a.w.b = if reuse { 1 } else { 0 }
    return Run(io, mut a)
}

// ListenJob is the native answer to core.IoOpListen: parse, open, bind
// and listen, read the bound port, wrap the socket. The socket is closed
// on every error path after it was opened.
ListenJob(a: *core.IoCallArgs) {
    var addr := ""
    var backlog := 0
    var reuse := true
    unsafe "a is the call's live record; p and pn are the caller's str" {
        addr = raw.Str((*a).p, (*a).pn)
        backlog = Trunc[int]((*a).w.a)
        reuse = (*a).w.b != 0
    }
    var sa := sys.SockAddr(family: sys.Inet4, port: 0, addr: [0; 16], scope_id: 0)
    if !ParseAddr(addr, true, mut sa) {
        Answer(a, core.IoResult(value: 0, err: core.SysErr.InvalidAddress, os: 0))
        return
    }
    s := sys.SocketOpen(sa.family, true)
    if s.err != core.SysErr.None {
        Answer(a, IoOf(s))
        return
    }
    sh := Trunc[uint](s.value)
    l := sys.SocketListenWith(sh, &sa, backlog, reuse)
    var bound := sa
    b := if l.err == core.SysErr.None { sys.SocketName(sh, mut bound) } else { l }
    if b.err != core.SysErr.None {
        _ = sys.SocketShutdownClose(sh)
        Answer(a, IoOf(b))
        return
    }
    h := NewSocketHandle(sh, sa.family)
    if h == 0 {
        _ = sys.SocketShutdownClose(sh)
        Answer(a, core.IoResult(value: 0, err: core.SysErr.Other, os: 0))
        return
    }
    Answer(a, core.IoResult(value: Trunc[i64](h), err: core.SysErr.None, os: i64(bound.port)))
}

// Answer stores the call's result in its record.
Answer(a: *core.IoCallArgs, r: core.IoResult) {
    unsafe "a is the call's live record" {
        (*a).res = r
    }
}
