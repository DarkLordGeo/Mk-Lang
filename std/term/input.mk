// Why: Term.Read must turn the raw bytes a terminal sends into key, paste,
// resize, mouse and focus events, and the decoding is a pure byte-level job
// best kept apart from the waiting and the OS calls.
//
// What: the public term.Event, term.Key and term.Mods, and the decoder Step
// (with Parse, Csi, Ss3, Escaped, CtlString) plus the UTF-8 helpers Utf8At,
// LossyLen and Lossy.
//
// How: one step over the bytes, after xterm's control sequences (ctlseqs:
// "PC-Style Function Keys", CSI and SS3 forms with the modifier parameter)
// and the key mapping of crossterm's Unix parser. The Windows console's key
// records, with virtual-terminal input on, carry the same sequences as
// UTF-16 units, which read.mk turns into these bytes.
//
// Step is a pure function of its bytes and one byte of state (the Term's skip,
// set while the rest of a long sequence is dropped): it never allocates, never
// recurses, and holds at most CsiMax bytes of a sequence, so every byte not yet
// decoded stays in the Term (Read is repeatable: a cut read loses nothing).
// Control strings (OSC, DCS, APC, PM, SOS), a terminal's answers to queries,
// are dropped whole, as DEC's VT500 parser (vt100.net) and libvterm, vte and
// termwiz after it drop them. Malformed input is never an error: a broken
// sequence is dropped, a broken UTF-8 sequence is one U+FFFD key (Unicode's
// maximal subpart rule, 3.9 "U+FFFD Substitution of Maximal Subparts").

/// One input event, from Term.Read.
///
/// - Key: a key with its modifiers. ch is the character for Key.Char, and
///   '\0' for every other key.
/// - Paste: the text of a bracketed paste. A long paste arrives as several
///   Paste events, in order.
/// - Resize: the terminal's new size.
/// - Mouse: a mouse report. button is 0 left, 1 middle, 2 right, 3 a
///   release, 64 to 67 the wheel up, down, left and right, and 128 to 131
///   the buttons 8 to 11, plus 32 while the pointer moves. row and col
///   count from 0.
/// - Focus: on is true when the terminal gained focus and false when it
///   lost it.
///
/// Mouse and Focus come only after MouseCapture turned them on.
export enum Event {
    /// A key with its modifiers; ch is the character for Key.Char.
    Key(key: Key, ch: char, mods: Mods)
    /// The text of a bracketed paste, or a part of a long one.
    Paste(text: str)
    /// The terminal's new size.
    Resize(cols: int, rows: int)
    /// A mouse report, once MouseCapture turned them on.
    Mouse(button: int, row: int, col: int, mods: Mods)
    /// The terminal gained focus (on) or lost it, once MouseCapture turned it
    /// on.
    Focus(on: bool)
}

/// A key: Char for any character (the character is Event.Key's ch), the
/// editing and cursor keys, and F(n) for the function keys F1 to F20.
export open enum Key {
    /// Any character; Event.Key's ch holds it.
    Char
    Enter
    Escape
    Backspace
    Tab
    Up
    Down
    Left
    Right
    Home
    End
    PageUp
    PageDown
    Insert
    Delete
    /// Function key n, F1 to F20.
    F(n: int)
}

/// The modifiers held with a key or a mouse report, as far as the terminal
/// reports them. shift is set only where the terminal's sequence states
/// it: an upper-case letter arrives as itself, without shift. alt is set
/// for a key that followed an escape, and ctrl for a control character.
export struct Mods {
    /// Shift, set only where the terminal's sequence states it.
    export shift: bool
    /// Alt, set for a key that followed an escape.
    export alt: bool
    /// Ctrl, set for a control character.
    export ctrl: bool
}

// What one step of Parse found. PSkipCsi and PSkipStr drop the bytes of a
// sequence that goes on past them: the step after skips the rest (Skip),
// so a long sequence is dropped whole with one byte of state (the Term's
// skip, DEC's csi_ignore and string states of the VT500 parser).
const PNone: u8 = 0
const PKey: u8 = 1
const PPaste: u8 = 2
const PMouse: u8 = 3
const PFocus: u8 = 4
const PSkipCsi: u8 = 5
const PSkipStr: u8 = 6

