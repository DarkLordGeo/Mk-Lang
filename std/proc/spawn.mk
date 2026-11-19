// Why: starting a process is where the targets differ most (search,
// quoting, handle inheritance, fork safety); this file gives one checked
// request and one result shape over both.
//
// What: proc.Command, DefaultCommand, Env, Stdio, Pass and proc.Spawn, with
// SpawnCheck (the runtime's answers as proc.Error) and Mode (a stream
// disposition into the runtime's record).
//
// How: std.rt's Spawn does the rules of the target: a name with no
// separator searched in PATH only (with PATHEXT on Windows), never in the
// current directory; arguments a list end to end (execve's argv; on Windows
// one command line quoted by CommandLineToArgvW's rules, and a .bat or .cmd
// target run by cmd.exe with every argument quoted as Rust's Command does,
// or refused); no implicit shell; every handle std opens close-on-exec, and
// the Linux child, made by vfork with CLONE_PIDFD (never fork while carriers
// run), closing the rest before execve. This file checks the request, maps
// it to the runtime's record, and wraps what comes back as the caller's
// linear values.

import std.core
import std.fs
import std.net
import std.rt

/// The environment a child gets.
export enum Env {
    /// This process's environment.
    Inherit
    /// Exactly vars, each NAME=value. Nothing is added: on Windows, pass
    /// SYSTEMROOT when the child uses Winsock or CryptoAPI, which need it.
    Replace(vars: []str)
    /// This process's environment with vars (each NAME=value) applied: each
    /// replaces the variable of its name, or is added. On Windows names
    /// compare ignoring case.
    Extend(vars: []str)
}

/// Where one of a child's standard streams goes.
export enum Stdio {
    /// The parent's own stream.
    Inherit
    /// The null device.
    Null
    /// A new pipe, whose parent end Spawn returns. The child's end is a
    /// pipe on both targets; on Linux the child may open it again by name
    /// (/dev/stdin, /dev/stdout, /proc/self/fd/N).
    Pipe
    /// A duplicate of an open file's handle. The child reads or writes at
    /// the OS's position of the file, which std.fs's own calls never move:
    /// the start for a file std.fs opened, and the end at each write for
    /// one opened with Append.
    File(f: &fs.File)
    /// A duplicate of the read end of a pipe made by proc.Pipe; stdin only.
    Reader(r: &PipeReader)
    /// A duplicate of the write end of a pipe made by proc.Pipe; stdout or
    /// stderr only.
    Writer(w: &PipeWriter)
    /// stderr only: the same stream as stdout.
    JoinStdout
}

/// A handle a Command lends its child beside the three standard streams:
/// Spawn duplicates it into the child, and the parent keeps its own. The
/// child takes it with proc.Inherited, as a net.Passed of the kind the OS
/// says it is; a pipe end arrives as a File. Plain.
export open enum Pass {
    /// An open file's handle.
    File(f: &fs.File)
    /// A connected stream, TCP or Unix-domain. Unsupported on Windows, where
    /// the socket's I/O would still complete in this process.
    Stream(s: &net.Stream)
    /// A listener; the child accepts on it beside this process. A
    /// Unix-domain listener is Unsupported on Windows.
    Listener(l: &net.Listener)
    /// The read end of a pipe made by proc.Pipe.
    Reader(r: &PipeReader)
    /// The write end of a pipe made by proc.Pipe.
    Writer(w: &PipeWriter)
}

/// How Spawn starts a child: the program and its arguments, environment,
/// directory, standard streams and other handles. Start from
/// DefaultCommand and assign the fields to change. Plain: it holds
/// references and slices, which Spawn reads and never keeps.
export struct Command {
    /// The program, args[0], and its arguments. A name with no separator is
    /// searched in this process's PATH only, never in the current
    /// directory (with PATHEXT on Windows).
    export args: []str
    /// The child's environment; a name given twice takes the later entry.
    export env: Env
    /// The child's working directory; none (or empty) keeps this process's.
    export dir: ?str
    /// Where stdin, stdout and stderr go.
    export stdin: Stdio
    /// Where the child's standard output goes.
    export stdout: Stdio
    /// Where the child's standard error goes.
    export stderr: Stdio
    /// Start the child as the leader of its own group: a process group on
    /// Linux, a job object without kill-on-close on Windows. Signal and
    /// Kill reach every process still in the group, and Wait kills what
    /// remains of it when the leader ends.
    export group: bool
    /// A pseudo-terminal that becomes the child's three standard streams
    /// and its controlling terminal; the three streams must then be
    /// Inherit. Provisional.
    export terminal: ?&Pty
    /// The handles the child gets beside its three streams, at most 64, in
    /// order; it gets no other handle of this process. The child takes them
    /// with proc.Inherited.
    export handles: []Pass
    sealed: u8
}

