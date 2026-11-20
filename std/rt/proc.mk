// Why: std.proc and std.term need pipes, process spawning, waits, signals,
// detach and terminal modes from the OS, which std.sim must also be able to
// answer from its own processes and terminal; a program should link each
// one, and its Windows imports, only when it uses it.
//
// What: the std-internal functions PipeOpen, Spawn, SpawnNamed, ChildWait,
// ChildSignal, ChildDetach (with its reaper thread), TermOpen, TermWait,
// TermRead and TermMode, each with its job. The pseudo-terminals are
// pty.mk's.
//
// How: each call goes through the Io's call entry (core.IoCall). Each
// function wraps the OS handles its job made as runtime handles here, on
// the calling task, after the call returns, so a cancel never separates a
// made handle from its wrapping. Routes:
// - inline (one non-blocking call, no cancellation point): PipeOpen,
//   ChildSignal, ChildDetach, TermOpen, TermMode;
// - pool, whole once started: Spawn (a child made is always answered);
// - ChildWait is the procwait entry's poller wait, plus the job's end.
// Pipe ends are the poller's on Linux (an end lent to a child under epoll is
// a blocking end of the same pipe, sys.PipeLend) and pool handles on
// Windows; the pty's ends and the terminal are pool handles (blocking reads
// and writes on the blocking pool, which a cancel interrupts), so an end
// lent to a child is a plain duplicate. Every pool read or write of a pipe,
// a pty end or the terminal is a wait job, which the pool's 64 threads do
// not bound.

import std.core
import std.raw
import std.sys

// DetachNode is one detached child waiting to be reaped (Linux).
struct DetachNode {
    h: uint
    next: u64
}

// DetachedState is the list of detached children (Linux) that the reaper
// thread does not hold (it could not start): head is the address of the
// first DetachNode (0 none), read and written only under lock.
struct DetachedState {
    lock: raw.Atomic[u32]
    head: raw.Atomic[u64]
}

var Detached: DetachedState = DetachedState(lock: raw.Atomic[u32].New(0), head: raw.Atomic[u64].New(0))

// ReaperState is the reaper of detached children (Linux): one thread,
// started at the first detach, that waits on the reaper set
// (sys.ReapSetWait), which holds the pidfd of each detached child not yet
// reaped with its runtime handle as the tag, and reaps each child as the
// set reports its end. So a process that never spawns again keeps a
// detached child as a zombie only until the reaper runs. state: ReaperIdle,
// ReaperStarting, ReaperRunning or ReaperFailed (no set or no thread:
// detached children go to the Detached list, reaped at the next Spawn or
// ChildDetach; a running reaper never fails). set is the set's descriptor once running;
// live counts the children in the set.
struct ReaperState {
    state: raw.Atomic[u32]
    set: raw.Atomic[u64]
    live: raw.Atomic[i64]
}

const ReaperIdle: u32 = 0
const ReaperStarting: u32 = 1
const ReaperRunning: u32 = 2
const ReaperFailed: u32 = 3

var Reaper: ReaperState = ReaperState(state: raw.Atomic[u32].New(0), set: raw.Atomic[u64].New(0),
    live: raw.Atomic[i64].New(0))

// ReaperStack is the reaper thread's stack, the pool threads' size: its
// own frames are ReapSetWait's 1 KiB event buffer, a close and a free, but
// a process-directed signal may run its handler, a fatal one's report
// included, on this thread. Pages are touched only as they are used.
const ReaperStack: int = 1048576

// ReaperPauseMin and ReaperPauseMax bound the reaper's pause after a
// failed wait, in nanoseconds.
const ReaperPauseMin: i64 = 1_000_000
const ReaperPauseMax: i64 = 1_000_000_000

// ReaperTags is how many ended children one wait of the reaper reports.
const ReaperTags: int = 64

// Fail is an error result with no OS code.
Fail(e: core.SysErr): core.IoResult {
    return core.IoResult(value: 0, err: e, os: 0)
}

PipeJob(a: *core.IoCallArgs) {
    Set(a, sys.Pipe())
}

