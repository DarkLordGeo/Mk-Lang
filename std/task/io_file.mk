// Why: file I/O must not hold a carrier while the disk or another process
// is slow, yet each backend can wait on files in different ways, so the
// route of every entry is chosen per backend and per handle kind.
//
// What: the native table's file entries (NativeOpen, NativeRead,
// NativeWrite, NativeSize, NativeSync, NativeClose, NativeRename,
// NativeRemove), the pool jobs (JobOpen, JobRead, JobWrite...), the route
// helpers OnPool, OnPoolWait, OnPoolIo, OnPoolWhole and OnPoller, and the
// test switches FilesOnPool and NinepTest.
//
// How: each entry runs on the poller where the backend can do the work and
// on the blocking pool (pool.mk) where it cannot; the rest of this comment
// lists the routes.
//
// Where each entry runs (FileRoute):
// - iocp: open, sync, rename and remove on the pool. A file is opened
//   overlapped and attached (sys.PollAttach); its reads and writes are
//   overlapped on the port. A handle that is not a disk file, or that the
//   port refuses, is closed and reopened for the pool (std.sys's contract).
// - io_uring: every entry on the ring (OPENAT, READ, WRITE, FSYNC,
//   RENAMEAT, UNLINKAT); a path that does not fit the frame's scratch
//   buffer goes to the pool, whose std.sys call has its own 4 KiB one. A
//   file on a 9p mount is not attached: its reads, writes and sync
//   go to the pool.
// - epoll: every entry on the pool (epoll cannot wait on a regular file).
// The standard streams are never attached (start.mk), so their reads and
// writes stay on the pool on every backend.
//
// An attached handle keeps its own stream position (HandleRec.pos): a read
// or write at pos -1 uses it and advances it by the count, as the OS file
// pointer would on the pool path (an overlapped Windows handle has none).
// Every entry is one OS call or one Await: a short count is returned to
// std, whose loops finish it. Partial reads and writes, EINTR, EAGAIN and
// the error mapping are std.sys's.

import std.core
import std.raw
import std.rt
import std.sys

// PathScratch is the frame buffer an io_uring path op copies its
// NUL-terminated paths into (std.sys's contract: the scratch lives until
// the completion, and Await does not return before it). Paths that do not
// fit go to the pool.
const PathScratch: int = 1024

// MaxFileChunk bounds one read or write (Windows ReadFile takes a u32
// length; std.sys clamps further); a larger buffer is a short count.
const MaxFileChunk: int = 0x4000_0000

// FilesOnPool is a test switch: 1 sends every file entry to the pool, so a
// block can compare the two routes on one backend (test "RT-File-RouteDiff") and
// time them (test "RT-Bench-File"). A handle keeps the
// route its open chose (HandleRec.async). Set by the test task before the
// opens it governs and read by that task, so the load needs no ordering
// edge beyond program order; the atomic only makes a stray reader on
// another carrier well defined.
var FilesOnPool: raw.Atomic[u32] = raw.Atomic[u32].New(0)

// NinepTest is a test switch: 1 makes every file the ring opens count as
// one on a 9p mount, so a block checks the routing in /tmp
// (test "RT8-File-Ninep"). Set and read by the test task, as FilesOnPool.
var NinepTest: raw.Atomic[u32] = raw.Atomic[u32].New(0)

// OnRing: the backend performs kind itself (io_uring for every file kind;
// iocp and epoll for none of open, sync, rename, remove).
OnRing(kind: u32): bool {
    // Edge: none; a test switch set before the test's tasks run.
    return FilesOnPool.Load() == 0 && sys.PollSupports(kind)
}

// AttachFiles: the backend's reads and writes of a file opened on the pool
// can go through the poller once attached (iocp only: epoll's PollAttach
// would register a blocking descriptor, and io_uring opens on the ring).
AttachFiles(): bool {
    // Edge: none; a test switch (above).
    return FilesOnPool.Load() == 0 && PollBackend() == 1
}