/// No arguments, the environment inherited, this process's directory, the
/// three streams inherited, no group, no terminal, no other handle.
export const DefaultCommand: Command = Command(args: [], env: Env.Inherit, dir: none, stdin: Stdio.Inherit,
    stdout: Stdio.Inherit, stderr: Stdio.Inherit, group: false, terminal: none, handles: [], sealed: 0)

// PassMax is the most handles a Command names (sys.PassMax).
const PassMax: int = 64

// Named writes the runtime handle of each of c's handles to out: Os(0) for
// more than PassMax, a handle with no OS handle behind it (a simulated
// Io's, or a file with no runtime handle), or a pipe end another Spawn
// returned (as Mode refuses one); Unsupported for a Stream, or a Unix-domain
// Listener, on Windows.
// O(handles); allocates nothing.
Named(hs: []Pass, out: mut [64]uint): Error!int {
    if hs.Len() > PassMax {
        fail Os(code: 0)
    }
    for i, p in hs {
        h := match p {
            .File(f) { f.IoHandle() }
            .Stream(s) {
                if Target.os == core.Os.Windows {
                    fail Unsupported
                }
                s.IoHandle()
            }
            .Listener(l) {
                // A Unix-domain listener (port 0): Windows' PassKind cannot
                // name an AF_UNIX socket's family for the child.
                if Target.os == core.Os.Windows && l.Port() == 0 {
                    fail Unsupported
                }
                l.IoHandle()
            }
            .Reader(r) { if r.lend { r.h } else { 0 } }
            .Writer(w) { if w.lend { w.h } else { 0 } }
            _ { 0 }
        }
        if !rt.PassNative(h) {
            fail Os(code: 0)
        }
        out[i] = h
    }
    return hs.Len()
}

// NamesHandles: v is an environment entry MK_HANDLES=... (the name's case
// ignored on Windows), which only Spawn writes.
NamesHandles(v: str): bool {
    n := "MK_HANDLES="
    b := v.AsBytes()
    if b.Len() < n.Len() {
        return false
    }
    for i, c in n.AsBytes() {
        var x := b[i]
        if Target.os == core.Os.Windows && x >= 97 && x <= 122 {
            x -= 32
        }
        if x != c {
            return false
        }
    }
    return true
}

// HasNul: s holds a NUL byte.
HasNul(s: str): bool {
    for b in s.AsBytes() {
        if b == 0 {
            return true
        }
    }
    return false
}

// SpawnCheck is r's value, or the proc.Error a call that makes runtime
// records (rt.Spawn, rt.PipeOpen, rt.PtyOpen) answers: Check's map, but
// Other with no OS code is OutOfMemory (for Spawn, Pipe and Pty.Open),
// since each answers that only when the process heap or a page
// map cannot give what the call needs. Their other Other answers with code
// 0 are requests std.proc never sends: for a spawn an unknown mode or env
// mode, or a lent stream with no handle (Mode refuses it); for PtyOpen a
// size outside 1 to 32,767 (SizeOk refuses it first). O(1).
SpawnCheck(r: core.IoResult): Error!i64 {
    if r.err == core.SysErr.Other && r.os == 0 {
        fail OutOfMemory
    }
    return try Check(r)
}

// DirFailure is the error a failed start answers: NotFound when dir is
// given and names no directory (missing, or not a directory), so both
// targets agree (Linux's chdir in the child gives ENOENT or ENOTDIR,
// Windows' CreateProcessW gives ERROR_DIRECTORY, 267); else e. One stat,
// on failure only; Canceled, Unsupported and OutOfMemory pass through
// unchanged.
DirFailure(io: Io, dir: str, e: Error): Error {
    if dir.Len() == 0 {
        return e
    }
    match e {
        Canceled { return e }
        Unsupported { return e }
        OutOfMemory { return e }
        _ {}
    }
    info := fs.Stat(io, dir) else f {
        return match f {
            fs.NotFound { NotFound }
            fs.NotDirectory { NotFound }
            _ { e }
        }
    }
    return if info.kind == fs.Kind.Dir { e } else { NotFound }
}

