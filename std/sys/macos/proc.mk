// Why: proc.Run and Supervise start child processes; on macOS fork is
// unsafe in a threaded process and vfork is deprecated, and posix_spawn is
// one system call that makes the child, sets up its descriptors and
// signals, and runs the program, so std drives it and keeps the PATH
// search, the pipes and the wait of its own.
//
// What: Spawn (SpawnWith, SpawnIn, RunSpawn), FindProgram, ChildStream,
// ProcKill, PipeRoom, SharedPort, PollRelease, ShareListener, and the
// inherited-handle calls Inherited and InheritSeal (MK_INHERIT; MK_HANDLES
// is passfd.mk's).
//
// How: the parent builds argv, envp and the program's path in pages it
// maps for the call, then one posix_spawn (libsystem_proc.mk) with these
// attributes (<sys/spawn.h>):
// - POSIX_SPAWN_CLOEXEC_DEFAULT (Apple's): every descriptor not named by a
//   file action is closed in the child, so the child holds only its three
//   streams and the handles it was given, whatever another thread opens;
//   a stream it shares with the parent is named by addinherit_np.
// - POSIX_SPAWN_SETSIGDEF with SIGPIPE: the runtime ignores SIGPIPE
//   (fault.mk) and an ignored signal survives execve, so the child is given
//   the default and a write to a closed pipe ends it (Rust's Command).
//   Every other ignored signal stays ignored (nohup's SIGHUP), and execve
//   itself resets every handled one to SIG_DFL.
// - POSIX_SPAWN_SETSIGMASK with the empty set, as Rust's Command.
// The file actions dup2 the child's sources onto 0 to 3; each source is a
// close-on-exec descriptor of 3 or above (Lift), so no dup2 meets a source
// an earlier one replaced. posix_spawn answers the error of the exec
// itself, so a program that cannot run is the call's error, with no child.
//
// The program is found in PATH by the parent (stat: a regular file with an
// execute bit, and faccessat's X_OK with AT_EACCESS for this process, as
// Go's LookPath). Each piped stream is a pipe (pipe, then FD_CLOEXEC on
// both ends: Darwin has no pipe2; CLOEXEC_DEFAULT keeps the window from
// reaching std's own spawns). The parent's end is non-blocking and
// attached to the poller (PollAttach), and a write after the child closed its end is EPIPE,
// which IoFail makes Closed. The child's handle is its pid: macOS has no
// process descriptor, and the poller's OpProcWait waits on the pid with
// kqueue's EVFILT_PROC and reaps it with waitpid; ProcKill is kill(2).

import std.core
import std.raw
import std.sys.lc

/// PipeRoom is Windows' write quota of a pipe end (windows/proc.mk). On
/// macOS the answer is always -1, unknown: a write takes what fits, and a
/// cancelled one reports the bytes it moved.
/// O(1); allocates nothing.
export PipeRoom(h: uint): int {
    return -1
}

/// SharedPort is the op.pos of an accept on a listener whose completion port
/// is another process's (Windows only, poll_iocp.mk); macOS ignores op.pos
/// for an accept, and an inherited descriptor is used here as any.
export const SharedPort: i64 = 1

// Darwin's <fcntl.h> and <sys/wait.h> values, the same on arm64 and x86_64
// (Go's syscall/zerrors_darwin_arm64.go and _amd64.go; AT_FDCWD and
// AT_EACCESS from <sys/fcntl.h>).
const PxFSetFd: i32 = 2
const PxFGetFl: i32 = 3
const PxFSetFl: i32 = 4
const PxFDupFdCloExec: i32 = 0x43
const PxFdCloExec: i32 = 1
const PxONonBlock: i32 = 0x4
const PxONoCtty: i32 = 0x2_0000
const PxWNoHang: i32 = 1
const PxAtFdcwd: i32 = -2
const PxAtEAccess: i32 = 0x10
const PxXOk: i32 = 1
// Darwin's errno values (<sys/errno.h>).
const PxEintr: i64 = 4
const PxEsrch: i64 = 3
const PxEperm: i64 = 1
// posix_spawnattr_setflags' bits (<sys/spawn.h>).
const PxSetPgroup: i32 = 0x2
const PxSetSigDef: i32 = 0x4
const PxSetSigMask: i32 = 0x8
const PxSetSid: i32 = 0x400
const PxCloExecDefault: i32 = 0x4000
// SIGPIPE's bit in a Darwin sigset_t (a u32, bit sig - 1).
const PxSigPipeBit: u32 = 0x1000
// PxMaxPath is Darwin's PATH_MAX (<sys/syslimits.h>).
const PxMaxPath: int = 1024

