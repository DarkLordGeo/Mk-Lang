// Why: when Main returns, its result must become a process exit status after
// the output buffers are flushed and any error is reported, on the main task.
//
// What: Finish, which ends the process from Main's result, with WriteErr,
// MainErrorWritten and the error-name helpers.
//
// How: Finish flushes stdout and stderr, writes the profile, exits 102 when
// the leak count shows live allocations, writes Main's error line (unless
// the entry wrote it already) and the trace, then exits. A failed final
// flush turns a status of 0 into 1.

import std.core
import std.raw
import std.sys

/// WriteErr writes to standard error through the direct table, after the
/// buffers were flushed, so the report cannot interleave with them.
/// Errors are ignored: nothing is left to report them to. O(len(data));
/// allocates nothing.
export WriteErr(data: []u8) {
    unsafe "Init ran, so R() is the page it mapped, which lives until exit (WriteErr runs at exit and in reports after Start)" {
        h := (*Proc()).stderr.h
        if h != 0 {
            _ = HandleWrite(h, data, -1)
            return
        }
    }
    sys.FatalWrite(data)
}

// MainLine is 1 once the entry wrote Main's error line itself
// (MainErrorWritten), so Finish writes none. Edge: none across tasks; the
// entry's Store and Finish's Load both run on the main task, in that order.
var MainLine: raw.Atomic[u32] = raw.Atomic[u32].New(0)

/// MainErrorWritten tells Finish that Main's error line is written: the
/// code the compiler generates around Main writes `error: ` and the error
/// as its `{e}` hole formats it, then calls this, so Finish writes no line
/// of its own and still writes the trace and exits 1. O(1); allocates
/// nothing.
export MainErrorWritten() {
    // Edge: none; program order on the main task orders it before Finish's
    // Load (the entry calls this after Main returned, then calls Finish).
    MainLine.Store(1)
}

// ErrorNameLine writes "error: <name>\n", the line for an error with no
// formatted form, into buf and returns its length, or 0 when it does not
// fit.
ErrorNameLine(buf: mut []u8, name: str): int {
    var n := Put(buf, 0, "error: ")
    n = Put(buf, n, name)
    n = Put(buf, n, "\n")
    return if n <= buf.Len() { n } else { 0 }
}

// WriteErrorName writes the error line of Main's error code by its name.
WriteErrorName(code: u32) {
    name := ErrorName(code)
    var buf: [256]u8 = [0; 256]
    n := ErrorNameLine(mut buf[..], name)
    if n > 0 {
        WriteErr(buf[..n])
        return
    }
    WriteErr("error: ".AsBytes())
    WriteErr(name.AsBytes())
    WriteErr("\n".AsBytes())
}

/// Finish ends the process with Main's result: it flushes the standard
/// streams, writes the profile, checks for leaks, prints the error line
/// (unless the entry wrote it, MainErrorWritten) and trace, then exits with
/// the status. Never returns. Its cost is the flush's
/// and the profile's; allocates nothing.
export Finish(r: MainResult): never {
    // 1 when the last flush of stdout or stderr failed, after its report
    // on stderr; it turns a status of 0 into 1 below.
    flushed := core.FlushStdCode(Proc(), NativeIo(), 0)
    WriteProfile()
    // Edge: every task has ended when Finish runs (Main returned after its
    // joins), so the counters' last Adds precede these loads in S.
    if Rt.counting.Load() == 1 && Rt.live_count.Load() > 0 {
        var buf: [128]u8 = [0; 128]
        var n := Put(mut buf[..], 0, "leak: ")
        n = Digits(mut buf[..], n, Trunc[u64](Rt.live_count.Load()))
        n = Put(mut buf[..], n, " allocations, ")
        n = Digits(mut buf[..], n, Trunc[u64](Rt.live_bytes.Load()))
        n = Put(mut buf[..], n, " bytes still live\n")
        WriteErr(buf[..n])
        sys.Exit(102)
    }
    if r.code != 0 {
        // Edge: none; MainErrorWritten's Store ran earlier on this task.
        if MainLine.Load() == 0 {
            WriteErrorName(r.code)
        }
        WriteTrace()
        sys.Exit(1)
    }
    if r.exit == 0 && flushed != 0 {
        sys.Exit(1)
    }
    sys.Exit(Trunc[i32](r.exit))
}
