// Why: std.fs, std.net, std.term and std.time need OS operations that std.sim
// must also be able to answer in a simulation, and a program should link
// each operation (and on Windows its kernel32 import) only when it uses it.
//
// What: the std-internal entry points for files (truncate, sync, stat, lock,
// map, copy, links, the working directory), sockets, terminals, the wall
// clock and the time zone, each with its own job function.
//
// How: each function passes its own job through the Io's call entry
// (core.IoCall); the job is the only code that calls std.sys for it. Routes
// (each function's doc line):
// - pool, cancellable before the call starts: Truncate, DataSync, StatEx,
//   SetModified, SetMode, Symlink, ReadLink, Cwd, Absolute;
// - pool, cancellable while it runs (an unknown prefix of the destination
//   may be written): CopyFile, which polls its job's cancel word between
//   bounded steps (IoCallArgs.stop);
// - inline, one non-blocking call, no cancellation point: Seek, Unlock,
//   Map, Unmap, the socket calls, IsTerminal, TermSize, TimeZone;
// - Lock: an inline try and, while another holder has the lock, Io.Sleep
//   between tries (1 ms doubling to 50 ms), so a cancel or a deadline ends
//   the wait at a sleep (repeatable: nothing is taken until a try succeeds)
//   and no pool thread is held by a wait.
// Every function's errors are its std.sys call's (each target's call.mk),
// with Canceled from a pool route or a sleep.

import std.core
import std.raw
import std.sys

// LockFirstWait and LockMaxWait bound UnixDial's sleeps between tries,
// LockFirstWait and LockPollMax FileLock's (1 ms doubling to 64 ms).
const LockFirstWait: i64 = 1_000_000
const LockMaxWait: i64 = 50_000_000
const LockPollMax: i64 = 64_000_000

// CallOf is a fresh record for op on runtime handle h (0 none), run by job.
CallOf(op: u32, route: u32, h: uint, job: fn(*core.IoCallArgs)): core.IoCallArgs {
    return core.IoCallArgs(op: op, route: route, h: h, os: 0, p: raw.Null[u8](), pn: 0, q: raw.Null[u8](), qn: 0,
        out: raw.Null[u8](), outn: 0, w: core.IoWords(a: 0, b: 0, c: 0, d: 0),
        res: core.IoResult(value: 0, err: core.SysErr.None, os: 0), job: job, stop: raw.Null[u32]())
}

// Run passes a to io's call entry; a stays in the caller's frame until it
// returns.
Run(io: Io, a: mut core.IoCallArgs): core.IoResult {
    unsafe "a is in the caller's frame until IoCall returns" {
        return core.IoCall(io, raw.OfMut(a))
    }
}

// Set stores a std.sys result in the record.
Set(a: *core.IoCallArgs, r: sys.OsResult) {
    unsafe "a is the call's live record" {
        (*a).res = core.IoResult(value: r.value, err: r.err, os: r.os)
    }
}

// In, In2 and Out are the record's byte views.
In(a: *core.IoCallArgs): []u8 {
    unsafe "p and pn are the caller's slice, live until the call returns" {
        return raw.Slice((*a).p, (*a).pn)
    }
}

In2(a: *core.IoCallArgs): []u8 {
    unsafe "q and qn are the caller's slice, live until the call returns" {
        return raw.Slice((*a).q, (*a).qn)
    }
}

Out(a: *core.IoCallArgs): mut []u8 {
    unsafe "out and outn are the caller's slice, live until the call returns" {
        return raw.SliceMut((*a).out, (*a).outn)
    }
}

/// FileSeek moves the stream position of the file h: whence 0 from the
/// start, 1 from the position, 2 from the end; value is the new position.
/// A handle on the poller keeps its position in its record, a pool
/// handle in the OS; a pipe, FIFO or socket on the Linux poller has none,
/// and its seek is the OS's error (ESPIPE). A result before 0, or a whence
/// above 2, is Other.
/// Inline; O(1); allocates nothing.
export FileSeek(io: Io, h: uint, off: i64, whence: u32): core.IoResult {
    var a := CallOf(core.IoOpSeek, core.IoRouteInline, h, SeekJob)
    a.w.a = off
    a.w.b = i64(whence)
    return Run(io, mut a)
}

SeekJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record; h is the owner's handle (File.Seek takes mut self)" {
        w := (*a).w
        if w.b < 0 || w.b > 2 {
            Set(a, sys.OsResult(value: 0, err: core.SysErr.Other, os: 0))
            return
        }
        r := Rec((*a).h)
        // A pipe, FIFO or socket on the Linux poller has no position to
        // keep: the OS answers (ESPIPE), as lseek does for every Linux
        // program.
        if !(*r).async || Target.os == Os.Linux && ((*r).kind == KindPipe || (*r).kind == KindSocket) {
            Set(a, sys.Seek((*a).os, w.a, Trunc[u32](w.b)))
            return
        }
        var from: i64 = 0
        if w.b == 1 {
            from = (*r).pos
        } else if w.b == 2 {
            s := sys.FileSize((*a).os)
            if s.err != core.SysErr.None {
                Set(a, s)
                return
            }
            from = s.value
        }
        pos := from +% w.a
        if (w.a > 0 && pos < from) || (w.a < 0 && pos > from) || pos < 0 {
            Set(a, sys.OsResult(value: 0, err: core.SysErr.Other, os: 0))
            return
        }
        (*r).pos = pos
        Set(a, sys.OsResult(value: pos, err: core.SysErr.None, os: 0))
    }
}

/// FileTruncate sets the file h's size (a longer size extends it with
/// zeros; the position does not move). A negative size is Other. Pool.
/// O(1) system calls; allocates nothing.
export FileTruncate(io: Io, h: uint, size: i64): core.IoResult {
    var a := CallOf(core.IoOpTruncate, core.IoRoutePool, h, TruncateJob)
    a.w.a = size
    return Run(io, mut a)
}

TruncateJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        Set(a, sys.Truncate((*a).os, (*a).w.a))
    }
}

/// FileDataSync flushes the file h's written data and the metadata a read
/// of it needs (its size, not its times): Linux fdatasync; Windows has no
/// data-only flush in kernel32, so FlushFileBuffers flushes everything.
/// Pool, cancellable before the call starts (a started flush runs to its
/// end); repeatable. O(1) system calls; allocates nothing.
export FileDataSync(io: Io, h: uint): core.IoResult {
    var a := CallOf(core.IoOpDataSync, core.IoRoutePool, h, DataSyncJob)
    return Run(io, mut a)
}

DataSyncJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        Set(a, sys.DataSync((*a).os))
    }
}

/// FileLock takes the whole-file advisory lock of h, shared or exclusive.
/// With once, one try: value 1 taken, 0 another holder has it; not a
/// cancellation point. Without once, value 1 once taken, holding no thread
/// while it waits: on Windows, for a handle on the completion port,
/// one overlapped LockFileEx on the poller (core.IoRouteLock), which the
/// kernel completes when the holder releases the lock; elsewhere (Linux, a
/// handle off the port, another Io) tries spaced by Io.Sleep from 1 ms
/// doubling to 64 ms, so the hand-over can lag the release by up to
/// 64 ms. Either way a cancel or a deadline ends the wait with Canceled,
/// and a scope cancelled before the call gets Canceled with nothing taken.
/// Inline tries, the first one checked; O(1) system calls per try;
/// allocates nothing.
export FileLock(io: Io, h: uint, exclusive: bool, once: bool): core.IoResult {
    if !once && core.IsNative(io) && sys.PollSupports(sys.OpLock) && OnPort(h) {
        var a := CallOf(core.IoOpLock, core.IoRouteLock, h, LockJob)
        a.w.a = if exclusive { 2 } else { 1 }
        return Run(io, mut a)
    }
    var wait := LockFirstWait
    // A waiting Lock is a cancellation point that checks before it starts,
    // whether or not it would wait: its first try goes by the checked route, so a cancelled
    // scope gets Canceled with nothing taken even when the lock is free
    // (RT8-Call-LockCancelFree); later tries follow a sleep, which checks.
    // TryLock never waits and is no cancellation point (inline).
    var route := if once { core.IoRouteInline } else { core.IoRouteChecked }
    for {
        var a := CallOf(core.IoOpLock, route, h, LockJob)
        route = core.IoRouteInline
        a.w.a = if exclusive { 2 } else { 1 }
        r := Run(io, mut a)
        if r.err != core.SysErr.None || r.value == 1 || once {
            return r
        }
        io.Sleep(core.DurationOf(wait)) else {
            return core.IoResult(value: 0, err: core.SysErr.Canceled, os: 0)
        }
        wait = if wait >= LockPollMax / 2 { LockPollMax } else { wait * 2 }
    }
}

