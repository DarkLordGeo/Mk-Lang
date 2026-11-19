// Why: std.rt's process and terminal API (pipes, spawning with a directory,
// group or pty, signals, reaping detached children, ptys, raw terminal mode)
// needs Linux calls that a plain spawn does not. They live in a file of
// their own so a program links them only when it names std.rt's functions
// (std/rt/proc.mk), while proc.mk's spawn stays what the IoTable's spawn
// entry links.
//
// What: Pipe, PipeAttach, PipeLend, SpawnEx, ChildSignal, TryReap, PtyOpen,
// PtyResize, TermOpen, TermMode and the reaper set.
//
// How:
// - Pipe: pipe2 with O_CLOEXEC, both ends blocking. Not a socket pair: an
//   end lent to a child is the same pipe, and closing a socket end with
//   shutdown would end the child's stream too. PipeAttach puts an end on
//   the poller (epoll: O_NONBLOCK and registered), and PipeLend gives a
//   child such an end blocking, as a new open file of the same pipe
//   (/proc/self/fd/N opened again), since O_NONBLOCK belongs to the open
//   file.
// - SpawnEx: proc.mk's vfork clone (CLONE_VM | CLONE_VFORK | CLONE_PIDFD,
//   musl's posix_spawn shape) over a request that adds the directory
//   (chdir), a process group (setpgid(0, 0)), a controlling terminal (setsid,
//   the pty's peer from TIOCGPTPEER as the three streams, TIOCSCTTY),
//   stderr joined to stdout, lent descriptors, and close_range(3, ~0) before
//   execve (Linux 5.9; ENOSYS ignored: every descriptor std opens is
//   close-on-exec), so the child holds nothing it was not given.
// - ChildSignal: pidfd_send_signal, or kill(-pgid) for a group; TryReap,
//   the detached children's reaper (waitid P_PIDFD, WNOHANG).
// - PtyOpen, PtyResize: /dev/ptmx (O_NOCTTY), TIOCSPTLCK, TIOCSWINSZ.
// - TermOpen, TermMode: /dev/tty, TCGETS and TCSETS with Go's x/term flags.
// Errors are MapError's (sys.mk) with call.mk's FailCall rows; ENXIO (no
// controlling terminal) is NotFound.

import std.core
import std.raw

// The request's words past proc.mk's: the flags, the directory, and the
// named handles: their count, and the offset in the pages of their
// source descriptors (i32 each).
const RqFlags: int = 44
const RqDir: int = 48
const RqNamed: int = 56
const RqNamedAt: int = 60
// RqFlags' bits.
const FlGroup: u32 = 1
const FlPty: u32 = 2
const FlJoin: u32 = 4

const ONoCtty: uint = 0o400
const Enxio: i64 = 6
const Tiocsptlck: uint = 0x4004_5431
const Tiocswinsz: uint = 0x5414
const Tiocgptpeer: uint = 0x5441
const Tiocsctty: uint = 0x540E
// TermiosBytes is the kernel's struct termios (NCCS 19), which TCGETS and
// TCSETS take: c_iflag, c_oflag, c_cflag, c_lflag at 0 to 15, c_line at
// 16, c_cc at 17.
const TermiosBytes: int = 36

/// Pipe makes a pipe: value the read end, os the write end, both blocking
/// and close-on-exec. O(1): one system call; allocates nothing.
export Pipe(): OsResult {
    var fds: [2]i32 = [0, 0]
    unsafe "system call; fds holds the two descriptors" {
        r := Syscall(Nrs().pipe2, raw.Addr(raw.DataMut(mut fds[..])), OCloExec, 0, 0, 0, 0)
        if IsErr(r) {
            return FailCall(i64(-r))
        }
    }
    return OsResult(value: i64(fds[0]), err: core.SysErr.None, os: i64(fds[1]))
}

// PipeFdOpen records whether this process can open a pipe again through
// /proc/self/fd (PipeLend): 0 not yet asked, 1 yes, 2 no (no procfs).
struct PipeFdOpenState {
    state: raw.Atomic[u32]
}

var PipeFdOpen: PipeFdOpenState = PipeFdOpenState(state: raw.Atomic[u32].New(0))

// ProcFdPath writes /proc/self/fd/<fd> and a NUL into b.
ProcFdPath(fd: uint, b: mut [40]u8) {
    p := "/proc/self/fd/".AsBytes()
    for i, c in p {
        b[i] = c
    }
    var digits: [20]u8 = [0; 20]
    var n := 0
    var v := fd
    for {
        digits[n] = Trunc[u8](48 + v % 10)
        n += 1
        v /= 10
        if v == 0 {
            break
        }
    }
    for i in 0..n {
        b[p.Len() + i] = digits[n - 1 - i]
    }
    b[p.Len() + n] = 0
}

