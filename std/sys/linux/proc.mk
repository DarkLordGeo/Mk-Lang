// Why: proc.Run and Supervise start child processes on Linux with no libc,
// so std owns the whole spawn: the clone, the child's few system calls, the
// PATH search, the pipes and the wait.
//
// What: Spawn (SpawnWith, SpawnIn, ChildMain), FindProgram, ChildStream,
// ProcTry and ProcKill, PipeRoom, and the inherited-handle calls ShareListener,
// Inherited, InheritSeal and HandlesValue (MK_INHERIT, MK_HANDLES).
//
// How: musl's posix_spawn shape. The parent blocks every signal and calls
// clone(CLONE_VM | CLONE_VFORK | CLONE_PIDFD | SIGCHLD) onto a stack it
// mapped; the child runs straight-line system calls over the request only
// (no allocation, no runtime state, nothing that can panic): each handled
// signal back to SIG_DFL (CLONE_VM shares the parent's memory, so a parent
// handler must never run in the child), dup3 of its streams onto 0 to 3, an
// empty signal mask (as Rust's Command), the soft descriptor limit PollInit
// raised put back (RaiseFdLimit), execve, and on failure the errno into the
// request and exit_group(127). CLONE_VFORK holds the parent until the exec
// or the exit, so the parent reads the request only after the child has left
// it (glibc's __spawni passes the errno back the same way).
//
// The program is found in PATH by the parent (statx: a regular file with an
// execute bit, and faccessat2's X_OK for this process, as Go's LookPath).
// Each piped stream is a pipe (pipe2, os/exec's choice), so the child can
// open it again by name (/dev/stdin, /proc/self/fd/N): the parent's end
// rides the poller (non-blocking and on epoll's list under epoll; io_uring
// polls a blocking pipe itself), and a write after the child closed its end
// is EPIPE, which IoFail makes Closed (the runtime ignores SIGPIPE). The
// child is waited for through its pidfd (waitid P_PIDFD, Linux 5.4) and
// killed with pidfd_send_signal.

import std.core
import std.raw

/// PipeRoom is Windows' write quota of a pipe end (windows/proc.mk). On
/// Linux the answer is always -1, unknown: a write takes what fits, and a
/// cancelled one reports the bytes it moved (SD8-Spawn-PipeCut).
/// O(1); allocates nothing.
export PipeRoom(h: uint): int {
    return -1
}

/// SharedPort is the op.pos of an accept on a listener other processes
/// accept on too. On Windows its completion port is another process's
/// (poll_iocp.mk). On Linux it marks a listener another process holds too
/// (ShareListener answers it as os), whose accepts under io_uring wait
/// non-exclusively (poll_uring.mk's Prep and SharedAccept); epoll ignores
/// it.
export const SharedPort: i64 = 1

const CloneVm: uint = 0x100
const CloneVfork: uint = 0x4000
const ClonePidfd: uint = 0x1000
const SigChld: uint = 17
const SigSetMask: uint = 2
const FDupFdCloExec: uint = 1030
const FSetFd: uint = 2
const FGetFd: uint = 1
const FSetFl: uint = 4
const ONonBlock: uint = 0o4000
const ORdWr: uint = 2
const PPidfd: uint = 3
const WExited: uint = 4
const WNoHang: uint = 1
const WNoWait: uint = 0x0100_0000
const Esrch: int = -3
const SpawnStack: int = 65536
const MaxPath: int = 4096

// The request (in the call's pages, read by the child): the program at 0,
// argv at 8, envp at 16, the four source descriptors at 24 (i32, -1 for
// none: the child keeps its own), the child's errno at 40.
const RqPath: int = 0
const RqArgv: int = 8
const RqEnvp: int = 16
const RqFds: int = 24
const RqErr: int = 40
const PgCand: int = 64
const PgInherit: int = 4160
const PgArgv: int = 4224

// SelfBytes bounds this program's path (Spawn's empty args[0]).
const SelfBytes: int = 4096

