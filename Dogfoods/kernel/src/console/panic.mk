// Why: every panic, from a failed Must to a CPU fault, must still reach the
// serial line, even when the console's lock is held.
//
// What: the panic hook for every image that imports the console, and
// PanicCode.
//
// How: the hook gets a free.PanicInfo and writes to COM1 through a Uart of
// its own, formatting with Print through a Writer over that Uart
// (UartSink): it takes no lock and allocates nothing.

import std.free
import src.arch

/// The status a panic ends QEMU with.
const PanicCode: u8 = 0x12

/// Prints the panic's message, code and site, and the CPU fault behind it
/// if there was one, then ends QEMU with PanicCode.
export extern MkPanicHook(info: &free.PanicInfo): never {
    var port := arch.Uart.New(arch.Com1)
    var buf: [64]u8 = [0; 64]
    var w := (&UartSink).Writer(mut port, mut buf[..])
    Print(mut w, "\npanic: {message} (code {code})\n  at {file}:{line}:{column}\n", (message: info.message, code: info.code, file: info.file, line: info.line, column: info.column)) else {
    }
    if fault := arch.LastFault() {
        Print(mut w, "  cpu fault: vector {vector}, error code 0x{code:x}, address 0x{address:x}\n", (vector: fault.vector, code: fault.code, address: fault.address)) else {
        }
    }
    w.Flush() else {
    }
    arch.Exit(PanicCode)
}
