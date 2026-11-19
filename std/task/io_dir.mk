// Why: std.fs's MakeDir, Stat and Dir block in the kernel on every
// backend, so they cannot run on a carrier thread.
//
// What: the native table's directory entries NativeMkdir, NativeStat,
// NativeOpenDir and NativeReadDir, the frame record DirArgs and the
// refill job JobDirFill.
//
// How: mkdir, stat, opendir and readdir run on the blocking pool on every
// backend, as rename and remove do on iocp and epoll. Every entry is one
// std.sys call or one Await; a handle an entry made is closed on each of
// its error paths. A listing reads entries from a block filled once per
// pool trip, not one trip per entry.

import std.core
import std.raw
import std.rt
import std.sys

// DirArgs is a directory job's frame record (op.buf points at it): the
// stream's OS handle and block (JobDirFill), or the facts (JobStat). It
// lives in the entry's frame until Blocking returns.
struct DirArgs {
    h: uint
    block: *u8
    st: core.IoStat
}

JobMkdir(op: *sys.Op) {
    unsafe "op is the caller's, live until the job completes" {
        (*op).res = sys.MakeDir((*op).path)
    }
}

JobStat(op: *sys.Op) {
    unsafe "op.buf is the caller's DirArgs" {
        a := raw.PtrCast[u8, DirArgs]((*op).buf)
        (*op).res = sys.StatPath((*op).path, mut (*a).st)
    }
}

JobOpenDir(op: *sys.Op) {
    unsafe "op is the caller's, live until the job completes" {
        (*op).res = sys.DirOpen((*op).path)
    }
}

// JobDirFill refills a directory stream's block (sys.DirFill): the only
// pool job of a listing, once per block of entries.
JobDirFill(op: *sys.Op) {
    unsafe "op.buf is the caller's DirArgs; its block is live" {
        a := raw.PtrCast[u8, DirArgs]((*op).buf)
        (*op).res = sys.DirFill((*a).h, raw.SliceMut((*a).block, sys.DirBlockBytes))
    }
}

NewDirArgs(): DirArgs {
    return DirArgs(h: 0, block: raw.Null[u8](), st: core.IoStat(kind: 0, size: 0, modified: 0))
}

NativeMkdir(io: Io, path: str): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    var op := sys.NewOp(sys.OpOpen, 0)
    op.path = path
    return OnPool(io, JobMkdir, mut op)
}

// NativeStat fills st only on success.
NativeStat(io: Io, path: str, st: mut core.IoStat): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    var a := NewDirArgs()
    var op := sys.NewOp(sys.OpOpen, 0)
    op.path = path
    unsafe "a is in this frame until OnPool returns" {
        op.buf = raw.PtrCast[DirArgs, u8](raw.OfMut(mut a))
    }
    r := OnPool(io, JobStat, mut op)
    if r.err == core.SysErr.None {
        *st = a.st
    }
    return r
}

// NativeOpenDir opens a directory stream. The descriptor a finished job
// made is wrapped even when a cancel came after it (the pool returns the
// job's result once it ran), so none is dropped; on a heap failure it is
// closed.
NativeOpenDir(io: Io, path: str): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    var op := sys.NewOp(sys.OpOpen, 0)
    op.path = path
    r := OnPool(io, JobOpenDir, mut op)
    if r.err != core.SysErr.None {
        return r
    }
    os := Trunc[uint](r.value)
    h := rt.NewDirHandle(os)
    if h == 0 {
        _ = sys.Close(os)
        return ErrOf(core.SysErr.Other)
    }
    return NoErr(Trunc[i64](h))
}

// NativeReadDir reads the next entry of the stream h (a KindDir handle;
// anything else is Other). name and st are written only on success. An
// entry already in the stream's block is served on the task's own stack
// with no system call (sys.DirNext); the pool runs only the refill
// (JobDirFill), once per block, not one pool round trip per entry. A
// cancel is seen at entry and during a refill.
NativeReadDir(io: Io, h: uint, name: mut []u8, st: mut core.IoStat): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    rec := rt.Rec(h)
    var a := NewDirArgs()
    unsafe "a live runtime handle" {
        if h == 0 || (*rec).kind != rt.KindDir || raw.IsNull((*rec).dir) {
            return ErrOf(core.SysErr.Other)
        }
        a.h = (*rec).os
        a.block = (*rec).dir
    }
    for {
        var r := sys.OsResult(value: 0, err: core.SysErr.None, os: 0)
        unsafe "the block is the handle's, used by one call at a time" {
            r = sys.DirNext(raw.SliceMut(a.block, sys.DirBlockBytes), name, st)
        }
        if r.err != core.SysErr.None || r.value >= 0 {
            return rt.IoOf(r)
        }
        var op := sys.NewOp(sys.OpOpen, 0)
        unsafe "a is in this frame until OnPool returns" {
            op.buf = raw.PtrCast[DirArgs, u8](raw.OfMut(mut a))
        }
        f := OnPool(io, JobDirFill, mut op)
        if f.err != core.SysErr.None {
            return f
        }
        if f.value == 0 {
            return NoErr(0)
        }
    }
}
