// Why: std.proc's Run and Supervise start, wait for and kill child
// processes; starting one blocks its thread, waiting must not.
//
// What: the native table's process entries NativeSpawn, NativeProcWait,
// NativeProcKill and NativeInherited, the frame record SpawnArgs, and the
// pipe helpers WrapPipe and ClosePipe.
//
// How: spawn runs on the blocking pool; the child's wait is one Await on
// the poller (a pidfd on Linux, a registered wait on Windows), so a waiting
// child holds no thread; the inherited listener is handed out once by a Cas.

import std.core
import std.raw
import std.rt
import std.sys

// SpawnArgs is a spawn job's frame record (op.buf points at it): the
// arguments, the child's request and answers, and the inherited socket's OS
// handle.
struct SpawnArgs {
    args: *str
    n: int
    child: core.IoChild
    inherit: uint
}

JobSpawn(op: *sys.Op) {
    unsafe "op.buf is the caller's SpawnArgs; its args are the caller's for the call" {
        a := raw.PtrCast[u8, SpawnArgs]((*op).buf)
        (*op).res = sys.Spawn((*rt.R()).init_sp, raw.Slice((*a).args, (*a).n), mut (*a).child, (*a).inherit)
    }
}

// WrapPipe makes the runtime handle of a pipe end sys.Spawn made: a pipe
// on the poller on Linux, an attached named pipe on Windows; 0 when the
// heap is exhausted (the caller closes the end).
WrapPipe(os: uint): uint {
    return rt.NewPipeHandle(os)
}

// ClosePipe closes a pipe end that has no runtime handle yet.
ClosePipe(os: uint) {
    _ = sys.Close(os)
}

// NativeSpawn starts a child process (the IoTable's spawn entry) on
// the blocking pool: CreateProcessW and the vfork clone both hold their
// thread until the child is made. value the child's runtime handle
// (KindProcess); child.pipes the runtime handles of the parent's pipe ends
// (read and written with the read and write entries at pos -1), child.pid
// the OS id. A child made after a cancel is still answered (the pool
// returns the job's result), so no process handle is lost. When a record
// cannot be had, the child is killed and reaped, as rt.Spawn does, every
// handle is closed, and the answer is Other with os 0 (std.proc's
// OutOfMemory): a child left running unwatched would keep what it
// inherited (a Supervise listener) past its caller's end.
NativeSpawn(io: Io, args: []str, child: mut core.IoChild): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    var a := SpawnArgs(args: raw.Null[str](), n: args.Len(), child: *child, inherit: 0)
    unsafe "args is the caller's for the call" {
        a.args = raw.Data(args)
        if child.inherit != 0 {
            rec := rt.Rec(child.inherit)
            if (*rec).kind != rt.KindSocket {
                return ErrOf(core.SysErr.Other)
            }
            a.inherit = (*rec).os
        }
    }
    var op := sys.NewOp(sys.OpOpen, 0)
    unsafe "a is in this frame until OnPool returns" {
        op.buf = raw.PtrCast[SpawnArgs, u8](raw.OfMut(mut a))
    }
    r := OnPool(io, JobSpawn, mut op)
    if r.err != core.SysErr.None {
        return r
    }
    var hs: [3]uint = [0; 3]
    var ok := true
    for i in 0..3 {
        if a.child.pipes[i] != 0 {
            hs[i] = WrapPipe(a.child.pipes[i])
            if hs[i] == 0 {
                ok = false
            }
        }
    }
    ph := rt.NewProcHandle(Trunc[uint](r.value))
    if ph == 0 || !ok {
        for i in 0..3 {
            if hs[i] != 0 {
                _ = rt.CloseRuntimeHandle(hs[i])
            } else if a.child.pipes[i] != 0 {
                ClosePipe(a.child.pipes[i])
            }
        }
        // A killed process ends in the kernel's time, so the reap loop is
        // bounded by it.
        os := Trunc[uint](r.value)
        _ = sys.ChildSignal(os, false, a.child.pid, 0, 9)
        for !sys.TryReap(os) {
            sys.ThreadYield()
        }
        if ph != 0 {
            _ = rt.CloseRuntimeHandle(ph)
        } else {
            _ = sys.Close(os)
        }
        return ErrOf(core.SysErr.Other)
    }
    child.pipes = hs
    child.pid = a.child.pid
    if child.inherit != 0 {
        // The child holds the listener too: this copy's close must not end
        // it for the child, and on Linux its accepts take the shared path.
        rt.PassOn(child.inherit)
    }
    return NoErr(Trunc[i64](ph))
}