/// Spawn starts a child (SpawnWith, below). An empty args[0] names this
/// program's own executable (ExePath: SelfBytes of UTF-8 at most), which
/// becomes the child's program and its args[0]: Supervise's re-exec
/// needs no path of its own. The substituted list and the path live
/// in pages mapped for the call.
/// O(n) in the bytes of args and the PATH search; the pages it maps are given
/// back before it returns.
export Spawn(sp: *u8, args: []str, child: mut core.IoChild, inherit: uint): OsResult {
    if args.Len() == 0 || args[0].Len() > 0 {
        return SpawnWith(sp, args, child, inherit)
    }
    list := args.Len() * SizeOf[str]()
    total := PageRound(list + SelfBytes)
    pg := Reserve(total, 4096)
    if raw.IsNull(pg) || !Commit(pg, total) {
        if !raw.IsNull(pg) {
            Release(pg, total)
        }
        return FailWith(core.SysErr.Other)
    }
    var r := FailWith(core.SysErr.Other)
    unsafe "the call's pages: the list, then the path" {
        path := raw.Offset(pg, list)
        n := ExePath(raw.SliceMut(path, SelfBytes))
        if n > 0 {
            l := raw.PtrCast[u8, str](pg)
            raw.Write(l, raw.Str(path, n))
            for i in 1..args.Len() {
                raw.Write(raw.Offset(l, i), args[i])
            }
            r = SpawnWith(sp, raw.Slice(l, args.Len()), child, inherit)
        }
    }
    Release(pg, total)
    return r
}

// SpawnWith starts args[0] with args, the standard streams as child.stdio says
// (core.IoStdInherit, IoStdNull, IoStdPipe), and, when inherit is a socket,
// that socket as descriptor 3 with MK_INHERIT=3 in the child's
// environment. value: the pidfd (close-on-exec); child.pipes[i] the
// parent's end of stream i (a pipe end, ChildStream's), 0 where it is not a
// pipe; child.pid. The environment is this
// process's (from sp) without MK_INHERIT.
SpawnWith(sp: *u8, args: []str, child: mut core.IoChild, inherit: uint): OsResult {
    if args.Len() == 0 {
        return FailWith(core.SysErr.InvalidPath)
    }
    var strbytes := 0
    for a in args {
        for b in a.AsBytes() {
            if b == 0 {
                return FailWith(core.SysErr.InvalidPath)
            }
        }
        strbytes += a.Len() + 1
    }
    for i in 0..3 {
        if child.stdio[i] > core.IoStdPipe {
            return FailWith(core.SysErr.Other)
        }
    }
    var envc := 0
    var argc := 0
    if !raw.IsNull(sp) {
        argc = Trunc[int](Word(sp, 0))
        for Word(sp, argc + 2 + envc) != 0 {
            envc += 1
        }
    }
    var empty: [1]u8 = [0]
    // GetEnvRaw answers -1 when PATH is unset: no entry to search.
    var pathlen := GetEnvRaw(sp, "PATH", mut empty[0..0])
    if pathlen < 0 {
        pathlen = 0
    }
    env_off := PgArgv + (args.Len() + 1) * 8
    str_off := env_off + (envc + 2) * 8
    path_off := str_off + strbytes
    data := PageRound(path_off + pathlen + 1)
    total := data + SpawnStack
    pg := Reserve(total, 4096)
    if raw.IsNull(pg) || !Commit(pg, total) {
        if !raw.IsNull(pg) {
            Release(pg, total)
        }
        return FailWith(MapError(12))
    }
    var r := Ok(0)
    unsafe "the call's pages: the regions above, then the child's stack" {
        // Every region is filled before the clone: argv, envp, PATH.
        argv := raw.Offset(pg, PgArgv)
        var s := str_off
        for i, a in args {
            Put64(argv, i * 8, u64(raw.Addr(raw.Offset(pg, s))))
            raw.Copy(raw.Offset(pg, s), raw.StrData(a), a.Len())
            s += a.Len() + 1
        }
        envp := raw.Offset(pg, env_off)
        var k := 0
        for j in 0..envc {
            e := raw.FromAddr[u8](Word(sp, argc + 2 + j))
            if !IsInheritEntry(e) {
                Put64(envp, k * 8, u64(raw.Addr(e)))
                k += 1
            }
        }
        if inherit != 0 {
            iv := raw.Offset(pg, PgInherit)
            raw.Copy(iv, raw.StrData("MK_INHERIT=3"), 12)
            Put64(envp, k * 8, u64(raw.Addr(iv)))
        }
        if pathlen > 0 {
            _ = GetEnvRaw(sp, "PATH", raw.SliceMut(raw.Offset(pg, path_off), pathlen))
        }
        Put64(pg, RqArgv, u64(raw.Addr(argv)))
        Put64(pg, RqEnvp, u64(raw.Addr(envp)))
        Put64(pg, RqPath, u64(raw.Addr(raw.Offset(pg, PgCand))))
        f := FindProgram(args[0], raw.Offset(pg, PgCand), raw.Slice(raw.Offset(pg, path_off), pathlen))
        if f.err != core.SysErr.None {
            r = f
        } else {
            r = SpawnIn(pg, raw.Offset(pg, total), child, inherit)
        }
    }
    Release(pg, total)
    return r
}