// PipeEnd wraps an end sys.Pipe made: on the poller when sys.PipeAttach
// takes it (Linux, with tasks: a KindPipe record as Spawn's parent ends
// have), else a blocking pool end (Windows); 0 when the heap
// is exhausted (the caller closes the end; an attached end leaves the
// poller first, as CloseRuntimeHandle's).
PipeEnd(os: uint): uint {
    if UsesTasks() && sys.PipeAttach(os) {
        h := NewPipeHandle(os)
        if h == 0 {
            sys.PollDetach(os)
        }
        return h
    }
    return NewHandle(os, false)
}

/// PipeOpen makes a pipe: ends[0] the read end's runtime handle, ends[1]
/// the write end's, never inherited until lent to a child
/// (core.IoStdHandle). On Linux each end is on the poller (sys.PipeAttach:
/// io_uring as it is, epoll non-blocking and registered), as Spawn's
/// parent ends are, so a waiting read or write holds no thread; an
/// end lent to a child under epoll is given as sys.PipeLend's blocking end
/// of the same pipe (SpawnJob). On Windows, and on Linux when an end
/// cannot be attached, the ends are synchronous and read and written on
/// the blocking pool as wait jobs, which the pool's 64 threads do not
/// bound. A read of an end
/// whose writers are all closed is 0; a write to an end whose readers are
/// all closed is Closed. Under a simulated Io the ends are the
/// simulation's (std.sim). Inline; O(1); allocates two HandleRec records from
/// the Heap (Other, with both ends closed, when it is exhausted).
export PipeOpen(io: Io, ends: mut [2]uint): core.IoResult {
    var a := CallOf(core.IoOpPipe, core.IoRouteInline, 0, PipeJob)
    r := Run(io, mut a)
    if r.err != core.SysErr.None {
        return r
    }
    if !core.IsNative(io) {
        // A simulated Io answers with its own runtime handles (std.sim's
        // pipe), which are not OS handles to wrap.
        ends[0] = Trunc[uint](r.value)
        ends[1] = Trunc[uint](r.os)
        return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    }
    rd := Trunc[uint](r.value)
    wr := Trunc[uint](r.os)
    hr := PipeEnd(rd)
    hw := PipeEnd(wr)
    if hr == 0 || hw == 0 {
        if hr != 0 {
            _ = CloseRuntimeHandle(hr)
        } else {
            _ = sys.Close(rd)
        }
        if hw != 0 {
            _ = CloseRuntimeHandle(hw)
        } else {
            _ = sys.Close(wr)
        }
        return Fail(core.SysErr.Other)
    }
    ends[0] = hr
    ends[1] = hw
    return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
}

/// KindDiskFile is sys.HandleKind's answer for a disk file.
export const KindDiskFile: u32 = 1