// CsiMax bounds what one step holds of a control sequence or a control
// string: a CSI with no final byte within it, or a string with no end
// within it, is dropped whole (its first CsiMax bytes, then the rest by
// Skip), so a peer cannot make the decoder wait for ever or hold more than
// this.
const CsiMax: int = 64

// The most parameters of a CSI kept; later ones are read and ignored. A
// parameter's value saturates at ParamMax, one past the last Unicode
// scalar (a kitty key code up to U+10FFFF is read whole).
const ParamCount: int = 8
const ParamMax: int = 0x11_0000

const NoMods: Mods = Mods(shift: false, alt: false, ctrl: false)

// Parsed is one step of the decoder: n bytes used (0: the bytes are the
// start of something longer, and more are needed), and what they were:
// PNone (dropped), PKey (key, ch, mods), PPaste (the start of a bracketed
// paste), PMouse (a button, b row, c col, mods), PFocus (a 1 in, 0 out), or
// PSkipCsi and PSkipStr (dropped, and the sequence goes on: Skip next).
struct Parsed {
    n: int
    kind: u8
    key: Key
    ch: char
    mods: Mods
    a: int
    b: int
    c: int
}

// A CSI's parameters: the first sub-parameter of each of the first
// ParamCount fields, the field count, and its private marker (0 none).
struct Params {
    v: [8]int
    count: int
    second: int
    private: u8
}

// KeyOf is a key step of n bytes.
KeyOf(n: int, key: Key, ch: char, mods: Mods): Parsed {
    return Parsed(n: n, kind: PKey, key: key, ch: ch, mods: mods, a: 0, b: 0, c: 0)
}

// Dropped is a step that used n bytes and found no event; Dropped(0) asks
// for more bytes.
Dropped(n: int): Parsed {
    return Parsed(n: n, kind: PNone, key: Key.Char, ch: '\0', mods: NoMods, a: 0, b: 0, c: 0)
}

// Step is one step of the decoder over b (not empty) in the state skip (0,
// or SkipOf of the step before): Skip's while a sequence is being skipped,
// else Parse's. The state after it is SkipOf(the step's kind). A step of 0
// bytes that leaves the state as it was asks for more bytes. O(len(b)) when
// skipping, else O(CsiMax); allocates nothing.
Step(b: []u8, final: bool, skip: u8): Parsed {
    if skip != 0 {
        return Skip(b, skip, final)
    }
    return Parse(b, final)
}

// SkipOf is the decoder's state after a step of kind k.
SkipOf(k: u8): u8 {
    return if k == PSkipCsi || k == PSkipStr { k } else { 0 }
}

// Parse decodes the first event of b, which is not empty. final says no
// more bytes are coming soon (the 25 ms after an escape passed): then an
// escape alone, or one that starts an unfinished sequence, is the Escape
// key, and an unfinished UTF-8 sequence is U+FFFD; otherwise such a start
// asks for more bytes (n 0). O(CsiMax); allocates nothing.
Parse(b: []u8, final: bool): Parsed {
    if b[0] == 0x1B {
        return Escaped(b, final)
    }
    return Single(b, final)
}

// Skip drops the rest of a sequence Parse began to drop (state PSkipCsi or
// PSkipStr): all of b when its end is not in b (a step of the same kind),
// else the bytes up to its end (a step of kind PNone). A CSI ends after its
// final byte (0x40 to 0x7E) and before any byte outside 0x20 to 0x7E, as
// csi_ignore does. A control string ends after BEL, CAN or SUB, after ST
// (ESC \), or before an escape that starts something else; an escape at
// the end of b waits for the next byte unless final. O(len(b)); allocates
// nothing.
Skip(b: []u8, state: u8, final: bool): Parsed {
    for i in 0..b.Len() {
        c := b[i]
        if state == PSkipCsi {
            if c >= 0x40 && c <= 0x7E {
                return Dropped(i + 1)
            }
            if c < 0x20 || c > 0x7E {
                return Dropped(i)
            }
        } else if c == 0x07 || c == 0x18 || c == 0x1A {
            return Dropped(i + 1)
        } else if c == 0x1B {
            if i + 1 < b.Len() {
                return Dropped(if b[i + 1] == b'\\' { i + 2 } else { i })
            }
            return if final { Dropped(i) } else { Skipping(i, state) }
        }
    }
    return Skipping(b.Len(), state)
}

