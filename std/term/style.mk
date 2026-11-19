// Why: coloured, styled text needs the SGR escape sequences written right,
// and a program needs one answer to "should I write colour at all?".
//
// What: term.Color, term.Style with Style.Format, and UseColor, with the
// helpers NoColor, ColorRefused, PutCode and PutColor.
//
// How: Select Graphic Rendition (ECMA-48 8.3.117, with xterm's 256-colour and
// direct-colour forms, 38;5;n and 38;2;r;g;b). A Style is written whole: SGR
// 0 first, then each attribute it sets, so the result never depends on what
// was in force before. NO_COLOR (no-color.org) is the program's to honour
// through UseColor, since a Writer does not know where its bytes go.

import std.core
import std.rt

/// A colour: one of the 256 indexed colours (0 to 7 the standard ones, 8 to
/// 15 their bright forms, 16 to 231 a 6 x 6 x 6 colour cube and 232 to 255
/// greys), or a 24-bit colour.
export enum Color {
    /// One of the 256 indexed colours, n.
    Ansi(n: u8)
    /// A 24-bit colour.
    Rgb(r: u8, g: u8, b: u8)
}

/// How text is drawn: a foreground and a background colour (none keeps the
/// terminal's default), bold, underline and reverse video.
export struct Style {
    /// The foreground colour; none keeps the terminal's default.
    export fg: ?Color
    /// The background colour; none keeps the terminal's default.
    export bg: ?Color
    /// Whether text is bold.
    export bold: bool
    /// Whether text is underlined.
    export underline: bool
    /// Whether foreground and background are swapped.
    export reverse: bool
}

/// Writes the SGR escape sequence that sets exactly this style: a reset,
/// then each attribute. A style with nothing set writes the reset alone
/// ("\x1b[0m"), which returns to the terminal's default. Style does not
/// implement the Format interface, so a Style cannot fill a hole of a
/// format string: call this method. Errors are w's. O(1), one w.Bytes
/// call; allocates nothing.
export Style.Format(&self, w: mut Writer): WriteError!void {
    // At most "\x1b[0;1;4;7" (9), two colours of ";38;2;255;255;255"
    // (17 each) and "m": 44 bytes.
    var b: [48]u8 = [0; 48]
    b[0] = 0x1B
    b[1] = b'['
    b[2] = b'0'
    var n := 3
    if self.bold {
        n = PutCode(mut b[..], n, 1)
    }
    if self.underline {
        n = PutCode(mut b[..], n, 4)
    }
    if self.reverse {
        n = PutCode(mut b[..], n, 7)
    }
    if c := self.fg {
        n = PutColor(mut b[..], n, c, 30, 90, 38)
    }
    if c := self.bg {
        n = PutColor(mut b[..], n, c, 40, 100, 48)
    }
    b[n] = b'm'
    try w.Bytes(b[0..n + 1])
}

/// Whether a program should write colour to the standard stream s. False
/// when the environment variable NO_COLOR is set and not empty
/// (no-color.org), when s is not a terminal (IsTerminal), or when s is
/// Stdout or Stderr on a Windows console that cannot read escape
/// sequences; true otherwise. Never fails: under a simulated Io, s is no
/// terminal.
///
/// NO_COLOR is read from the environment as the process started, so a
/// later change to the environment is not seen.
///
/// On a Windows console whose output mode lacks virtual-terminal
/// processing (ENABLE_VIRTUAL_TERMINAL_PROCESSING), UseColor(Stdout) or
/// UseColor(Stderr) turns it on, and is false only when the console
/// refuses it (a classic console without virtual-terminal support). The
/// mode stays on after the process ends. Asked while a Term's Raw mode is
/// in force, it sees the mode Raw set, which Restore and the exit take
/// away again on a console that lacked it: ask before Raw or after
/// Restore. A standard output stream that is the console's input buffer
/// answers by its echo setting, with nothing written. Do not call it while
/// another task changes the terminal's mode: the mode is read and written
/// back with the bit, so a change between the two is lost. Stdin's mode is
/// never written.
///
/// O(environment) to find NO_COLOR, then one system call, or three when
/// the mode is off (the one time it is turned on, or every time on a
/// console that refuses it); allocates nothing.
export UseColor(io: Io, s: Std): bool {
    if NoColor() {
        return false
    }
    r := rt.StdVt(io, StdIndex(s))
    return r.err == core.SysErr.None && r.value == 1
}

// NoColor is whether this process's NO_COLOR is set and not empty, in the
// environment rt decoded at start.
NoColor(): bool {
    return ColorRefused(rt.Env("NO_COLOR"))
}

// ColorRefused decides NoColor from NO_COLOR's value: set and not empty
// refuses colour (no-color.org).
ColorRefused(v: ?str): bool {
    s := v else {
        return false
    }
    return s.Len() > 0
}

// PutCode writes ";" and v at b[at..] and returns the index after it.
PutCode(b: mut []u8, at: int, v: int): int {
    b[at] = b';'
    return PutNum(b, at + 1, v)
}

// PutColor writes c's SGR parameters at b[at..]: base + n for the
// standard colours, bright + n - 8 for the bright ones, then ext;5;n or
// ext;2;r;g;b.
PutColor(b: mut []u8, at: int, c: Color, base: int, bright: int, ext: int): int {
    match c {
        .Ansi(n) {
            if n < 8 {
                return PutCode(b, at, base + int(n))
            }
            if n < 16 {
                return PutCode(b, at, bright + int(n) - 8)
            }
            var k := PutCode(b, at, ext)
            k = PutCode(b, k, 5)
            return PutCode(b, k, int(n))
        }
        .Rgb(r, g, b: bl) {
            var k := PutCode(b, at, ext)
            k = PutCode(b, k, 2)
            k = PutCode(b, k, int(r))
            k = PutCode(b, k, int(g))
            return PutCode(b, k, int(bl))
        }
    }
}