// OpenPipeAgain opens the pipe that fd is an end of as a new open file of
// its own (Linux opens a pipe's /proc/self/fd link as a FIFO), for reading
// or for writing, non-blocking (so a FIFO open never waits for the other
// side) and close-on-exec: the descriptor, or -errno (ENXIO for writing
// when the pipe has no reader).
OpenPipeAgain(fd: uint, write: bool): int {
    var b: [40]u8 = [0; 40]
    ProcFdPath(fd, mut b)
    access: uint = if write { 1 } else { ORdOnly }
    unsafe "system call; b is a NUL-terminated path in this frame" {
        return Syscall(Nrs().openat, AtFdcwd, raw.Addr(raw.Data(b[..])), access | ONonBlock | OCloExec, 0, 0, 0)
    }
}

// PipeReopens: PipeLend can open a pipe again (asked once, with the end
// fd: a reader opened and closed at once).
PipeReopens(fd: uint): bool {
    // Edge: a cache; every racing asker stores the same answer.
    s := PipeFdOpen.state.Load()
    if s != 0 {
        return s == 1
    }
    d := OpenPipeAgain(fd, false)
    ok := d >= 0
    if ok {
        CloseFd(d)
    }
    // Edge: publishes the answer above to later Loads.
    PipeFdOpen.state.Store(if ok { 1 } else { 2 })
    return ok
}

/// PipeAttach readies an end Pipe made (fd) for the poller, as SpawnEx's
/// parent ends are (proc.mk ChildStream): io_uring reads and writes
/// it on the ring as it is; epoll needs it non-blocking and registered, and
/// takes it only when PipeLend can give a child a blocking end of the same
/// pipe (this process can open /proc/self/fd links), since O_NONBLOCK is
/// shared by every duplicate. True when the end is the poller's; false
/// leaves it blocking and unregistered, a pool end. O(1) system calls (the
/// first call also opens and closes the pipe once); allocates nothing.
export PipeAttach(fd: uint): bool {
    if IsUring() {
        return true
    }
    if !PipeReopens(fd) {
        return false
    }
    unsafe "system calls that pass only values and a descriptor" {
        nb := Syscall(Nrs().fcntl, fd, FSetFl, ONonBlock, 0, 0, 0)
        if IsErr(nb) {
            return false
        }
        if !EpollAdd(fd) {
            _ = Syscall(Nrs().fcntl, fd, FSetFl, 0, 0, 0, 0)
            return false
        }
    }
    return true
}

/// PipeLend is the descriptor to give a child for the pipe end fd, an end
/// PipeAttach put on the poller: under epoll, the pipe opened
/// again, for reading or writing as fd is, as a new open file, blocking
/// (O_NONBLOCK cleared on it alone) and close-on-exec, value the new
/// descriptor and os 1 (the caller closes it once the child has its
/// copy), so the child's end blocks while this process's stays
/// non-blocking on the poller, and no task waiting on it is disturbed.
/// Otherwise value fd and os 0: io_uring's ends are blocking already, and
/// a write end whose pipe has no reader left (ENXIO) is lent as it is (a
/// child's write to it fails with EPIPE either way). Errors: the OS's (the
/// open or the fcntl). O(1) system calls; allocates nothing.
export PipeLend(fd: uint): OsResult {
    if IsUring() {
        return OsResult(value: Trunc[i64](fd), err: core.SysErr.None, os: 0)
    }
    var write := false
    unsafe "a system call that passes only values and a descriptor" {
        fl := Syscall(Nrs().fcntl, fd, FGetFl, 0, 0, 0, 0)
        if IsErr(fl) {
            return Result(fl)
        }
        write = (fl & 3) == 1
    }
    d := OpenPipeAgain(fd, write)
    if d == -Trunc[int](Enxio) && write {
        return OsResult(value: Trunc[i64](fd), err: core.SysErr.None, os: 0)
    }
    if IsErr(d) {
        return Result(d)
    }
    unsafe "a system call that passes only values and a descriptor" {
        c := Syscall(Nrs().fcntl, Trunc[uint](d), FSetFl, 0, 0, 0, 0)
        if IsErr(c) {
            CloseFd(d)
            return Result(c)
        }
    }
    return OsResult(value: i64(d), err: core.SysErr.None, os: 1)
}

// NameLen is the length of an environment entry's name: the bytes before
// its first '=' (an entry with none is all name).
NameLen(e: []u8): int {
    for i, b in e {
        if b == 0x3D {
            return i
        }
    }
    return e.Len()
}

// Overridden: the NUL-terminated parent entry p has the name of one of env's
// entries. O(len(env) * the name's bytes).
Overridden(p: *u8, env: []str): bool {
    for e in env {
        b := e.AsBytes()
        k := NameLen(b)
        var same := true
        unsafe "p is a NUL-terminated string from the kernel: a NUL ends the compare" {
            for j in 0..k {
                if raw.Read(raw.Offset(p, j)) != b[j] {
                    same = false
                    break
                }
            }
            if same && raw.Read(raw.Offset(p, k)) != 0x3D {
                same = false
            }
        }
        if same {
            return true
        }
    }
    return false
}