// Skipping is a step that drops n bytes of a sequence that goes on.
Skipping(n: int, kind: u8): Parsed {
    return Parsed(n: n, kind: kind, key: Key.Char, ch: '\0', mods: NoMods, a: 0, b: 0, c: 0)
}

// IsStringStart is whether c after an escape opens a control string: OSC
// (]), DCS (P), APC (_), PM (^) or SOS (X).
IsStringStart(c: u8): bool {
    return c == b']' || c == b'P' || c == b'_' || c == b'^' || c == b'X'
}

// CtlString decodes ESC, a string opener (b[1]) and the string up to its
// end, which is dropped: a terminal's answer to a query (OSC 10, 11, 52,
// DCS DECRQSS and XTGETTCAP, kitty's APC) never reaches the program as
// keys. A string ends in ST (ESC \) or BEL, and CAN or SUB aborts it. A
// string with no end within CsiMax bytes is skipped to its end (PSkipStr).
// An escape that starts something else before any end means no terminal
// sent a string: the opener was a key typed with alt (Alt+], Alt+P,
// ...), and the bytes after it are keys, as they are when final comes with
// no end yet. Otherwise, with no end yet, it waits for more. Each decision
// rests on the first CsiMax bytes only, so how reads split the input never
// changes it. O(CsiMax); allocates nothing.
CtlString(b: []u8, final: bool): Parsed {
    var i := 2
    for {
        if i >= CsiMax {
            return Skipping(i, PSkipStr)
        }
        if i >= b.Len() {
            return if final { AltKey(b) } else { Dropped(0) }
        }
        c := b[i]
        if c == 0x07 || c == 0x18 || c == 0x1A {
            return Dropped(i + 1)
        }
        if c == 0x1B {
            if i + 1 >= b.Len() {
                // ST, or an escape that starts something else: the next
                // byte tells.
                return if final { AltKey(b) } else { Dropped(0) }
            }
            return if b[i + 1] == b'\\' { Dropped(i + 2) } else { AltKey(b) }
        }
        i += 1
    }
}

// AltKey is ESC and the key at b[1] (not an escape) as one key with alt: an
// opener with nothing after it that a terminal sent (ESC [, ESC O, a string
// opener) was typed with alt. O(1); allocates nothing.
AltKey(b: []u8): Parsed {
    p := Single(b[1..], true)
    return KeyOf(p.n + 1, p.key, p.ch, AltMods(p.mods))
}

// Single decodes one character or control key at b[0], which is not an
// escape.
Single(b: []u8, final: bool): Parsed {
    c := b[0]
    if c >= 0x80 {
        (u, n) := Utf8At(b, final)
        if n == 0 {
            return Dropped(0)
        }
        return KeyOf(n, Key.Char, Cast[char](u), NoMods)
    }
    if c >= 0x20 && c != 0x7F {
        return KeyOf(1, Key.Char, char(c), NoMods)
    }
    ctrl := Mods(shift: false, alt: false, ctrl: true)
    return match c {
        0x0D { KeyOf(1, Key.Enter, '\0', NoMods) }
        0x09 { KeyOf(1, Key.Tab, '\0', NoMods) }
        0x7F { KeyOf(1, Key.Backspace, '\0', NoMods) }
        0x00 { KeyOf(1, Key.Char, ' ', ctrl) }
        _ {
            if c <= 0x1A {
                // Ctrl+A to Ctrl+Z (0x08 is Ctrl+H, 0x0A Ctrl+J: raw mode
                // gives Enter as CR).
                KeyOf(1, Key.Char, char(0x60 + c), ctrl)
            } else {
                // 0x1C to 0x1F: Ctrl+4 to Ctrl+7.
                KeyOf(1, Key.Char, char(0x34 + c - 0x1C), ctrl)
            }
        }
    }
}

