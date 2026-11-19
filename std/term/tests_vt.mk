// Why: UseColor turns on a console's virtual-terminal processing, a write
// to the console's mode that must hit the right handles and no other.
// What: Windows pseudo-console tests of UseColor on Stdout, Stderr, Stdin.
// How: a child clears the bit and asks; a grandchild gets read-only
// handles to reach the refused branch.

// Tests for term.UseColor's virtual-terminal processing: on a Windows console
// whose output mode lacks ENABLE_VIRTUAL_TERMINAL_PROCESSING,
// UseColor(Stdout) and UseColor(Stderr) turn it on, and UseColor(Stdin)
// writes no mode. A classic console cannot be reached from a test, so the
// child clears the bit on its pseudo-console and asks. The branch where the
// console refuses the bit (false) is reached through a read-only CONOUT$
// handle lent to a grandchild as its stdout and stderr (SetConsoleMode needs
// write access); the screen-buffer check through CONIN$ lent the same way.

import std.core
import std.fs
import std.proc
import std.rt

test "UseColor turns on a console's virtual-terminal processing for Stdout and Stderr, and writes no mode for Stdin" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    io := sys.io
    // NO_COLOR unset: only what a Windows process needs of its environment.
    var base: [1]str = [""]
    var nb := 0
    if root := sys.Env("SystemRoot") {
        base[0] = sys.arena.Fmt("SystemRoot={root}", (root)).Must()
        nb = 1
    }
    p := proc.Pty.Open(io, 80, 24).Must()
    (code, text) := TmOnPtyEnv(sys, &p, "vt", proc.Env.Replace(vars: base[0..nb]))
    p.Close()
    // 60 to 64: the child could not open, read or clear the modes. 63 is a
    // console that kept a bit the child cleared, which would make the rest
    // vacuous.
    Assert(code == 0)
    Assert(TmSame(sys, text, "vt in true echo-off true out-off true out true on true err true on true ro false ro-off true conin false echo-kept true\n"))
}

// TmVtChild is mode "vt". On Windows it clears ENABLE_ECHO_INPUT (0x4) on
// its console's input and ENABLE_VIRTUAL_TERMINAL_PROCESSING (0x4) on its
// output, checks that both are off, then asks UseColor of Stdin (which must
// leave both off), of Stdout (which must turn the output bit on), clears
// the output bit again and asks of Stderr (the same), then clears it once
// more and asks through a grandchild (TmVtLend) whose stdout and stderr are
// a read-only CONOUT$ (false, the bit left off) and then CONIN$ (false,
// echo left off). On Linux it checks that the three calls leave the
// termios as it was. It writes the answers to path and puts the modes
// back.
TmVtChild(sys: &System, path: str): never {
    io := sys.io
    var h: [2]uint = [0; 2]
    if rt.TermOpen(io, mut h).err != core.SysErr.None {
        proc.Exit(60)
    }
    var old: [64]u8 = [0; 64]
    n := TmVtGet(io, h, mut old[..])
    var cin := false
    var echo_off := false
    var out_off := false
    var cout := false
    var on1 := false
    var cerr := false
    var on2 := false
    var ro := "false"
    var ro_off := false
    var conin := "false"
    var echo_kept := false
    if Target.os == core.Os.Windows {
        if n != 8 {
            proc.Exit(61)
        }
        var cleared: [8]u8 = [0; 8]
        for i in 0..8 {
            cleared[i] = old[i]
        }
        cleared[0] = cleared[0] & 0xFB
        cleared[4] = cleared[4] & 0xFB
        TmVtSet(io, h, cleared[..])
        var now: [64]u8 = [0; 64]
        _ = TmVtGet(io, h, mut now[..])
        if now[0] & 4 != 0 || now[4] & 4 != 0 {
            proc.Exit(63)
        }
        cin = UseColor(io, Std.Stdin)
        _ = TmVtGet(io, h, mut now[..])
        echo_off = now[0] & 4 == 0
        out_off = now[4] & 4 == 0
        cout = UseColor(io, Std.Stdout)
        _ = TmVtGet(io, h, mut now[..])
        on1 = now[4] & 4 != 0
        TmVtSet(io, h, cleared[..])
        _ = TmVtGet(io, h, mut now[..])
        if now[4] & 4 != 0 {
            proc.Exit(63)
        }
        cerr = UseColor(io, Std.Stderr)
        _ = TmVtGet(io, h, mut now[..])
        on2 = now[4] & 4 != 0
        // The console refuses the bit: a read-only CONOUT$ handle (no
        // GENERIC_WRITE) passes GetConsoleMode and the screen-buffer
        // check, and SetConsoleMode refuses it, so a grandchild whose
        // stdout and stderr are that handle answers false, and the bit
        // stays off.
        TmVtSet(io, h, cleared[..])
        ro = TmVtWord(TmVtLend(sys, path, "CONOUT$", fs.ReadOnly))
        _ = TmVtGet(io, h, mut now[..])
        ro_off = now[4] & 4 == 0
        // Not a screen buffer: the console's input buffer (CONIN$) as the
        // grandchild's stdout and stderr answers false and writes no mode,
        // so the input's echo bit (also 0x4) stays off.
        conin = TmVtWord(TmVtLend(sys, path, "CONIN$", fs.ReadWrite))
        _ = TmVtGet(io, h, mut now[..])
        echo_kept = now[0] & 4 == 0
        TmVtSet(io, h, old[0..8])
    } else {
        cin = UseColor(io, Std.Stdin)
        cout = UseColor(io, Std.Stdout)
        cerr = UseColor(io, Std.Stderr)
        var now: [64]u8 = [0; 64]
        same := TmVtGet(io, h, mut now[..]) == n && TmVtSame(old[0..n], now[0..n])
        echo_off = same
        out_off = same
        on1 = same
        on2 = same
        // The console legs are Windows': the line has the same words.
        ro_off = same
        echo_kept = same
    }
    line := sys.arena.Fmt("vt in {cin} echo-off {echo_off} out-off {out_off} out {cout} on {on1} err {cerr} on {on2} ro {ro} ro-off {ro_off} conin {conin} echo-kept {echo_kept}\n",
        (cin, echo_off, out_off, cout, on1, cerr, on2, ro, ro_off, conin, echo_kept)).Must()
    fs.WriteAll(io, path, line.AsBytes()) else { proc.Exit(74) }
    proc.Exit(0)
}