// SpawnJob lends each IoStdHandle source's OS handle, except that on
// Windows a disk file opened for overlapped I/O (attached to the port) is
// lent as a synchronous handle that sys.ReOpenSync makes, for reading as
// stdin and for writing as stdout or stderr, closed here once the child
// has its duplicate: a child's ReadFile or WriteFile with no OVERLAPPED
// fails on an overlapped handle. The new handle is a new file object at
// position 0, which std.fs's calls never move on the old one either. On
// Linux a proc.Pipe end on the epoll poller (non-blocking) is lent as the
// blocking end of the same pipe that sys.PipeLend opens, closed here the
// same way, since O_NONBLOCK belongs to the open file every duplicate
// shares.
SpawnJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record; w.a is the caller's IoSpawnReq and p, q, out its slices, live until the call returns" {
        s := raw.FromAddr[core.IoSpawnReq](Trunc[uint]((*a).w.a))
        var src: [3]uint = [0; 3]
        // own[i] is a handle made here for stream i, closed after the
        // spawn when bit i of owned is set: a bit, not a non-zero value,
        // since PipeLend's descriptor can be 0 when fd 0 is free.
        var own: [3]uint = [0; 3]
        var owned: u32 = 0
        var r := sys.OsResult(value: 0, err: core.SysErr.None, os: 0)
        for i in 0..3 {
            if (*s).stdio[i] != core.IoStdHandle {
                continue
            }
            rec := Rec((*s).src[i])
            src[i] = (*rec).os
            if Target.os == Os.Windows && (*rec).async && (*rec).kind == KindDiskFile {
                o := sys.ReOpenSync((*rec).os, i != 0)
                if o.err != core.SysErr.None {
                    r = o
                    break
                }
                own[i] = Trunc[uint](o.value)
                owned |= u32(1) << Trunc[u32](i)
                src[i] = own[i]
            }
            if Target.os == Os.Linux && (*rec).async && (*rec).kind == KindPipe {
                // A proc.Pipe end on the poller: the child gets a
                // blocking end of the same pipe.
                o := sys.PipeLend((*rec).os)
                if o.err != core.SysErr.None {
                    r = o
                    break
                }
                src[i] = Trunc[uint](o.value)
                if o.os == 1 {
                    own[i] = src[i]
                    owned |= u32(1) << Trunc[u32](i)
                }
            }
        }
        // The named handles: each one's OS handle, and on Windows a
        // reopen bit for a disk file opened for overlapped I/O (the child
        // gets a synchronous one, as for a lent stream above); on Linux a
        // pipe end on the poller is lent as PipeLend's blocking end, closed
        // here as own's are (bit i of nowned: nos[i] is one).
        named := raw.Slice(raw.FromAddr[uint](Trunc[uint]((*a).w.b)), Trunc[int]((*a).w.c))
        var nos: [64]uint = [0; 64]
        var nowned: u64 = 0
        var reopen: u64 = 0
        if named.Len() > sys.PassMax {
            r = sys.OsResult(value: 0, err: core.SysErr.Other, os: 0)
        } else if r.err == core.SysErr.None {
            for i, x in named {
                rec := Rec(x)
                nos[i] = (*rec).os
                if Target.os == Os.Windows && (*rec).async && (*rec).kind == KindDiskFile {
                    reopen |= u64(1) << Trunc[u64](i)
                }
                if Target.os == Os.Linux && (*rec).async && (*rec).kind == KindPipe {
                    o := sys.PipeLend((*rec).os)
                    if o.err != core.SysErr.None {
                        r = o
                        break
                    }
                    nos[i] = Trunc[uint](o.value)
                    if o.os == 1 {
                        nowned |= u64(1) << Trunc[u64](i)
                    }
                }
            }
        }
        if r.err == core.SysErr.None {
            args := raw.Slice(raw.PtrCast[u8, str]((*a).p), (*a).pn)
            env := raw.Slice(raw.PtrCast[u8, str]((*a).q), (*a).qn)
            dir := raw.Str((*a).out, (*a).outn)
            r = sys.SpawnEx((*R()).init_sp, args, env, dir, mut *s, src, (*s).pty, nos[0..named.Len()], reopen)
        }
        for i in 0..3 {
            if (owned >> Trunc[u32](i)) & 1 != 0 {
                _ = sys.Close(own[i])
            }
        }
        for i in 0..sys.PassMax {
            if (nowned >> Trunc[u64](i)) & 1 != 0 {
                _ = sys.Close(nos[i])
            }
        }
        Set(a, r)
    }
}

/// Spawn starts args[0] with args as s says (core.IoSpawnReq): value the
/// child's runtime handle (KindProcess, waited for by the procwait entry
/// or ChildWait), os its pid; s.pipes the runtime handles of the parent's
/// pipe ends (on the poller), s.pid, s.job. env is used as s.env_mode
/// says; dir, when not empty, is the child's working directory; a program
/// name with no separator is searched in this process's PATH only. Lent
/// handles (IoStdHandle) are duplicated into the child; the caller keeps
/// its own (on Windows a disk file opened for overlapped I/O is lent as a
/// synchronous handle on the same file at position 0, made for the call).
/// On Linux each spawn first reaps the listed detached children that have
/// ended (only when the reaper thread could not start: ChildDetach).
/// Errors: the OS's (sys SpawnEx); IoStdHandle without a
/// handle, or an unknown mode, is Other; when the Heap cannot give a
/// record, the child is killed and reaped and the answer is Other. Pool, whole once
/// started; O(bytes of args, env and the environment); allocates one
/// HandleRec per handle from the Heap. On Windows a child started on a
/// pseudo-console (s.pty, PtyOpen's term) is counted on it until ChildWait
/// answers for it, and a console that closed after its last child was
/// waited for is opened again (pty.mk). Under a simulated Io the call's
/// answer is the simulation's own handles (std.sim's child).
export Spawn(io: Io, args: []str, env: []str, dir: str, s: mut core.IoSpawnReq): core.IoResult {
    var nothing: [0]uint = []
    return SpawnNamed(io, args, env, dir, s, nothing[..])
}