// OnPort: h's record is on the poller (an overlapped handle attached to
// Windows' completion port).
OnPort(h: uint): bool {
    unsafe "h is a live runtime handle" {
        return (*Rec(h)).async
    }
}

LockJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        Set(a, sys.FileLock((*a).os, (*a).w.a == 2, true))
    }
}

/// FileUnlock drops h's whole-file lock; none held succeeds. Inline; O(1);
/// allocates nothing.
export FileUnlock(io: Io, h: uint): core.IoResult {
    var a := CallOf(core.IoOpUnlock, core.IoRouteInline, h, UnlockJob)
    return Run(io, mut a)
}

UnlockJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        Set(a, sys.FileUnlock((*a).os))
    }
}

/// FileMap maps len bytes (len > 0) of the file h from off, read-only and
/// private: value is the address of byte off, w.c the mapping's
/// base and w.d its length, which FileUnmap takes. Inline; O(1) system
/// calls; allocates nothing but the mapping.
export FileMap(io: Io, h: uint, off: i64, len: int, w: mut core.IoWords): core.IoResult {
    var a := CallOf(core.IoOpMap, core.IoRouteInline, h, MapJob)
    a.w.a = off
    a.w.b = i64(len)
    r := Run(io, mut a)
    *w = a.w
    return r
}

MapJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        n := (*a).w.b
        if n <= 0 || n > 0x7FFF_FFFF_FFFF {
            Set(a, sys.OsResult(value: 0, err: core.SysErr.Other, os: 0))
            return
        }
        Set(a, sys.MapFile((*a).os, (*a).w.a, Trunc[int](n), mut (*a).w))
    }
}

/// FileUnmap releases a mapping FileMap made, with its w.c and w.d.
/// Inline; O(1); allocates nothing.
export FileUnmap(io: Io, base: i64, len: i64): core.IoResult {
    var a := CallOf(core.IoOpUnmap, core.IoRouteInline, 0, UnmapJob)
    a.w.a = base
    a.w.b = len
    return Run(io, mut a)
}

UnmapJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        Set(a, sys.UnmapFile(Trunc[uint]((*a).w.a), Trunc[int]((*a).w.b)))
    }
}

/// StatEx fills w for the path p: a the kind (core.IoKind*), b the size,
/// c the modification time in nanoseconds since the epoch, d the mode bits;
/// nofollow reports a final link itself (fs.StatLink). Pool; O(1) system
/// calls; allocates nothing.
export StatEx(io: Io, p: []u8, nofollow: bool, w: mut core.IoWords): core.IoResult {
    var a := CallOf(core.IoOpStat, core.IoRoutePool, 0, StatJob)
    a.p = raw.Data(p)
    a.pn = p.Len()
    a.w.a = if nofollow { 1 } else { 0 }
    r := Run(io, mut a)
    *w = a.w
    return r
}

StatJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        Set(a, sys.StatEx(In(a), (*a).w.a == 1, mut (*a).w))
    }
}

/// SetModified sets the path p's modification time, nanoseconds since the
/// epoch, following a link. Pool; O(1) system calls; allocates nothing.
export SetModified(io: Io, p: []u8, nanos: i64): core.IoResult {
    var a := CallOf(core.IoOpSetModified, core.IoRoutePool, 0, SetModifiedJob)
    a.p = raw.Data(p)
    a.pn = p.Len()
    a.w.a = nanos
    return Run(io, mut a)
}

SetModifiedJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        Set(a, sys.SetModified(In(a), (*a).w.a))
    }
}

/// SetMode sets the path p's mode bits (Windows: the read-only attribute
/// from the owner's write bit). Pool; O(1) system calls; allocates nothing.
export SetMode(io: Io, p: []u8, mode: u32): core.IoResult {
    var a := CallOf(core.IoOpSetMode, core.IoRoutePool, 0, SetModeJob)
    a.p = raw.Data(p)
    a.pn = p.Len()
    a.w.a = i64(mode)
    return Run(io, mut a)
}

SetModeJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        Set(a, sys.SetMode(In(a), Trunc[u32]((*a).w.a)))
    }
}