// SpawnPlan.fds' values that name no descriptor: the parent's own stream
// (inherited), and the null device (opened by the child's file actions).
const PxFdOwn: i32 = -1
const PxFdNull: i32 = -2

// The call's pages: the program's path at PgCand (PxMaxPath bytes), a
// pty's peer name at PgTty (128 bytes, TIOCPTYGNAME's), MK_INHERIT=3 at
// PgInherit, the named handles' sources at PgNamed (i32 each, PassMax of
// them), then argv at PgArgv, envp, the strings and PATH's value.
const PgCand: int = 0
const PgTty: int = 1024
const PgInherit: int = 1152
const PgNamed: int = 1168
const PgArgv: int = 1536

// SelfBytes bounds this program's path (Spawn's empty args[0]).
const SelfBytes: int = 4096

// SpawnPlan is what one posix_spawn is given: the program, argv and envp
// (NULL-terminated arrays), the working directory and a pseudo-terminal's
// peer path (each NUL-terminated, null for none), each stream's source
// (fds[0..3]: a descriptor, PxFdOwn or PxFdNull; fds[3] a descriptor for
// the child's 3 or PxFdOwn for none), stderr joined to stdout, a new
// process group, and nnamed sources (i32 at named) for 3 to 3 + n - 1.
struct SpawnPlan {
    path: *u8
    argv: *u8
    envp: *u8
    dir: *u8
    tty: *u8
    fds: [4]i32
    join: bool
    group: bool
    named: *u8
    nnamed: int
}

// NewPlan is the plan for the program and arrays in the call's pages pg:
// every stream the parent's own, nothing else.
NewPlan(pg: *u8, envp: *u8): SpawnPlan {
    unsafe "offsets in the call's pages" {
        return SpawnPlan(path: raw.Offset(pg, PgCand), argv: raw.Offset(pg, PgArgv), envp: envp,
            dir: raw.Null[u8](), tty: raw.Null[u8](), fds: [PxFdOwn, PxFdOwn, PxFdOwn, PxFdOwn], join: false,
            group: false, named: raw.Offset(pg, PgNamed), nnamed: 0)
    }
}

// PxPut64 and PxGet32 write and read one aligned word at p + off.
PxPut64(p: *u8, off: int, v: u64) {
    unsafe "p + off is an aligned word in the call's pages" {
        raw.Write(raw.PtrCast[u8, u64](raw.Offset(p, off)), v)
    }
}

PxPut32(p: *u8, off: int, v: i32) {
    unsafe "p + off is an aligned word in the call's pages" {
        raw.Write(raw.PtrCast[u8, i32](raw.Offset(p, off)), v)
    }
}

PxGet32(p: *u8, off: int): i32 {
    unsafe "p + off is an aligned word in the call's pages" {
        return raw.Read(raw.PtrCast[u8, i32](raw.Offset(p, off)))
    }
}

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
    total := PxPageRound(list + SelfBytes)
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

// EnvCount is how many entries this process's environment (from sp) has.
EnvCount(sp: *u8): int {
    if raw.IsNull(sp) {
        return 0
    }
    envs := Envp(sp)
    var n := 0
    for Word(envs, n) != 0 {
        n += 1
    }
    return n
}