/// SpawnNamed is Spawn with the handles a Command names: named
/// are runtime handles (at most sys.PassMax, each native: PassNative), and
/// the child gets a copy of each beside its streams, listed in its
/// MK_HANDLES, as sys.SpawnEx says; the caller keeps its own, and each
/// socket among them is marked shared (PassOn) once the child starts.
/// Under a simulated Io named is ignored (std.sim's child runs no program).
export SpawnNamed(io: Io, args: []str, env: []str, dir: str, s: mut core.IoSpawnReq, named: []uint): core.IoResult {
    if Target.os != Os.Windows || s.pty == 0 || !core.IsNative(io) {
        return SpawnOn(io, args, env, dir, s, named)
    }
    term := s.pty
    c := ConsoleOf(term)
    b := ConsoleSpawnBegin(c)
    if b.err != core.SysErr.None {
        return core.IoResult(value: 0, err: b.err, os: b.os)
    }
    s.pty = Trunc[uint](b.value)
    r := SpawnOn(io, args, env, dir, s, named)
    s.pty = term
    started := r.err == core.SysErr.None
    if started {
        unsafe "the process record SpawnOn just made, not yet shared" {
            (*Rec(Trunc[uint](r.value))).dir = raw.PtrCast[Console, u8](c)
        }
    }
    ConsoleSpawnEnd(c, started)
    return r
}

// SpawnOn is Spawn with s.pty the OS's own pseudo-terminal (Linux's master
// descriptor, Windows' HPCON).
SpawnOn(io: Io, args: []str, env: []str, dir: str, s: mut core.IoSpawnReq, named: []uint): core.IoResult {
    for i in 0..3 {
        if s.stdio[i] == core.IoStdHandle && s.src[i] == 0 {
            return Fail(core.SysErr.Other)
        }
    }
    Reap()
    var a := CallOf(core.IoOpSpawn, core.IoRouteWhole, 0, SpawnJob)
    unsafe "args, env and dir are the caller's for the call; s is in the caller's frame until Run returns" {
        a.p = raw.PtrCast[str, u8](raw.Data(args))
        a.pn = args.Len()
        a.q = raw.PtrCast[str, u8](raw.Data(env))
        a.qn = env.Len()
        a.out = raw.StrData(dir)
        a.outn = dir.Len()
        a.w.a = Trunc[i64](raw.Addr(raw.OfMut(s)))
        a.w.b = Trunc[i64](raw.Addr(raw.Data(named)))
        a.w.c = i64(named.Len())
    }
    r := Run(io, mut a)
    if r.err != core.SysErr.None || !core.IsNative(io) {
        // A simulated Io's answer holds its own runtime handles already.
        return r
    }
    var hs: [3]uint = [0; 3]
    var ok := true
    for i in 0..3 {
        if s.pipes[i] != 0 {
            hs[i] = NewPipeHandle(s.pipes[i])
            if hs[i] == 0 {
                ok = false
            }
        }
    }
    ph := NewProcHandle(Trunc[uint](r.value))
    if ph == 0 || !ok {
        for i in 0..3 {
            if hs[i] != 0 {
                _ = CloseRuntimeHandle(hs[i])
            } else if s.pipes[i] != 0 {
                _ = sys.Close(s.pipes[i])
            }
        }
        // The child started but cannot be handed back, so it is killed
        // (its group with it) and reaped here rather than left running
        // unwatched. A killed process ends in the
        // kernel's time, so the reap loop is bounded by it.
        os := Trunc[uint](r.value)
        _ = sys.ChildSignal(os, s.group, s.pid, s.job, 9)
        for !sys.TryReap(os) {
            sys.ThreadYield()
        }
        if ph != 0 {
            _ = CloseRuntimeHandle(ph)
        } else {
            _ = sys.Close(Trunc[uint](r.value))
        }
        if s.job != 0 {
            sys.GroupClose(s.job)
            s.job = 0
        }
        s.pipes = [0; 3]
        return Fail(core.SysErr.Other)
    }
    if s.group {
        unsafe "ph is the record NewProcHandle just made, not yet shared" {
            // The procwait entry passes pos 1 to Linux's poller, which then
            // holds the leader unreaped while it kills the group.
            (*Rec(ph)).pos = 1
        }
    }
    s.pipes = hs
    for x in named {
        PassOn(x)
    }
    return core.IoResult(value: Trunc[i64](ph), err: core.SysErr.None, os: s.pid)
}

GroupEndJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        Set(a, sys.GroupEnd(Trunc[uint]((*a).w.a)))
    }
}

/// ChildWait waits for the child h to end (the procwait entry: a poller
/// wait, holding no thread; Canceled leaves the child running and
/// unreaped): value its exit code, or minus the signal that ended it on
/// Linux. When the child leads a group, what remains of the group is
/// killed before the answer: Linux's poller does it while the leader is
/// still unreaped (sys ProcTry), Windows ends the job here and closes it
/// (job is the child's IoSpawnReq.job, 0 for none, and is not used after a
/// successful wait). A child started on a Windows pseudo-console is
/// uncounted on it, and the console closes after the last (pty.mk).
/// O(1) besides the wait; allocates nothing.
export ChildWait(io: Io, h: uint, job: uint): core.IoResult {
    r := core.IoProcWait(io, h)
    if Target.os == Os.Windows && r.err == core.SysErr.None && !IsForeign(h) {
        unsafe "h is the caller's live process record" {
            rec := Rec(h)
            if !raw.IsNull((*rec).dir) {
                c := raw.PtrCast[u8, Console]((*rec).dir)
                (*rec).dir = raw.Null[u8]()
                ConsoleChildEnded(c)
            }
        }
    }
    if r.err == core.SysErr.None && job != 0 {
        var a := CallOf(core.IoOpGroupEnd, core.IoRouteInline, 0, GroupEndJob)
        a.w.a = Trunc[i64](job)
        _ = Run(core.IoMasked(io), mut a)
    }
    return r
}

SignalJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record; h is the owner's live handle" {
        r := Rec((*a).h)
        w := (*a).w
        if (*r).family == 1 {
            // Ended and reaped: nothing to signal, and a group's id may be
            // another's now.
            Set(a, sys.OsResult(value: 0, err: core.SysErr.None, os: 0))
            return
        }
        Set(a, sys.ChildSignal((*r).os, w.b == 1, w.c, Trunc[uint](w.d), Trunc[u32](w.a)))
    }
}

/// ChildSignal sends sig (Linux's numbers: 1 hang-up, 2 interrupt, 9 kill,
/// 15 terminate) to the child h, or with group to every process of its
/// group (pid the child's, job its IoSpawnReq.job). A child whose wait has
/// answered, or that has already ended, is success. Windows delivers only
/// 9 (TerminateJobObject or TerminateProcess); any other is Unsupported.
/// A child another Io made (KindForeign: std.sim's) is signalled by its
/// owner, whatever io is. Inline; O(1); allocates nothing.
export ChildSignal(io: Io, h: uint, group: bool, pid: i64, job: uint, sig: u32): core.IoResult {
    if IsForeign(h) {
        unsafe "h is the caller's live foreign record; dir is null or its owner's static ForeignOps" {
            ops := (*Rec(h)).dir
            if raw.IsNull(ops) {
                return Fail(core.SysErr.Closed)
            }
            f := (*raw.PtrCast[u8, ForeignOps](ops)).signal
            return f(h, sig)
        }
    }
    var a := CallOf(core.IoOpSignal, core.IoRouteInline, h, SignalJob)
    a.w.a = i64(sig)
    a.w.b = if group { 1 } else { 0 }
    a.w.c = pid
    a.w.d = Trunc[i64](job)
    return Run(io, mut a)
}

DetachJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record; h is the owner's handle, given up here" {
        h := (*a).h
        job := Trunc[uint]((*a).w.a)
        if job != 0 {
            sys.GroupClose(job)
        }
        if Target.os == Os.Windows || (*Rec(h)).family == 1 {
            Set(a, CloseRuntimeHandle(h))
            return
        }
        if ReaperOn() {
            // Edge: live is a count for DetachedLen; the add comes before
            // the child can be reported, so the reaper's Sub never takes
            // it below the children still held.
            _ = Reaper.live.Add(1)
            set := Trunc[uint](Reaper.set.Load())
            if sys.ReapSetAdd(set, (*Rec(h)).os, u64(h), false).err == core.SysErr.None {
                // h now belongs to the reaper, which may free it at once.
                Set(a, sys.OsResult(value: 0, err: core.SysErr.None, os: 0))
                return
            }
            // Edge: none; a count: the set refused h, so the reaper never
            // reports it and its Sub never comes; the Add above is undone.
            _ = Reaper.live.Add(-1)
        }
        p := HeapAlloc(SizeOf[DetachNode](), AlignOf[DetachNode]())
        if raw.IsNull(p) {
            // No reaper and no node: the child stays a zombie after it
            // ends, until this process exits; its pidfd is closed now.
            Set(a, CloseRuntimeHandle(h))
            return
        }
        n := raw.PtrCast[u8, DetachNode](p)
        Lock(&Detached.lock)
        // Edge: none; head is read and written only under Detached.lock,
        // whose Lock and Unlock order every access.
        raw.Write(n, DetachNode(h: h, next: Detached.head.Load()))
        Detached.head.Store(u64(raw.Addr(p)))
        Unlock(&Detached.lock)
        Set(a, sys.OsResult(value: 0, err: core.SysErr.None, os: 0))
    }
}

/// ChildDetach gives up the child h and its group's job (0 none): the
/// child and its group run on, and the caller never waits for them.
/// Windows closes both handles. Linux hands the pidfd to the reaper
/// thread, started by the first detach, which reaps the child when it
/// ends, so a detached child is a zombie only until the reaper wakes,
/// whether or not this process spawns again; if the reaper cannot start
/// (no thread or no epoll set), the pidfd goes on a list reaped at the
/// next Spawn or ChildDetach. No SIGCHLD disposition is changed. Inline;
/// O(1), plus O(children on the list) when the reaper could not start;
/// on Linux the first call starts one thread (a 1 MiB stack) and one
/// epoll set, kept until exit. A child another Io made (KindForeign) is
/// given up by closing its handle, which tells its owner.
export ChildDetach(io: Io, h: uint, job: uint): core.IoResult {
    if IsForeign(h) {
        return IoOf(CloseRuntimeHandle(h))
    }
    var a := CallOf(core.IoOpDetach, core.IoRouteInline, h, DetachJob)
    a.w.a = Trunc[i64](job)
    r := Run(io, mut a)
    Reap()
    return r
}

// ReaperOn starts the reaper once (the first detach on Linux) and answers
// whether it runs. A concurrent first detach waits for the starter's few
// system calls.
ReaperOn(): bool {
    // Edge: a Load of Running reads the starter's Store below, which follows
    // its Store of the set, so the caller's Load of the set (DetachJob) after
    // this returns true reads the open set.
    s := Reaper.state.Load()
    if s == ReaperRunning {
        return true
    }
    // Edge: the Cas picks the one starter; its Store of the state, after
    // the set's Store, publishes the set to every later Load of Running,
    // and sys.ThreadStart orders both before the reaper thread's start.
    if s == ReaperIdle && Reaper.state.Cas(ReaperIdle, ReaperStarting) {
        o := sys.ReapSetOpen()
        var ok := o.err == core.SysErr.None
        if ok {
            Reaper.set.Store(Trunc[u64](o.value))
            unsafe "the reaper runs ReaperMain until the process exits; it takes no argument" {
                ok = sys.ThreadStart(ReaperMain, raw.Null[u8](), ReaperStack).err == core.SysErr.None
            }
            if !ok {
                _ = sys.Close(Trunc[uint](o.value))
            }
        }
        // Edge: the one Store that ends Starting; Running publishes the set's
        // Store above to every Load that reads it.
        Reaper.state.Store(if ok { ReaperRunning } else { ReaperFailed })
        return ok
    }
    // Edge: a waiter reads the starter's Store of Running or Failed (the
    // state never returns to Starting); Running publishes the set to it as
    // on the fast path, and Failed sends the detach to the list.
    for Reaper.state.Load() == ReaperStarting {
        sys.ThreadYield()
    }
    return Reaper.state.Load() == ReaperRunning
}