/// Symlink makes link a symbolic link to target. Unsupported on Windows
/// without the privilege. Pool; O(1) system calls; allocates nothing.
export Symlink(io: Io, target: []u8, link: []u8): core.IoResult {
    var a := CallOf(core.IoOpSymlink, core.IoRoutePool, 0, SymlinkJob)
    a.p = raw.Data(target)
    a.pn = target.Len()
    a.q = raw.Data(link)
    a.qn = link.Len()
    return Run(io, mut a)
}

SymlinkJob(a: *core.IoCallArgs) {
    Set(a, sys.Symlink(In(a), In2(a)))
}

/// ReadLink writes the target of the link p into out: value the bytes
/// written, out.Len() when it may be cut (call again with more room).
/// Pool; O(1) system calls; allocates nothing.
export ReadLink(io: Io, p: []u8, out: mut []u8): core.IoResult {
    var a := CallOf(core.IoOpReadLink, core.IoRoutePool, 0, ReadLinkJob)
    a.p = raw.Data(p)
    a.pn = p.Len()
    a.out = raw.DataMut(out)
    a.outn = out.Len()
    return Run(io, mut a)
}

ReadLinkJob(a: *core.IoCallArgs) {
    Set(a, sys.ReadLink(In(a), Out(a)))
}

/// Cwd writes the working directory into out: value as ReadLink's. Pool;
/// O(1) system calls; allocates nothing.
export Cwd(io: Io, out: mut []u8): core.IoResult {
    var a := CallOf(core.IoOpCwd, core.IoRoutePool, 0, CwdJob)
    a.out = raw.DataMut(out)
    a.outn = out.Len()
    return Run(io, mut a)
}

CwdJob(a: *core.IoCallArgs) {
    Set(a, sys.Cwd(Out(a)))
}

/// Absolute writes p as an absolute path into out (Windows resolves '.'
/// and '..'; Linux joins the working directory and p): value as
/// ReadLink's. Pool; O(1) system calls; allocates nothing.
export Absolute(io: Io, p: []u8, out: mut []u8): core.IoResult {
    var a := CallOf(core.IoOpAbsolute, core.IoRoutePool, 0, AbsoluteJob)
    a.p = raw.Data(p)
    a.pn = p.Len()
    a.out = raw.DataMut(out)
    a.outn = out.Len()
    return Run(io, mut a)
}

AbsoluteJob(a: *core.IoCallArgs) {
    Set(a, sys.Absolute(In(a), Out(a)))
}

/// CopyFile copies src into dst (created or replaced): value the bytes
/// copied. A copy of a file onto itself, by any path or link, is Denied
/// and truncates nothing, on both targets. Pool, cancellable
/// before it starts and while it runs: a cancel or a deadline ends it with
/// Canceled at the next step (an open that waits, as a FIFO's with no
/// writer, or the next chunk), with an unknown prefix of src in dst (on
/// Windows CopyFileExW deletes the partial dst). O(size) in the
/// kernel; allocates nothing.
export CopyFile(io: Io, src: []u8, dst: []u8): core.IoResult {
    var a := CallOf(core.IoOpCopy, core.IoRoutePool, 0, CopyJob)
    a.p = raw.Data(src)
    a.pn = src.Len()
    a.q = raw.Data(dst)
    a.qn = dst.Len()
    return Run(io, mut a)
}

CopyJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        Set(a, sys.CopyFile(In(a), In2(a), (*a).stop))
    }
}

/// SockOption sets one option (core.IoSock*) of the socket h: b its value,
/// c the second (KeepAlive's idle seconds, Linger's seconds); NoDelay and
/// KeepAlive on a Unix-domain socket are Unsupported. Inline; O(1);
/// allocates nothing.
export SockOption(io: Io, h: uint, opt: i64, b: i64, c: i64): core.IoResult {
    var a := CallOf(core.IoOpSockOpt, core.IoRouteInline, h, SockOptJob)
    a.w.a = opt
    a.w.b = b
    a.w.c = c
    return Run(io, mut a)
}

SockOptJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        w := (*a).w
        // TCP's options have no meaning on a Unix-domain socket (unix.mk).
        if (w.a == core.IoSockNoDelay || w.a == core.IoSockKeepAlive) && IsUnix((*a).h) {
            Set(a, sys.OsResult(value: 0, err: core.SysErr.Unsupported, os: 0))
            return
        }
        r := sys.SocketOption((*a).os, w.a, w.b, w.c)
        if r.err == core.SysErr.None && w.a == core.IoSockLinger {
            // The close of a socket set to reset skips the shutdown
            // (HandleRec.abort); the job runs on the owner's task.
            (*Rec((*a).h)).abort = w.b == 1 && w.c == 0
        }
        Set(a, r)
    }
}

/// SockOptionGet reads one option of the socket h (core.IoSock*): value
/// NoDelay's, KeepAlive's or ReuseAddr's 0 or 1, RecvBuffer's or
/// SendBuffer's bytes, Linger's 1 or 0; os KeepAlive's idle seconds before
/// the first probe (0 when the OS does not answer them) or Linger's
/// seconds. A Unix-domain socket's TCP option, and ReuseAddr on Windows,
/// are Unsupported or Other as SockOption's; an Io with no OS socket (a
/// Sim, a Tape) answers Unsupported. The call op is IoOpSockOpt, so it
/// adds none. Inline; O(1); allocates nothing.
export SockOptionGet(io: Io, h: uint, opt: i64): core.IoResult {
    var a := CallOf(core.IoOpSockOpt, core.IoRouteInline, h, SockOptGetJob)
    a.w.a = opt
    return Run(io, mut a)
}

SockOptGetJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        w := (*a).w
        if (w.a == core.IoSockNoDelay || w.a == core.IoSockKeepAlive) && IsUnix((*a).h) {
            Set(a, sys.OsResult(value: 0, err: core.SysErr.Unsupported, os: 0))
            return
        }
        Set(a, sys.SocketOptionGet((*a).os, w.a))
    }
}

/// SockShutdown shuts down the socket h: how 0 read, 1 write, 2 both; Other
/// for any other how. A receive parked on h in another task then ends with
/// the end of the stream (how 0 or 2), and a send with Reset (how 1 or 2),
/// on Windows as on Linux. Inline; O(p), p the ops parked on h; allocates
/// nothing.
export SockShutdown(io: Io, h: uint, how: u32): core.IoResult {
    var a := CallOf(core.IoOpShutdown, core.IoRouteInline, h, ShutdownJob)
    a.w.a = i64(how)
    return Run(io, mut a)
}

// ShutdownJob: on Windows a shutdown that succeeded also ends the
// receives or sends parked on the socket in other tasks (ShutWake), which
// the port would leave waiting.
ShutdownJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        how := Trunc[u32]((*a).w.a)
        r := sys.SocketShutdown((*a).os, how)
        if Target.os == Os.Windows && r.err == core.SysErr.None && how <= 2 && (*Rec((*a).h)).kind == KindSocket {
            ShutWake((*a).h, how)
        }
        Set(a, r)
    }
}

/// SockAddress reads the socket h's local address, or with peer its
/// peer's: out (16 bytes or more) gets the address bytes in network order
/// (core.IoAddr.ip's layout), w.a the port, w.b 1 for IPv6, w.c the zone;
/// Unsupported on a Unix-domain socket. The peer of a socket the native
/// accept made is the address that accept returned, read from its record
/// with no system call; any other address is getsockname's or
/// getpeername's. Inline; O(1); allocates nothing.
export SockAddress(io: Io, h: uint, peer: bool, out: mut []u8, w: mut core.IoWords): core.IoResult {
    var a := CallOf(if peer { core.IoOpPeerAddr } else { core.IoOpLocalAddr }, core.IoRouteInline, h, AddrJob)
    a.out = raw.DataMut(out)
    a.outn = out.Len()
    r := Run(io, mut a)
    *w = a.w
    return r
}