// SpawnWith starts args[0] with args, the standard streams as child.stdio says
// (core.IoStdInherit, IoStdNull, IoStdPipe), and, when inherit is a socket,
// that socket as descriptor 3 with MK_INHERIT=3 in the child's
// environment. value: the pid; child.pipes[i] the parent's end of stream i
// (a pipe end, ChildStream's), 0 where it is not a pipe; child.pid. The
// environment is this process's (from sp) without MK_INHERIT and
// MK_HANDLES.
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
    envc := EnvCount(sp)
    var empty: [1]u8 = [0]
    // GetEnvRaw answers -1 when PATH is unset: no entry to search.
    var pathlen := GetEnvRaw(sp, "PATH", mut empty[0..0])
    if pathlen < 0 {
        pathlen = 0
    }
    env_off := PgArgv + (args.Len() + 1) * 8
    str_off := env_off + (envc + 2) * 8
    path_off := str_off + strbytes
    total := PxPageRound(path_off + pathlen + 1)
    pg := Reserve(total, 4096)
    if raw.IsNull(pg) || !Commit(pg, total) {
        if !raw.IsNull(pg) {
            Release(pg, total)
        }
        return FailWith(MapError(12))
    }
    var r := Ok(0)
    // The pages are fresh and zero, so every string copied below ends in a
    // NUL already, and argv and envp end in a NULL.
    unsafe "the call's pages: the regions above" {
        argv := raw.Offset(pg, PgArgv)
        var s := str_off
        for i, a in args {
            PxPut64(argv, i * 8, u64(raw.Addr(raw.Offset(pg, s))))
            raw.Copy(raw.Offset(pg, s), raw.StrData(a), a.Len())
            s += a.Len() + 1
        }
        envp := raw.Offset(pg, env_off)
        var k := 0
        if envc > 0 {
            envs := Envp(sp)
            for j in 0..envc {
                e := raw.FromAddr[u8](Word(envs, j))
                if !IsInheritEntry(e) {
                    PxPut64(envp, k * 8, u64(raw.Addr(e)))
                    k += 1
                }
            }
        }
        if inherit != 0 {
            iv := raw.Offset(pg, PgInherit)
            raw.Copy(iv, raw.StrData("MK_INHERIT=3"), 12)
            PxPut64(envp, k * 8, u64(raw.Addr(iv)))
        }
        if pathlen > 0 {
            _ = GetEnvRaw(sp, "PATH", raw.SliceMut(raw.Offset(pg, path_off), pathlen))
        }
        f := FindProgram(args[0], raw.Offset(pg, PgCand), raw.Slice(raw.Offset(pg, path_off), pathlen))
        if f.err != core.SysErr.None {
            r = f
        } else {
            r = SpawnIn(pg, envp, child, inherit)
        }
    }
    Release(pg, total)
    return r
}