// The jobs: one std.sys call each, with its arguments in op and its result
// in op.res. op.h is the OS handle, except for JobWrite (the runtime
// handle, for the console path). JobOpenAsync opens for overlapped I/O.
JobOpen(op: *sys.Op) {
    unsafe "op is the caller's, live until the job completes" {
        (*op).res = sys.OpenFile((*op).path, Trunc[u32]((*op).pos), false)
    }
}

JobOpenAsync(op: *sys.Op) {
    unsafe "op is the caller's, live until the job completes" {
        (*op).res = sys.OpenFile((*op).path, Trunc[u32]((*op).pos), true)
    }
}

// JobRead is one ReadOnce: a pool thread's read can be interrupted,
// and an interruption the job did not ask for is repeated by the
// pool (RunJob), not here.
JobRead(op: *sys.Op) {
    unsafe "op.buf holds op.len bytes of the caller's buffer" {
        (*op).res = sys.ReadOnce((*op).h, raw.SliceMut((*op).buf, (*op).len), (*op).pos)
    }
}

// JobWrite is one WriteOnce, as JobRead is one ReadOnce, so a pool
// thread's write ends at an interrupt and a cancel or a deadline
// ends a write to a full pipe; the answer may be short, which every
// caller of the write entry finishes. A console handle keeps
// rt.HandleWrite's conversion (its writes never wait on a reader), and an
// attached one its refusal.
JobWrite(op: *sys.Op) {
    unsafe "op.buf holds op.len bytes of the caller's data; op.h is a live runtime handle" {
        r := rt.Rec((*op).h)
        data := raw.Slice((*op).buf, (*op).len)
        if (*r).console || (*r).async {
            (*op).res = rt.HandleWrite((*op).h, data, (*op).pos)
        } else {
            (*op).res = sys.WriteOnce((*r).os, data, (*op).pos)
        }
    }
}

// JobWriteOs is JobWrite's WriteOnce to op.h as an OS handle (a Windows
// Pty end's, held by PtyUseBegin).
JobWriteOs(op: *sys.Op) {
    unsafe "op.buf holds op.len bytes of the caller's data; op.h is an OS handle the caller holds" {
        (*op).res = sys.WriteOnce((*op).h, raw.Slice((*op).buf, (*op).len), (*op).pos)
    }
}

JobSync(op: *sys.Op) {
    unsafe "op is the caller's" {
        (*op).res = sys.SyncFile((*op).h)
    }
}

JobRename(op: *sys.Op) {
    unsafe "op.path and op.path2 are the caller's strings" {
        (*op).res = sys.Rename((*op).path, (*op).path2)
    }
}

JobRemove(op: *sys.Op) {
    unsafe "op.path is the caller's string" {
        (*op).res = sys.Remove((*op).path)
    }
}

// OnPool runs job over op on the blocking pool; Canceled when the cancel
// came before the job started, else the job's result.
OnPool(io: Io, job: fn(*sys.Op), op: mut sys.Op): core.IoResult {
    unsafe "op is in the caller's frame until Blocking returns" {
        e := Blocking(job, raw.OfMut(op), Cancellable(io))
        if e != core.SysErr.None {
            return ErrOf(e)
        }
    }
    return rt.IoOf(op.res)
}

// OnPoolWait is OnPool for a read or write that may wait for another
// process without bound (BlockingWait): it raises the pool's
// thread bound while it waits, so it does not hold one of the pool's
// MaxPoolThreads (up to BlockingWait's limits).
OnPoolWait(io: Io, job: fn(*sys.Op), op: mut sys.Op): core.IoResult {
    unsafe "op is in the caller's frame until BlockingWait returns" {
        e := BlockingWait(job, raw.OfMut(op), Cancellable(io))
        if e != core.SysErr.None {
            return ErrOf(e)
        }
    }
    return rt.IoOf(op.res)
}