// SpawnIn makes the child's descriptors, clones, and closes every
// descriptor it made for the child; on a failure the parent's pipe ends
// too. top is the child stack's top.
SpawnIn(pg: *u8, top: *u8, child: mut core.IoChild, inherit: uint): OsResult {
    var theirs: [4]int = [-1, -1, -1, -1]
    var mine: [3]int = [-1, -1, -1]
    var r := Ok(0)
    for i in 0..3 {
        r = ChildStream(i, child.stdio[i], mut theirs[i], mut mine[i])
        if r.err != core.SysErr.None {
            break
        }
    }
    if r.err == core.SysErr.None && inherit != 0 {
        // The caller's socket stays open in this process: a copy above 3
        // is what the child's dup3 reads.
        unsafe "a system call that passes no memory, only values and handles" {
            d := Syscall(Nrs().fcntl, inherit, FDupFdCloExec, 4, 0, 0, 0)
            if IsErr(d) {
                r = Result(d)
            } else {
                theirs[3] = d
            }
        }
    }
    if r.err == core.SysErr.None {
        unsafe "the request's descriptor words" {
            for i in 0..4 {
                Put32(pg, RqFds + i * 4, Trunc[u32](theirs[i]))
            }
        }
        r = CloneChild(pg, top, ChildMain)
    }
    for i in 0..4 {
        if theirs[i] >= 0 {
            CloseFd(theirs[i])
        }
    }
    if r.err != core.SysErr.None {
        for i in 0..3 {
            if mine[i] >= 0 {
                if !IsUring() {
                    EpollDel(Trunc[uint](mine[i]))
                }
                CloseFd(mine[i])
            }
        }
        return r
    }
    for i in 0..3 {
        child.pipes[i] = if mine[i] >= 0 { Trunc[uint](mine[i]) } else { 0 }
    }
    child.pid = r.os
    return Ok(r.value)
}

// PidfdReady: this kernel has waitid(P_PIDFD) (Linux 5.4; CLONE_PIDFD is
// 5.2), which every child wait needs. A probe on a descriptor that cannot
// be open: EBADF says P_PIDFD is known, EINVAL that it is not.
// One system call per spawn, no state.
PidfdReady(): bool {
    var si: [16]u64 = [0; 16]
    unsafe "system call; si holds a siginfo_t" {
        r := Syscall(Nrs().waitid, PPidfd, 0x7FFF_FFFF, raw.Addr(raw.DataMut(mut si[..])), WExited | WNoHang, 0, 0)
        return r != -22
    }
}

// CloneChild blocks every signal, clones the child onto top, running entry
// over the request pg (ChildMain, or child.mk's ChildMainEx), and restores
// the mask. value the pidfd and os the pid; a child whose execve failed
// is reaped and its errno returned. A kernel older than 5.4 has no way to
// wait for the child here (no pidfd, or no waitid on one): Other with os
// 38 (ENOSYS), checked before any child exists. A clone that still came
// back without a pidfd (an old kernel ignores the unknown CLONE_PIDFD bit)
// kills and reaps its child by pid rather than leave it unwatched.
CloneChild(pg: *u8, top: *u8, entry: fn(*u8)): OsResult {
    var all: u64 = 0xFFFF_FFFF_FFFF_FFFF
    var old: u64 = 0
    var pidfd: u32 = 0xFFFF_FFFF
    if !PidfdReady() {
        return Fail(38)
    }
    unsafe "system calls; the request and stack are the call's pages" {
        Put32(pg, RqErr, 0)
        b := Syscall(Nrs().rt_sigprocmask, SigSetMask, raw.Addr(raw.Of(&all)), raw.Addr(raw.OfMut(mut old)), 8, 0, 0)
        if IsErr(b) {
            return Result(b)
        }
        pid := Clone(CloneVm | CloneVfork | ClonePidfd | SigChld, top, raw.OfMut(mut pidfd), raw.Null[u32](), 0,
            entry, pg)
        _ = Syscall(Nrs().rt_sigprocmask, SigSetMask, raw.Addr(raw.Of(&old)), 0, 8, 0, 0)
        if IsErr(pid) {
            return Result(pid)
        }
        if pidfd == 0xFFFF_FFFF {
            _ = Syscall(Nrs().kill, Trunc[uint](pid), 9, 0, 0, 0, 0)
            var w := -4
            for w == -4 {
                w = Syscall(Nrs().wait4, Trunc[uint](pid), 0, 0, 0, 0, 0)
            }
            return Fail(38)
        }
        e := Get32(pg, RqErr)
        if e != 0 {
            _ = ReapChild(uint(pidfd))
            CloseFd(int(pidfd))
            return Fail(i64(e))
        }
        // epoll waits for the child on the pidfd's readable edge (ProcTry);
        // a pidfd the table cannot hold (EMFILE's case) ends the child,
        // which nothing could wait for.
        if !IsUring() && !EpollAdd(uint(pidfd)) {
            _ = Syscall(Nrs().pidfd_send_signal, uint(pidfd), 9, 0, 0, 0, 0)
            _ = ReapChild(uint(pidfd))
            CloseFd(int(pidfd))
            return Fail(24)
        }
        return OsResult(value: i64(pidfd), err: core.SysErr.None, os: i64(pid))
    }
}

