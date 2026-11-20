// Why: the exit and panic paths must write output when no task can suspend,
// and every runtime handle needs one record that says what it is.
//
// What: HandleRec (the record behind a runtime handle, whose address is the
// handle), its Kind constants, the New*Handle constructors, Rec,
// CloseRuntimeHandle and HandleWrite, and DirectIo, an Io over DirectTable.
//
// How: a runtime handle is the address of a HandleRec from the heap. The
// direct IoTable writes on the calling thread and never suspends; its wait
// returns at once.

import std.core
import std.raw
import std.sys

/// The record behind a runtime handle; the handle is its address.
/// Only the task that owns the handle (File is linear) reads or writes it,
/// so no field needs an atomic; two kinds are shared: a KindPty record's
/// os is kept under its Console's lock (a Share Pty's tasks use it at
/// once), and a socket's shut and parked under its wake lock (a Share
/// Conn's Read, WriteAll and Shutdown run in different tasks).
export struct HandleRec {
    /// The OS handle or descriptor.
    export os: uint
    /// sys.HandleKind's answer, or KindSocket for the sockets
    /// NewSocketHandle records (Windows GetFileType cannot tell a socket
    /// from a pipe), or one of the Kind constants below.
    export kind: u32
    /// Writes go through sys.WriteConsole.
    export console: bool
    /// The handle is attached to the poller (sys.PollAttach returned true,
    /// or a socket std.sys attached), so every read and write of it goes
    /// through the poller, never a blocking call: an overlapped Windows
    /// handle has no file pointer, and a synchronous call on it could post a
    /// completion packet naming a pool thread's frame.
    export async: bool
    /// The console writer's partial UTF-8 sequence between writes.
    export carry: sys.ConsoleCarry
    /// A socket's address family (an accept needs the listener's); for a
    /// KindProcess record, 1 when the child's exit code is known.
    export family: u16
    /// A final OS close failed and the OS handle is gone, so only the record
    /// is left to free.
    export released: bool
    /// A socket whose SO_LINGER is on with 0 seconds (rt.SockOption): its
    /// close skips the shutdown (sys.SocketAbortClose), so it resets the
    /// connection and leaves no TIME_WAIT entry; a shutdown first would
    /// send a FIN and end in TIME_WAIT on both targets (measured on
    /// Windows by the RT-Os-Inherit block's connections).
    export abort: bool
    /// An attached file's stream position, the offset a read or write at pos
    /// -1 uses and then advances (std.task NativeRead); a KindProcess
    /// record's exit code; an inherited socket's shared port.
    export pos: i64
    /// A socket's spin lock over shut and parked (Windows; sockwake.mk).
    export wake: raw.Atomic[u32]
    /// The directions a Shutdown of this socket shut (ShutRead, ShutWrite;
    /// Windows), under wake.
    export shut: u32
    /// The receives and sends of this socket submitted to the Windows port
    /// and not yet completed, linked from their callers' frames, so a
    /// Shutdown can cancel them (sockwake.mk), under wake.
    export parked: *Parked
    /// A KindDir record's sys.DirBlockBytes block, a KindForeign record's
    /// ForeignOps, the Console a KindProcess record's child was started
    /// on (Windows, until ChildWait answers; pty.mk), or a KindPty record's
    /// Console; null otherwise.
    export dir: *u8
    /// A socket the native accept made: its peer's address as the accept
    /// op returned it (AcceptEx's buffer through GetAcceptExSockaddrs,
    /// accept4's sockaddr under epoll and io_uring), which SockAddress
    /// answers with no getpeername. NoPeer (family
    /// 0) on every other record, and on an accepted socket whose address
    /// the OS gave in a family other than Inet4 and Inet6.
    export peer: sys.SockAddr
}

/// NoPeer is HandleRec.peer when no accept named the peer.
export const NoPeer = sys.SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0)

/// KindSocket is HandleRec.kind of a socket; it is closed with
/// sys.SocketShutdownClose (closesocket; on Linux it leaves epoll first).
export const KindSocket: u32 = 4

/// IsNativeSocket: h is a runtime handle on an OS socket (its record's
/// kind is KindSocket), not a simulated one (a Sim's handles are
/// KindForeign) and not 0. Takes no Io, so std.net tells a native socket
/// from its record, not from an Io's answer.
/// O(1); allocates nothing.
export IsNativeSocket(h: uint): bool {
    if h == 0 {
        return false
    }
    unsafe "h is a runtime handle: every handle a safe program holds is a live record" {
        return (*Rec(h)).kind == KindSocket
    }
}