// TmVtLend opens name (CONOUT$ or CONIN$) with o and runs mode "color" in
// a grandchild whose stdout and stderr are that handle: its
// UseColor(Stdout), 1 true, 0 false, -1 another line. Exits 65 when the
// open fails, 66 the spawn, 67 the wait, 68 a grandchild's exit other
// than 0.
TmVtLend(sys: &System, path: str, name: str, o: fs.Options): int {
    io := sys.io
    f := fs.OpenWith(io, name, o) else {
        proc.Exit(65)
    }
    lent := sys.arena.Fmt("{path}.lent", (path)).Must()
    var a: [4]str = [sys.args[0], sys.args[1], "color", lent]
    var c := proc.DefaultCommand
    c.args = a[..]
    c.stdout = proc.Stdio.File(f: &f)
    c.stderr = proc.Stdio.File(f: &f)
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c) else {
        proc.Exit(66)
    }
    TmCloseEnds(stdin, stdout, stderr)
    st := child.Wait(io) else {
        proc.Exit(67)
    }
    f.Abandon()
    if (st.code else { -1 }) != 0 {
        proc.Exit(68)
    }
    text := fs.ReadText(sys.arena, io, lent) else { "" }
    fs.Remove(io, lent) else {}
    return if text.StartsWith("color true ") { 1 } else if text.StartsWith("color false ") { 0 } else { -1 }
}

// TmVtWord is TmVtLend's answer as a word: "true", "false" or "?".
TmVtWord(n: int): str {
    return if n == 1 { "true" } else if n == 0 { "false" } else { "?" }
}

// TmVtGet reads the terminal h's modes into into and returns their length;
// exits 62 when it cannot.
TmVtGet(io: Io, h: [2]uint, into: mut []u8): int {
    var none_: [0]u8 = []
    r := rt.TermMode(io, h[0], h[1], core.IoTermGet, none_[..], into)
    if r.err != core.SysErr.None || r.value <= 0 || r.value > 64 {
        proc.Exit(62)
    }
    return Trunc[int](r.value)
}

// TmVtSet sets the terminal h's modes to mode; exits 64 when it cannot.
TmVtSet(io: Io, h: [2]uint, mode: []u8) {
    var back: [64]u8 = [0; 64]
    if rt.TermMode(io, h[0], h[1], core.IoTermSaved, mode, mut back[..]).err != core.SysErr.None {
        proc.Exit(64)
    }
}

// TmVtSame: a and b hold the same bytes.
TmVtSame(a: []u8, b: []u8): bool {
    if a.Len() != b.Len() {
        return false
    }
    for i in 0..a.Len() {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}