// ChildMain is the vfork child (CloneChild): system calls over the request
// only, then execve; it never returns.
ChildMain(rq: *u8) {
    n := Nrs()
    var dfl: [4]u64 = [0; 4]
    var cur: [4]u64 = [0; 4]
    unsafe "the vfork child: the request is the parent's pages, held by CLONE_VFORK" {
        // A handler of this process goes back to SIG_DFL; an ignored signal
        // stays ignored (nohup's SIGHUP, which posix_spawn and Go keep),
        // except SIGPIPE, which the runtime itself ignores (FaultHook) and
        // which a SIG_IGN would carry through execve: the child gets the
        // default, so a write to a closed pipe ends it (Rust's Command).
        for s in 1..65 {
            if s != 9 && s != 19 {
                r := Syscall(n.rt_sigaction, Trunc[uint](s), 0, raw.Addr(raw.DataMut(mut cur[..])), 8, 0, 0)
                if r == 0 && (cur[0] > 1 || s == 13) {
                    _ = Syscall(n.rt_sigaction, Trunc[uint](s), raw.Addr(raw.Data(dfl[..])), 0, 8, 0, 0)
                }
            }
        }
        for t in 0..4 {
            src := Get32(rq, RqFds + t * 4)
            if src != 0xFFFF_FFFF {
                d := Syscall(n.dup3, uint(src), Trunc[uint](t), 0, 0, 0, 0)
                if IsErr(d) {
                    Put32(rq, RqErr, Trunc[u32](-d))
                    for {
                        _ = Syscall(n.exit_group, 127, 0, 0, 0, 0, 0)
                    }
                }
            }
        }
        // The soft descriptor limit this process started with
        // (RaiseFdLimit); a failure leaves the raised one.
        // Edge: PollInit's Stores, made before any spawn, so this child reads
        // the replaced pair.
        if FdLimit.max.Load() != 0 {
            var lim: [2]u64 = [FdLimit.cur.Load(), FdLimit.max.Load()]
            _ = Syscall(n.prlimit64, 0, 7, raw.Addr(raw.Data(lim[..])), 0, 0, 0)
        }
        var nomask: u64 = 0
        _ = Syscall(n.rt_sigprocmask, SigSetMask, raw.Addr(raw.Of(&nomask)), 0, 8, 0, 0)
        e := Syscall(n.execve, Trunc[uint](Get64(rq, RqPath)), Trunc[uint](Get64(rq, RqArgv)),
            Trunc[uint](Get64(rq, RqEnvp)), 0, 0, 0)
        Put32(rq, RqErr, Trunc[u32](-e))
        for {
            _ = Syscall(n.exit_group, 127, 0, 0, 0, 0, 0)
        }
    }
}