// NativeProcWait waits for the child h to end on the poller (OpProcWait:
// Linux a readability wait on the pidfd, Windows a registered wait posting
// to the port), so a waiting child holds no thread and Supervise's workers
// never use up the pool: value its exit code. A cancel ends the wait with
// Canceled and leaves the child running. The first answer is kept in the
// record (Linux reaps the child), so a second wait answers it again at once.
NativeProcWait(io: Io, h: uint): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    rec := rt.Rec(h)
    unsafe "h is a live runtime handle, used by its owner" {
        if h == 0 || (*rec).kind != rt.KindProcess {
            return ErrOf(core.SysErr.Other)
        }
        if (*rec).family == 1 {
            return NoErr((*rec).pos)
        }
        var op := sys.NewOp(sys.OpProcWait, (*rec).os)
        // pos 1 marks a group leader (std.rt's Spawn): Linux's poller holds
        // it unreaped while it kills the group (sys ProcTry); 0 otherwise.
        op.pos = (*rec).pos
        r := OnPoller(io, mut op)
        // The wait's own resources go only now that no completion or
        // callback can touch op (Windows: the registered wait).
        sys.PollRelease(raw.OfMut(mut op))
        if r.err == core.SysErr.None {
            (*rec).pos = r.value
            (*rec).family = 1
        }
        return r
    }
}

// NativeProcKill ends the child h at once; a child that already ended is
// success.
NativeProcKill(io: Io, h: uint): core.IoResult {
    rec := rt.Rec(h)
    unsafe "h is a live runtime handle" {
        if h == 0 || (*rec).kind != rt.KindProcess {
            return ErrOf(core.SysErr.Other)
        }
        return if (*rec).family == 1 { NoErr(0) } else { rt.IoOf(sys.ProcKill((*rec).os)) }
    }
}

// Inherit is the inherited entry's once word: 0 unasked, 1 taken.
struct InheritState {
    taken: raw.Atomic[u32]
}

var Inherit: InheritState = InheritState(taken: raw.Atomic[u32].New(0))

// NativeInherited answers the listening socket this process inherited from
// a spawn with inherit, at most once (a second call is NotFound): the Cas
// gives it to one caller, and nothing else is published by it. os is the
// socket's bound port, as listen answers it (Listener.Port()).
NativeInherited(io: Io): core.IoResult {
    // Edge: none beyond the Cas, which gives the socket to one caller
    // (the comment above); nothing else is published by it.
    if Inherit.taken.Load() != 0 || !Inherit.taken.Cas(0, 1) {
        return ErrOf(core.SysErr.NotFound)
    }
    var sp := raw.Null[u8]()
    unsafe "the RtState page Init mapped, which lives until exit; init_sp is set once at start" {
        sp = (*rt.R()).init_sp
    }
    r := sys.Inherited(sp)
    if r.err != core.SysErr.None {
        return rt.IoOf(r)
    }
    os := Trunc[uint](r.value)
    var a := sys.SockAddr(family: 0, port: 0, addr: [0; 16], scope_id: 0)
    n := sys.SocketName(os, mut a)
    if n.err != core.SysErr.None {
        _ = sys.SocketAbortClose(os)
        return rt.IoOf(n)
    }
    h := rt.NewInheritedSocket(os, a.family, r.os)
    if h == 0 {
        _ = sys.SocketAbortClose(os)
        return ErrOf(core.SysErr.Other)
    }
    // The parent and its other children hold the listener too, so a close
    // here releases this copy only.
    rt.PassShared(h)
    return core.IoResult(value: Trunc[i64](h), err: core.SysErr.None, os: i64(a.port))
}