// Shadowed: a later entry of env has env[i]'s name, so env[i] is left out:
// the last entry of a name wins, as Go's os/exec (dedupEnv) and Rust's
// Command give it. O(len(env) * the name's bytes).
Shadowed(env: []str, i: int): bool {
    a := env[i].AsBytes()
    k := NameLen(a)
    var j := i + 1
    for j < env.Len() {
        b := env[j].AsBytes()
        if NameLen(b) == k {
            var same := true
            for x in 0..k {
                if a[x] != b[x] {
                    same = false
                    break
                }
            }
            if same {
                return true
            }
        }
        j += 1
    }
    return false
}

/// SpawnEx starts args[0] (found as proc.mk's Spawn finds it, in PATH) as
/// s says (core.IoSpawnReq): src[i] the OS descriptor of a lent stream
/// (IoStdHandle), pty the master of a pseudo-terminal (0 none), which
/// replaces the three streams. env is used as s.env_mode says, dir (empty:
/// this process's) is the child's working directory. value the pidfd (on
/// epoll's list when epoll is the backend), os the pid; s.pipes the
/// parent's pipe ends (pipes on the poller, proc.mk's ChildStream),
/// s.pid. Every descriptor made for the child is closed before it returns,
/// and the parent's pipe ends too on a failure. A NUL in an argument, an
/// entry or dir, an empty program, or an unknown stream mode: InvalidPath or
/// Other, with no child. When env names a variable twice, the later entry
/// wins. named are the OS descriptors of the handles the Command
/// names, at most PassMax (else Other): the child gets them as descriptors
/// 3 to 3 + n - 1, in order, and MK_HANDLES=3,4,... in its environment
/// (this process's own MK_HANDLES and MK_INHERIT are never passed on), and
/// nothing past them. O(bytes of args, env, the environment and PATH, plus entries of
/// env times the environment's for IoEnvExtend, and entries of env squared); the pages
/// it maps are given back before it returns.
export SpawnEx(sp: *u8, args: []str, env: []str, dir: str, s: mut core.IoSpawnReq, src: [3]uint, pty: uint,
    named: []uint, reopen: u64): OsResult {
    if args.Len() == 0 || args[0].Len() == 0 {
        return FailWith(core.SysErr.InvalidPath)
    }
    if named.Len() > PassMax {
        return FailWith(core.SysErr.Other)
    }
    // The named handles' sources (4 bytes each, after 4 of alignment), and
    // MK_HANDLES=3,4,...: 11 bytes, then at most two digits and a comma per
    // handle (PassMax keeps each below 100), then the NUL.
    var strbytes := dir.Len() + 1 + 4 + named.Len() * 4 + 12 + named.Len() * 3
    for a in args {
        for b in a.AsBytes() {
            if b == 0 {
                return FailWith(core.SysErr.InvalidPath)
            }
        }
        strbytes += a.Len() + 1
    }
    for e in env {
        for b in e.AsBytes() {
            if b == 0 {
                return FailWith(core.SysErr.InvalidPath)
            }
        }
        strbytes += e.Len() + 1
    }
    for b in dir.AsBytes() {
        if b == 0 {
            return FailWith(core.SysErr.InvalidPath)
        }
    }
    for i in 0..3 {
        if s.stdio[i] > core.IoStdJoin || (s.stdio[i] == core.IoStdJoin && i != 2) {
            return FailWith(core.SysErr.Other)
        }
    }
    if s.env_mode > core.IoEnvExtend {
        return FailWith(core.SysErr.Other)
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
    var pathlen := GetEnvRaw(sp, "PATH", mut empty[0..0])
    if pathlen < 0 {
        pathlen = 0
    }
    env_off := PgArgv + (args.Len() + 1) * 8
    str_off := env_off + (envc + env.Len() + 2) * 8
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
    var flags: u32 = 0
    // The pages are fresh and zero, so every string copied below ends in
    // a NUL already.
    unsafe "the call's pages: the request, argv, envp, the strings, PATH, then the child's stack" {
        argv := raw.Offset(pg, PgArgv)
        var at := str_off
        for i, a in args {
            Put64(argv, i * 8, u64(raw.Addr(raw.Offset(pg, at))))
            raw.Copy(raw.Offset(pg, at), raw.StrData(a), a.Len())
            at += a.Len() + 1
        }
        envp := raw.Offset(pg, env_off)
        var k := 0
        if s.env_mode != core.IoEnvReplace {
            for j in 0..envc {
                e := raw.FromAddr[u8](Word(sp, argc + 2 + j))
                if !IsInheritEntry(e) && (s.env_mode == core.IoEnvInherit || !Overridden(e, env)) {
                    Put64(envp, k * 8, u64(raw.Addr(e)))
                    k += 1
                }
            }
        }
        if s.env_mode != core.IoEnvInherit {
            for i, e in env {
                if Shadowed(env, i) {
                    continue
                }
                Put64(envp, k * 8, u64(raw.Addr(raw.Offset(pg, at))))
                raw.Copy(raw.Offset(pg, at), raw.StrData(e), e.Len())
                at += e.Len() + 1
                k += 1
            }
        }
        if named.Len() > 0 {
            // MK_HANDLES lists the child's descriptors 3 to 3 + n - 1.
            Put64(envp, k * 8, u64(raw.Addr(raw.Offset(pg, at))))
            k += 1
            raw.Copy(raw.Offset(pg, at), raw.StrData("MK_HANDLES="), 11)
            at += 11
            for i in 0..named.Len() {
                d := 3 + i
                if i > 0 {
                    raw.Write(raw.Offset(pg, at), 44)
                    at += 1
                }
                if d >= 10 {
                    raw.Write(raw.Offset(pg, at), Trunc[u8](48 + d / 10))
                    at += 1
                }
                raw.Write(raw.Offset(pg, at), Trunc[u8](48 + d % 10))
                at += 1
            }
            at += 1
        }
        if dir.Len() > 0 {
            raw.Copy(raw.Offset(pg, at), raw.StrData(dir), dir.Len())
            Put64(pg, RqDir, u64(raw.Addr(raw.Offset(pg, at))))
            at += dir.Len() + 1
        }
        // The named sources' array, 4-aligned; SpawnExIn fills it.
        at = (at + 3) / 4 * 4
        Put32(pg, RqNamed, Trunc[u32](named.Len()))
        Put32(pg, RqNamedAt, Trunc[u32](at))
        if pathlen > 0 {
            _ = GetEnvRaw(sp, "PATH", raw.SliceMut(raw.Offset(pg, path_off), pathlen))
        }
        Put64(pg, RqArgv, u64(raw.Addr(argv)))
        Put64(pg, RqEnvp, u64(raw.Addr(envp)))
        Put64(pg, RqPath, u64(raw.Addr(raw.Offset(pg, PgCand))))
        if s.group {
            flags |= FlGroup
        }
        if s.stdio[2] == core.IoStdJoin {
            flags |= FlJoin
        }
        if pty != 0 {
            flags |= FlPty
        }
        f := FindProgram(args[0], raw.Offset(pg, PgCand), raw.Slice(raw.Offset(pg, path_off), pathlen))
        if f.err != core.SysErr.None {
            r = f
        } else {
            r = SpawnExIn(pg, raw.Offset(pg, total), s, src, pty, flags, named)
        }
    }
    Release(pg, total)
    return r
}

// SpawnExIn makes the child's descriptors, clones, and closes every
// descriptor it made for the child; on a failure the parent's pipe ends
// too. top is the child stack's top. Each named handle is duplicated
// close-on-exec at 3 + n or above, so the child's dup3 onto 3 to 3 + n - 1
// never meets another's source; the duplicates are the request's array.
SpawnExIn(pg: *u8, top: *u8, s: mut core.IoSpawnReq, src: [3]uint, pty: uint, flags: u32, named: []uint): OsResult {
    // own: the descriptors made for the child, each closed once below; req:
    // the request's source for each stream (the pty's peer may be all three).
    var own: [3]int = [-1, -1, -1]
    var req: [4]int = [-1, -1, -1, -1]
    var mine: [3]int = [-1, -1, -1]
    var r := Ok(0)
    var made := 0
    unsafe "a system call that passes no memory; the array is the request's, in the call's pages" {
        arr := raw.Offset(pg, Trunc[int](Get32(pg, RqNamedAt)))
        for i, d in named {
            c := Syscall(Nrs().fcntl, d, FDupFdCloExec, Trunc[uint](3 + named.Len()), 0, 0, 0)
            if IsErr(c) {
                r = Result(c)
                break
            }
            Put32(arr, i * 4, Trunc[u32](c))
            made += 1
        }
    }
    if r.err == core.SysErr.None && pty != 0 {
        unsafe "a system call that passes no memory, only values and handles" {
            p := Syscall(Nrs().ioctl, pty, Tiocgptpeer, ORdWr | ONoCtty | OCloExec, 0, 0, 0)
            if IsErr(p) {
                r = FailCall(i64(-p))
            } else {
                l := Lift(p)
                if IsErr(l) {
                    r = Result(l)
                } else {
                    own[0] = l
                    req[0] = l
                    req[1] = l
                    req[2] = l
                }
            }
        }
    } else if r.err == core.SysErr.None {
        for i in 0..3 {
            mode := s.stdio[i]
            if mode == core.IoStdHandle {
                unsafe "a system call that passes no memory, only values and handles" {
                    d := Syscall(Nrs().fcntl, src[i], FDupFdCloExec, 4, 0, 0, 0)
                    if IsErr(d) {
                        r = Result(d)
                    } else {
                        own[i] = d
                    }
                }
            } else if mode == core.IoStdNull || mode == core.IoStdPipe {
                r = ChildStream(i, mode, mut own[i], mut mine[i])
            }
            if r.err != core.SysErr.None {
                break
            }
            req[i] = own[i]
        }
    }
    if r.err == core.SysErr.None {
        unsafe "the request's words" {
            for i in 0..4 {
                Put32(pg, RqFds + i * 4, Trunc[u32](req[i]))
            }
            Put32(pg, RqFlags, flags)
        }
        r = CloneChild(pg, top, ChildMainEx)
    }
    for i in 0..3 {
        if own[i] >= 0 {
            CloseFd(own[i])
        }
    }
    unsafe "the request's array, in the call's pages" {
        arr := raw.Offset(pg, Trunc[int](Get32(pg, RqNamedAt)))
        for i in 0..made {
            CloseFd(int(Get32(arr, i * 4)))
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
        s.pipes[i] = if mine[i] >= 0 { Trunc[uint](mine[i]) } else { 0 }
    }
    s.pid = r.os
    s.job = 0
    return Ok(r.value)
}

// ChildFail is the vfork child's failure: the errno into the request,
// then exit_group(127); CLONE_VFORK makes the parent read it after.
ChildFail(rq: *u8, r: int): never {
    unsafe "the vfork child: the request is the parent's pages" {
        Put32(rq, RqErr, Trunc[u32](-r))
        for {
            _ = Syscall(Nrs().exit_group, 127, 0, 0, 0, 0, 0)
        }
    }
}

// ChildMainEx is SpawnEx's vfork child: proc.mk's ChildMain, with the
// directory, the group or session, the joined stderr and the controlling
// terminal, and close_range before execve. System calls over the request
// only; it never returns.
ChildMainEx(rq: *u8) {
    n := Nrs()
    var dfl: [4]u64 = [0; 4]
    var cur: [4]u64 = [0; 4]
    unsafe "the vfork child: the request is the parent's pages, held by CLONE_VFORK" {
        // As ChildMain: handlers back to SIG_DFL, SIGPIPE's default.
        for sg in 1..65 {
            if sg != 9 && sg != 19 {
                r := Syscall(n.rt_sigaction, Trunc[uint](sg), 0, raw.Addr(raw.DataMut(mut cur[..])), 8, 0, 0)
                if r == 0 && (cur[0] > 1 || sg == 13) {
                    _ = Syscall(n.rt_sigaction, Trunc[uint](sg), raw.Addr(raw.Data(dfl[..])), 0, 8, 0, 0)
                }
            }
        }
        flags := Get32(rq, RqFlags)
        dir := Get64(rq, RqDir)
        if dir != 0 {
            c := Syscall(n.chdir, Trunc[uint](dir), 0, 0, 0, 0, 0)
            if IsErr(c) {
                ChildFail(rq, c)
            }
        }
        // A terminal's child leads a new session (and so a new group); a
        // group's child leads a new group in the parent's session.
        if flags & FlPty != 0 {
            g := Syscall(n.setsid, 0, 0, 0, 0, 0, 0)
            if IsErr(g) {
                ChildFail(rq, g)
            }
        } else if flags & FlGroup != 0 {
            g := Syscall(n.setpgid, 0, 0, 0, 0, 0, 0)
            if IsErr(g) {
                ChildFail(rq, g)
            }
        }
        for t in 0..3 {
            src := Get32(rq, RqFds + t * 4)
            if src != 0xFFFF_FFFF {
                d := Syscall(n.dup3, uint(src), Trunc[uint](t), 0, 0, 0, 0)
                if IsErr(d) {
                    ChildFail(rq, d)
                }
            }
        }
        if flags & FlJoin != 0 {
            d := Syscall(n.dup3, 1, 2, 0, 0, 0, 0)
            if IsErr(d) {
                ChildFail(rq, d)
            }
        }
        if flags & FlPty != 0 {
            c := Syscall(n.ioctl, 0, Tiocsctty, 0, 0, 0, 0)
            if IsErr(c) {
                ChildFail(rq, c)
            }
        }
        // The named handles onto 3 to 3 + n - 1; each source sits at
        // 3 + n or above and is close-on-exec, and dup3 clears the flag on
        // its copy.
        named := Trunc[int](Get32(rq, RqNamed))
        arr := raw.Offset(rq, Trunc[int](Get32(rq, RqNamedAt)))
        for i in 0..named {
            d := Syscall(n.dup3, uint(Get32(arr, i * 4)), Trunc[uint](3 + i), 0, 0, 0, 0)
            if IsErr(d) {
                ChildFail(rq, d)
            }
        }
        // Nothing past the three streams and the named handles survives
        // the exec. ENOSYS (before 5.9) is ignored: every descriptor std
        // opens is close-on-exec.
        _ = Syscall(n.close_range, Trunc[uint](3 + named), 0xFFFF_FFFF, 0, 0, 0, 0)
        // Edge: PollInit's Stores, made before any spawn (ChildMain's).
        if FdLimit.max.Load() != 0 {
            var lim: [2]u64 = [FdLimit.cur.Load(), FdLimit.max.Load()]
            _ = Syscall(n.prlimit64, 0, 7, raw.Addr(raw.Data(lim[..])), 0, 0, 0)
        }
        var nomask: u64 = 0
        _ = Syscall(n.rt_sigprocmask, SigSetMask, raw.Addr(raw.Of(&nomask)), 0, 8, 0, 0)
        e := Syscall(n.execve, Trunc[uint](Get64(rq, RqPath)), Trunc[uint](Get64(rq, RqArgv)),
            Trunc[uint](Get64(rq, RqEnvp)), 0, 0, 0)
        ChildFail(rq, e)
    }
}

/// ChildSignal sends sig (Linux's number) to the child of the pidfd h, or
/// with group to every process of its group (kill(-pid)); the caller sends
/// to a group only while its leader is unreaped, so the id is still the
/// group's. A child or group already gone (ESRCH) is success.
/// O(1): one system call; allocates nothing.
export ChildSignal(h: uint, group: bool, pid: i64, job: uint, sig: u32): OsResult {
    if sig == 0 || sig > 64 || (group && pid <= 0) {
        return FailWith(core.SysErr.Other)
    }
    unsafe "a system call that passes no memory, only values and handles" {
        r := if group {
            Syscall(Nrs().kill, Trunc[uint](-pid), uint(sig), 0, 0, 0, 0)
        } else {
            Syscall(Nrs().pidfd_send_signal, h, uint(sig), 0, 0, 0, 0)
        }
        if IsErr(r) && r != Esrch {
            return FailCall(i64(-r))
        }
    }
    return Ok(0)
}

/// TryReap reaps the child of the pidfd h if it has ended: true when it
/// was reaped now or cannot be waited for any more (ECHILD: reaped
/// before), false while it runs. O(1): one system call, EINTR repeated;
/// allocates nothing.
export TryReap(h: uint): bool {
    var si: [16]u64 = [0; 16]
    unsafe "system call; si holds a siginfo_t, si_pid at 16" {
        for {
            r := Syscall(Nrs().waitid, PPidfd, h, raw.Addr(raw.DataMut(mut si[..])), WExited | WNoHang, 0, 0)
            if r == -4 {
                continue
            }
            return IsErr(r) || Get32(raw.PtrCast[u64, u8](raw.Data(si[..])), 16) != 0
        }
    }
}

// WinSize writes cols and rows to fd's terminal (TIOCSWINSZ); Other when
// either is outside 1 to 65,535.
WinSize(fd: uint, cols: int, rows: int): OsResult {
    if cols < 1 || cols > 65535 || rows < 1 || rows > 65535 {
        return FailWith(core.SysErr.Other)
    }
    var ws: [1]u64 = [0]
    unsafe "system call; ws holds a struct winsize: rows, columns, then two unused u16" {
        q := raw.PtrCast[u64, u8](raw.DataMut(mut ws[..]))
        Put16(q, 0, Trunc[u16](rows))
        Put16(q, 2, Trunc[u16](cols))
        r := Syscall(Nrs().ioctl, fd, Tiocswinsz, raw.Addr(q), 0, 0, 0)
        if IsErr(r) {
            return FailCall(i64(-r))
        }
    }
    return Ok(0)
}

/// PtyOpen opens a pseudo-terminal's master (/dev/ptmx, O_NOCTTY,
/// close-on-exec, blocking), unlocks its peer and sets its size: value the
/// master, and w.a and w.b it too (Windows' read and write ends). A kernel
/// without devpts is the OS's error (ENOENT: NotFound).
/// O(1): three system calls; the master is closed on every failure.
export PtyOpen(cols: int, rows: int, w: mut core.IoWords): OsResult {
    if cols < 1 || cols > 65535 || rows < 1 || rows > 65535 {
        return FailWith(core.SysErr.Other)
    }
    unsafe "system calls; the path is a NUL-terminated constant, z an int in this frame" {
        fd := Syscall(Nrs().openat, AtFdcwd, raw.Addr(raw.StrData("/dev/ptmx\u{0}")), ORdWr | ONoCtty | OCloExec, 0,
            0, 0)
        if IsErr(fd) {
            return FailCall(i64(-fd))
        }
        var z: u32 = 0
        u := Syscall(Nrs().ioctl, Trunc[uint](fd), Tiocsptlck, raw.Addr(raw.OfMut(mut z)), 0, 0, 0)
        if IsErr(u) {
            CloseFd(fd)
            return FailCall(i64(-u))
        }
        z2 := WinSize(Trunc[uint](fd), cols, rows)
        if z2.err != core.SysErr.None {
            CloseFd(fd)
            return z2
        }
        // OpReady waits for the master on epoll's edges.
        if !IsUring() && !EpollAdd(Trunc[uint](fd)) {
            CloseFd(fd)
            return Fail(24)
        }
        w.a = i64(fd)
        w.b = i64(fd)
        return Ok(i64(fd))
    }
}

/// PtyResize sets the size of the pseudo-terminal whose master is fd; its
/// session's foreground group gets SIGWINCH from the kernel.
/// O(1): one system call; allocates nothing.
export PtyResize(fd: uint, cols: int, rows: int): OsResult {
    return WinSize(fd, cols, rows)
}

/// PtyClose is Windows' ClosePseudoConsole: Linux's pty is its master
/// descriptor alone, closed as a handle. O(1).
export PtyClose(hpc: uint) {
}

/// GroupEnd is Windows' end of a job: Linux's poller kills the group in
/// the leader's wait (proc.mk ProcTry). O(1).
export GroupEnd(job: uint): OsResult {
    return Ok(0)
}

/// GroupClose is Windows' close of a job: nothing on Linux. O(1).
export GroupClose(job: uint) {
}

/// TermOpen opens this process's controlling terminal (/dev/tty, O_NOCTTY,
/// close-on-exec): value its descriptor, which is both the input and the
/// output; none (ENXIO) is NotFound. O(1): one system call.
export TermOpen(): OsResult {
    unsafe "system call; the path is a NUL-terminated constant" {
        fd := Syscall(Nrs().openat, AtFdcwd, raw.Addr(raw.StrData("/dev/tty\u{0}")), ORdWr | ONoCtty | OCloExec, 0, 0,
            0)
        if IsErr(fd) {
            if i64(fd) == -Enxio {
                return OsResult(value: 0, err: core.SysErr.NotFound, os: Enxio)
            }
            return FailCall(i64(-fd))
        }
        // OpReady waits for the terminal on epoll's edges.
        if !IsUring() && !EpollAdd(Trunc[uint](fd)) {
            CloseFd(fd)
            return Fail(24)
        }
        return OsResult(value: i64(fd), err: core.SysErr.None, os: i64(fd))
    }
}

/// TermMode reads the terminal h's mode into old (TCGETS) and sets mode:
/// core.IoTermSaved sets saved (as an earlier call's old gave it),
/// IoTermRaw raw mode (Go's x/term MakeRaw, which is cfmakeraw), and
/// IoTermNoEcho echo off with line editing and signals kept (x/term's
/// ReadPassword); IoTermGet sets nothing (TCGETS alone). out is unused
/// (the same descriptor). value the bytes of old (36). A short old or
/// saved, or an unknown mode: Other with nothing set. O(1): two system
/// calls, one for IoTermGet; allocates nothing.
export TermMode(h: uint, out: uint, mode: i64, saved: []u8, old: mut []u8): OsResult {
    if old.Len() < TermiosBytes || mode < 0 || mode > core.IoTermGet ||
        (mode == core.IoTermSaved && saved.Len() < TermiosBytes) {
        return FailWith(core.SysErr.Other)
    }
    var t: [8]u64 = [0; 8]
    unsafe "system calls; t holds a struct termios" {
        q := raw.PtrCast[u64, u8](raw.DataMut(mut t[..]))
        g := Syscall(Nrs().ioctl, h, Tcgets, raw.Addr(q), 0, 0, 0)
        if IsErr(g) {
            return FailCall(i64(-g))
        }
        for i in 0..TermiosBytes {
            old[i] = raw.Read(raw.Offset(q, i))
        }
        if mode == core.IoTermGet {
            return Ok(i64(TermiosBytes))
        }
        if mode == core.IoTermSaved {
            for i in 0..TermiosBytes {
                raw.Write(raw.Offset(q, i), saved[i])
            }
        } else if mode == core.IoTermRaw {
            // IGNBRK BRKINT PARMRK ISTRIP INLCR IGNCR ICRNL IXON; OPOST;
            // ECHO ECHONL ICANON ISIG IEXTEN; CSIZE PARENB off, CS8 on;
            // VMIN 1, VTIME 0.
            Put32(q, 0, Get32(q, 0) & ~u32(0x1 | 0x2 | 0x8 | 0x20 | 0x40 | 0x80 | 0x100 | 0x400))
            Put32(q, 4, Get32(q, 4) & ~u32(0x1))
            Put32(q, 8, (Get32(q, 8) & ~u32(0x30 | 0x100)) | 0x30)
            Put32(q, 12, Get32(q, 12) & ~u32(0x8 | 0x40 | 0x2 | 0x1 | 0x8000))
            raw.Write(raw.Offset(q, 17 + 6), 1)
            raw.Write(raw.Offset(q, 17 + 5), 0)
        } else {
            // ECHO off; ICANON and ISIG on; ICRNL on.
            Put32(q, 12, (Get32(q, 12) & ~u32(0x8)) | 0x2 | 0x1)
            Put32(q, 0, Get32(q, 0) | 0x100)
        }
        s := Syscall(Nrs().ioctl, h, Tcsets, raw.Addr(q), 0, 0, 0)
        if IsErr(s) {
            return FailCall(i64(-s))
        }
    }
    return Ok(i64(TermiosBytes))
}

/// TermRecords reads the bytes the terminal h holds now, without waiting
/// (a ppoll with a zero timeout, then one read): value the bytes read, 0
/// when none is there. Windows answers console records instead.
/// O(1): two system calls; allocates nothing.
export TermRecords(h: uint, out: mut []u8): OsResult {
    if out.Len() == 0 {
        return Ok(0)
    }
    var pfd: [1]u64 = [0]
    var ts: [2]u64 = [0, 0]
    unsafe "system calls; pfd is a struct pollfd, ts a zero timespec, out the caller's" {
        q := raw.PtrCast[u64, u8](raw.DataMut(mut pfd[..]))
        Put32(q, 0, Trunc[u32](h))
        Put16(q, 4, 1)
        for {
            r := Syscall(Nrs().ppoll, raw.Addr(q), 1, raw.Addr(raw.Data(ts[..])), 0, 8, 0)
            if r == -4 {
                continue
            }
            if IsErr(r) {
                return FailCall(i64(-r))
            }
            if r == 0 {
                return Ok(0)
            }
            break
        }
        for {
            n := Syscall(Nrs().read, h, raw.Addr(raw.DataMut(out)), Trunc[uint](out.Len()), 0, 0, 0)
            if n == -4 {
                continue
            }
            // A terminal whose other side is gone reads EIO: the end.
            if n == -5 {
                return Ok(0)
            }
            return ResultCall(n)
        }
    }
}

// The reaper set behind std.rt's detached children:
// an epoll set of pidfds, each registered for input once (EPOLLONESHOT)
// with the caller's tag as its data. A pidfd turns readable when its
// process ends, so a wait on the set returns only ended children, and a
// child that ended before it was added is returned by the next wait.
const EpollOneShot: u32 = 0x4000_0000
const EpollCtlMod: uint = 3

/// ReapSetOpen makes an empty reaper set: value its descriptor
/// (close-on-exec), kept until exit. Errors: the OS's (epoll_create1).
/// O(1): one system call; allocates nothing in the process.
export ReapSetOpen(): OsResult {
    unsafe "system call; passes only values" {
        return Result(Syscall(Nrs().epoll_create1, EpollCloExec, 0, 0, 0, 0, 0))
    }
}

/// ReapSetAdd registers the pidfd h in set for one report of its end,
/// with tag as the report's data; again re-arms an h already registered.
/// Errors: the OS's (epoll_ctl). O(1): one system call; allocates nothing
/// in the process.
export ReapSetAdd(set: uint, h: uint, tag: u64, again: bool): OsResult {
    var e: [2]u64 = [0; 2]
    unsafe "system call; e holds one epoll_event of the target's layout" {
        ep8 := raw.PtrCast[u64, u8](raw.Data(e[..]))
        Put32(ep8, 0, EpollIn | EpollOneShot)
        Put64(ep8, EvData(), tag)
        op := if again { EpollCtlMod } else { EpollCtlAdd }
        return Result(Syscall(Nrs().epoll_ctl, set, op, h, raw.Addr(ep8), 0, 0))
    }
}

/// ReapSetWait waits, with no timeout, until children registered in set
/// have ended, and writes their tags to tags: value how many (at least 1,
/// at most tags.Len() and 85, a 1 KiB event buffer's). An interrupted wait
/// is waited again.
/// Errors: the OS's (epoll_pwait); EINVAL for an empty tags. O(1) system
/// calls besides the wait; allocates nothing.
export ReapSetWait(set: uint, tags: mut []u64): OsResult {
    var events: [128]u64 = [0; 128]
    var max := 1024 / EvBytes()
    if max > tags.Len() {
        max = tags.Len()
    }
    if max < 1 {
        return Fail(22)
    }
    unsafe "system call; events holds max epoll_events of the target's layout" {
        evp := raw.PtrCast[u64, u8](raw.DataMut(mut events[..]))
        forever: i64 = -1
        for {
            r := Syscall(Nrs().epoll_pwait, set, raw.Addr(evp), Trunc[uint](max), Trunc[uint](forever), 0, 8)
            if r == -4 {
                continue
            }
            if IsErr(r) {
                return Fail(i64(-r))
            }
            for k in 0..r {
                tags[k] = Get64(evp, k * EvBytes() + EvData())
            }
            return Ok(i64(r))
        }
    }
}
