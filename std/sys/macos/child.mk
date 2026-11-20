// Why: std.rt's process and terminal API (pipes, spawning with a directory,
// group or pty, signals, reaping detached children, ptys, raw terminal mode)
// needs macOS calls that a plain spawn does not. They live in a file of
// their own so a program links them only when it names std.rt's functions
// (std/rt/proc.mk), while proc.mk's spawn stays what the IoTable's spawn
// entry links.
//
// What: Pipe, PipeAttach, PipeLend, SpawnEx, ChildSignal, TryReap, PtyOpen,
// PtyResize, TermOpen, TermMode, TermRecords, the reaper set, and the
// exports whose work is signal.mk's (TermFatalInstall, RaiseFatal).
//
// How:
// - Pipe: pipe, then FD_CLOEXEC on both ends, both blocking. Darwin cannot
//   open a pipe again as a new open file (/dev/fd/N duplicates the
//   descriptor), so an end made non-blocking for the poller could only be
//   lent to a child non-blocking: PipeAttach declines every end, which std.rt
//   then reads and writes on its blocking pool, and PipeLend lends an end
//   as it is.
// - SpawnEx: proc.mk's posix_spawn over a plan that adds the directory
//   (addchdir_np), a process group (POSIX_SPAWN_SETPGROUP, pgroup 0), a
//   controlling terminal (POSIX_SPAWN_SETSID, then the pty's peer opened as
//   descriptor 0 without O_NOCTTY, which a session leader's first terminal
//   open makes its controlling terminal, and dup2 onto 1 and 2), stderr
//   joined to stdout, and the lent descriptors; POSIX_SPAWN_CLOEXEC_DEFAULT
//   closes everything else, so the child holds nothing it was not given.
// - A child's handle is its pid (macOS has no process descriptor).
//   ChildSignal is kill(pid), or kill(-pgid) for a group; TryReap, the
//   detached children's reaper, is waitpid with WNOHANG.
// - The reaper set is a kqueue: EVFILT_PROC with NOTE_EXIT per child, one
//   shot, the tag as its udata.
// - PtyOpen, PtyResize: /dev/ptmx (O_NOCTTY), TIOCPTYGRANT, TIOCPTYUNLK;
//   the peer's name is TIOCPTYGNAME's (as libSystem's grantpt, unlockpt
//   and ptsname). Darwin's master refuses TIOCSWINSZ, TIOCGETA and
//   TIOCSETA with ENOTTY until its peer was first opened, and the first
//   open of a peer that no one holds zeroes the window size and resets
//   termios. So the size each master was last given is kept in PtySizes,
//   a table indexed by the master's descriptor; SpawnEx starts a pty's
//   child stopped (POSIX_SPAWN_START_SUSPENDED, after its file actions
//   opened the peer), sets that size on the master, and sends SIGCONT, so
//   the child's first instruction already sees it. The parent never opens
//   the peer itself: a last close of it waits briefly for the master to
//   read and then discards what is left, so a parent's copy closed after a
//   quick child exited would lose that child's output. PtyResize keeps the
//   size and sets it on the master, where ENOTTY (no peer opened yet)
//   leaves it to the next spawn.
// - TermOpen, TermMode: /dev/tty, TIOCGETA and TIOCSETA with Go's x/term
//   flags over Darwin's 72-byte struct termios.
// ioctl request numbers and flag bits are Go's (syscall/zerrors_darwin_
// arm64.go and _amd64.go agree on every one used here). Errors are
// MapError's; ENXIO (no controlling terminal) is NotFound.

import std.core
import std.raw
import std.sys.lc

// <sys/ttycom.h>, <sys/filio.h> request numbers.
const ChTiocptygrant: uint = 0x2000_7454
const ChTiocptyunlk: uint = 0x2000_7452
const ChTiocptygname: uint = 0x4080_7453
const ChTiocswinsz: uint = 0x8008_7467
const ChTiocgeta: uint = 0x4048_7413
const ChTiocseta: uint = 0x8048_7414
const ChFionread: uint = 0x4004_667F
const ChTiocoutq: uint = 0x4004_7473
const ChEnxio: i64 = 6
const ChEio: i64 = 5
const ChEnotty: i64 = 25
const ChSigCont: i32 = 19
// ChStartSuspended is POSIX_SPAWN_START_SUSPENDED (<spawn.h>): proc.mk's
// SpawnSetup sets it with POSIX_SPAWN_SETSID for a pty's child.
const ChStartSuspended: i32 = 0x80
// ChTermiosAll is sizeof(struct termios) on Darwin: c_iflag, c_oflag,
// c_cflag and c_lflag as u64 at 0 to 31, c_cc[20] at 32 (VMIN 16, VTIME 17),
// 4 bytes of padding, then c_ispeed and c_ospeed at 56 and 64. ChTermSaved
// is how much of it a saved mode keeps: std.rt's terminal-restore registry
// holds 64 bytes of a mode, and term.mk's TermRestoreMode lays them over
// the terminal's current termios, so the output speed stays the
// terminal's.
const ChTermiosAll: int = 72
const ChTermSaved: int = 64
// The kqueue filter, flag and note values (<sys/event.h>).
const ChEvfiltProc: u64 = 0xFFFB
const ChEvfiltUser: u64 = 0xFFF6
const ChEvAdd: u64 = 0x1
const ChEvOneShot: u64 = 0x10
const ChEvError: u64 = 0x4000
const ChEvfiltRead: u64 = 0xFFFF
const ChNoteExit: u64 = 0x8000_0000
const ChNoteTrigger: u64 = 0x0100_0000
// ChEventBytes is sizeof(struct kevent): ident 0, filter (i16) 8, flags
// (u16) 10, fflags (u32) 12, data 16, udata 24.
const ChEventBytes: int = 32