// ChildStream makes stream i's descriptors (0 stdin, 1 stdout, 2 stderr):
// theirs the child's (above 3, close-on-exec, which dup3 clears on the
// copy), mine the parent's pipe end: the write end for stdin, the read end
// otherwise. Under epoll the parent's end is made non-blocking and put on
// epoll's list; io_uring takes it blocking, since its write to a
// non-blocking pipe can answer EAGAIN instead of waiting.
ChildStream(i: int, mode: u32, theirs: mut int, mine: mut int): OsResult {
    unsafe "system calls; the path is a NUL-terminated constant, fds a pair in this frame" {
        if mode == core.IoStdInherit {
            return Ok(0)
        }
        if mode == core.IoStdNull {
            fd := Syscall(Nrs().openat, AtFdcwd, raw.Addr(raw.StrData("/dev/null\u{0}")), ORdWr | OCloExec, 0, 0, 0)
            if IsErr(fd) {
                return Result(fd)
            }
            l := Lift(fd)
            if IsErr(l) {
                return Result(l)
            }
            *theirs = l
            return Ok(0)
        }
        var fds: [2]i32 = [0, 0]
        p := Syscall(Nrs().pipe2, raw.Addr(raw.DataMut(mut fds[..])), OCloExec, 0, 0, 0, 0)
        if IsErr(p) {
            return Result(p)
        }
        child := if i == 0 { int(fds[0]) } else { int(fds[1]) }
        par := if i == 0 { int(fds[1]) } else { int(fds[0]) }
        l := Lift(child)
        if IsErr(l) {
            CloseFd(par)
            return Result(l)
        }
        if !IsUring() {
            nb := Syscall(Nrs().fcntl, Trunc[uint](par), FSetFl, ONonBlock, 0, 0, 0)
            if IsErr(nb) || !EpollAdd(Trunc[uint](par)) {
                CloseFd(l)
                CloseFd(par)
                return if IsErr(nb) { Result(nb) } else { FailWith(core.SysErr.Other) }
            }
        }
        *theirs = l
        *mine = par
        return Ok(0)
    }
}

// Lift moves fd above 3 (so the child's dup3 onto 0 to 3 never meets its
// own source) and closes the original; the descriptor, or -errno.
Lift(fd: int): int {
    if fd > 3 {
        return fd
    }
    unsafe "a system call that passes no memory, only values and handles" {
        d := Syscall(Nrs().fcntl, Trunc[uint](fd), FDupFdCloExec, 4, 0, 0, 0)
        CloseFd(fd)
        return d
    }
}

CloseFd(fd: int) {
    unsafe "system call; close always releases the descriptor" {
        _ = Syscall(Nrs().close, Trunc[uint](fd), 0, 0, 0, 0, 0)
    }
}

// IsInheritEntry: the environment entry e starts with MK_INHERIT= or
// MK_HANDLES=, neither of which a child is given from this
// process's environment: each names handles of this process only.
IsInheritEntry(e: *u8): bool {
    return HasPrefix(e, "MK_INHERIT=") || HasPrefix(e, "MK_HANDLES=")
}

// HasPrefix: the NUL-terminated entry e starts with s.
HasPrefix(e: *u8, s: str): bool {
    for k, c in s.AsBytes() {
        unsafe "e is NUL-terminated: a NUL ends the compare" {
            if raw.Read(raw.Offset(e, k)) != c {
                return false
            }
        }
    }
    return true
}

// FindProgram writes the program's path, NUL-terminated, to cand
// (MaxPath bytes): a name with a slash as it is; else the first entry of
// path (PATH's value) where dir/name is a regular file with an execute
// bit. An entry that is empty or relative (the current directory, or a
// path under it) is skipped: Go 1.19's LookPath refuses such a result
// (exec.ErrDot) and Windows' FindProgram never searches the current
// directory, so both targets keep the rule. NotFound when none;
// a name that does not fit is InvalidPath.
FindProgram(name: str, cand: *u8, path: []u8): OsResult {
    b := name.AsBytes()
    if b.Len() == 0 {
        return Fail(2)
    }
    var slash := false
    for x in b {
        if x == 47 {
            slash = true
        }
    }
    unsafe "cand holds MaxPath bytes" {
        if slash {
            if b.Len() + 1 > MaxPath {
                return FailWith(core.SysErr.InvalidPath)
            }
            raw.Copy(cand, raw.Data(b), b.Len())
            raw.Write(raw.Offset(cand, b.Len()), 0)
            return Ok(0)
        }
        var i := 0
        for i <= path.Len() {
            var j := i
            for j < path.Len() && path[j] != 58 {
                j += 1
            }
            n := j - i
            if n > 0 && path[i] == 47 && n + 1 + b.Len() + 1 <= MaxPath {
                raw.Copy(cand, raw.Offset(raw.Data(path), i), n)
                raw.Write(raw.Offset(cand, n), 47)
                raw.Copy(raw.Offset(cand, n + 1), raw.Data(b), b.Len())
                raw.Write(raw.Offset(cand, n + 1 + b.Len()), 0)
                if IsExecutable(cand) {
                    return Ok(0)
                }
            }
            i = j + 1
        }
    }
    return Fail(2)
}