// Escaped decodes what starts with an escape at b[0]: a CSI or SS3
// sequence, a control string (dropped), a key with alt (an escape, then the
// key), or the escape alone.
Escaped(b: []u8, final: bool): Parsed {
    if b.Len() == 1 {
        return if final { KeyOf(1, Key.Escape, '\0', NoMods) } else { Dropped(0) }
    }
    d := b[1]
    if d == b'[' {
        return Csi(b, final)
    }
    if d == b'O' {
        return Ss3(b, final)
    }
    if IsStringStart(d) {
        return CtlString(b, final)
    }
    if d == 0x1B {
        // Two escapes: alt with a sequence (rxvt's Alt+Up is ESC ESC [ A),
        // the Escape key before a control string, else Alt+Escape. Never
        // deeper, so no input recurses.
        if b.Len() == 2 {
            return if final { KeyOf(2, Key.Escape, '\0', AltMods(NoMods)) } else { Dropped(0) }
        }
        if b[2] == b'[' || b[2] == b'O' || IsStringStart(b[2]) {
            p := if b[2] == b'[' {
                Csi(b[1..], final)
            } else if b[2] == b'O' {
                Ss3(b[1..], final)
            } else {
                CtlString(b[1..], final)
            }
            if p.n == 0 {
                return p
            }
            if p.n == 1 {
                // The inner escape began no sequence (final, unfinished):
                // the outer one is Alt+Escape.
                return KeyOf(2, Key.Escape, '\0', AltMods(NoMods))
            }
            if p.kind != PKey {
                // A paste's start, a report or a dropped sequence takes no
                // alt: the outer escape is the Escape key, and the
                // sequence is decoded afresh after it.
                return KeyOf(1, Key.Escape, '\0', NoMods)
            }
            return KeyOf(p.n + 1, p.key, p.ch, AltMods(p.mods))
        }
        return KeyOf(2, Key.Escape, '\0', AltMods(NoMods))
    }
    p := Single(b[1..], final)
    if p.n == 0 {
        return if final { KeyOf(1, Key.Escape, '\0', NoMods) } else { p }
    }
    return KeyOf(p.n + 1, p.key, p.ch, AltMods(p.mods))
}

// AltMods is m with alt set.
AltMods(m: Mods): Mods {
    return Mods(shift: m.shift, alt: true, ctrl: m.ctrl)
}

// ModsOf is the modifiers of xterm's parameter m: m - 1 holds shift (1),
// alt (2) and ctrl (4); meta and super (8 and up) are not kept.
ModsOf(m: int): Mods {
    if m <= 1 {
        return NoMods
    }
    bits := m - 1
    return Mods(shift: bits & 1 != 0, alt: bits & 2 != 0, ctrl: bits & 4 != 0)
}

// Ss3 decodes ESC O and its final byte: the cursor keys, Home and End in
// application mode, F1 to F4, and keypad Enter.
Ss3(b: []u8, final: bool): Parsed {
    if b.Len() < 3 {
        // ESC O alone is Alt+Shift+O.
        return if final { AltKey(b) } else { Dropped(0) }
    }
    key := match b[2] {
        b'A' { Key.Up }
        b'B' { Key.Down }
        b'C' { Key.Right }
        b'D' { Key.Left }
        b'H' { Key.Home }
        b'F' { Key.End }
        b'P' { Key.F(n: 1) }
        b'Q' { Key.F(n: 2) }
        b'R' { Key.F(n: 3) }
        b'S' { Key.F(n: 4) }
        b'M' { Key.Enter }
        _ { return Dropped(3) }
    }
    return KeyOf(3, key, '\0', NoMods)
}

