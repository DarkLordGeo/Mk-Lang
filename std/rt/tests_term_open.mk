// Why: term_open.mk's calls must refuse what they cannot serve without
// touching the OS wrongly.
// What: tests of TermRegisterOpen's and TermSizeOf's refusals.
// How: called with a zero handle, an over-long mode and a pipe; their
// successes need a terminal and are tested by std.term's pty blocks.

import std.core

test "TermRegisterOpen refuses a zero handle and a mode longer than 64 bytes" {
    var long: [65]u8 = [0; 65]
    var short: [8]u8 = [0; 8]
    Assert(TermRegisterOpen(0, 1, short[..], false) == -1)
    Assert(TermRegisterOpen(1, 0, short[..], false) == -1)
    Assert(TermRegisterOpen(1, 1, long[..], false) == -1)
}

test "TermSizeOf on a pipe is the OS's error, and asks no standard stream" (sys: &System) {
    io := sys.io
    var p: [2]uint = [0; 2]
    r := PipeOpen(io, mut p)
    Assert(r.err == core.SysErr.None)
    var w := core.IoWords(a: 0, b: 0, c: 0, d: 0)
    z := TermSizeOf(io, p[0], p[1], mut w)
    Assert(z.err != core.SysErr.None)
    _ = CloseRuntimeHandle(p[0])
    _ = CloseRuntimeHandle(p[1])
}