// IsExecutable: the path c names a regular file with an execute bit
// (statx STATX_TYPE | STATX_MODE, following links) that this process may
// run: faccessat2(X_OK, AT_EACCESS) checks the effective ids, ACLs and a
// noexec mount as execve does (as Go's LookPath); a
// kernel before 5.8 (ENOSYS) or a filter that refuses the call (EPERM)
// leaves the execute bits as the answer, as Go does.
IsExecutable(c: *u8): bool {
    var b: [32]u64 = [0; 32]
    unsafe "system call; b holds 256 bytes" {
        p := raw.PtrCast[u64, u8](raw.DataMut(mut b[..]))
        r := Syscall(Nrs().statx, AtFdcwd, raw.Addr(c), 0, 0x3, raw.Addr(p), 0)
        if IsErr(r) {
            return false
        }
        mode := u32(Get16(p, 28))
        if (mode & 0o170000) != 0o100000 || (mode & 0o111) == 0 {
            return false
        }
        a := Syscall(Nrs().faccessat2, AtFdcwd, raw.Addr(c), 1, 0x200, 0, 0)
        return a == 0 || a == -38 || a == -1
    }
}

// ReapChild waits for the child of pidfd h and reaps it, repeating EINTR: the
// raw siginfo result (0 or -errno). For an exec that failed only.
ReapChild(h: uint): int {
    var si: [16]u64 = [0; 16]
    unsafe "system call; si holds a siginfo_t (128 bytes)" {
        for {
            r := Syscall(Nrs().waitid, PPidfd, h, raw.Addr(raw.DataMut(mut si[..])), WExited, 0, 0)
            if r != -4 {
                return r
            }
        }
    }
}

// ProcTry reaps op's child if it has ended (waitid P_PIDFD with WNOHANG,
// Linux 5.4; EINTR repeated): true with op.res its exit status (0 to 255)
// or minus the signal that ended it; false while it runs (si_pid stays 0).
// A second reap after the first is ECHILD (Other); std.task keeps the
// first answer. The poller calls it: epoll's Try on submission and on each
// readable edge of the pidfd, io_uring after its POLL_ADD completes.
ProcTry(op: *Op): bool {
    var si: [16]u64 = [0; 16]
    unsafe "system call; si holds a siginfo_t: si_code at 8, si_pid at 16, si_status at 24" {
        p := raw.PtrCast[u64, u8](raw.Data(si[..]))
        // op.pos 1: a group leader (child.mk's SpawnEx). It is only peeked
        // at (WNOWAIT), so while its group is killed the unreaped leader
        // still holds the group's id, which no new process can then get;
        // then it is reaped.
        hold := (*op).pos == 1
        for {
            r := Syscall(Nrs().waitid, PPidfd, (*op).h, raw.Addr(raw.DataMut(mut si[..])),
                if hold { WExited | WNoHang | WNoWait } else { WExited | WNoHang }, 0, 0)
            if r == -4 {
                continue
            }
            if IsErr(r) {
                (*op).res = Result(r)
                return true
            }
            if Get32(p, 16) == 0 {
                return false
            }
            if hold {
                // The group's id is the leader's pid; ESRCH (nothing left
                // in it) is fine. Then the reap, which cannot wait: the
                // leader has ended.
                _ = Syscall(Nrs().kill, Trunc[uint](-i64(Get32(p, 16))), 9, 0, 0, 0, 0)
                _ = ReapChild((*op).h)
            }
            // si_code 1 (CLD_EXITED): an exit status; else the signal.
            status := i64(Get32(p, 24))
            (*op).res = if Get32(p, 8) == 1 { Ok(status) } else { Ok(-status) }
            return true
        }
    }
}

/// PollRelease releases what a submission of op keeps past its completion:
/// nothing on Linux (the POLL_ADD or the epoll wait list held op until its
/// completion). Windows releases a registered wait and its event here.
/// O(1); allocates nothing.
export PollRelease(op: *Op) {
}

/// ProcKill sends SIGKILL to the child of the pidfd h. A child that already
/// ended and was reaped is success (ESRCH).
/// O(1): one system call; allocates nothing.
export ProcKill(h: uint): OsResult {
    unsafe "a system call that passes no memory, only values and handles" {
        r := Syscall(Nrs().pidfd_send_signal, h, 9, 0, 0, 0, 0)
        if IsErr(r) && r != Esrch {
            return Result(r)
        }
    }
    return Ok(0)
}