// SpawnIn makes the child's descriptors, spawns, and closes every
// descriptor it made for the child; on a failure the parent's pipe ends
// too.
SpawnIn(pg: *u8, envp: *u8, child: mut core.IoChild, inherit: uint): OsResult {
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
        // is what the child's dup2 onto 3 reads.
        unsafe "fcntl passes no memory" {
            d := fcntl(Trunc[i32](inherit), PxFDupFdCloExec, 4)
            if d < 0 {
                r = Fail(Errno())
            } else {
                theirs[3] = int(d)
            }
        }
    }
    if r.err == core.SysErr.None {
        var p := NewPlan(pg, envp)
        for i in 0..4 {
            p.fds[i] = Trunc[i32](theirs[i])
        }
        r = RunSpawn(p)
    }
    for i in 0..4 {
        if theirs[i] >= 0 {
            _ = lc.CloseFd(Trunc[i32](theirs[i]))
        }
    }
    if r.err != core.SysErr.None {
        for i in 0..3 {
            if mine[i] >= 0 {
                PollDetach(Trunc[uint](mine[i]))
                _ = lc.CloseFd(Trunc[i32](mine[i]))
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

// RunSpawn runs one posix_spawn as p says: value and os the child's pid.
// The file-action and attribute objects live only for the call. Errors:
// the error number posix_spawn or a setup call answered (the exec's own
// for a program that cannot run), with no child.
RunSpawn(p: SpawnPlan): OsResult {
    var fa: u64 = 0
    var at: u64 = 0
    var pid: i32 = 0
    unsafe "fa and at are the objects' words in this frame; p's paths and arrays are the call's pages" {
        fap := raw.PtrCast[u64, u8](raw.OfMut(mut fa))
        atp := raw.PtrCast[u64, u8](raw.OfMut(mut at))
        var rc := posix_spawn_file_actions_init(fap)
        if rc != 0 {
            return Fail(i64(rc))
        }
        rc = posix_spawnattr_init(atp)
        if rc != 0 {
            _ = posix_spawn_file_actions_destroy(fap)
            return Fail(i64(rc))
        }
        rc = SpawnSetup(&p, fap, atp)
        if rc == 0 {
            rc = posix_spawn(raw.OfMut(mut pid), p.path, fap, atp, p.argv, p.envp)
        }
        _ = posix_spawnattr_destroy(atp)
        _ = posix_spawn_file_actions_destroy(fap)
        if rc != 0 {
            return Fail(i64(rc))
        }
    }
    return OsResult(value: i64(pid), err: core.SysErr.None, os: i64(pid))
}

// SpawnSetup fills the attributes at and the file actions fa from p: the
// flags (the file's header), a new session for a pseudo-terminal's child
// or a new group (pgroup 0: the child's own pid) for a group's, then in
// order the directory, the three streams (a pty's peer opened as 0, which
// makes it the new session's controlling terminal, then dup2 onto 1 and
// 2), stderr joined to stdout, descriptor 3, and the named handles onto 3
// to 3 + n - 1. A parent stream that is closed is left closed (an
// addinherit_np of it would fail the spawn). 0, or the first call's error
// number.
SpawnSetup(p: &SpawnPlan, fa: *u8, at: *u8): i32 {
    tty := !raw.IsNull(p.tty)
    var flags: i32 = PxSetSigDef | PxSetSigMask | PxCloExecDefault
    if tty {
        flags |= PxSetSid
    } else if p.group {
        flags |= PxSetPgroup
    }
    var dfl: u32 = PxSigPipeBit
    var nomask: u32 = 0
    unsafe "posix_spawn's setup calls over the objects and the call's pages" {
        var rc := posix_spawnattr_setflags(at, flags)
        if rc == 0 {
            rc = posix_spawnattr_setsigdefault(at, raw.OfMut(mut dfl))
        }
        if rc == 0 {
            rc = posix_spawnattr_setsigmask(at, raw.OfMut(mut nomask))
        }
        if rc == 0 && !tty && p.group {
            rc = posix_spawnattr_setpgroup(at, 0)
        }
        if rc == 0 && !raw.IsNull(p.dir) {
            rc = posix_spawn_file_actions_addchdir_np(fa, p.dir)
        }
        if rc != 0 {
            return rc
        }
        if tty {
            rc = posix_spawn_file_actions_addopen(fa, 0, p.tty, ORdWr, 0)
            if rc == 0 {
                rc = posix_spawn_file_actions_adddup2(fa, 0, 1)
            }
            if rc == 0 {
                rc = posix_spawn_file_actions_adddup2(fa, 0, 2)
            }
        } else {
            for t in 0..3 {
                s := p.fds[t]
                if s >= 0 {
                    rc = posix_spawn_file_actions_adddup2(fa, s, Trunc[i32](t))
                } else if s == PxFdNull {
                    rc = posix_spawn_file_actions_addopen(fa, Trunc[i32](t), raw.StrData("/dev/null\u{0}"), ORdWr, 0)
                } else if fcntl(Trunc[i32](t), FGetFd) != -1 {
                    rc = posix_spawn_file_actions_addinherit_np(fa, Trunc[i32](t))
                }
                if rc != 0 {
                    return rc
                }
            }
        }
        if rc == 0 && p.join {
            rc = posix_spawn_file_actions_adddup2(fa, 1, 2)
        }
        if rc == 0 && p.fds[3] >= 0 {
            rc = posix_spawn_file_actions_adddup2(fa, p.fds[3], 3)
        }
        for i in 0..p.nnamed {
            if rc == 0 {
                rc = posix_spawn_file_actions_adddup2(fa, PxGet32(p.named, i * 4), Trunc[i32](3 + i))
            }
        }
        return rc
    }
}

// PipeCloExec makes a pipe into fds (read end, write end), both
// close-on-exec and blocking; on a failure neither end is left open.
PipeCloExec(fds: mut [2]i32): OsResult {
    unsafe "pipe writes two i32 into fds; fcntl passes no memory" {
        if lc.PipeFds(raw.DataMut(mut fds[..])) != 0 {
            return Fail(Errno())
        }
        for i in 0..2 {
            if fcntl(fds[i], PxFSetFd, PxFdCloExec) == -1 {
                e := Errno()
                _ = lc.CloseFd(fds[0])
                _ = lc.CloseFd(fds[1])
                return Fail(e)
            }
        }
    }
    return Ok(0)
}

// SetNonBlock sets O_NONBLOCK on fd's open file; 0, or the errno.
SetNonBlock(fd: i32): i64 {
    unsafe "fcntl passes no memory" {
        fl := fcntl(fd, PxFGetFl)
        if fl == -1 || fcntl(fd, PxFSetFl, fl | PxONonBlock) == -1 {
            return Errno()
        }
    }
    return 0
}

// ChildStream makes stream i's descriptors (0 stdin, 1 stdout, 2 stderr):
// theirs the child's (3 or above, close-on-exec, which the child's dup2
// clears on its copy; PxFdNull for the null device, which the child
// opens), mine the parent's pipe end: the write end for stdin, the read
// end otherwise, non-blocking and attached to the poller (PollAttach).
ChildStream(i: int, mode: u32, theirs: mut int, mine: mut int): OsResult {
    if mode == core.IoStdInherit {
        return Ok(0)
    }
    if mode == core.IoStdNull {
        *theirs = int(PxFdNull)
        return Ok(0)
    }
    var fds: [2]i32 = [0, 0]
    p := PipeCloExec(mut fds)
    if p.err != core.SysErr.None {
        return p
    }
    child := if i == 0 { int(fds[0]) } else { int(fds[1]) }
    par := if i == 0 { int(fds[1]) } else { int(fds[0]) }
    l := Lift(child)
    if l < 0 {
        _ = lc.CloseFd(Trunc[i32](par))
        return Fail(i64(-l))
    }
    e := SetNonBlock(Trunc[i32](par))
    if e != 0 || !PollAttach(Trunc[uint](par)) {
        _ = lc.CloseFd(Trunc[i32](l))
        _ = lc.CloseFd(Trunc[i32](par))
        return if e != 0 { Fail(e) } else { FailWith(core.SysErr.Other) }
    }
    *theirs = l
    *mine = par
    return Ok(0)
}

// Lift moves fd to 3 or above (so no dup2 onto 0 to 2 meets its own
// source) and closes the original; the descriptor, or -errno.
Lift(fd: int): int {
    if fd >= 3 {
        return fd
    }
    unsafe "fcntl passes no memory" {
        d := fcntl(Trunc[i32](fd), PxFDupFdCloExec, 3)
        e := Errno()
        _ = lc.CloseFd(Trunc[i32](fd))
        return if d < 0 { -Trunc[int](e) } else { int(d) }
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
// (PxMaxPath bytes): a name with a slash as it is; else the first entry of
// path (PATH's value) where dir/name is a regular file with an execute
// bit. An entry that is empty or relative (the current directory, or a
// path under it) is skipped: Go 1.19's LookPath refuses such a result
// (exec.ErrDot) and Windows' FindProgram never searches the current
// directory, so every target keeps the rule. NotFound when none;
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
    unsafe "cand holds PxMaxPath bytes" {
        if slash {
            if b.Len() + 1 > PxMaxPath {
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
            if n > 0 && path[i] == 47 && n + 1 + b.Len() + 1 <= PxMaxPath {
                raw.Copy(cand, raw.Offset(raw.Data(path), i), n)
                raw.Write(raw.Offset(cand, n), 47)
                raw.Copy(raw.Offset(cand, n + 1), raw.Data(b), b.Len())
                raw.Write(raw.Offset(cand, n + 1 + b.Len()), 0)
                if IsExecutable(cand, n + 1 + b.Len() + 1) {
                    return Ok(0)
                }
            }
            i = j + 1
        }
    }
    return Fail(2)
}

// IsExecutable: the path c (n bytes with its NUL) names a regular file
// with an execute bit (stat, following links) that this process may run:
// faccessat(X_OK, AT_EACCESS) checks the effective ids and ACLs as execve
// does (as Go's LookPath); a sandbox that refuses the check (EPERM) leaves
// the execute bits as the answer, as Go does.
IsExecutable(c: *u8, n: int): bool {
    var st: [18]u64 = [0; 18]
    unsafe "c holds n bytes, NUL-terminated" {
        if PathStat(raw.Slice(c, n), false, mut st) != 0 {
            return false
        }
        mode := StatMode(&st)
        if (mode & 0o170000) != 0o100000 || (mode & 0o111) == 0 {
            return false
        }
        if faccessat(PxAtFdcwd, c, PxXOk, PxAtEAccess) == 0 {
            return true
        }
        return Errno() == PxEperm
    }
}

/// PollRelease releases what a submission of op keeps past its completion:
/// nothing on macOS (a kqueue registration is one-shot and gone by the
/// completion). Windows releases a registered wait and its event here.
/// O(1); allocates nothing.
export PollRelease(op: *Op) {
}

/// ProcKill sends SIGKILL to the child whose pid is h (kill(2)). A child
/// that already ended and was reaped is success (ESRCH). The pid names the
/// child only until it is reaped: std.task asks no kill after its wait
/// answered.
/// O(1): one system call; allocates nothing.
export ProcKill(h: uint): OsResult {
    unsafe "kill passes no memory" {
        if kill(Trunc[i32](h), 9) != 0 {
            e := Errno()
            if e != PxEsrch {
                return Fail(e)
            }
        }
    }
    return Ok(0)
}

/// Inherited answers the socket this process inherited from a Spawn with
/// inherit (MK_INHERIT=<fd> in the environment): close-on-exec again,
/// non-blocking and attached to the poller, as SocketOpen's async sockets.
/// NotFound when there is none; a value that names no socket is Other.
/// ShareListener has std/sys/linux's contract for a listener another
/// process holds too; on macOS it changes nothing and answers os 0: kqueue
/// wakes every holder of a shared listener, and Inherited already makes an
/// inherited one non-blocking. O(1); allocates nothing.
export ShareListener(fd: uint): OsResult {
    return OsResult(value: 0, err: core.SysErr.None, os: 0)
}

/// O(1) system calls besides the environment scan; allocates nothing
/// beyond the socket's registration with the poller.
export Inherited(sp: *u8): OsResult {
    iv := InheritValue(sp)
    if iv == -1 {
        return FailWith(core.SysErr.NotFound)
    }
    if iv < 0 || HandleKind(Trunc[uint](iv)) != 4 {
        return FailWith(core.SysErr.Other)
    }
    v := Trunc[i32](iv)
    unsafe "fcntl passes no memory" {
        if fcntl(v, PxFSetFd, PxFdCloExec) == -1 {
            return Fail(Errno())
        }
    }
    e := SetNonBlock(v)
    if e != 0 {
        return Fail(e)
    }
    if !PollAttach(Trunc[uint](v)) {
        return FailWith(core.SysErr.Other)
    }
    return Ok(i64(v))
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
/// names is made close-on-exec at once. The spawn's dup2 cleared the flag
/// on descriptor 3, and without this a child that starts a program before
/// it calls Inherited, or never calls it, hands the listener to a program
/// started by any route other than std's spawn (whose CLOEXEC_DEFAULT
/// closes it anyway), which then keeps the port open. Nothing else is
/// touched: the descriptor stays this process's for Inherited.
/// The descriptors MK_HANDLES names (the handles a Command named, which
/// the child's dup2 left without the flag) are checked first, before this
/// touches any descriptor, and the verdict is kept for HandlesValue
/// (passfd.mk's PassHandlesSeal): a list that passes is made close-on-exec;
/// a list that fails is left as it is, every descriptor it names untouched.
/// O(1) system calls; allocates nothing.
export InheritSeal(sp: *u8) {
    v := InheritValue(sp)
    PassHandlesSeal(sp, v)
    if v >= 0 && HandleKind(Trunc[uint](v)) == 4 {
        unsafe "fcntl passes no memory" {
            _ = fcntl(Trunc[i32](v), PxFSetFd, PxFdCloExec)
        }
    }
}

// PxPageRound rounds n up to whole 4,096-byte units (mem.mk rounds those
// to the OS page).
PxPageRound(n: int): int {
    return (n + 4095) / 4096 * 4096
}
