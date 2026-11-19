// Why: Main receives a System, the program's view of its process: arguments,
// environment, the standard streams and the root allocators. The runtime
// builds it before Main, so its layout and constructors are shared here.
//
// What: BufState, EnvVar, Process and System, with System.Stdout, Stderr,
// Print, PrintErr, Stdin and Env, NewSystem, and the exit-time flushes
// FlushStdCode and TryFlushStd.
//
// How: Process holds the three process streams' BufStates (8 KiB for output,
// 64 KiB for stdin) and the environment; the stream Writers and Readers are
// table views over them (io_stream.mk), so taking one costs nothing. Env
// compares names as the target OS does (case-blind on Windows).

import std.raw

/// The buffer and lock of one process stream (stdout, stderr or stdin).
/// Every Writer from System.Stdout shares the one for stdout.
export struct BufState {
    export lock: raw.Atomic[u32]
    export h: uint
    export buf: *u8
    export len: int
    export cap: int
    export start: int
}

/// One environment variable, as the runtime read it when the process
/// started.
export struct EnvVar {
    export name: str
    export value: str
}

/// The process state the runtime builds before Main: the buffers of the
/// three standard streams and the environment.
export struct Process {
    export stdout: BufState
    export stderr: BufState
    export stdin: BufState
    export env: []EnvVar
}

/// What Main is given: the command-line arguments (args[0] is the
/// program's path, as it was started), the Io, the process arena and the heap. It also gives
/// the standard streams (Stdout, Stderr, Stdin, Print) and the environment
/// (Env).
export struct System {
    /// The command-line arguments; args[0] is the program's path, as it was started.
    export args: []str
    /// The Io that Main runs on.
    export io: Io
    /// The process arena.
    export arena: &Arena
    /// The process heap.
    export heap: &Heap
    proc: *Process
}

// ProcStream is the process stream i: 0 stdout, 1 stderr, 2 stdin.
ProcStream(p: *Process, i: int): *BufState {
    unsafe "p is the runtime's live Process" {
        if i == 0 {
            return raw.Of(&(*p).stdout)
        }
        if i == 1 {
            return raw.Of(&(*p).stderr)
        }
        return raw.Of(&(*p).stdin)
    }
}

/// Returns a Writer to standard output. Every Writer from Stdout shares
/// the one 8 KiB process buffer, so taking one per call costs nothing.
/// The buffer is flushed when Main returns or proc.Exit runs, and Flush
/// forces it out earlier. If that last flush fails, the process writes
/// `error: WriteError on stdout` to standard error, when that still works,
/// and exits with status 1 where it would have exited 0. A write to it is
/// not a cancellation point: it goes on under a cancelled scope. To stop
/// writing, check the scope's cancellation with another Io call. O(1); it
/// allocates nothing.
export System.Stdout(&self): Writer {
    return WriterOver(self.io, &StreamWriteTable, ProcCtx(self.proc, 0))
}

/// Returns a Writer to standard error, over its shared process buffer. It
/// is flushed as standard output is, when Main returns or proc.Exit runs;
/// if that last flush fails, the process exits with status 1 where it would
/// have exited 0. A write to it is not a cancellation point: it goes on
/// under a cancelled scope. To stop writing, check the scope's cancellation
/// with another Io call. O(1); it allocates nothing.
export System.Stderr(&self): Writer {
    return WriterOver(self.io, &StreamWriteTable, ProcCtx(self.proc, 1))
}

/// Formats args by f and writes the text to standard output, as Print on
/// Stdout() does. It writes to the one process buffer, so a call costs no
/// more than one Print. Fails with WriteError when the stream fails, or
/// with the error of a user Format. O(output length) plus the Format calls;
/// it allocates nothing.
///
/// f is a const argument: the compiler checks each call's holes against
/// args at that call, and a mismatch is E0706. With no hole, args may be
/// left out: sys.Print("done\n").
export System.Print[A: Fields[Format] + Plain](&self, const f: str, args: A): WriteError!void {
    var w := self.Stdout()
    try Print(mut w, f, args)
}