/// ShareListener readies the listening socket fd for accepts beside other
/// processes that hold it (a listener handed to a child, inherited from a
/// parent, or passed over a Unix socket): non-blocking, with O_NONBLOCK
/// ORed into the open file's flags, which every holder shares; os
/// SharedPort, which the socket's record keeps as pos, so under io_uring
/// every accept on it waits non-exclusively (poll_uring.mk's Prep and
/// SharedAccept). Errors: the OS's (fcntl). O(1) system calls; allocates
/// nothing.
export ShareListener(fd: uint): OsResult {
    unsafe "a system call that passes no memory, only values and handles" {
        fl := Syscall(Nrs().fcntl, fd, FGetFl, 0, 0, 0, 0)
        if IsErr(fl) {
            return Result(fl)
        }
        if (Trunc[uint](fl) & ONonBlock) == 0 {
            nb := Syscall(Nrs().fcntl, fd, FSetFl, Trunc[uint](fl) | ONonBlock, 0, 0, 0)
            if IsErr(nb) {
                return Result(nb)
            }
        }
    }
    return OsResult(value: 0, err: core.SysErr.None, os: SharedPort)
}

/// Inherited answers the socket this process inherited from a Spawn with
/// inherit (MK_INHERIT=<fd> in the environment): close-on-exec again and
/// ready to share (ShareListener: os SharedPort); under epoll also
/// registered as SocketOpen's async sockets.
/// NotFound when there is none; a value that names no socket is Other.
/// O(1) system calls besides the environment scan; allocates nothing beyond the
/// socket's registration with the poller.
export Inherited(sp: *u8): OsResult {
    iv := InheritValue(sp)
    if iv == -1 {
        return FailWith(core.SysErr.NotFound)
    }
    if iv < 0 || HandleKind(Trunc[uint](iv)) != 4 {
        return FailWith(core.SysErr.Other)
    }
    v := Trunc[uint](iv)
    unsafe "a system call that passes no memory, only values and handles" {
        c := Syscall(Nrs().fcntl, v, FSetFd, 1, 0, 0, 0)
        if IsErr(c) {
            return Result(c)
        }
    }
    s := ShareListener(v)
    if s.err != core.SysErr.None {
        return s
    }
    if !IsUring() && !EpollAdd(v) {
        return FailWith(core.SysErr.Other)
    }
    return OsResult(value: Trunc[i64](v), err: core.SysErr.None, os: SharedPort)
}

// InheritValue parses MK_INHERIT: the descriptor, -1 when it is not set,
// -2 when it is not a decimal of at most 9 digits.
InheritValue(sp: *u8): i64 {
    var buf: [24]u8 = [0; 24]
    n := GetEnvRaw(sp, "MK_INHERIT", mut buf[..])
    if n <= 0 {
        return -1
    }
    if n > 9 {
        return -2
    }
    var v: i64 = 0
    for i in 0..n {
        c := buf[i]
        if c < 48 || c > 57 {
            return -2
        }
        v = v * 10 + i64(c - 48)
    }
    return v
}

/// InheritSeal runs at start-up (std.task's Start): the socket MK_INHERIT
/// names is made close-on-exec at once. The spawn's dup3 cleared the flag
/// on descriptor 3, and without this a child that spawns before it calls
/// Inherited, or never calls it, hands the listener to every program it
/// starts, which then keeps the port open. Nothing else is
/// touched: the descriptor stays this process's for Inherited.
/// O(1) system calls; allocates nothing.
/// The descriptors MK_HANDLES names (the handles a Command named,
/// which the child's dup3 left without the flag) are checked first
/// (HandlesCheck), before this touches any descriptor, and the verdict is
/// kept for HandlesValue: a list that passes is made close-on-exec, so a
/// program this process starts by any route gets none of them; a list
/// that fails is left as it is, every descriptor it names untouched.
export InheritSeal(sp: *u8) {
    v := InheritValue(sp)
    var hs: [64]uint = [0; 64]
    n := HandlesCheck(sp, v, mut hs[..])
    // Edge: written once by start-up's only thread, before any task or
    // carrier exists; HandlesValue's Load comes after.
    Seal.handles.Store(if n == -1 { SealNone } else if n < 0 { SealRefused } else { SealGood })
    if v >= 0 && HandleKind(Trunc[uint](v)) == 4 {
        unsafe "a system call that passes no memory, only values and handles" {
            _ = Syscall(Nrs().fcntl, Trunc[uint](v), FSetFd, 1, 0, 0, 0)
        }
    }
    for i in 0..n {
        unsafe "a system call that passes no memory, only values and handles" {
            _ = Syscall(Nrs().fcntl, hs[i], FSetFd, 1, 0, 0, 0)
        }
    }
}