// Mode sets stream i (0 stdin, 1 stdout, 2 stderr) of s from d: its
// IoStdio mode and, for a lent handle, the handle. A disposition stream i
// cannot take (JoinStdout but on stderr, a read end but on stdin, a write
// end on stdin, an end Spawn made, which is on this process's poller) is
// Os(0). A lent file is given by its runtime handle (std.fs's core.IoHandle
// seam), which the runtime duplicates into the child.
Mode(d: &Stdio, i: int, s: mut core.IoSpawnReq): Error!void {
    match *d {
        .Inherit { s.stdio[i] = core.IoStdInherit }
        .Null { s.stdio[i] = core.IoStdNull }
        .Pipe { s.stdio[i] = core.IoStdPipe }
        .File(f) {
            // A file with no runtime handle is refused here, so the
            // runtime's Other for a missing handle never reaches
            // SpawnCheck, whose Other with code 0 means memory.
            h := f.IoHandle()
            if h == 0 {
                fail Os(code: 0)
            }
            s.stdio[i] = core.IoStdHandle
            s.src[i] = h
        }
        .Reader(r) {
            if i != 0 || !r.lend {
                fail Os(code: 0)
            }
            s.stdio[i] = core.IoStdHandle
            s.src[i] = r.h
        }
        .Writer(w) {
            if i == 0 || !w.lend {
                fail Os(code: 0)
            }
            s.stdio[i] = core.IoStdHandle
            s.src[i] = w.h
        }
        .JoinStdout {
            if i != 2 {
                fail Os(code: 0)
            }
            s.stdio[i] = core.IoStdJoin
        }
    }
}

