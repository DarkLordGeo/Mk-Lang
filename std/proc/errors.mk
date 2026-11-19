// Why: every std.proc call fails with the same small error set, and the
// runtime's SysErr must map to it the same way everywhere.
//
// What: the members of proc.Error (NotFound, Denied, TooLarge, Os,
// Unsupported, with core's Canceled and OutOfMemory) and Check, the one map from SysErr to them.
//
// How: NotFound, Denied, Canceled and Unsupported map by name; anything else
// becomes Os with the OS's code, or 0 when the runtime had none.

import std.core

/// The program or its directory was not found: no program at args[0],
/// args or args[0] is empty, or dir names no directory (Command.dir is
/// missing or not a directory).
export error NotFound
/// The OS refused to start the program.
export error Denied
/// A child's output passed the limit given to Run.
export error TooLarge
/// Any other OS failure, with the OS's error code. The code is 0 for an
/// argument out of range: a NUL in an argument, an environment entry or the
/// directory; a disposition a stream cannot take; a batch-file argument
/// that cmd.exe cannot be given safely. It is also 0 when the runtime
/// reports an impossible result.
export error Os(code: i64)
/// Something this target or this Io cannot do: a signal Windows cannot
/// deliver, a pseudo-console before Windows 10 1809, or a process, pipe or
/// terminal under a Recorder or Replayer.
export error Unsupported

/// Every way a std.proc operation fails.
export open type Error = NotFound | Denied | TooLarge | Os | Canceled | OutOfMemory | Unsupported

// Check is r's value, or the proc.Error its SysErr maps to: NotFound,
// Denied, Canceled and Unsupported by name, anything else Os with the OS's
// own code (0 when the runtime had none: InvalidPath, a refused argument).
Check(r: core.IoResult): Error!i64 {
    match r.err {
        .None { return r.value }
        .NotFound { fail NotFound }
        .Denied { fail Denied }
        .Canceled { fail Canceled }
        .Unsupported { fail Unsupported }
        _ { fail Os(code: r.os) }
    }
}