/// NewHandle wraps an OS handle; 0 when the heap is exhausted.
/// O(1): asks the OS the handle's kind and whether it is a console; allocates
/// one HandleRec from the Heap.
export NewHandle(os: uint, async: bool): uint {
    p := HeapAlloc(SizeOf[HandleRec](), AlignOf[HandleRec]())
    if raw.IsNull(p) {
        return 0
    }
    unsafe "p is a fresh block of HandleRec's size" {
        r := raw.PtrCast[u8, HandleRec](p)
        raw.Write(r, HandleRec(os: os, kind: sys.HandleKind(os), console: sys.IsConsole(os), async: async,
            carry: sys.NewCarry(), family: 0, released: false, abort: false, pos: 0,
            wake: raw.Atomic[u32].New(0), shut: 0, parked: raw.Null[Parked](), dir: raw.Null[u8](), peer: NoPeer))
        return raw.Addr(p)
    }
}

/// KindDir is HandleRec.kind of a directory stream: dir is its
/// sys.DirBlockBytes block from the heap, zeroed, freed when the record is.
export const KindDir: u32 = 5
/// ProcHandleIsFd reports whether a child's OS handle is a descriptor or
/// handle the OS closes (Linux's pidfd, Windows' process handle); on macOS
/// it is the pid, which nothing closes.
/// O(1); allocates nothing.
export ProcHandleIsFd(): bool {
    return Target.os != Os.Macos
}

/// KindProcess is HandleRec.kind of a child process (os the pidfd, the
/// process handle, or on macOS the pid).
export const KindProcess: u32 = 6

/// NewDirHandle wraps the directory stream os (sys.DirOpen) with a fresh
/// zeroed block; 0 when the heap is exhausted (the caller closes os).
/// O(1); allocates one HandleRec and one sys.DirBlockBytes block from the Heap.
export NewDirHandle(os: uint): uint {
    blk := NewDirBlock()
    if raw.IsNull(blk) {
        return 0
    }
    return DirHandleWith(os, blk)
}

// NewDirBlock is a zeroed sys.DirBlockBytes block from the Heap for a
// directory stream, or null when the heap is exhausted.
NewDirBlock(): *u8 {
    blk := HeapAlloc(sys.DirBlockBytes, 16)
    if !raw.IsNull(blk) {
        unsafe "blk is a fresh block of DirBlockBytes" {
            raw.Fill(blk, 0, sys.DirBlockBytes)
        }
    }
    return blk
}

// DirHandleWith wraps the directory stream os with blk, a NewDirBlock
// block the record takes (filled or not); 0 when the heap is exhausted,
// blk then freed (the caller closes os).
DirHandleWith(os: uint, blk: *u8): uint {
    p := HeapAlloc(SizeOf[HandleRec](), AlignOf[HandleRec]())
    if raw.IsNull(p) {
        HeapFree(blk, sys.DirBlockBytes, 16)
        return 0
    }
    unsafe "p is a fresh block of HandleRec's size; blk of DirBlockBytes" {
        r := raw.PtrCast[u8, HandleRec](p)
        raw.Write(r, HandleRec(os: os, kind: KindDir, console: false, async: false, carry: sys.NewCarry(),
            family: 0, released: false, abort: false, pos: 0,
            wake: raw.Atomic[u32].New(0), shut: 0, parked: raw.Null[Parked](), dir: blk, peer: NoPeer))
        return raw.Addr(p)
    }
}

/// KindForeign is HandleRec.kind of a handle another Io made (std.sim):
/// no OS handle is behind it, and dir is null or points at a
/// global ForeignOps (never freed). CloseRuntimeHandle, and so rt.Abandon,
/// calls its close with the handle while the record is still valid, then
/// frees the record; close must not close or free h itself.
export const KindForeign: u32 = 7

/// What a KindForeign record's owner is told.
export struct ForeignOps {
    /// Called with the handle when it is closed, before the record is freed.
    export close: fn(uint)
    /// Called by ChildSignal with a foreign child's handle and the signal
    /// (Linux's numbers), in place of the OS call: the owner's answer.
    export signal: fn(uint, u32): core.IoResult
}

