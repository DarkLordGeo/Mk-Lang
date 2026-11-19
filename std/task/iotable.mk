// Why: std.core's Io is a table of functions so that tests and hosts can
// swap the I/O layer; native programs need the one table that goes to the
// operating system through the scheduler.
//
// What: NativeTable, the IoTable of every native Io, NativeIo (Main's Io),
// the result helpers NoErr and ErrOf, the cancellation check Stop,
// PollBackend and the stdio entry NativeStdio.
//
// How: NativeTable is a const with one function per entry; each entry
// submits to the poller or the pool and parks the calling task until the
// operation completes.

import std.core
import std.raw
import std.rt

/// NativeTable is the IoTable of every native Io: each entry submits to the
/// poller or the pool and parks the calling task until it completes.
export const NativeTable = core.IoTable(open: NativeOpen, read: NativeRead, write: NativeWrite, size: NativeSize,
    sync: NativeSync, close: NativeClose, rename: NativeRename, remove: NativeRemove, listen: NativeListen,
    accept: NativeAccept, dial: NativeDial, recv: NativeRecv, send: NativeSend, now: NativeNow,
    sleep: NativeSleep, random: NativeRandom, yield: NativeYield, signal: NativeSignal, wait: NativeWait,
    wake: NativeWake, mkdir: NativeMkdir, stat: NativeStat, opendir: NativeOpenDir, readdir: NativeReadDir,
    udp: NativeUdp, sendto: NativeSendTo, recvfrom: NativeRecvFrom, resolve: NativeResolveAll,
    spawn: NativeSpawn, procwait: NativeProcWait, prockill: NativeProcKill, inherited: NativeInherited,
    enter: core.InertHook, leave: core.InertHook, deadline: NativeDeadline,
    stdio: NativeStdio, call: NativeCall)

// NativeIo is Main's Io: the native table, no flags.
NativeIo(): Io {
    unsafe "NativeTable is a constant, alive for the whole process" {
        return core.MakeIo(raw.Of(&NativeTable), 0)
    }
}

// Result values every entry returns.
NoErr(v: i64): core.IoResult {
    return core.IoResult(value: v, err: core.SysErr.None, os: 0)
}

ErrOf(e: core.SysErr): core.IoResult {
    return core.IoResult(value: 0, err: e, os: 0)
}

// Stop is the cancellation check every cancellable entry makes before it
// starts; off a task stack nothing is cancelled.
// While no live scope has its flag set (core.CancelQuiet) the answer
// is false without finding the task.
Stop(io: Io): bool {
    if core.CancelQuiet() {
        return false
    }
    t := Current()
    return if raw.IsNull(t) { false } else { IsCancelled(t, io) }
}

/// PollBackend is what sys.PollInit chose (1 iocp, 2 io_uring, 3 epoll).
/// O(1); allocates nothing.
export PollBackend(): u32 {
    unsafe "the TaskState page Start mapped, which lives until exit" {
        return (*TS()).backend
    }
}

// NativeStdio is the native table's stdio entry: data whole to the
// process stream i, after its buffered bytes, under its lock
// (core.StdWrite); not a cancellation point. Other on a failed write or an
// i other than 0 and 1.
NativeStdio(io: Io, i: u32, data: []u8): core.IoResult {
    if core.StdWrite(rt.Proc(), i, io, data) {
        return NoErr(i64(data.Len()))
    }
    return ErrOf(core.SysErr.Other)
}