// ReaperMain is the reaper thread: each child the set
// reports has ended, so it is reaped, its runtime handle (its pidfd, which
// leaves the set as it closes) closed and freed, and live lowered. A child
// reported but not reaped (a pidfd is readable only once its process
// ended, so none is expected) is registered again. A failed wait (EBADF,
// EFAULT or EINVAL would each be a bug here; EINTR is retried in sys) is
// tried again after a pause doubling from 1 ms to 1 s, and the reaper
// never stops: the children already in the set have no other reaper, and
// a detach that read Running must find one.
ReaperMain(arg: *u8) {
    var tags: [ReaperTags]u64 = [0; ReaperTags]
    // Edge: ReaperOn stored the set before its sys.ThreadStart of this
    // thread, which orders that Store before this Load.
    set := Trunc[uint](Reaper.set.Load())
    var pause: i64 = ReaperPauseMin
    for {
        r := sys.ReapSetWait(set, mut tags[..])
        if r.err != core.SysErr.None {
            sys.SleepNanos(pause)
            pause = if pause >= ReaperPauseMax / 2 { ReaperPauseMax } else { pause * 2 }
            continue
        }
        pause = ReaperPauseMin
        for k in 0..Trunc[int](r.value) {
            h := Trunc[uint](tags[k])
            unsafe "h is a detached child's runtime handle, given to the reaper by DetachJob and used by nothing else" {
                if sys.TryReap((*Rec(h)).os) {
                    _ = CloseRuntimeHandle(h)
                    // Edge: as in DetachJob.
                    _ = Reaper.live.Add(-1)
                } else {
                    _ = sys.ReapSetAdd(set, (*Rec(h)).os, tags[k], true)
                }
            }
        }
    }
}

// Reap reaps each detached child on the Detached list that has ended,
// closes its pidfd and frees its node (Linux, only when the reaper could
// not start; the list is empty on Windows). O(listed children) system
// calls, under Detached.lock.
Reap() {
    // Edge: none; a racy peek at an empty list, which only skips the lock
    // (a child detached concurrently is reaped by the next Reap).
    if Detached.head.Load() == 0 {
        return
    }
    Lock(&Detached.lock)
    // Edge: none; under Detached.lock (DetachedState).
    var prev: u64 = 0
    var cur := Detached.head.Load()
    for cur != 0 {
        unsafe "cur is a live node of the list, which only this lock's holder changes" {
            n := raw.FromAddr[DetachNode](Trunc[uint](cur))
            next := (*n).next
            if sys.TryReap((*Rec((*n).h)).os) {
                _ = CloseRuntimeHandle((*n).h)
                if prev == 0 {
                    // Edge: none; under Detached.lock.
                    Detached.head.Store(next)
                } else {
                    (*raw.FromAddr[DetachNode](Trunc[uint](prev))).next = next
                }
                HeapFree(raw.PtrCast[DetachNode, u8](n), SizeOf[DetachNode](), AlignOf[DetachNode]())
            } else {
                prev = cur
            }
            cur = next
        }
    }
    Unlock(&Detached.lock)
}

TermOpenJob(a: *core.IoCallArgs) {
    Set(a, sys.TermOpen())
}

/// TermOpen opens this process's controlling terminal (/dev/tty on Linux,
/// CONIN$ and CONOUT$ on Windows), whatever its standard streams are: t[0]
/// the input's runtime handle, t[1] the output's (the same handle on
/// Linux), read and written on the blocking pool. No terminal is NotFound.
/// Under a simulated Io the handles are the Sim's terminal's (std.sim).
/// Inline; O(1); allocates one or two HandleRec records from the Heap.
export TermOpen(io: Io, t: mut [2]uint): core.IoResult {
    var a := CallOf(core.IoOpTermOpen, core.IoRouteInline, 0, TermOpenJob)
    r := Run(io, mut a)
    if r.err != core.SysErr.None {
        return r
    }
    if !core.IsNative(io) {
        // A simulated Io answers with its own runtime handles, which are
        // not OS handles to wrap (as PipeOpen's).
        t[0] = Trunc[uint](r.value)
        t[1] = Trunc[uint](r.os)
        return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    }
    i := Trunc[uint](r.value)
    o := Trunc[uint](r.os)
    hi := NewHandle(i, false)
    var ho := hi
    if o != i {
        ho = NewHandle(o, false)
    }
    if hi == 0 || ho == 0 {
        if hi != 0 {
            _ = CloseRuntimeHandle(hi)
        } else {
            _ = sys.Close(i)
        }
        if o != i {
            if ho != 0 {
                _ = CloseRuntimeHandle(ho)
            } else {
                _ = sys.Close(o)
            }
        }
        return Fail(core.SysErr.Other)
    }
    t[0] = hi
    t[1] = ho
    return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
}

