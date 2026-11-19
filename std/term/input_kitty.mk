// Why: the legacy key forms cannot tell Ctrl+I from Tab, Escape from the
// start of a sequence, or Ctrl+Enter from Enter; the kitty keyboard protocol
// and xterm's modifyOtherKeys can, and a raw program needs both decoded.
//
// What: KittyKeys (the public switch), the decoder Kitty with KittyKey and
// CharKey, and the Writer-side bookkeeping KittyCount, KittyScan, KittyStep
// and KittyPopAll that pops the flags a program left pushed.
//
// How: decodes the CSI u key form of the kitty keyboard protocol
// (sw.kovidgoyal.net/kitty/keyboard-protocol, "disambiguate escape codes"
// and "report event types") and xterm's modifyOtherKeys form
// CSI 27 ; m ; code ~. The unit is self-contained: its other lines are the
// dispatch line in input.mk's Csi and, for the flags a Writer leaves pushed,
// term.mk's Term.kitty field, its KittyScan calls in WatchAlt and WatchCut,
// its counts in Note and Track, and Leave's pops; removing it deletes this
// file, those lines, the kitty child modes' line in tests_pty.mk,
// tests_kitty.mk and std/std-term_kitty_test.go.
//
// KittyKeys turns the protocol on and off; these forms are decoded
// whenever a terminal sends them, whoever asked. A key's release, which a
// terminal sends only with event reporting on, is dropped in every form,
// so it never reads as a second press. crossterm's parser is the reference
// for the mapping.
//
// A Term's Writer follows the protocol's push (CSI > flags u) and pop
// (CSI < number u: one entry when number is absent, none when it is 0, as
// kitty reads it; foot pops one for 0) in what it writes, KittyKeys' and
// any other, and counts the flags left pushed on each screen, since the
// protocol keeps one stack for the main screen and one for the alternate
// screen. Only push and pop are followed: the set form (CSI = flags ;
// mode u), which on an empty stack sets the flags of entry 0 as a push
// would, is not counted, so flags turned on that way are not popped.
// Close pops them, and so does the runtime's restore registry on a panic,
// proc.Exit or a fatal signal: the alternate screen's before it
// leaves that screen, the main screen's after. A terminal whose stack
// holds fewer entries (the protocol has a terminal drop the oldest when
// full) takes the extra pops as pops of an empty stack, which reset its
// flags.

import std.core

/// Turns the kitty keyboard protocol's "disambiguate escape codes" mode on
/// or off. On, a terminal that has the protocol reports Escape, and keys
/// with ctrl or alt that the usual encoding confuses (Ctrl+I and Tab,
/// Ctrl+Enter), in a form Term.Read tells apart. A terminal without the
/// protocol ignores both calls.
///
/// On pushes the mode on the terminal's stack (CSI > 1 u) and off pops it
/// (CSI < u), so pair each on with an off. Written through a Term's
/// Writer, the pushes left on each screen are counted, and Term.Close pops
/// them, as the runtime does on a panic, proc.Exit or a fatal signal. Only
/// pushes and pops are counted: flags a program sets with the protocol's
/// set form (CSI = flags ; mode u) are not popped for it.
///
/// Errors are w's. O(1); allocates nothing.
export KittyKeys(w: mut Writer, on: bool): WriteError!void {
    try w.Str(if on { "\x1b[>1u" } else { "\x1b[<u" })
}

// KittyCount is a Term Writer's view of the kitty flags it left pushed:
// main and alt, the pushes less the pops written on each screen,
// never below 0 and at most KittyMax; at and n, KittyStep's state across
// writes: at 0 nothing, 1 an escape, 2 a CSI, 3 a push's parameter, 4 a
// pop's; n the parameter so far (-1 none).
struct KittyCount {
    main: int
    alt: int
    at: u8
    n: int
}

// KittyMax bounds a count and a pop's parameter: past it a count stays at
// KittyMax, more than any terminal's stack holds.
const KittyMax: int = 1_000_000

// KittyZero is a KittyCount with nothing pushed and nothing scanned.
KittyZero(): KittyCount {
    return KittyCount(main: 0, alt: 0, at: 0, n: -1)
}

// KittyScan follows b for the protocol's pushes and pops, from k before
// it, with the alternate screen on (alt) and alt_at bytes of AltEnter's
// common prefix matched before it: each push or pop is counted on the
// screen in force at its last byte (AltScan's state there). pops: a pop
// is counted (false for the part of a failed write that may not be out,
// as WatchCut takes it, so a push there counts and a pop does not).
// O(len(b)); allocates nothing.
KittyScan(k: KittyCount, alt: bool, alt_at: int, b: []u8, pops: bool): KittyCount {
    var c := k
    var on := alt
    var m := alt_at
    var from := 0
    for j in 0..b.Len() {
        ev := KittyStep(mut c, b[j])
        if ev == 0 {
            continue
        }
        // The screen in force here: the alternate screen's state after the
        // bytes up to this one.
        s := AltScan(on, m, b[from..j + 1])
        on = s.alt
        m = s.at
        from = j + 1
        if ev == 1 {
            if on {
                c.alt = KittyAdd(c.alt, 1)
            } else {
                c.main = KittyAdd(c.main, 1)
            }
        } else if pops {
            // Absent: one entry. An explicit 0 counts as none, as kitty
            // pops none (foot pops one, which leaves the count one more
            // than that terminal's stack: a pop too many at Close).
            n := if c.n < 0 { 1 } else { c.n }
            if on {
                c.alt = KittyAdd(c.alt, -n)
            } else {
                c.main = KittyAdd(c.main, -n)
            }
        }
    }
    return c
}

// KittyAdd is count plus d, within 0 and KittyMax.
KittyAdd(count: int, d: int): int {
    v := count + d
    return if v < 0 { 0 } else if v > KittyMax { KittyMax } else { v }
}

