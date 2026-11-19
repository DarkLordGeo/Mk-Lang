// Why: a full-screen program wants clicks, drags, the wheel and focus
// changes as events, and the terminal reports them as escape sequences
// mixed into the key input.
//
// What: MouseCapture, which turns the reports on and off, and Pointer and
// MouseOf, which decode them into Event.Mouse and Event.Focus.
//
// How: the unit is self-contained: its other lines are the dispatch line in
// input.mk's Csi and the two kinds in read.mk's Take; removing it deletes
// this file, those lines, tests_mouse.mk and the "mouse" mode of
// tests_pty.mk.
//
// The reports are xterm's (ctlseqs, "Mouse Tracking"): MouseCapture asks
// for button-event tracking (DECSET 1000 and 1002) in the SGR form (1006),
// whose coordinates have no upper bound and whose release names no
// guesswork, and for focus reports (1004). A terminal without SGR sends
// the X10 form (CSI M and three bytes), which is decoded too, as crossterm
// and termion do. On Windows, virtual-terminal input passes the same
// sequences through as key records (tests_pty.mk's "mouse" block).

/// Turns mouse and focus reports on or off. On, Term.Read returns a
/// button's press and release, a move with a button held, and the wheel as
/// Event.Mouse, and the terminal gaining or losing focus as Event.Focus.
/// The sequences are xterm's modes 1000, 1002, 1006 and 1004; off turns
/// all four off.
///
/// Written through Term.Writer, capture is recorded: Term.Restore turns it
/// off first, and Term.Close and the runtime, on a panic, proc.Exit or a
/// fatal signal it handles, turn it off before they leave the alternate
/// screen. Errors are w's. O(1); allocates nothing.
export MouseCapture(w: mut Writer, on: bool): WriteError!void {
    try w.Str(if on { MouseOn } else { MouseOff })
}

// MouseOn starts with term.mk's MouseWatch, which the Writer follows;
// MouseOff is term.mk's, since Close writes it too.
const MouseOn: str = "\x1b[?1000h\x1b[?1002h\x1b[?1006h\x1b[?1004h"

// ButtonBits are the bits of a report's button byte that Event.Mouse's
// button keeps: the button (1, 2), a move (32), the wheel (64) and the
// extra buttons (128). The modifier bits (4 shift, 8 alt, 16 ctrl) go to
// mods.
const ButtonBits: int = 0xE3

// Release is the button of a release (X10's 3; SGR's final m).
const Release: int = 3

// Pointer decodes, at a whole CSI of n bytes whose parameters are p and
// whose final byte is f, the reports MouseCapture turns on: SGR mouse
// (CSI < b ; x ; y M, or m for a release), X10 mouse (CSI M then three
// bytes, each the value plus 32) and focus (CSI I in, CSI O out). none for
// any other CSI, which CsiFinal maps. An SGR report without three
// parameters, and an X10 report holding a control byte, are dropped. O(1);
// allocates nothing.
Pointer(b: []u8, n: int, p: &Params, f: u8, final: bool): ?Parsed {
    if p.private == b'<' && (f == b'M' || f == b'm') {
        if p.count != 3 {
            return some(Dropped(n))
        }
        cb := p.v[0]
        button := if f == b'm' { Release } else { cb & ButtonBits }
        return some(MouseOf(n, button, cb, p.v[2] - 1, p.v[1] - 1))
    }
    if p.private != 0 || p.count != 0 {
        return none
    }
    if f == b'I' || f == b'O' {
        on := if f == b'I' { 1 } else { 0 }
        return some(Parsed(n: n, kind: PFocus, key: Key.Char, ch: '\0', mods: NoMods, a: on, b: 0, c: 0))
    }
    if f != b'M' || n != 3 {
        return none
    }
    // X10: the three bytes after CSI M.
    if b.Len() < 6 {
        return some(if final { Dropped(b.Len()) } else { Dropped(0) })
    }
    if b[3] < 0x20 || b[4] < 0x20 || b[5] < 0x20 {
        return some(Dropped(3))
    }
    cb := int(b[3]) - 32
    return some(MouseOf(6, cb & ButtonBits, cb, int(b[5]) - 33, int(b[4]) - 33))
}

// MouseOf is a mouse step of n bytes: button, the modifiers of the report's
// button byte cb, and row and col from 0 (a coordinate before the first is
// 0).
MouseOf(n: int, button: int, cb: int, row: int, col: int): Parsed {
    mods := Mods(shift: cb & 4 != 0, alt: cb & 8 != 0, ctrl: cb & 16 != 0)
    r := if row < 0 { 0 } else { row }
    c := if col < 0 { 0 } else { col }
    return Parsed(n: n, kind: PMouse, key: Key.Char, ch: '\0', mods: mods, a: button, b: r, c: c)
}
