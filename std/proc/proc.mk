// Why: a program needs to wait for a signal sent to it, to end itself with
// a code after flushing its output, and to take the handles its parent
// passed; none of that belongs to a child process.
//
// What: Signal, WaitSignal, Inherited and Exit.
//
// How: WaitSignal goes through IoTable.signal, Inherited reads MK_HANDLES as
// the runtime checked it at start-up, and Exit calls rt.Exit, which flushes
// the standard streams first. Nothing here allocates but Inherited.

import std.core
import std.net
import std.raw
import std.rt

/// A signal a program can wait for with WaitSignal, or send to a child
/// with Child.Signal.
///
/// On Windows, Interrupt is Ctrl+C, Terminate is Ctrl+Break, a console
/// close, a logoff or a shutdown, and Hangup never arrives. Windows cannot
/// send any of them to a child: Child.Signal fails with Unsupported.
export open enum Signal {
    /// SIGINT on Linux; Ctrl+C on Windows.
    Interrupt
    /// SIGTERM on Linux; Ctrl+Break, a console close, a logoff or a shutdown on
    /// Windows.
    Terminate
    /// SIGHUP on Linux; it never arrives on Windows.
    Hangup
}

// SignalCode is IoTable.signal's number for s: 0, 1, 2.
SignalCode(s: Signal): u32 {
    return match s {
        .Interrupt { 0 }
        .Terminate { 1 }
        .Hangup { 2 }
    }
}

/// Waits until the process receives s. Only a signal that arrives after
/// the call starts counts; an earlier one is not seen.
///
/// The first wait for s turns off s's default action for the rest of the
/// process: a program that waits for Interrupt is not ended by Ctrl+C
/// from then on.
///
/// Canceled when the task's scope is cancelled. Panics with code 8 when
/// io cannot watch s at all. One runtime call; allocates nothing.
export WaitSignal(io: Io, s: Signal): Canceled!void {
    r := core.IoSignal(io, SignalCode(s))
    if r.err == core.SysErr.None {
        return
    }
    if r.err == core.SysErr.Canceled {
        fail Canceled
    }
    raw.Fail(8, "proc.WaitSignal: the runtime cannot watch this signal")
}

/// Takes the handles this process's parent passed in Command.handles,
/// pushes them onto into in the order the parent named them, and returns
/// how many. Each is typed by what the OS says it is (a net.Passed; a pipe
/// end arrives as a File).
///
/// Only the first call in a process takes them. Every later call, a
/// process that was passed nothing (no MK_HANDLES in its environment), and
/// a call under a simulated Io return 0. The handles are already
/// close-on-exec (not inheritable on Windows) when the program starts, so
/// a program this process starts gets none of them unless its own Command
/// names them.
///
/// Errors:
/// - Os(code: 22) when MK_HANDLES fails the check made at start-up, before
///   the runtime opens any handle of its own. On Linux the list must be
///   exactly 3, 4 and on, each open and not close-on-exec; on Windows the
///   values must be distinct, open, inheritable and not a standard handle;
///   on both, none may be one MK_INHERIT names. So an MK_HANDLES passed on
///   by a program that is not an Mk Spawn, without its handles, takes
///   nothing and touches no handle.
/// - Os(code) for a value that names no open handle (EBADF, 9, on Linux;
///   ERROR_INVALID_HANDLE, 6, on Windows).
/// - Unsupported for a socket that is not a stream socket (on Windows, any
///   socket but a listener), or a handle the OS cannot type.
/// - OutOfMemory when into cannot grow, or the process heap cannot give a
///   handle's record.
///
/// On an error, the handles already taken are closed and the rest are left
/// as they are. O(handles) plus a scan of the environment; allocates a
/// runtime record per handle and what into's Push takes from a.
export Inherited(a: &Alloc, io: Io, into: mut HeapList[net.Passed]): Error!int {
    var ps: [64]rt.Passed = [rt.Passed(h: 0, kind: 0, family: 0, port: 0); 64]
    r := rt.InheritedFds(io, mut ps[..])
    if r.err == core.SysErr.Unsupported {
        fail Unsupported
    }
    if r.err == core.SysErr.Other && r.os == 0 {
        fail OutOfMemory
    }
    if r.err != core.SysErr.None {
        fail Os(code: r.os)
    }
    k := Trunc[int](r.os)
    for i in 0..k {
        p := net.Passed.Adopt(ps[i])
        into.Push(a, p) else {
            p.Close()
            for j in i + 1..k {
                net.Passed.Adopt(ps[j]).Close()
            }
            fail OutOfMemory
        }
    }
    return k
}

/// Flushes the standard streams, then ends the process at once with exit
/// code code. Other tasks are not joined, and nothing else runs.
///
/// A code outside i32 panics with code 5, since the OS keeps a 32-bit
/// status and would report a different code; the panic still flushes the
/// streams. O(bytes buffered in the streams); allocates nothing.
export Exit(code: int): never {
    if code < -2_147_483_648 || code > 2_147_483_647 {
        raw.Fail(5, "proc.Exit: the code is outside i32")
    }
    rt.Exit(code)
}
