// Why: a hosted program may want to see every panic before the report, as a
// freestanding one must, for example to log it or flush its own state.
//
// What: CallPanicHook, which calls a package's `export extern
// MkPanicHook(info: &runtime.PanicInfo)` with HookInfo, the layout of
// std.runtime.PanicInfo.
//
// How: Reporting calls the hook once per panic, after it wins the panicking
// flag and before the report; a nested panic skips it and replays the kept
// report. The freestanding form is in std/free/free.mk. The support pack's
// weak default returns at once (internal/codegen/support_rt.go,
// hostedPanicHook), so a program with no hook reports as before.

import std.raw

// HookInfo is std.runtime.PanicInfo's layout (code, message, file, line,
// column), which the hook receives through a pointer: std.rt is below
// std.runtime and cannot name its type. The driver's TestPanicHook_Hosted
// reads every field back through runtime.PanicInfo.
struct HookInfo {
    code: u32
    message: str
    file: str
    line: int
    column: int
}

extern MkPanicHook(info: *HookInfo)

/// CallPanicHook calls the program's MkPanicHook with the report of a
/// panic of code (0 for a fault that is no panic code) at site (null for
/// none) whose message is a + b + c, cut to a alone when the three do not
/// fit 1,024 bytes. O(message); allocates nothing (1 KiB of stack). Only Reporting calls it,
/// once, on the thread that won the panicking flag.
export CallPanicHook(code: u32, site: *Site, a: str, b: str, c: str) {
    var info := HookInfo(code: code, message: a, file: "", line: 0, column: 0)
    // The message lives in this frame, which outlives the hook's call.
    var text: [1024]u8 = [0; 1024]
    if a.Len() + b.Len() + c.Len() <= 1024 && b.Len() + c.Len() > 0 {
        var n := Put(mut text[..], 0, a)
        n = Put(mut text[..], n, b)
        n = Put(mut text[..], n, c)
        unsafe "text holds n bytes: whole strs joined, so UTF-8" {
            info.message = raw.Str(raw.Data(text[..]), n)
        }
    }
    if !raw.IsNull(site) {
        unsafe "site is not null: a static site constant, alive for the whole process" {
            info.file = (*site).file
            info.line = int((*site).line)
            info.column = int((*site).col)
        }
    }
    unsafe "MkPanicHook is the program's or the support pack's weak default, and info lives in this frame" {
        MkPanicHook(raw.Of(&info))
    }
}