// Csi decodes ESC [ (private marker) (parameters) (intermediates) final.
// A byte outside 0x20 to 0x7E ends the sequence as malformed: the bytes
// before it are dropped and it is decoded afresh. A sequence longer than
// CsiMax is dropped whole: its first CsiMax bytes here (PSkipCsi), the rest
// by Skip.
Csi(b: []u8, final: bool): Parsed {
    var p := Params(v: [0; 8], count: 0, second: -1, private: 0)
    var i := 2
    if i < b.Len() && b[i] >= 0x3C && b[i] <= 0x3F {
        p.private = b[i]
        i += 1
    }
    var field := 0
    var sub := 0
    var digits := false
    for {
        if i >= CsiMax {
            return Skipping(i, PSkipCsi)
        }
        if i >= b.Len() {
            if !final {
                return Dropped(0)
            }
            // ESC [ alone is Alt+[ (an escape before a key adds alt);
            // a longer start of a sequence is the Escape key.
            return if b.Len() == 2 { AltKey(b) } else { KeyOf(1, Key.Escape, '\0', NoMods) }
        }
        c := b[i]
        if c >= b'0' && c <= b'9' {
            digits = true
            if field < ParamCount && sub == 0 {
                p.v[field] = Saturate(p.v[field] * 10 + int(c - b'0'))
            } else if field == 1 && sub == 1 {
                prev := if p.second < 0 { 0 } else { p.second }
                p.second = Saturate(prev * 10 + int(c - b'0'))
            }
            i += 1
            continue
        }
        if c == b';' {
            field += 1
            sub = 0
            i += 1
            continue
        }
        if c == b':' {
            sub += 1
            i += 1
            continue
        }
        if c >= 0x20 && c <= 0x2F {
            // An intermediate byte: no key sequence std.term decodes has
            // one, so the sequence is read to its end and dropped.
            return DropTo(b, i, final)
        }
        if c >= 0x40 && c <= 0x7E {
            p.count = if digits || field > 0 { field + 1 } else { 0 }
            if m := Pointer(b, i + 1, &p, c, final) {
                return m
            }
            if k := Kitty(i + 1, &p, c) {
                return k
            }
            return CsiFinal(b, i + 1, &p, c)
        }
        if c >= 0x3C && c <= 0x3F {
            // A private marker out of place: the VT500 parser's
            // csi_ignore, the sequence dropped whole
            // to its final byte.
            return DropTo(b, i, final)
        }
        // A control byte, DEL or a byte past 0x7E: the bytes before it are
        // dropped, and it is decoded afresh.
        return Dropped(i)
    }
}

// DropTo finds the final byte of the sequence whose intermediate, or
// private marker out of place, is at b[i] and drops the whole sequence.
DropTo(b: []u8, i: int, final: bool): Parsed {
    var k := i
    for {
        if k >= CsiMax {
            return Skipping(k, PSkipCsi)
        }
        if k >= b.Len() {
            return if final { KeyOf(1, Key.Escape, '\0', NoMods) } else { Dropped(0) }
        }
        c := b[k]
        if c >= 0x40 && c <= 0x7E {
            return Dropped(k + 1)
        }
        if c < 0x20 || c > 0x7E {
            return Dropped(k)
        }
        k += 1
    }
}

// Saturate is v, or ParamMax when it is larger.
Saturate(v: int): int {
    return if v > ParamMax { ParamMax } else { v }
}

// CsiFinal maps a whole CSI of n bytes, parameters p and final byte f, to
// its key, or drops it.
CsiFinal(b: []u8, n: int, p: &Params, f: u8): Parsed {
    if p.private != 0 {
        return Dropped(n)
    }
    mods := if p.count >= 2 { ModsOf(p.v[1]) } else { NoMods }
    key := match f {
        b'A' { Key.Up }
        b'B' { Key.Down }
        b'C' { Key.Right }
        b'D' { Key.Left }
        b'H' { Key.Home }
        b'F' { Key.End }
        b'P' { Key.F(n: 1) }
        b'Q' { Key.F(n: 2) }
        b'S' { Key.F(n: 4) }
        b'R' {
            // CSI 1 ; m R is F3 with modifiers; CSI row ; col R is a cursor
            // position report, which std.term never asks for.
            if p.count == 2 && p.v[0] != 1 {
                return Dropped(n)
            }
            Key.F(n: 3)
        }
        b'Z' { return KeyOf(n, Key.Tab, '\0', Mods(shift: true, alt: false, ctrl: false)) }
        b'~' {
            if p.v[0] == 200 {
                return Parsed(n: n, kind: PPaste, key: Key.Char, ch: '\0', mods: NoMods, a: 0, b: 0, c: 0)
            }
            Tilde(p.v[0]) else {
                return Dropped(n)
            }
        }
        _ { return Dropped(n) }
    }
    return KeyOf(n, key, '\0', mods)
}