// OnPoolIo runs a pool read or write of the record r: a disk file's
// (bounded by the device) on the pool's threads, anything else's (a pipe,
// a terminal, a pty end, a socket off the poller: each may wait for
// another process) as a wait job (OnPoolWait).
OnPoolIo(io: Io, r: *rt.HandleRec, job: fn(*sys.Op), op: mut sys.Op): core.IoResult {
    unsafe "r is a live runtime handle's record" {
        if (*r).kind != rt.KindDiskFile {
            return OnPoolWait(io, job, op)
        }
    }
    return OnPool(io, job, op)
}

// OnPoolWhole is OnPool for a job never interrupted once it runs
// (BlockingWhole): Canceled when the cancel came before a pool thread took
// it, else the job's result.
OnPoolWhole(io: Io, job: fn(*sys.Op), op: mut sys.Op): core.IoResult {
    unsafe "op is in the caller's frame until BlockingWhole returns" {
        e := BlockingWhole(job, raw.OfMut(op), Cancellable(io))
        if e != core.SysErr.None {
            return ErrOf(e)
        }
    }
    return rt.IoOf(op.res)
}

// OnPoller runs op on the poller (Await); Canceled when the cancel won,
// else the operation's result (a completion with data wins over a later
// cancel).
OnPoller(io: Io, op: mut sys.Op): core.IoResult {
    unsafe "op is in the caller's frame until Await returns" {
        e := Await(raw.OfMut(op), Cancellable(io))
        if e != core.SysErr.None {
            return ErrOf(e)
        }
    }
    return rt.IoOf(op.res)
}

// Wrap makes the runtime handle of a fresh OS handle; on a heap failure the
// OS handle is closed (no leak) and the result is Other.
Wrap(os: uint, async: bool): core.IoResult {
    h := rt.NewHandle(os, async)
    if h == 0 {
        if async {
            sys.PollDetach(os)
        }
        _ = sys.Close(os)
        return ErrOf(core.SysErr.Other)
    }
    return NoErr(Trunc[i64](h))
}

// NativeOpen opens path (mode 0 read, 1 create-truncate read-write). A
// completion that carries a descriptor wins over a cancel that came after
// it, so no descriptor is dropped.
NativeOpen(io: Io, path: str, mode: u32): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    if OnRing(sys.OpOpen) && path.Len() < PathScratch {
        var scratch: [1024]u8 = [0; 1024]
        var op := sys.NewOp(sys.OpOpen, 0)
        op.path = path
        op.pos = i64(mode)
        unsafe "scratch is in this frame until OnPoller returns" {
            op.buf = raw.DataMut(mut scratch[..])
        }
        op.len = PathScratch
        r := OnPoller(io, mut op)
        if r.err != core.SysErr.None {
            return r
        }
        os := Trunc[uint](r.value)
        // A file on a 9p mount stays off the ring,
        // so its reads and writes are the pool's blocking calls, which drop
        // their file reference before they return; a ring read of it would
        // go to io-wq and leave the last reference to a deferred free that
        // exit_group can interrupt. One fstatfs per ring open.
        // Edge: none; a test switch set by the test task before its opens
        // (as FilesOnPool).
        if NinepTest.Load() == 1 || sys.OnNinep(os) {
            return Wrap(os, false)
        }
        return Wrap(os, sys.PollAttach(os))
    }
    var op := sys.NewOp(0, 0)
    op.path = path
    op.pos = i64(mode)
    // An append handle stays off the port: a write there names an offset,
    // and FILE_APPEND_DATA appends only a synchronous write (sys.OpenFile).
    append := (mode & core.IoOpenOptions) != 0 && (mode & core.IoOpenAppend) != 0
    if !AttachFiles() || append {
        r := OnPool(io, JobOpen, mut op)
        return if r.err != core.SysErr.None { r } else { Wrap(Trunc[uint](r.value), false) }
    }
    r := OnPool(io, JobOpenAsync, mut op)
    if r.err != core.SysErr.None {
        return r
    }
    os := Trunc[uint](r.value)
    // Only a disk file goes on the port: a console, pipe or device opened
    // by name keeps the pool's synchronous calls (and the console's
    // WriteConsole path, which NewHandle detects).
    if sys.HandleKind(os) == 1 && sys.PollAttach(os) {
        return Wrap(os, true)
    }
    // Refused: reopen for the pool (an overlapped handle cannot read
    // at the stream position synchronously). Mode 1 truncates again a file
    // this call just created empty, which changes nothing. The reopen is
    // not cancellable: the first open already succeeded, and a cancel here
    // would only turn a working open into Canceled.
    _ = sys.Close(os)
    var again := sys.NewOp(0, 0)
    again.path = path
    again.pos = i64(mode)
    r2 := OnPool(io.NoCancel(), JobOpen, mut again)
    return if r2.err != core.SysErr.None { r2 } else { Wrap(Trunc[uint](r2.value), false) }
}