// KittyStep is KittyScan's step at byte x: 1 when x ends a push, 2 when it
// ends a pop (its parameter in c.n), else 0. An escape anywhere starts a
// new match; any other byte that does not continue one ends it. O(1).
KittyStep(c: mut KittyCount, x: u8): int {
    st := c.at
    if st == 3 || st == 4 {
        if x >= b'0' && x <= b'9' {
            d := int(x - b'0')
            v := if c.n < 0 { d } else { c.n * 10 + d }
            c.n = if v > KittyMax { KittyMax } else { v }
            return 0
        }
        if x == b'u' {
            c.at = 0
            return if st == 3 { 1 } else { 2 }
        }
    } else if st == 2 {
        if x == b'>' || x == b'<' {
            c.at = if x == b'>' { 3 } else { 4 }
            c.n = -1
            return 0
        }
    } else if st == 1 {
        if x == b'[' {
            c.at = 2
            return 0
        }
    }
    c.at = if x == 0x1B { 1 } else { 0 }
    return 0
}

// KittyPopMost bounds the pops Close writes for one screen: the registry's
// cap (std/rt/term.mk's TermKittyMax), more than any terminal's stack
// holds, so a count saturated by a runaway program costs Close at most
// 16 writes.
const KittyPopMost: int = 255

// KittyPopAll writes n pops (CSI < u), at most KittyPopMost, to h, each
// write whole (WriteAll), at most 16 to a write: the failed write's
// result, or a success. O(1) system calls (at most 16); allocates nothing.
KittyPopAll(io: Io, h: uint, n: int): core.IoResult {
    var b: [64]u8 = [0; 64]
    pop := "\x1b[<u".AsBytes()
    for j in 0..64 {
        b[j] = pop[j % 4]
    }
    var left := if n > KittyPopMost { KittyPopMost } else { n }
    for left > 0 {
        k := if left > 16 { 16 } else { left }
        r := WriteAll(io, h, b[0..k * 4])
        if r.err != core.SysErr.None {
            return r
        }
        left -= k
    }
    return core.IoResult(value: 0, err: core.SysErr.None, os: 0)
}

// Kitty decodes, at a whole CSI of n bytes whose parameters are p and
// whose final byte is f: a release (event type 3, the second field's
// sub-parameter) of any key form, which is dropped; CSI code ; mods u; and
// CSI 27 ; mods ; code ~. none for any other CSI, which CsiFinal maps.
// O(1); allocates nothing.
Kitty(n: int, p: &Params, f: u8): ?Parsed {
    if p.private != 0 {
        return none
    }
    if p.second == 3 && p.count >= 2 && IsKeyFinal(f) {
        return some(Dropped(n))
    }
    if f == b'u' && p.count >= 1 {
        mods := if p.count >= 2 { ModsOf(p.v[1]) } else { NoMods }
        return some(KittyKey(n, p.v[0], mods))
    }
    if f == b'~' && p.count == 3 && p.v[0] == 27 {
        return some(KittyKey(n, p.v[2], ModsOf(p.v[1])))
    }
    return none
}

// IsKeyFinal is whether f ends a key sequence of xterm's or kitty's.
IsKeyFinal(f: u8): bool {
    return match f {
        b'A' | b'B' | b'C' | b'D' | b'E' | b'H' | b'F' | b'P' | b'Q' | b'R' | b'S' | b'~' | b'u' { true }
        _ { false }
    }
}

// KittyKey is the key step of n bytes for the kitty key code code with
// mods: the functional codes are their keys (the keypad's are its keys and
// characters; 57376 to 57383 are F13 to F20), and any other code is
// CharKey's.
KittyKey(n: int, code: int, mods: Mods): Parsed {
    key := match code {
        13 | 57414 { Key.Enter }
        9 { Key.Tab }
        27 { Key.Escape }
        127 { Key.Backspace }
        57417 { Key.Left }
        57418 { Key.Right }
        57419 { Key.Up }
        57420 { Key.Down }
        57421 { Key.PageUp }
        57422 { Key.PageDown }
        57423 { Key.Home }
        57424 { Key.End }
        57425 { Key.Insert }
        57426 { Key.Delete }
        _ {
            if code >= 57376 && code <= 57383 {
                Key.F(n: code - 57363)
            } else {
                return CharKey(n, code, mods)
            }
        }
    }
    return KeyOf(n, key, '\0', mods)
}

// CharKey is the Char step of n bytes for code: a keypad character, or the
// scalar code itself; dropped for a control, a surrogate, one of kitty's
// functional codes no Key names (locks, media and modifier keys: 57344 to
// 57454), or a code past U+10FFFF (a saturated parameter, ParamMax,
// among them).
CharKey(n: int, code: int, mods: Mods): Parsed {
    if c := KeypadChar(code) {
        return KeyOf(n, Key.Char, c, mods)
    }
    if code < 0x20 || code == 0x7F || code >= 0xD800 && code <= 0xDFFF || code >= 57344 && code <= 57454 || code > 0x10_FFFF {
        return Dropped(n)
    }
    return KeyOf(n, Key.Char, Cast[char](Trunc[u32](code)), mods)
}

// KeypadChar is the character of a keypad key code (KP_0 57399 to
// KP_SEPARATOR 57416, Enter aside), or none.
KeypadChar(code: int): ?char {
    if code >= 57399 && code <= 57408 {
        return some(char(Trunc[u8](code - 57399) + b'0'))
    }
    return match code {
        57409 { some('.') }
        57410 { some('/') }
        57411 { some('*') }
        57412 { some('-') }
        57413 { some('+') }
        57415 { some('=') }
        57416 { some(',') }
        _ { none }
    }
}