/// IsForeign: h is a live runtime handle another Io made (KindForeign),
/// which no OS call may be given. O(1); allocates nothing.
export IsForeign(h: uint): bool {
    if h == 0 {
        return false
    }
    unsafe "h is a live runtime handle" {
        return (*Rec(h)).kind == KindForeign
    }
}

// FreeRec gives back a record and its directory block (a foreign record's
// dir is its global ForeignOps, left alone).
FreeRec(r: *HandleRec) {
    unsafe "a live runtime handle, not used after this" {
        if !raw.IsNull((*r).dir) && (*r).kind != KindForeign && (*r).kind != KindPty {
            HeapFree((*r).dir, sys.DirBlockBytes, 16)
        }
        HeapFree(raw.PtrCast[HandleRec, u8](r), SizeOf[HandleRec](), AlignOf[HandleRec]())
    }
}

/// NewSocketHandle wraps a socket of the given family; 0 when the heap is
/// exhausted (the caller closes the socket). No system call: a socket is
/// never a console, and its kind is known.
/// O(1); allocates one HandleRec from the Heap.
export NewSocketHandle(os: uint, family: u16): uint {
    p := HeapAlloc(SizeOf[HandleRec](), AlignOf[HandleRec]())
    if raw.IsNull(p) {
        return 0
    }
    unsafe "p is a fresh block of HandleRec's size" {
        r := raw.PtrCast[u8, HandleRec](p)
        raw.Write(r, HandleRec(os: os, kind: KindSocket, console: false, async: true, carry: sys.NewCarry(),
            family: family, released: false, abort: false, pos: 0,
            wake: raw.Atomic[u32].New(0), shut: 0, parked: raw.Null[Parked](), dir: raw.Null[u8](), peer: NoPeer))
        return raw.Addr(p)
    }
}

/// KindPty is HandleRec.kind of a Windows Pty end, one of the two pipes a
/// pseudo-console is read and written through: its dir is the Pty's
/// Console (pty.mk), whose lock guards its os, which a Spawn that reopens
/// the console replaces. A read or write takes its OS handle from
/// PtyUseBegin. The Pty owns the Console, so freeing the record leaves it.
export const KindPty: u32 = 8

/// KindPipe is sys.HandleKind's answer for a pipe.
export const KindPipe: u32 = 3

/// NewPipeHandle wraps the parent's end of a child's pipe on Windows (the
/// spawn): an attached named pipe server, recorded as a pipe without asking
/// HandleKind, whose GetNamedPipeInfo fails on an outbound server end (no
/// FILE_READ_ATTRIBUTES) and so would call it a socket; 0 when the heap is
/// exhausted. O(1); allocates one HandleRec from the heap.
export NewPipeHandle(os: uint): uint {
    p := HeapAlloc(SizeOf[HandleRec](), AlignOf[HandleRec]())
    if raw.IsNull(p) {
        return 0
    }
    unsafe "p is a fresh block of HandleRec's size" {
        r := raw.PtrCast[u8, HandleRec](p)
        raw.Write(r, HandleRec(os: os, kind: KindPipe, console: false, async: true, carry: sys.NewCarry(),
            family: 0, released: false, abort: false, pos: 0,
            wake: raw.Atomic[u32].New(0), shut: 0, parked: raw.Null[Parked](), dir: raw.Null[u8](), peer: NoPeer))
        return raw.Addr(p)
    }
}

/// NewProcHandle wraps a child process (the pidfd on Linux, the process
/// handle on Windows); 0 when the heap is exhausted (the caller closes os).
/// async: the epoll backend registered the pidfd (sys.Spawn), so the close
/// leaves epoll first (PollDetach; nothing elsewhere). family 1 says the
/// child's exit code is known and pos holds it (std.task's procwait; Linux
/// reaps the child once).
/// O(1); allocates one HandleRec from the Heap.
export NewProcHandle(os: uint): uint {
    p := HeapAlloc(SizeOf[HandleRec](), AlignOf[HandleRec]())
    if raw.IsNull(p) {
        return 0
    }
    unsafe "p is a fresh block of HandleRec's size" {
        r := raw.PtrCast[u8, HandleRec](p)
        raw.Write(r, HandleRec(os: os, kind: KindProcess, console: false, async: true, carry: sys.NewCarry(),
            family: 0, released: false, abort: false, pos: 0,
            wake: raw.Atomic[u32].New(0), shut: 0, parked: raw.Null[Parked](), dir: raw.Null[u8](), peer: NoPeer))
        return raw.Addr(p)
    }
}