// Tilde is the key of CSI v ~ (the VT220 editing keypad and F5 to F20),
// or none.
Tilde(v: int): ?Key {
    return match v {
        1 | 7 { some(Key.Home) }
        2 { some(Key.Insert) }
        3 { some(Key.Delete) }
        4 | 8 { some(Key.End) }
        5 { some(Key.PageUp) }
        6 { some(Key.PageDown) }
        11 | 12 | 13 | 14 | 15 { some(Key.F(n: v - 10)) }
        17 | 18 | 19 | 20 | 21 { some(Key.F(n: v - 11)) }
        23 | 24 | 25 | 26 { some(Key.F(n: v - 12)) }
        28 | 29 { some(Key.F(n: v - 13)) }
        31 | 32 | 33 | 34 { some(Key.F(n: v - 14)) }
        _ { none }
    }
}

// Utf8At decodes the UTF-8 sequence at b[0] (a byte of 0x80 or more): the
// scalar and its length; U+FFFD and the length of the maximal subpart for
// an ill-formed sequence (Unicode 3.9, table 3-7's well-formed ranges);
// length 0 when b ends inside a sequence that could still be well formed
// and final is false.
Utf8At(b: []u8, final: bool): (u: u32, n: int) {
    c := b[0]
    var need := 0
    var lo: u8 = 0x80
    var hi: u8 = 0xBF
    var v := u32(0)
    if c >= 0xC2 && c <= 0xDF {
        need = 1
        v = u32(c & 0x1F)
    } else if c >= 0xE0 && c <= 0xEF {
        need = 2
        v = u32(c & 0x0F)
        if c == 0xE0 {
            lo = 0xA0
        } else if c == 0xED {
            hi = 0x9F
        }
    } else if c >= 0xF0 && c <= 0xF4 {
        need = 3
        v = u32(c & 0x07)
        if c == 0xF0 {
            lo = 0x90
        } else if c == 0xF4 {
            hi = 0x8F
        }
    } else {
        return (u: 0xFFFD, n: 1)
    }
    for k in 1..need + 1 {
        if k >= b.Len() {
            return if final { (u: 0xFFFD, n: k) } else { (u: 0, n: 0) }
        }
        d := b[k]
        if d < lo || d > hi {
            return (u: 0xFFFD, n: k)
        }
        lo = 0x80
        hi = 0xBF
        v = (v << 6) | u32(d & 0x3F)
    }
    return (u: v, n: need + 1)
}

// LossyLen is the length of b as UTF-8 with each ill-formed subpart
// replaced by U+FFFD (three bytes).
LossyLen(b: []u8): int {
    var i := 0
    var n := 0
    for i < b.Len() {
        if b[i] < 0x80 {
            i += 1
            n += 1
            continue
        }
        (u, n: k) := Utf8At(b[i..], true)
        n += if u == 0xFFFD && !IsFffd(b[i..]) { 3 } else { k }
        i += k
    }
    return n
}

// IsFffd is whether b starts with U+FFFD's own encoding.
IsFffd(b: []u8): bool {
    return b.Len() >= 3 && b[0] == 0xEF && b[1] == 0xBF && b[2] == 0xBD
}

// Lossy copies b into a as a str, each ill-formed UTF-8 subpart replaced
// by U+FFFD. O(n); allocates LossyLen(b) bytes in a.
Lossy(a: &Arena, b: []u8): OutOfMemory!str from a {
    var out := try a.Slice[u8](LossyLen(b))
    var i := 0
    var o := 0
    for i < b.Len() {
        if b[i] < 0x80 {
            out[o] = b[i]
            i += 1
            o += 1
            continue
        }
        (u, n: k) := Utf8At(b[i..], true)
        if u == 0xFFFD && !IsFffd(b[i..]) {
            out[o] = 0xEF
            out[o + 1] = 0xBF
            out[o + 2] = 0xBD
            o += 3
        } else {
            for j in 0..k {
                out[o + j] = b[i + j]
            }
            o += k
        }
        i += k
    }
    // Must: every byte written above is well-formed UTF-8 (ASCII, a
    // sequence Utf8At accepted, or U+FFFD), so From cannot fail.
    return str.From(out[..]).Must()
}