// ReadyJob is IoRouteReady's job, which the native entry never runs.
ReadyJob(a: *core.IoCallArgs) {
}

/// TermWait waits until the terminal input h (TermOpen's t[0], or a pty's
/// read handle on Linux) has input, consuming none: no byte and no console
/// record, so a cancel or a deadline ends it with Canceled and the next
/// read sees every key (repeatable). On the poller (io_uring's
/// POLL_ADD, epoll, a registered wait on the console handle), holding no
/// thread. O(1) besides the wait; allocates nothing.
export TermWait(io: Io, h: uint): core.IoResult {
    var a := CallOf(core.IoOpReady, core.IoRouteReady, h, ReadyJob)
    return Run(io, mut a)
}

TermReadJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record; os is its live handle's" {
        Set(a, sys.TermRecords((*a).os, Out(a)))
    }
}

/// TermRead reads the input the terminal h (TermOpen's t[0]) holds now,
/// without waiting: Linux the bytes; Windows whole console INPUT_RECORDs
/// (20 bytes each; key events carry the UTF-16 unit that
/// virtual-terminal input makes, window-size events the new size). value
/// the bytes, 0 when nothing is queued: TermWait first, then TermRead, so
/// a cancel never takes a key. An out too short for one Windows record is
/// Other. Inline; O(records); allocates nothing.
export TermRead(io: Io, h: uint, out: mut []u8): core.IoResult {
    var a := CallOf(core.IoOpTermRead, core.IoRouteInline, h, TermReadJob)
    unsafe "out is the caller's for the call" {
        a.out = raw.DataMut(out)
        a.outn = out.Len()
    }
    return Run(io, mut a)
}

TermModeJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record; a.w.a is the owner's output handle" {
        w := (*a).w
        out := (*Rec(Trunc[uint](w.a))).os
        Set(a, sys.TermMode((*a).os, out, w.b, In(a), Out(a)))
    }
}

/// TermMode reads the mode of the terminal whose input is h and output out
/// (runtime handles, TermOpen's or a standard stream's) into old and sets
/// mode: core.IoTermSaved sets saved back (an earlier call's old),
/// IoTermRaw raw mode, IoTermNoEcho echo off with line editing kept,
/// IoTermGet nothing (the mode is only read). value
/// the bytes of old (36 on Linux, 8 on Windows; IoTermModeBytes fits
/// either). Not a terminal is the OS's error; a console without
/// virtual-terminal input is Unsupported. On Linux a process outside its
/// terminal's foreground group that sets a mode is stopped by SIGTTOU, as
/// every Unix program is. Inline; O(1); allocates nothing.
export TermMode(io: Io, h: uint, out: uint, mode: i64, saved: []u8, old: mut []u8): core.IoResult {
    var a := CallOf(core.IoOpTermMode, core.IoRouteInline, h, TermModeJob)
    a.w.a = Trunc[i64](out)
    a.w.b = mode
    unsafe "saved and old are the caller's for the call" {
        a.p = raw.Data(saved)
        a.pn = saved.Len()
        a.out = raw.DataMut(old)
        a.outn = old.Len()
    }
    return Run(io, mut a)
}

/// DetachedLen is the number of detached children not reaped yet, held by
/// the reaper or on the list (Linux; 0 on Windows), for std's tests.
/// O(listed children), under the list's lock.
export DetachedLen(): int {
    Lock(&Detached.lock)
    // Edge: a count; the reaper's Sub follows its reap.
    var n := Trunc[int](Reaper.live.Load())
    // Edge: none; under Detached.lock (DetachedState).
    var cur := Detached.head.Load()
    for cur != 0 {
        n += 1
        unsafe "cur is a live node of the list, under its lock" {
            cur = (*raw.FromAddr[DetachNode](Trunc[uint](cur))).next
        }
    }
    Unlock(&Detached.lock)
    return n
}