// SealState is InheritSeal's verdict on MK_HANDLES.
struct SealState {
    handles: raw.Atomic[u32]
}

// Seal.handles: SealUnsealed until InheritSeal runs (a program with no
// task never runs it), then SealNone, SealGood or SealRefused.
var Seal: SealState = SealState(handles: raw.Atomic[u32].New(0))

const SealUnsealed: u32 = 0
const SealNone: u32 = 1
const SealGood: u32 = 2
const SealRefused: u32 = 3

/// HandlesValue is MK_HANDLES, the handles a Command named for
/// this process, as InheritSeal's HandlesCheck judged it at start-up,
/// before the runtime opened a descriptor of its own: the descriptors 3 up
/// to 3 + n - 1, in order, written to out (room for PassMax), and the
/// count; -1 when MK_HANDLES is not set; -2 when the list failed the check
/// (it is not 3 to 3 + n - 1, or names more than out has room for, a
/// descriptor that was not open, one that was close-on-exec, which no
/// spawn's dup3 leaves, or MK_INHERIT's), so this process was not given
/// those descriptors by a Spawn and none is to be taken. In a process
/// where InheritSeal never ran, the check runs now: nothing has made the
/// named descriptors close-on-exec, and every descriptor the runtime opens
/// is. O(bytes of the environment); allocates nothing.
export HandlesValue(sp: *u8, out: mut []uint): int {
    // Edge: reads InheritSeal's Store, which start-up's only thread made
    // before any task existed, so the verdict it published is seen whole.
    s := Seal.handles.Load()
    if s == SealUnsealed {
        return HandlesCheck(sp, InheritValue(sp), out)
    }
    if s == SealNone {
        return -1
    }
    if s == SealRefused {
        return -2
    }
    return HandlesList(sp, out)
}

// HandlesList parses MK_HANDLES: the count of descriptors written to out,
// -1 when it is not set, -2 when it is not a list of at most out.Len()
// decimals of 3 and above.
HandlesList(sp: *u8, out: mut []uint): int {
    var buf: [1536]u8 = [0; 1536]
    n := GetEnvRaw(sp, "MK_HANDLES", mut buf[..])
    if n < 0 {
        return -1
    }
    if n > buf.Len() {
        return -2
    }
    return ParseHandles(buf[0..n], 3, out)
}

// HandlesCheck is HandlesList, then a list of n is -2 unless it is
// exactly 3, 4, ..., 3 + n - 1 (as a spawn's dup3 put them: no repeat, no
// gap, no other order), and each is open and not close-on-exec, and none
// is inherit (MK_INHERIT's descriptor, or -1). Nothing is changed.
// O(bytes of the environment) and one system call per descriptor;
// allocates nothing.
HandlesCheck(sp: *u8, inherit: i64, out: mut []uint): int {
    n := HandlesList(sp, out)
    if n < 0 {
        return n
    }
    for i in 0..n {
        d := out[i]
        if d != Trunc[uint](3 + i) || Trunc[i64](d) == inherit {
            return -2
        }
        unsafe "a system call that passes no memory, only values and handles" {
            f := Syscall(Nrs().fcntl, d, FGetFd, 0, 0, 0, 0)
            if IsErr(f) || (f & 1) != 0 {
                return -2
            }
        }
    }
    return n
}

// ParseHandles parses a comma-separated list of decimal handle values,
// each no less than least and of at most 18 digits, into out: the count, or
// -2 when text is not such a list or holds more than out has room for.
// An empty text is the empty list. O(text's bytes); allocates nothing.
ParseHandles(text: []u8, least: u64, out: mut []uint): int {
    var k := 0
    var v: u64 = 0
    var digits := 0
    for i in 0..text.Len() + 1 {
        if i == text.Len() || text[i] == 44 {
            if i == text.Len() && k == 0 && digits == 0 {
                return 0
            }
            if digits == 0 || v < least || k >= out.Len() {
                return -2
            }
            out[k] = Trunc[uint](v)
            k += 1
            v = 0
            digits = 0
            continue
        }
        c := text[i]
        if c < 48 || c > 57 || digits >= 18 {
            return -2
        }
        v = v * 10 + u64(c - 48)
        digits += 1
    }
    return k
}

PageRound(n: int): int {
    return (n + 4095) / 4096 * 4096
}