// Positioned is one read or write of an attached handle on the poller. At
// pos -1 a file uses its record's stream position and advances it by the
// count. A socket and a pipe have no position, so their record's is never
// touched (two tasks may read one Share pipe end): Linux is given -1 (a
// pread of a pipe is ESPIPE), Windows 0, which its overlapped pipe I/O
// ignores. The position is
// not synchronised: pos -1 is for the handle's exclusive owner (mut File,
// the fresh handles of fs.ReadBytes and fs.WriteAll); callers that share a
// handle (File.ReadAt, File.WriteAt take &self) pass pos >= 0, which never
// touches it.
Positioned(io: Io, kind: u32, r: *rt.HandleRec, p: *u8, n: int, pos: i64): core.IoResult {
    unsafe "r is the live record of the caller's handle, used by its owner only" {
        unpositioned := (*r).kind == rt.KindSocket || (*r).kind == rt.KindPipe
        stream := pos < 0 && !unpositioned
        var op := sys.NewOp(kind, (*r).os)
        op.buf = p
        op.len = if n > MaxFileChunk { MaxFileChunk } else { n }
        // 0 spares the port the file-type check it makes for a negative
        // offset.
        op.pos = if stream { (*r).pos } else if pos < 0 && unpositioned && Target.os == Os.Windows { 0 } else { pos }
        res := OnPoller(io, mut op)
        if stream && res.err == core.SysErr.None && res.value > 0 {
            (*r).pos += res.value
        }
        return res
    }
}

NativeRead(io: Io, h: uint, buf: mut []u8, pos: i64): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    r := rt.Rec(h)
    unsafe "h is a live runtime handle" {
        // A socket on the poller reads as recv. A socket wrapped by rt.NewHandle (async false: not on the
        // poller) keeps the pool's blocking read below, as a wait job
        // (OnPoolIo), as every pool read of a pipe, a pty end or
        // a terminal is.
        // With no task no handle is on the poller (only a task field
        // opens one), so these tests fold away with the poller's code.
        if rt.UsesTasks() && (*r).kind == rt.KindSocket && (*r).async {
            // A Unix stream's reset reads as Reset, as a TCP stream's does.
            return NativeRecv(io, h, buf)
        }
        if rt.UsesTasks() && (*r).async {
            return Positioned(io, sys.OpRead, r, raw.DataMut(buf), buf.Len(), pos)
        }
        // A Windows Pty end's OS handle is the Console's (a Spawn may
        // replace it): held from before the job is queued until it ends.
        pty := Target.os == Os.Windows && (*r).kind == rt.KindPty
        os := if pty { rt.PtyUseBegin(h) } else { (*r).os }
        var op := sys.NewOp(sys.OpRead, os)
        op.buf = raw.DataMut(buf)
        op.len = buf.Len()
        op.pos = pos
        res := OnPoolIo(io, r, JobRead, mut op)
        if pty {
            rt.PtyUseEnd(h, os)
        }
        return res
    }
}

