// Why: every std.term operation reports failure through one error type, and
// the runtime's SysErr must map to it the same way everywhere.
//
// What: the members NotTerminal, Device and Unsupported, the open type
// term.Error, and Check and OpenCheck, which turn a core.IoResult into a
// value or a term.Error.
//
// How: Canceled and Unsupported map by name; NotFound and the OS's "not a
// terminal" codes (ENOTTY, ERROR_INVALID_HANDLE) become NotTerminal; any
// other code is Device with the OS's code.

import std.core

/// No terminal: the process has no controlling terminal, or the handle or
/// stream is not one.
export error NotTerminal
/// The terminal refused an operation, with the OS's code.
export error Device(code: i64)
/// This target or this Io cannot do it: for example, a Windows console
/// without virtual-terminal input.
export error Unsupported

/// Every way a std.term operation fails.
export open type Error = NotTerminal | Device | Unsupported | Canceled | OutOfMemory

// ENOTTY (Linux) and ERROR_INVALID_HANDLE (Windows: GetConsoleMode on a
// handle that is no console) both say "not a terminal".
const NoTty: i64 = 25
const NoConsole: i64 = 6

// Check is r's value, or the term.Error its SysErr maps to: Canceled and
// Unsupported by name, NotFound (no controlling terminal) and the OS's
// "not a terminal" codes as NotTerminal, anything else Device with the
// OS's own code.
Check(r: core.IoResult): Error!i64 {
    match r.err {
        .None { return r.value }
        .Canceled { fail Canceled }
        .Unsupported { fail Unsupported }
        .NotFound { fail NotTerminal }
        _ {
            if r.os == NoTty && Target.os != core.Os.Windows || r.os == NoConsole && Target.os == core.Os.Windows {
                fail NotTerminal
            }
            fail Device(code: r.os)
        }
    }
}

// OpenCheck is Check for rt.TermOpen, whose one answer of Other with no OS
// code is a handle record the process heap refused: OutOfMemory,
// as std.proc maps rt's same answer for Pty.Open, Spawn and Pipe (a
// simulated Io's open answers it too, for memory alone). Check
// keeps Device for it, since TermMode answers a bare Other for a short
// buffer.
OpenCheck(r: core.IoResult): Error!i64 {
    if r.err == core.SysErr.Other && r.os == 0 {
        fail OutOfMemory
    }
    return try Check(r)
}