/// Starts the program c.args[0] with the arguments c.args, set up as c
/// says. Returns the Child, and the parent's end of each standard stream
/// whose Stdio is Pipe (none for the others); all are the caller's linear
/// values. No shell runs: the arguments reach the program as a list.
///
/// A file or pipe end lent through c is duplicated into the child, and the
/// parent keeps its own: close a lent write end before reading the read
/// end, or the read never sees the end of input. The child gets its three
/// standard streams and c.handles, and no other handle of this process. On
/// Linux the handles are its descriptors 3, 4 and on; on Windows they are
/// inheritable duplicates, a disk file becoming a new synchronous file
/// object at position 0. A socket among them is shared from then on: closing either
/// process's copy releases only that copy, with no shutdown, and the peer
/// sees the end when the last copy closes.
///
/// A relative path in args[0] that contains a separator is taken in c.dir
/// when c.dir is given, on both targets.
///
/// Errors:
/// - NotFound when args or args[0] is empty, no program is found, or c.dir
///   names no directory.
/// - Denied when the OS refuses to start the program.
/// - Os(0) for a NUL in an argument (args[0] included), an environment
///   entry or the directory; an environment entry MK_HANDLES=, which only
///   Spawn writes; a Stdio its stream cannot take; a stream that is not
///   Inherit beside a terminal; more than 64 handles; a handle with no OS
///   handle behind it (a simulated Io's); a pipe end that another Spawn
///   returned, as a Stdio or a handle (lend the ends of a proc.Pipe
///   instead); a .bat or .cmd program with an argument cmd.exe cannot be
///   given safely (a CR or LF).
/// - Unsupported under a Recorder or Replayer, and on Windows for a
///   Pass.Stream or a Unix-domain Pass.Listener.
/// - Canceled when the task's scope is cancelled before the child starts,
///   also while the start waits for one of the 64 threads that run
///   blocking calls (a waiting Read of a proc.Pipe end holds none of
///   them); a deadline cuts that wait too.
/// - OutOfMemory when the memory for the call cannot be had, or the
///   process heap cannot hold the record of a started child, which is then
///   killed and waited for.
/// - Any other failure is the OS's. On Linux that includes the error of
///   opening a lent proc.Pipe end again through /proc/self/fd (ENOENT
///   after a chroot, EACCES after a uid change, EMFILE).
///
/// Apart from that OutOfMemory, a child that has started is always
/// returned: a cancel does not separate it from its caller.
///
/// O(bytes of args, the environment and PATH), plus on Windows one stat
/// per PATH entry and extension and one read of the current directory for
/// a drive-relative args[0] beside a dir with no drive, and one stat of
/// dir after a failure when dir is given. It allocates nothing in a on
/// Linux. On Windows, for a name searched in PATH or a relative path taken
/// in dir, it takes a sub-arena of a (its first chunk from a's heap),
/// freed before Spawn returns, holding
/// the argument list and either one candidate path (the longest PATH
/// entry, the name and the longest PATHEXT extension, plus 2 bytes) or dir
/// and args[0] plus 1 byte and, for a drive-relative args[0], the current
/// directory's path. The records of the child and its pipe ends come from
/// the process heap.
///
/// Under a simulated Io the child is simulated: it runs no program, copies
/// its stdin to its stdout, and exits with code 0 when its input ends. The
/// arguments, environment, directory and handles are ignored, except that
/// a program named "missing" is NotFound.
export Spawn(a: &Arena, io: Io, c: &Command): Error!(child: Child, stdin: ?PipeWriter, stdout: ?PipeReader, stderr: ?PipeReader) {
    if c.args.Len() == 0 || c.args[0].Len() == 0 {
        fail NotFound
    }
    // A NUL in args[0] is refused here, before Windows' search, whose
    // stat would read it as "no such program".
    if HasNul(c.args[0]) {
        fail Os(code: 0)
    }
    var s := core.IoSpawnReq(stdio: [core.IoStdInherit; 3], src: [0; 3], env_mode: core.IoEnvInherit, group: c.group, pty: 0,
        pipes: [0; 3], pid: 0, job: 0)
    try Mode(&c.stdin, 0, mut s)
    try Mode(&c.stdout, 1, mut s)
    try Mode(&c.stderr, 2, mut s)
    if t := c.terminal {
        for i in 0..3 {
            if s.stdio[i] != core.IoStdInherit {
                fail Os(code: 0)
            }
        }
        s.pty = t.p.term
    }
    var vars: []str = c.args[0..0]
    match c.env {
        .Inherit {}
        .Replace(vars: v) {
            s.env_mode = core.IoEnvReplace
            vars = v
        }
        .Extend(vars: v) {
            s.env_mode = core.IoEnvExtend
            vars = v
        }
    }
    for v in vars {
        if NamesHandles(v) {
            fail Os(code: 0)
        }
    }
    var hs: [64]uint = [0; 64]
    nh := try Named(c.handles, mut hs)
    named := hs[0..nh]
    dir := c.dir else { "" }
    // An Io that is not the OS's (a Sim, a Recorder or Replayer) is given
    // the request as it stands, after the checks that need no call: a Sim
    // starts its simulated child (a program named "missing" is NotFound), a
    // tape answers Unsupported. Neither is searched, joined or stat'd, so
    // Windows' PATH search never answers NotFound from a Sim's disk.
    if !core.IsNative(io) {
        v := try SpawnCheck(rt.SpawnNamed(io, c.args, vars, dir, mut s, named))
        return Started(v, &s, c.group)
    }
    // On Windows std.proc searches PATH itself (search.mk), so the
    // current directory is never searched, and takes a relative path in
    // dir, as Linux's chdir before execve does; on Linux the runtime
    // searches.
    win := Target.os == core.Os.Windows
    r := if win && !HasSeparator(c.args[0]) {
        try SpawnSearched(a, io, c.args, vars, dir, mut s, named)
    } else if win && dir.Len() > 0 && !IsAbsolute(c.args[0].AsBytes()) {
        try SpawnJoined(a, io, c.args, vars, dir, mut s, named)
    } else {
        rt.SpawnNamed(io, c.args, vars, dir, mut s, named)
    }
    v := SpawnCheck(r) else e {
        fail DirFailure(io, dir, e)
    }
    return Started(v, &s, c.group)
}

// Started is Spawn's answer for the child whose runtime handle is v, as s
// and group describe it. O(1); allocates nothing.
Started(v: i64, s: &core.IoSpawnReq, group: bool): (child: Child, stdin: ?PipeWriter, stdout: ?PipeReader, stderr: ?PipeReader) {
    child := Child(h: Trunc[uint](v), ospid: s.pid, job: s.job, group: group)
    stdin: ?PipeWriter = if s.pipes[0] != 0 { some(PipeWriter(h: s.pipes[0], lend: false, writes: Mutex[u8].New(0))) } else { none }
    stdout: ?PipeReader = if s.pipes[1] != 0 { some(PipeReader(h: s.pipes[1], lend: false)) } else { none }
    stderr: ?PipeReader = if s.pipes[2] != 0 { some(PipeReader(h: s.pipes[2], lend: false)) } else { none }
    return (child: child, stdin: stdin, stdout: stdout, stderr: stderr)
}