/// Pipe makes a pipe: value the read end, os the write end, both blocking
/// and close-on-exec. O(1): three system calls; allocates nothing.
export Pipe(): OsResult {
    var fds: [2]i32 = [0, 0]
    p := PipeCloExec(mut fds)
    if p.err != core.SysErr.None {
        return p
    }
    return OsResult(value: i64(fds[0]), err: core.SysErr.None, os: i64(fds[1]))
}

/// PipeAttach readies an end Pipe made (fd) for the poller: on macOS it
/// declines every end. O_NONBLOCK belongs to the open file, which every
/// duplicate shares, and Darwin has no way to open the pipe again as a
/// file of its own, so a child lent a non-blocking end would get
/// EAGAIN where it expects to wait. false: the end stays blocking and
/// unregistered, a pool end. O(1); allocates nothing.
export PipeAttach(fd: uint): bool {
    return false
}

/// PipeLend is the descriptor to give a child for the pipe end fd: on
/// macOS fd itself, value fd and os 0 (nothing for the caller to close),
/// since PipeAttach never makes an end non-blocking. O(1); allocates
/// nothing.
export PipeLend(fd: uint): OsResult {
    return OsResult(value: Trunc[i64](fd), err: core.SysErr.None, os: 0)
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
        unsafe "p is a NUL-terminated string from dyld: a NUL ends the compare" {
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
/// replaces the three streams and makes the child lead a new session with
/// it as the controlling terminal. env is used as s.env_mode says, dir
/// (empty: this process's) is the child's working directory. value and os
/// the child's pid, which is its handle on macOS; s.pipes the parent's
/// pipe ends (on the poller, proc.mk's ChildStream), s.pid; s.job 0. With
/// s.group the child leads a new process group. Every descriptor made for
/// the child is closed before it returns, and the parent's pipe ends too
/// on a failure. A NUL in an argument, an entry or dir, an empty program,
/// or an unknown stream mode: InvalidPath or Other, with no child. When env
/// names a variable twice, the later entry wins. named are the OS
/// descriptors of the handles the Command names, at most PassMax (else
/// Other): the child gets them as descriptors 3 to 3 + n - 1, in order,
/// and MK_HANDLES=3,4,... in its environment (this process's own
/// MK_HANDLES and MK_INHERIT are never passed on), and nothing past them.
/// reopen is Windows'. O(bytes of args, env, the environment and PATH, plus
/// entries of env times the environment's for IoEnvExtend, and entries of
/// env squared); the pages it maps are given back before it returns.
export SpawnEx(sp: *u8, args: []str, env: []str, dir: str, s: mut core.IoSpawnReq, src: [3]uint, pty: uint,
    named: []uint, reopen: u64): OsResult {
    if args.Len() == 0 || args[0].Len() == 0 {
        return FailWith(core.SysErr.InvalidPath)
    }
    if named.Len() > PassMax {
        return FailWith(core.SysErr.Other)
    }
    // MK_HANDLES=3,4,...: 11 bytes, then at most two digits and a comma per
    // handle (PassMax keeps each below 100), then the NUL.
    var strbytes := dir.Len() + 1 + 12 + named.Len() * 3
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
    envc := EnvCount(sp)
    var empty: [1]u8 = [0]
    var pathlen := GetEnvRaw(sp, "PATH", mut empty[0..0])
    if pathlen < 0 {
        pathlen = 0
    }
    env_off := PgArgv + (args.Len() + 1) * 8
    str_off := env_off + (envc + env.Len() + 2) * 8
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
    // The pages are fresh and zero, so every string copied below ends in
    // a NUL already, and argv and envp end in a NULL.
    unsafe "the call's pages: argv, envp, the strings, then PATH" {
        argv := raw.Offset(pg, PgArgv)
        var at := str_off
        for i, a in args {
            PxPut64(argv, i * 8, u64(raw.Addr(raw.Offset(pg, at))))
            raw.Copy(raw.Offset(pg, at), raw.StrData(a), a.Len())
            at += a.Len() + 1
        }
        envp := raw.Offset(pg, env_off)
        var k := 0
        if s.env_mode != core.IoEnvReplace && envc > 0 {
            envs := Envp(sp)
            for j in 0..envc {
                e := raw.FromAddr[u8](Word(envs, j))
                if !IsInheritEntry(e) && (s.env_mode == core.IoEnvInherit || !Overridden(e, env)) {
                    PxPut64(envp, k * 8, u64(raw.Addr(e)))
                    k += 1
                }
            }
        }
        if s.env_mode != core.IoEnvInherit {
            for i, e in env {
                if Shadowed(env, i) {
                    continue
                }
                PxPut64(envp, k * 8, u64(raw.Addr(raw.Offset(pg, at))))
                raw.Copy(raw.Offset(pg, at), raw.StrData(e), e.Len())
                at += e.Len() + 1
                k += 1
            }
        }
        if named.Len() > 0 {
            // MK_HANDLES lists the child's descriptors 3 to 3 + n - 1.
            PxPut64(envp, k * 8, u64(raw.Addr(raw.Offset(pg, at))))
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
        var p := NewPlan(pg, envp)
        if dir.Len() > 0 {
            raw.Copy(raw.Offset(pg, at), raw.StrData(dir), dir.Len())
            p.dir = raw.Offset(pg, at)
            at += dir.Len() + 1
        }
        if pathlen > 0 {
            _ = GetEnvRaw(sp, "PATH", raw.SliceMut(raw.Offset(pg, path_off), pathlen))
        }
        p.group = s.group
        p.join = s.stdio[2] == core.IoStdJoin
        f := FindProgram(args[0], raw.Offset(pg, PgCand), raw.Slice(raw.Offset(pg, path_off), pathlen))
        if f.err != core.SysErr.None {
            r = f
        } else {
            r = SpawnExIn(pg, mut p, s, src, pty, named)
        }
    }
    Release(pg, total)
    return r
}

// SpawnExIn makes the child's descriptors, spawns, and closes every
// descriptor it made for the child; on a failure the parent's pipe ends
// too. Each named handle is duplicated close-on-exec at 3 + n or above, so
// the child's dup2 onto 3 to 3 + n - 1 never meets another's source; the
// duplicates are the plan's array. A lent stream is duplicated at 3 or
// above, so the dup2s onto 0 to 2 never meet each other's sources.
SpawnExIn(pg: *u8, p: mut SpawnPlan, s: mut core.IoSpawnReq, src: [3]uint, pty: uint, named: []uint): OsResult {
    // own: the descriptors made for the child, each closed once below.
    var own: [3]int = [-1, -1, -1]
    var mine: [3]int = [-1, -1, -1]
    var r := Ok(0)
    var made := 0
    unsafe "fcntl passes no memory; the array is the plan's, in the call's pages" {
        for d in named {
            c := fcntl(Trunc[i32](d), PxFDupFdCloExec, Trunc[i32](3 + named.Len()))
            if c < 0 {
                r = Fail(Errno())
                break
            }
            PxPut32(p.named, made * 4, c)
            made += 1
        }
    }
    p.nnamed = made
    if r.err == core.SysErr.None && pty != 0 {
        // The peer's name, NUL-terminated in 128 bytes; the child opens it.
        unsafe "ioctl writes at most 128 bytes at PgTty in the call's pages" {
            tty := raw.Offset(pg, PgTty)
            if ioctl(Trunc[i32](pty), ChTiocptygname, tty) == -1 {
                r = Fail(Errno())
            } else {
                p.tty = tty
            }
        }
    } else if r.err == core.SysErr.None {
        for i in 0..3 {
            mode := s.stdio[i]
            if mode == core.IoStdHandle {
                unsafe "fcntl passes no memory" {
                    d := fcntl(Trunc[i32](src[i]), PxFDupFdCloExec, 3)
                    if d < 0 {
                        r = Fail(Errno())
                    } else {
                        own[i] = int(d)
                    }
                }
            } else if mode == core.IoStdNull || mode == core.IoStdPipe {
                r = ChildStream(i, mode, mut own[i], mut mine[i])
            }
            if r.err != core.SysErr.None {
                break
            }
            p.fds[i] = Trunc[i32](own[i])
        }
    }
    if r.err == core.SysErr.None {
        r = RunSpawn(*p)
        if r.err == core.SysErr.None && pty != 0 {
            z := PtyStart(pty, Trunc[i32](r.os))
            if z.err != core.SysErr.None {
                ReapKilled(Trunc[i32](r.os))
                r = z
            }
        }
    }
    for i in 0..3 {
        if own[i] >= 0 {
            _ = lc.CloseFd(Trunc[i32](own[i]))
        }
    }
    for i in 0..made {
        _ = lc.CloseFd(PxGet32(p.named, i * 4))
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
        s.pipes[i] = if mine[i] >= 0 { Trunc[uint](mine[i]) } else { 0 }
    }
    s.pid = r.os
    s.job = 0
    return Ok(r.value)
}

// PtyStart lets the child pid, spawned stopped on the master pty
// (POSIX_SPAWN_START_SUSPENDED, after its spawn opened the peer), run: it
// sets the size PtySizes holds for pty on the master, which the child's
// open made settable and zeroed, then sends SIGCONT. A size the master
// refuses is the answer, with the child killed first (its caller reaps it
// as a failed spawn's). O(1): two system calls; allocates nothing.
PtyStart(pty: uint, pid: i32): OsResult {
    var r := Ok(0)
    slot := SizeSlot(pty)
    if !raw.IsNull(slot) {
        var v: u32 = 0
        unsafe "slot is pty's word in the table" {
            v = (*slot).Load()
        }
        if v != 0 {
            r = WinSize(pty, int(v >> 16), int(v & 0xFFFF))
        }
    }
    unsafe "kill passes no memory" {
        if r.err != core.SysErr.None {
            _ = kill(pid, 9)
        }
        _ = kill(pid, ChSigCont)
    }
    return r
}

// ReapKilled waits for the child pid, which was sent SIGKILL, so a failed
// spawn leaves no zombie. Blocks only until the kill lands; EINTR is
// repeated.
ReapKilled(pid: i32) {
    var st: i32 = 0
    for {
        unsafe "waitpid writes one i32 status into st" {
            if waitpid(pid, raw.OfMut(mut st), 0) != -1 || Errno() != PxEintr {
                return
            }
        }
    }
}

// DarwinSignal is the Darwin number of Linux's signal sig (std.proc speaks
// Linux's numbers), from <sys/signal.h> as Go's zerrors_darwin_*.go lists
// it; 0 for one Darwin lacks (SIGSTKFLT 16, SIGPWR 30, the real-time
// signals). Linux 1 to 6, 8, 9, 11, 13 to 15 and 21, 22, 24 to 28 have
// Darwin's number.
DarwinSignal(sig: u32): i32 {
    return match sig {
        7 { 10 }
        10 { 30 }
        12 { 31 }
        16 { 0 }
        17 { 20 }
        18 { 19 }
        19 { 17 }
        20 { 18 }
        23 { 16 }
        29 { 23 }
        30 { 0 }
        31 { 12 }
        _ { if sig >= 1 && sig <= 31 { Trunc[i32](sig) } else { 0 } }
    }
}

/// ChildSignal sends sig (Linux's number, translated to Darwin's) to the
/// child whose pid is h, or with group to every process of its group
/// (kill(-pid)); the caller sends to a group only while its leader is
/// unreaped, so the id is still the group's. A pid names the child only
/// until it is reaped, so the caller signals only a child it has not
/// waited for. A child or group already gone (ESRCH) is success; a signal
/// Darwin lacks is Other. O(1): one system call; allocates nothing.
export ChildSignal(h: uint, group: bool, pid: i64, job: uint, sig: u32): OsResult {
    d := DarwinSignal(sig)
    if d == 0 || (group && pid <= 0) {
        return FailWith(core.SysErr.Other)
    }
    target: i32 = if group { -Trunc[i32](pid) } else { Trunc[i32](h) }
    unsafe "kill passes no memory" {
        if kill(target, d) != 0 {
            e := Errno()
            if e != PxEsrch {
                return Fail(e)
            }
        }
    }
    return Ok(0)
}

/// TryReap reaps the child whose pid is h if it has ended (waitpid with
/// WNOHANG): true when it was reaped now or cannot be waited for any more
/// (ECHILD: reaped before), false while it runs. O(1): one system call,
/// EINTR repeated; allocates nothing.
export TryReap(h: uint): bool {
    var st: i32 = 0
    for {
        unsafe "waitpid writes one i32 status into st" {
            r := waitpid(Trunc[i32](h), raw.OfMut(mut st), PxWNoHang)
            if r == -1 {
                if Errno() == PxEintr {
                    continue
                }
                return true
            }
            return r != 0
        }
    }
}

// PtySizeTable is the base of PtySizes' table (0 before the first PtyOpen
// maps it): MaxFds u32 words, one per descriptor, each a master's last
// size as cols << 16 | rows (0 none). Mapped once and never given back;
// untouched pages cost nothing.
struct PtySizeTable {
    base: raw.Atomic[u64]
}

var PtySizes: PtySizeTable = PtySizeTable(base: raw.Atomic[u64].New(0))

// SizeSlot is the master fd's word in PtySizes, mapping the table on the
// first call (a racing caller's mapping is given back); null for an fd
// past the table or when the mapping fails.
SizeSlot(fd: uint): *raw.Atomic[u32] {
    if fd >= Trunc[uint](MaxFds) {
        return raw.Null[raw.Atomic[u32]]()
    }
    var b := PtySizes.base.Load()
    if b == 0 {
        m := Map(raw.Null[u8](), MaxFds * 4, ProtRW, 0)
        if raw.IsNull(m) {
            return raw.Null[raw.Atomic[u32]]()
        }
        // Edge: the Cas publishes the fresh zero pages to every later
        // Load (sequentially consistent).
        if PtySizes.base.Cas(0, u64(raw.Addr(m))) {
            b = u64(raw.Addr(m))
        } else {
            Unmap(m, MaxFds * 4)
            b = PtySizes.base.Load()
        }
    }
    unsafe "fd < MaxFds, and the table holds MaxFds words" {
        return raw.PtrCast[u8, raw.Atomic[u32]](raw.Offset(raw.FromAddr[u8](Trunc[uint](b)), Trunc[int](fd) * 4))
    }
}

// WinSize writes cols and rows to fd's terminal (TIOCSWINSZ); Other when
// either is outside 1 to 65,535.
WinSize(fd: uint, cols: int, rows: int): OsResult {
    if cols < 1 || cols > 65535 || rows < 1 || rows > 65535 {
        return FailWith(core.SysErr.Other)
    }
    var ws: [4]u16 = [Trunc[u16](rows), Trunc[u16](cols), 0, 0]
    unsafe "ioctl reads a struct winsize: rows, columns, then two unused u16" {
        if ioctl(Trunc[i32](fd), ChTiocswinsz, raw.PtrCast[u16, u8](raw.Data(ws[..]))) == -1 {
            return Fail(Errno())
        }
    }
    return Ok(0)
}

/// PtyOpen opens a pseudo-terminal's master (/dev/ptmx, O_NOCTTY,
/// close-on-exec, blocking), grants and unlocks its peer and records its
/// size in PtySizes, which SpawnEx sets once a child opened the peer
/// (PtyStart): value the master, and
/// w.a and w.b it too (Windows' read and write ends). The master is
/// attached to the poller (PollAttach) for OpReady; one it refuses, or a
/// size table that cannot be mapped, is EMFILE's answer (24). O(1) system
/// calls; the master is closed on every failure.
export PtyOpen(cols: int, rows: int, w: mut core.IoWords): OsResult {
    if cols < 1 || cols > 65535 || rows < 1 || rows > 65535 {
        return FailWith(core.SysErr.Other)
    }
    unsafe "open's path is a NUL-terminated constant; the ioctls pass no memory" {
        fd := open(raw.StrData("/dev/ptmx\u{0}"), ORdWr | PxONoCtty | OCloExec)
        if fd < 0 {
            return Fail(Errno())
        }
        if ioctl(fd, ChTiocptygrant) == -1 || ioctl(fd, ChTiocptyunlk) == -1 {
            e := Errno()
            _ = lc.CloseFd(fd)
            return Fail(e)
        }
        slot := SizeSlot(Trunc[uint](fd))
        if raw.IsNull(slot) {
            _ = lc.CloseFd(fd)
            return Fail(24)
        }
        (*slot).Store((Trunc[u32](cols) << 16) | Trunc[u32](rows))
        // OpReady waits for the master on the poller.
        if !PollAttach(Trunc[uint](fd)) {
            _ = lc.CloseFd(fd)
            return Fail(24)
        }
        w.a = i64(fd)
        w.b = i64(fd)
        return Ok(i64(fd))
    }
}

/// PtyResize sets the size of the pseudo-terminal whose master is fd; its
/// session's foreground group gets SIGWINCH from the kernel. The size is
/// also recorded for the next spawn on it, so a master whose peer was never
/// opened (ENOTTY) takes it then. Other for a size outside 1 to 65,535.
/// O(1): one system call; allocates nothing.
export PtyResize(fd: uint, cols: int, rows: int): OsResult {
    if cols < 1 || cols > 65535 || rows < 1 || rows > 65535 {
        return FailWith(core.SysErr.Other)
    }
    slot := SizeSlot(fd)
    if !raw.IsNull(slot) {
        unsafe "slot is fd's word in the table" {
            (*slot).Store((Trunc[u32](cols) << 16) | Trunc[u32](rows))
        }
    }
    r := WinSize(fd, cols, rows)
    if r.err != core.SysErr.None && r.os == ChEnotty && !raw.IsNull(slot) {
        return Ok(0)
    }
    return r
}

/// PtyClose is Windows' ClosePseudoConsole: macOS's pty is its master
/// descriptor alone, closed as a handle. O(1).
export PtyClose(hpc: uint) {
}

/// GroupEnd is Windows' end of a job: on macOS the poller kills the group
/// in the leader's wait. O(1).
export GroupEnd(job: uint): OsResult {
    return Ok(0)
}

/// GroupClose is Windows' close of a job: nothing on macOS. O(1).
export GroupClose(job: uint) {
}

/// TermOpen opens this process's controlling terminal (/dev/tty, O_NOCTTY,
/// close-on-exec): value its descriptor, which is both the input and the
/// output, attached to the poller (PollAttach) for OpReady, EMFILE's
/// answer (24) when it refuses; none (ENXIO) is NotFound. O(1) system
/// calls.
export TermOpen(): OsResult {
    unsafe "open's path is a NUL-terminated constant" {
        fd := open(raw.StrData("/dev/tty\u{0}"), ORdWr | PxONoCtty | OCloExec)
        if fd < 0 {
            e := Errno()
            if e == ChEnxio {
                return OsResult(value: 0, err: core.SysErr.NotFound, os: ChEnxio)
            }
            return Fail(e)
        }
        // OpReady waits for the terminal on the poller.
        if !PollAttach(Trunc[uint](fd)) {
            _ = lc.CloseFd(fd)
            return Fail(24)
        }
        return OsResult(value: i64(fd), err: core.SysErr.None, os: i64(fd))
    }
}

/// TermMode reads the terminal h's mode into old (TIOCGETA) and sets mode:
/// core.IoTermSaved sets saved (as an earlier call's old gave it, laid over
/// the terminal's current termios), IoTermRaw raw mode (Go's x/term
/// MakeRaw, which is cfmakeraw), and IoTermNoEcho echo off with line
/// editing and signals kept (x/term's ReadPassword); IoTermGet sets nothing
/// (TIOCGETA alone). out is unused (the same descriptor). value the bytes
/// of old (64: Darwin's struct termios up to c_ospeed, ChTermSaved). A short
/// old or saved, or an unknown mode: Other with nothing set. O(1): two
/// system calls, one for IoTermGet; allocates nothing.
export TermMode(h: uint, out: uint, mode: i64, saved: []u8, old: mut []u8): OsResult {
    if old.Len() < ChTermSaved || mode < 0 || mode > core.IoTermGet ||
        (mode == core.IoTermSaved && saved.Len() < ChTermSaved) {
        return FailWith(core.SysErr.Other)
    }
    var t: [9]u64 = [0; 9]
    unsafe "the ioctls read and write t, a 72-byte struct termios" {
        q := raw.PtrCast[u64, u8](raw.DataMut(mut t[..]))
        if ioctl(Trunc[i32](h), ChTiocgeta, q) == -1 {
            return Fail(Errno())
        }
        for i in 0..ChTermSaved {
            old[i] = raw.Read(raw.Offset(q, i))
        }
        if mode == core.IoTermGet {
            return Ok(i64(ChTermSaved))
        }
        if mode == core.IoTermSaved {
            for i in 0..ChTermSaved {
                raw.Write(raw.Offset(q, i), saved[i])
            }
        } else if mode == core.IoTermRaw {
            // IGNBRK BRKINT PARMRK ISTRIP INLCR IGNCR ICRNL IXON; OPOST;
            // ECHO ECHONL ICANON ISIG IEXTEN; CSIZE PARENB off, CS8 on;
            // VMIN (byte 48) 1, VTIME (byte 49) 0.
            t[0] &= ~u64(0x1 | 0x2 | 0x8 | 0x20 | 0x40 | 0x80 | 0x100 | 0x200)
            t[1] &= ~u64(0x1)
            t[2] = (t[2] & ~u64(0x300 | 0x1000)) | 0x300
            t[3] &= ~u64(0x8 | 0x10 | 0x100 | 0x80 | 0x400)
            t[6] = (t[6] & ~u64(0xFFFF)) | 1
        } else {
            // ECHO off; ICANON and ISIG on; ICRNL on.
            t[3] = (t[3] & ~u64(0x8)) | 0x100 | 0x80
            t[0] |= 0x100
        }
        for {
            if ioctl(Trunc[i32](h), ChTiocseta, q) == 0 {
                break
            }
            e := Errno()
            if e != PxEintr {
                return Fail(e)
            }
        }
    }
    return Ok(i64(ChTermSaved))
}

/// TermFatalInstall makes Interrupt, Terminate and Hangup restore the
/// terminal-restore registry's modes before their default action, as
/// signal.mk's SignalFatalInstall says (restore is std.rt's
/// TermRestoreAll). Called once, at the registry's first record. O(1)
/// system calls; allocates nothing.
export TermFatalInstall(restore: fn()) {
    SignalFatalInstall(restore)
}

/// RaiseFatal sends Interrupt, Terminate or Hangup (sig 0 to 2) to this
/// process whatever handles it, for the registry's tests (signal.mk's
/// SignalRaiseFatal); false for another sig or when the call fails.
/// O(1): one system call; allocates nothing.
export RaiseFatal(sig: u32): bool {
    return SignalRaiseFatal(sig)
}

// TermAvail is how many bytes a read of the terminal h finds now: for a
// pty's master (the one side TIOCPTYGNAME answers on) its peer's output
// queue, TIOCOUTQ, since Darwin's FIONREAD on a master counts the peer's
// input; for any other terminal FIONREAD's count. Minus the errno when
// the ioctl fails, but 0 for a master no child has opened yet. O(1): two system calls; allocates nothing.
TermAvail(h: uint): i64 {
    var name: [16]u64 = [0; 16]
    var avail: i32 = 0
    unsafe "TIOCPTYGNAME writes at most 128 bytes into name; FIONREAD and TIOCOUTQ write one int" {
        req := if ioctl(Trunc[i32](h), ChTiocptygname, raw.PtrCast[u64, u8](raw.DataMut(mut name[..]))) == 0 {
            ChTiocoutq
        } else {
            ChFionread
        }
        if ioctl(Trunc[i32](h), req, raw.PtrCast[i32, u8](raw.OfMut(mut avail))) == -1 {
            e := Errno()
            // A master whose peer was never opened refuses TIOCOUTQ
            // (ENOTTY), and holds nothing.
            return if req == ChTiocoutq && e == ChEnotty { 0 } else { -e }
        }
    }
    return i64(avail)
}

// TermReadyNow says whether a read of the terminal h would end now,
// without waiting: 1 when it holds bytes or its other side is gone, 0
// when the read would wait, or minus an errno. Darwin's poll answers
// POLLNVAL for every device, so the poller's readiness try asks this for
// a terminal: a kqueue of its own takes h's EVFILT_READ with a zero
// timeout (ready, or EV_EOF once the other side closed); a terminal
// kqueue refuses is answered by TermAvail alone. O(1): four system calls;
// allocates nothing.
TermReadyNow(h: uint): i64 {
    var ev: [8]u64 = [0; 8]
    var ts: [2]i64 = [0, 0]
    unsafe "ev holds two struct kevent records; ts is a zero struct timespec" {
        kq := kqueue()
        if kq < 0 {
            return -Errno()
        }
        e := raw.PtrCast[u64, u8](raw.DataMut(mut ev[..]))
        out := raw.Offset(e, ChEventBytes)
        ChKevent(e, u64(h), ChEvfiltRead, ChEvAdd, 0, 0)
        n := kevent(kq, e, 1, out, 1, raw.DataMut(mut ts[..]))
        x := Errno()
        _ = lc.CloseFd(kq)
        if n < 0 {
            return -x
        }
        if n == 0 {
            return 0
        }
        // flags at byte 10 of the event: EV_ERROR is a refused filter,
        // whose errno is the data word.
        flags := (ev[5] >> 16) & 0xFFFF
        if (flags & ChEvError) == 0 {
            return 1
        }
    }
    a := TermAvail(h)
    if a > 0 {
        return 1
    }
    return a
}

/// TermRecords reads the bytes the terminal h holds now, without waiting
/// (TermAvail, then one read of at most that many: Darwin's poll does not
/// take a terminal): value the bytes read, 0 when none is there. Windows
/// answers console records instead.
/// O(1): three system calls; allocates nothing.
export TermRecords(h: uint, out: mut []u8): OsResult {
    if out.Len() == 0 {
        return Ok(0)
    }
    a := TermAvail(h)
    if a < 0 {
        return Fail(-a)
    }
    avail := Trunc[i32](a)
    unsafe "read fills at most n bytes of out, the caller's" {
        if avail <= 0 {
            return Ok(0)
        }
        n := if int(avail) < out.Len() { int(avail) } else { out.Len() }
        for {
            r := read(Trunc[i32](h), raw.DataMut(out), Trunc[uint](n))
            if r >= 0 {
                return Ok(i64(r))
            }
            e := Errno()
            if e == PxEintr {
                continue
            }
            // A terminal whose other side is gone reads EIO: the end.
            if e == ChEio {
                return Ok(0)
            }
            return Fail(e)
        }
    }
}

// The reaper set behind std.rt's detached children: a kqueue on which
// each child is registered once (EVFILT_PROC, NOTE_EXIT, EV_ONESHOT) with
// the caller's tag as its udata, so a wait on the set returns only ended
// children. A child that ended before it was added (the registration
// answers ESRCH), or one reported but not yet reapable, is put on the set
// as a triggered EVFILT_USER event with the same tag, so the next wait
// returns it too.

// ChKevent writes one struct kevent at e.
ChKevent(e: *u8, ident: u64, filter: u64, flags: u64, fflags: u64, udata: u64) {
    unsafe "e holds one 32-byte struct kevent" {
        w := raw.PtrCast[u8, u64](e)
        raw.Write(w, ident)
        raw.Write(raw.Offset(w, 1), filter | (flags << 16) | (fflags << 32))
        raw.Write(raw.Offset(w, 2), 0)
        raw.Write(raw.Offset(w, 3), udata)
    }
}

// ChChange applies one change to the kqueue set, EINTR repeated: 0 or the
// errno (with no event list, a change's error is kevent's).
ChChange(set: uint, e: *u8): i64 {
    for {
        unsafe "kevent reads the one change at e" {
            if kevent(Trunc[i32](set), e, 1, raw.Null[u8](), 0, raw.Null[i64]()) != -1 {
                return 0
            }
        }
        x := Errno()
        if x != PxEintr {
            return x
        }
    }
}

/// ReapSetOpen makes an empty reaper set: value its descriptor (a kqueue,
/// close-on-exec; a kqueue is not inherited by a child anyway), kept until
/// exit. Errors: the OS's (kqueue). O(1): two system calls; allocates
/// nothing in the process.
export ReapSetOpen(): OsResult {
    unsafe "kqueue and fcntl pass no memory" {
        kq := kqueue()
        if kq < 0 {
            return Fail(Errno())
        }
        _ = fcntl(kq, PxFSetFd, PxFdCloExec)
        return Ok(i64(kq))
    }
}

/// ReapSetAdd registers the child whose pid is h in set for one report of
/// its end, with tag as the report's data. A child that has already ended
/// is reported by the next wait. again re-arms an h already reported:
/// NOTE_EXIT was posted, so the child is exiting and is reported by the
/// next wait again (the triggered EVFILT_USER event) until its reap
/// succeeds, since an EVFILT_PROC added after its NOTE_EXIT would never
/// fire.
/// Errors: the OS's (kevent). O(1): one or two system calls; allocates
/// nothing in the process.
export ReapSetAdd(set: uint, h: uint, tag: u64, again: bool): OsResult {
    var e: [4]u64 = [0; 4]
    unsafe "e holds one struct kevent in this frame" {
        ep := raw.PtrCast[u64, u8](raw.DataMut(mut e[..]))
        if !again {
            ChKevent(ep, u64(h), ChEvfiltProc, ChEvAdd | ChEvOneShot, ChNoteExit, tag)
            x := ChChange(set, ep)
            if x == 0 {
                return Ok(0)
            }
            if x != PxEsrch {
                return Fail(x)
            }
        }
        ChKevent(ep, u64(h), ChEvfiltUser, ChEvAdd | ChEvOneShot, ChNoteTrigger, tag)
        y := ChChange(set, ep)
        return if y == 0 { Ok(0) } else { Fail(y) }
    }
}

/// ReapSetWait waits, with no timeout, until children registered in set
/// have ended, and writes their tags to tags: value how many (at least 1,
/// at most tags.Len() and 32, a 1 KiB event buffer's). An interrupted wait
/// is waited again.
/// Errors: the OS's (kevent); EINVAL for an empty tags. O(1) system
/// calls besides the wait; allocates nothing.
export ReapSetWait(set: uint, tags: mut []u64): OsResult {
    var events: [128]u64 = [0; 128]
    var max := 1024 / ChEventBytes
    if max > tags.Len() {
        max = tags.Len()
    }
    if max < 1 {
        return Fail(22)
    }
    unsafe "kevent writes at most max events into events" {
        evp := raw.PtrCast[u64, u8](raw.DataMut(mut events[..]))
        for {
            r := kevent(Trunc[i32](set), raw.Null[u8](), 0, evp, Trunc[i32](max), raw.Null[i64]())
            if r == -1 {
                e := Errno()
                if e == PxEintr {
                    continue
                }
                return Fail(e)
            }
            if r == 0 {
                continue
            }
            for k in 0..int(r) {
                tags[k] = events[k * 4 + 3]
            }
            return Ok(i64(r))
        }
    }
}