/// NewInheritedSocket wraps the listening socket this process inherited
/// (std.task's inherited entry), as NewSocketHandle does; pos is shared
/// (sys.SharedPort when the socket's completion port is the parent's,
/// Windows, or on Linux; else 0), which every accept on it passes on as
/// op.pos.
/// 0 when the heap is exhausted. O(1); allocates one HandleRec from the Heap.
export NewInheritedSocket(os: uint, family: u16, shared: i64): uint {
    h := NewSocketHandle(os, family)
    if h != 0 {
        unsafe "h is the record NewSocketHandle just made, not yet shared" {
            (*Rec(h)).pos = shared
        }
    }
    return h
}

/// Rec is the record behind a runtime handle.
/// h must be live; nothing is checked. O(1); allocates nothing.
export Rec(h: uint): *HandleRec {
    unsafe "h is a runtime handle NewHandle made" {
        return raw.FromAddr[HandleRec](h)
    }
}

/// CloseRuntimeHandle closes the OS handle (a socket with
/// SocketShutdownClose) and frees the record. When that final OS close
/// fails, the error is returned and the record stays valid and owned, so
/// the caller's Abandon still runs; but the record is marked
/// released, because the OS handle is gone either way (Linux close releases
/// the descriptor even when it fails, and a second close could hit a
/// descriptor another thread has reused). A close of a released record
/// frees it with no OS call and succeeds, and a foreign record (KindForeign)
/// is handed to its ForeignOps first, never to the OS. h 0 is the inert
/// handle. An
/// attached file leaves the poller first (sys.PollDetach, as the poller's
/// contract asks; a socket's SocketShutdownClose does that itself).
/// O(1): one or two system calls; frees the record (and a directory block) when
/// the close succeeds.
export CloseRuntimeHandle(h: uint): sys.OsResult {
    ok := sys.OsResult(value: 0, err: core.SysErr.None, os: 0)
    if h == 0 {
        return ok
    }
    r := Rec(h)
    unsafe "a live runtime handle" {
        if (*r).kind == KindForeign {
            // No OS handle: tell its owner, then free the record.
            if !raw.IsNull((*r).dir) {
                f := (*raw.PtrCast[u8, ForeignOps]((*r).dir)).close
                f(h)
            }
            FreeRec(r)
            return ok
        }
        if (*r).kind == KindProcess && !raw.IsNull((*r).dir) {
            // A child started on a Windows pseudo-console and not waited
            // for holds a reference to its Console (pty.mk).
            ConsoleRelease(raw.PtrCast[u8, Console]((*r).dir))
            (*r).dir = raw.Null[u8]()
        }
        if (*r).released {
            FreeRec(r)
            return ok
        }
        var res := ok
        if (*r).kind == KindProcess && !ProcHandleIsFd() {
            // A macOS child's handle is its pid, no descriptor: nothing to
            // close (the poller holds no registration for it either).
        } else if (*r).kind == KindSocket {
            res = if (*r).abort { sys.SocketAbortClose((*r).os) } else { sys.SocketShutdownClose((*r).os) }
        } else {
            if (*r).async {
                sys.PollDetach((*r).os)
            }
            res = sys.Close((*r).os)
        }
        if res.err == core.SysErr.None {
            FreeRec(r)
        } else {
            (*r).released = true
        }
        return res
    }
}

/// HandleWrite writes data to a runtime handle on the calling thread. An
/// attached handle is Other: a synchronous write of an overlapped handle can
/// post a completion packet for an OVERLAPPED in this frame (std.task writes
/// those through the poller; the exit path writes only the standard streams,
/// which are never attached).
/// Else the OS's error. O(len(data)): one write call, or a console write per
/// chunk of converted text; allocates nothing.
export HandleWrite(h: uint, data: []u8, pos: i64): sys.OsResult {
    r := Rec(h)
    unsafe "a live runtime handle" {
        if (*r).async {
            return sys.OsResult(value: 0, err: core.SysErr.Other, os: 0)
        }
        if (*r).console {
            return sys.WriteConsole((*r).os, data, mut (*r).carry)
        }
        return sys.WriteSync((*r).os, data, pos)
    }
}