NativeWrite(io: Io, h: uint, data: []u8, pos: i64): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    r := rt.Rec(h)
    unsafe "h is a live runtime handle; the write only reads the data" {
        // As NativeRead: only a socket on the poller writes as send.
        // With no task: as NativeRead.
        if rt.UsesTasks() && (*r).kind == rt.KindSocket && (*r).async {
            // A write to a Unix stream whose reader is gone is Reset, as a
            // TCP stream's is.
            return NativeSend(io, h, data)
        }
        if rt.UsesTasks() && (*r).async {
            var n := data.Len()
            if (*r).kind == rt.KindPipe && n > 0 {
                // A child's stdin on Windows (data wins over a cancel): at
                // most the pipe's free quota, which completes inside
                // WriteFile, or 1 byte when the
                // quota is 0, which a cancel ends whole or not at all; a
                // larger pending write that a cancel ends after the child
                // read part of it answers 995 with a count of 0. The answer
                // may be short, as a socket's is.
                room := sys.PipeRoom((*r).os)
                if room == 0 {
                    n = 1
                } else if room > 0 && room < n {
                    n = room
                }
            }
            return Positioned(io, sys.OpWrite, r, raw.Data(data), n, pos)
        }
        if Target.os == Os.Windows && (*r).kind == rt.KindPty {
            // As NativeRead's Pty end; the job writes to the held OS
            // handle, not to the record's.
            os := rt.PtyUseBegin(h)
            var held := sys.NewOp(sys.OpWrite, os)
            held.buf = raw.Data(data)
            held.len = data.Len()
            held.pos = pos
            res := OnPoolIo(io, r, JobWriteOs, mut held)
            rt.PtyUseEnd(h, os)
            return res
        }
        var op := sys.NewOp(sys.OpWrite, h)
        op.buf = raw.Data(data)
        op.len = data.Len()
        op.pos = pos
        return OnPoolIo(io, r, JobWrite, mut op)
    }
}

NativeSize(io: Io, h: uint): core.IoResult {
    unsafe "h is a live runtime handle" {
        return rt.IoOf(sys.FileSize((*rt.Rec(h)).os))
    }
}

NativeSync(io: Io, h: uint): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    unsafe "h is a live runtime handle" {
        r := rt.Rec(h)
        var op := sys.NewOp(sys.OpSync, (*r).os)
        if (*r).async && OnRing(sys.OpSync) {
            return OnPoller(io, mut op)
        }
        return OnPool(io, JobSync, mut op)
    }
}

// NativeClose is not cancellable.
NativeClose(io: Io, h: uint): core.IoResult {
    return rt.IoOf(rt.CloseRuntimeHandle(h))
}

NativeRename(io: Io, src: str, dst: str): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    var op := sys.NewOp(sys.OpRename, 0)
    op.path = src
    op.path2 = dst
    if OnRing(sys.OpRename) && src.Len() + dst.Len() + 2 <= PathScratch {
        var scratch: [1024]u8 = [0; 1024]
        unsafe "scratch is in this frame until OnPoller returns" {
            op.buf = raw.DataMut(mut scratch[..])
        }
        op.len = PathScratch
        return OnPoller(io, mut op)
    }
    return OnPool(io, JobRename, mut op)
}

NativeRemove(io: Io, path: str): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    var op := sys.NewOp(sys.OpRemove, 0)
    op.path = path
    if OnRing(sys.OpRemove) && path.Len() < PathScratch {
        var scratch: [1024]u8 = [0; 1024]
        unsafe "scratch is in this frame until OnPoller returns" {
            op.buf = raw.DataMut(mut scratch[..])
        }
        op.len = PathScratch
        return OnPoller(io, mut op)
    }
    return OnPool(io, JobRemove, mut op)
}
