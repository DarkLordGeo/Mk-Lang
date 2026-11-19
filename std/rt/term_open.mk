// Why: std.term holds runtime handles and no unsafe, so it cannot read the
// OS handle behind one; the terminal-restore registry and the terminal's
// size need it, so these calls read it here (std-internal: user code
// cannot import std.rt).
//
// What: TermRegisterOpen, TermVtOnOpen and TermVtBackOpen for the
// terminal-restore registry, TermSizeOf, and StdVt, term.UseColor's
// question of a standard stream.
//
// How: each call maps the runtime handles rt.TermOpen gave to their OS
// handles and calls std.sys; size and StdVt go through the Io's call entry.
// Tests: std/rt/tests_term_open.mk, and end to end std.term's pty blocks (a
// panic, proc.Exit and, on Linux, a SIGTERM in raw mode on the alternate
// screen leave the mode as it was).

import std.core
import std.sys

/// TermRegisterOpen registers, for the exit paths (TermRestoreAll), the
/// mode the terminal whose input is input and output out (runtime handles,
/// TermOpen's) had before std.term changed it: mode is the old bytes
/// TermMode gave (no bytes: only the alternate screen to leave), alt
/// whether the alternate screen is on. The record's kind is the target's
/// (TermKindConsole on Windows, TermKindTermios elsewhere) and its h and
/// out are the two handles' OS handles. Returns TermRegister's id, or -1
/// when a handle is 0 or another Io's (a Sim's terminal: there is
/// no OS terminal to restore), mode is longer than 64 bytes or the table is
/// full (std.term then restores only in its own Restore and Close).
/// O(TermSlots); allocates nothing.
export TermRegisterOpen(input: uint, out: uint, mode: []u8, alt: bool): int {
    if input == 0 || out == 0 || mode.Len() > 64 || IsForeign(input) || IsForeign(out) {
        return -1
    }
    kind := if Target.os == Os.Windows { sys.TermKindConsole } else { sys.TermKindTermios }
    var rec := TermSaved(kind: kind, h: 0, out: 0, alt: alt, mouse: false, len: mode.Len(), mode: [0; 64])
    for i in 0..mode.Len() {
        rec.mode[i] = mode[i]
    }
    unsafe "input and out are live runtime handles TermOpen made" {
        rec.h = (*Rec(input)).os
        rec.out = (*Rec(out)).os
    }
    return TermRegister(rec)
}

/// TermVtOnOpen is sys.TermVtOn for out, a runtime handle TermOpen gave:
/// on Windows, virtual-terminal processing turned on for Term.Leave's
/// writes when the console's output mode lacks it. 0 (nothing to
/// put back) for 0, another Io's handle (a Sim's terminal) or Linux.
/// Called directly, not through an Io, as TermRegisterOpen is. O(1);
/// allocates nothing.
export TermVtOnOpen(out: uint): u64 {
    if out == 0 || IsForeign(out) {
        return 0
    }
    kind := if Target.os == Os.Windows { sys.TermKindConsole } else { sys.TermKindTermios }
    unsafe "out is a live runtime handle TermOpen made" {
        return sys.TermVtOn(kind, (*Rec(out)).os)
    }
}

/// TermVtBackOpen puts back what TermVtOnOpen(out) changed; held is its
/// answer, and 0 does nothing. O(1); allocates nothing.
export TermVtBackOpen(out: uint, held: u64) {
    if held == 0 || out == 0 || IsForeign(out) {
        return
    }
    unsafe "out is a live runtime handle TermOpen made" {
        sys.TermVtBack((*Rec(out)).os, held)
    }
}

/// TermSizeOf reads the size of the terminal whose input is input and
/// output out (runtime handles, TermOpen's), whatever the standard streams
/// are: w.a columns, w.b rows. TIOCGWINSZ on input on Linux; on Windows
/// the window of out's screen buffer (GetConsoleScreenBufferInfo). Not a
/// terminal is the OS's error. The call is IoOpTermSize with w.a -1 (no
/// standard stream) and the handle; a simulated Io answers 80 by 24 for
/// its terminal's handles. Inline; O(1); allocates nothing.
export TermSizeOf(io: Io, input: uint, out: uint, w: mut core.IoWords): core.IoResult {
    h := if Target.os == Os.Windows { out } else { input }
    var a := CallOf(core.IoOpTermSize, core.IoRouteInline, h, TermSizeOfJob)
    a.w.a = -1
    r := Run(io, mut a)
    *w = a.w
    return r
}

TermSizeOfJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record; os is its live handle's" {
        Set(a, sys.TermSize((*a).os, mut (*a).w))
    }
}

/// StdVt answers value 1 when the standard stream s (0 in, 1 out, 2 err)
/// is a terminal that reads escape sequences, else 0; Other for another s.
/// For s 1 and 2, on a Windows console, it turns on
/// ENABLE_VIRTUAL_TERMINAL_PROCESSING when it is off (sys.TermVt) and
/// answers 0 when the console refuses it; the bit is left on. For s 0 it
/// is IsTerminal's answer and writes no mode: on an input handle that bit
/// is ENABLE_ECHO_INPUT. On Linux it is IsTerminal's answer. The call is
/// IoOpIsTerminal with w.b 1, so a simulated Io or a tape answers it as it
/// answers IsTerminal (Unsupported). Inline; O(1); allocates nothing.
export StdVt(io: Io, s: u32): core.IoResult {
    var a := CallOf(core.IoOpIsTerminal, core.IoRouteInline, 0, StdVtJob)
    a.w.a = i64(s)
    a.w.b = 1
    return Run(io, mut a)
}

StdVtJob(a: *core.IoCallArgs) {
    unsafe "a is the call's live record" {
        s := (*a).w.a
        if s < 0 || s > 2 {
            Set(a, sys.OsResult(value: 0, err: core.SysErr.Other, os: 0))
            return
        }
        h := sys.StdHandle(Trunc[int](s))
        yes := if s == 0 { sys.IsTerminal(h) } else { sys.TermVt(h) }
        Set(a, sys.OsResult(value: if yes { 1 } else { 0 }, err: core.SysErr.None, os: 0))
    }
}