/// IoOf turns an OsResult into the IoResult std sees.
/// O(1); allocates nothing.
export IoOf(r: sys.OsResult): core.IoResult {
    return core.IoResult(value: r.value, err: r.err, os: r.os)
}

OtherResult(): core.IoResult {
    return core.IoResult(value: 0, err: core.SysErr.Other, os: 0)
}

/// DirectTable is the IoTable of DirectIo: write runs on the calling thread
/// and never suspends; wait returns at once (a spurious wake), wake does
/// nothing, and every other entry answers Other.
export const DirectTable = core.IoTable(open: DirectOpen, read: DirectRead, write: DirectWrite, size: DirectHandle,
    sync: DirectHandle, close: DirectHandle, rename: DirectRename, remove: DirectPath, listen: DirectPath,
    accept: DirectHandle, dial: DirectPath, recv: DirectRecv, send: DirectSend, now: DirectNone,
    sleep: DirectSleep, random: DirectRandom, yield: DirectNone, signal: DirectSignal, wait: DirectWait,
    wake: DirectWake, mkdir: DirectPath, stat: DirectStat, opendir: DirectPath, readdir: DirectReadDir,
    udp: DirectUdp, sendto: DirectSendTo, recvfrom: DirectRecvFrom, resolve: DirectResolve,
    spawn: DirectSpawn, procwait: DirectHandle, prockill: DirectHandle, inherited: DirectNone,
    enter: core.InertHook, leave: core.InertHook, deadline: core.InertDeadline,
    stdio: core.InertStdio, call: core.InertCall)

/// DirectIo is an Io over DirectTable.
/// O(1); allocates nothing.
export DirectIo(): Io {
    unsafe "DirectTable is a constant, alive for the whole process" {
        return core.MakeIo(raw.Of(&DirectTable), 1)
    }
}

DirectWrite(io: Io, h: uint, data: []u8, pos: i64): core.IoResult {
    return IoOf(HandleWrite(h, data, pos))
}

DirectOpen(io: Io, p: str, mode: u32): core.IoResult {
    return OtherResult()
}

DirectRead(io: Io, h: uint, buf: mut []u8, pos: i64): core.IoResult {
    return OtherResult()
}

DirectHandle(io: Io, h: uint): core.IoResult {
    return OtherResult()
}

DirectRename(io: Io, src: str, dst: str): core.IoResult {
    return OtherResult()
}

DirectPath(io: Io, p: str): core.IoResult {
    return OtherResult()
}

DirectRecv(io: Io, h: uint, buf: mut []u8): core.IoResult {
    return OtherResult()
}

DirectSend(io: Io, h: uint, data: []u8): core.IoResult {
    return OtherResult()
}

DirectNone(io: Io): core.IoResult {
    return OtherResult()
}

DirectSleep(io: Io, nanos: i64): core.IoResult {
    return OtherResult()
}

DirectRandom(io: Io, buf: mut []u8): core.IoResult {
    return OtherResult()
}

DirectSignal(io: Io, s: u32): core.IoResult {
    return OtherResult()
}

// DirectWait spins: it returns at once, a spurious wake every caller
// tolerates.
DirectWait(io: Io, addr: *u32, expected: u32): core.IoResult {
    sys.ThreadYield()
    return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
}

DirectWake(io: Io, addr: *u32, all: bool) {
}

// The directory, stat and process entries: none is on the exit or panic path, and each
// would suspend or open something, so the direct table refuses them all.
DirectStat(io: Io, p: str, st: mut core.IoStat): core.IoResult {
    return OtherResult()
}

DirectReadDir(io: Io, h: uint, name: mut []u8, st: mut core.IoStat): core.IoResult {
    return OtherResult()
}

DirectUdp(io: Io, addr: core.IoAddr): core.IoResult {
    return OtherResult()
}

DirectSendTo(io: Io, h: uint, data: []u8, to: core.IoAddr): core.IoResult {
    return OtherResult()
}

DirectRecvFrom(io: Io, h: uint, buf: mut []u8, from: mut core.IoAddr): core.IoResult {
    return OtherResult()
}

DirectResolve(io: Io, host: str, out: mut []core.IoAddr): core.IoResult {
    return OtherResult()
}

DirectSpawn(io: Io, args: []str, child: mut core.IoChild): core.IoResult {
    return OtherResult()
}