AddrJob(a: *core.IoCallArgs) {
    out := Out(a)
    if out.Len() < 16 {
        Set(a, sys.OsResult(value: 0, err: core.SysErr.Other, os: 0))
        return
    }
    var s := sys.SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0)
    unsafe "a is the call's live record" {
        // A Unix-domain socket's address is a path (unix.mk).
        if IsUnix((*a).h) {
            Set(a, sys.OsResult(value: 0, err: core.SysErr.Unsupported, os: 0))
            return
        }
        // An accepted socket's peer is the address its accept returned
        // (HandleRec.peer): no getpeername, and a peer that reset
        // since is still named.
        p := (*Rec((*a).h)).peer
        if (*a).op == core.IoOpPeerAddr && p.family != 0 {
            s = p
        } else {
            r := if (*a).op == core.IoOpLocalAddr { sys.SocketName((*a).os, mut s) } else { sys.SocketPeer((*a).os, mut s) }
            if r.err != core.SysErr.None {
                Set(a, r)
                return
            }
        }
        for i in 0..16 {
            out[i] = s.addr[i]
        }
        (*a).w.a = i64(s.port)
        (*a).w.b = if s.family == sys.Inet6 { 1 } else { 0 }
        (*a).w.c = i64(s.scope_id)
        Set(a, sys.OsResult(value: 0, err: core.SysErr.None, os: 0))
    }
}

/// IsTerminal: value 1 when the standard stream s (0 in, 1 out, 2 err) is
/// a terminal (a Windows console), else 0; Other for another s. Inline;
/// O(1); allocates nothing.
export IsTerminal(io: Io, s: u32): core.IoResult {
    var a := CallOf(core.IoOpIsTerminal, core.IoRouteInline, 0, IsTerminalJob)
    a.w.a = i64(s)
    return Run(io, mut a)
}

IsTerminalJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        s := (*a).w.a
        if s < 0 || s > 2 {
            Set(a, sys.OsResult(value: 0, err: core.SysErr.Other, os: 0))
            return
        }
        yes := sys.IsTerminal(sys.StdHandle(Trunc[int](s)))
        Set(a, sys.OsResult(value: if yes { 1 } else { 0 }, err: core.SysErr.None, os: 0))
    }
}

/// TermSize reads the size of the terminal on the standard stream s: w.a
/// columns, w.b rows. Inline; O(1); allocates nothing.
export TermSize(io: Io, s: u32, w: mut core.IoWords): core.IoResult {
    var a := CallOf(core.IoOpTermSize, core.IoRouteInline, 0, TermSizeJob)
    a.w.a = i64(s)
    r := Run(io, mut a)
    *w = a.w
    return r
}

TermSizeJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        s := (*a).w.a
        if s < 0 || s > 2 {
            Set(a, sys.OsResult(value: 0, err: core.SysErr.Other, os: 0))
            return
        }
        Set(a, sys.TermSize(sys.StdHandle(Trunc[int](s)), mut (*a).w))
    }
}

/// TimeZone writes the OS's time-zone record for year (0 the zone in force)
/// into out: Windows' only (sys.TimeZone); Unsupported on Linux. Inline;
/// O(1); allocates nothing.
export TimeZone(io: Io, year: i64, out: mut []u8): core.IoResult {
    var a := CallOf(core.IoOpTimeZone, core.IoRouteInline, 0, TimeZoneJob)
    a.w.a = year
    a.out = raw.DataMut(out)
    a.outn = out.Len()
    return Run(io, mut a)
}

TimeZoneJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        Set(a, sys.TimeZone((*a).w.a, Out(a)))
    }
}

/// WallClock is the wall clock, nanoseconds since the Unix epoch: the OS's
/// (CLOCK_REALTIME; GetSystemTimePreciseAsFileTime), or a Sim's simulated
/// one under std.sim; std.time's Wall reads it. Inline; O(1);
/// allocates nothing; never fails natively, Unsupported on an Io with no
/// clock.
export WallClock(io: Io): core.IoResult {
    var a := CallOf(core.IoOpWall, core.IoRouteInline, 0, WallJob)
    return Run(io, mut a)
}

WallJob(a: *core.IoCallArgs) {
    Set(a, sys.OsResult(value: sys.WallNow(), err: core.SysErr.None, os: 0))
}

/// MapBytes is the bytes of a live mapping FileMap made: addr its value,
/// len the length mapped; len <= 0 gives an empty slice. std-internal:
/// std.fs's Mapping is its only caller, and holds the mapping for as long
/// as the slice lives (Mapping.Bytes(&self): []u8 from self). O(1);
/// allocates nothing.
export MapBytes(addr: i64, len: int): []u8 {
    if len <= 0 {
        return "".AsBytes()
    }
    unsafe "addr is FileMap's address of a live mapping of len bytes, which only std.fs's Mapping holds" {
        return raw.Slice(raw.FromAddr[u8](Trunc[uint](addr)), len)
    }
}