/// Formats args by f and writes the text to standard error, as Print on
/// Stderr() does. Fails with WriteError when the stream fails, or with the
/// error of a user Format. O(output length) plus the Format calls; it
/// allocates nothing. With no hole, args may be left out, as for Print.
export System.PrintErr[A: Fields[Format] + Plain](&self, const f: str, args: A): WriteError!void {
    var w := self.Stderr()
    try Print(mut w, f, args)
}

/// Returns a Reader from standard input, over the shared 64 KiB process
/// buffer. A read that must wait for input flushes standard output and
/// standard error first, so a prompt shows before the program waits. A
/// line read from it with Reader.Line may be up to 64 KiB. O(1); it
/// allocates nothing.
export System.Stdin(&self): Reader {
    return ReaderOver(self.io, &StreamReadTable, ProcCtx(self.proc, 3))
}

/// Returns the value of the environment variable name, or none when it is
/// not set. The environment is the one read when the process started; when
/// a name appears twice, the first wins. O(total bytes of the names); it
/// allocates nothing.
///
/// On Windows, names compare without regard to ASCII case, so Path and
/// PATH are one variable; a non-ASCII letter must match exactly. Windows
/// itself also folds non-ASCII letters, so such a name can be found by the
/// OS and not here.
export System.Env(&self, name: str): ?str from self {
    unsafe "proc is the runtime's live Process, alive while self is" {
        for v in (*self.proc).env {
            if EnvNameIs(v.name, name) {
                return some(v.value)
            }
        }
    }
    return none
}

// EnvNameIs is the environment's name comparison on the target OS.
EnvNameIs(a: str, b: str): bool {
    if Target.os != Os.Windows {
        return a == b
    }
    n := a.Len()
    if n != b.Len() {
        return false
    }
    x := a.AsBytes()
    y := b.AsBytes()
    for i in 0..n {
        if AsciiUpper(x[i]) != AsciiUpper(y[i]) {
            return false
        }
    }
    return true
}

AsciiUpper(c: u8): u8 {
    if c >= 0x61 && c <= 0x7A {
        return c - 0x20
    }
    return c
}

// ProcCtx is the ctx of a stream writer or reader: the BufState of stream
// 0 or 1 for a writer, the Process itself (i = 3) for the stdin reader.
ProcCtx(p: *Process, i: int): *u8 {
    unsafe "pointer casts; nothing is dereferenced" {
        if i == 3 {
            return raw.PtrCast[Process, u8](p)
        }
        return raw.PtrCast[BufState, u8](ProcStream(p, i))
    }
}
/// Flushes and seals stdout and stderr for an exit with status code, and
/// returns the status to exit with. The runtime calls it when Main returns
/// and in proc.Exit. Std-internal.
///
/// Sealing stops a task still running on another thread from starting a
/// write the exit would cut, so both streams end with whole writes. When
/// the last flush of stdout fails, `error: WriteError on stdout` goes to
/// stderr (when stderr's own flush worked); when either flush fails, a
/// status of 0 becomes 1. A `try` on each Print covers the writes during
/// the run; this covers the bytes still buffered at the end, which no
/// caller could see fail. O(buffered bytes); it allocates nothing.
export FlushStdCode(p: *Process, io: Io, code: int): int {
    out := SealStream(ProcStream(p, 0), io)
    err := SealStream(ProcStream(p, 1), io)
    if out && err {
        return code
    }
    if !out && err {
        unsafe "p is the runtime's live Process" {
            _ = WriteHandle(IoMasked(io), (*ProcStream(p, 1)).h, "error: WriteError on stdout\n".AsBytes())
        }
    }
    if code == 0 {
        return 1
    }
    return code
}

/// Flushes and seals stdout and stderr before a panic report. A stream
/// whose lock is held is skipped, and a failure is ignored; FlushStdCode is
/// the normal exit's flush. Std-internal. O(buffered bytes); it allocates
/// nothing.
export TryFlushStd(p: *Process, io: Io) {
    TrySealStream(ProcStream(p, 0), io)
    TrySealStream(ProcStream(p, 1), io)
}
/// Builds the System the runtime hands Main. Std-internal. O(1); it
/// allocates nothing.
export NewSystem(args: []str, io: Io, arena: &Arena, heap: &Heap, p: *Process): System from args, arena, heap {
    return System(args: args, io: io, arena: arena, heap: heap, proc: p)
}
