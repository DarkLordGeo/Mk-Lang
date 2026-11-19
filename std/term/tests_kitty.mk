// Why: kitty and modifyOtherKeys keys must decode right, and the flags a
// program leaves pushed must be popped on every way out.
// What: decoder blocks over recorded streams and pseudo-terminal blocks of
// the pops on Close, panic, proc.Exit and alone.
// How: the pty blocks reduce the terminal's bytes to tokens (TmKittyName)
// and compare them with TmKittySame.

// Test blocks for the kitty keyboard unit (input_kitty.mk): CSI u and
// modifyOtherKeys keys over recorded byte streams, releases dropped, and the
// same keys typed on a pseudo-terminal. A Go test of package std checks the
// decoder against its model.

import std.fs
import std.proc
import std.raw

test "Parse: kitty CSI u keys give Escape, ctrl and alt letters, Enter with ctrl, the keypad and F13 to F20" (sys: &System) {
    got := DecodeAll(sys.arena, "\x1b[27u\x1b[97;5u\x1b[105;5u\x1b[13;5u\x1b[9;2u\x1b[127;3u\x1b[57399u\x1b[57414u\x1b[57376u\x1b[57383;5u\x1b[233u".AsBytes())
    Assert(got == "Escape/5 Char[a]+c/7 Char[i]+c/8 Enter+c/7 Tab+s/6 Bs+a/8 Char[0]/8 Enter/8 F13/8 F20+c/10 Char[\u{e9}]/6 ")
}

test "Parse: xterm's modifyOtherKeys form CSI 27 ; m ; code tilde is the same key" (sys: &System) {
    got := DecodeAll(sys.arena, "\x1b[27;5;105~\x1b[27;2;13~".AsBytes())
    Assert(got == "Char[i]+c/11 Enter+s/10 ")
}

test "Parse: a release in any key form is dropped, a repeat is a key, and codes that name no key are dropped" (sys: &System) {
    got := DecodeAll(sys.arena, "\x1b[97;1:3u\x1b[1;1:3A\x1b[3;1:3~\x1b[97;1:2u\x1b[57441u\x1b[55296u\x1b[1u\x1b[9999999u\x1b[u".AsBytes())
    Assert(got == "-/9 -/8 -/8 Char[a]/9 -/8 -/8 -/4 -/10 -/3 ")
}

test "KittyKeys pushes flag 1 and pops it" (sys: &System) {
    var t := Text.New(sys.arena)
    var w := t.Writer()
    KittyKeys(mut w, true).Must()
    KittyKeys(mut w, false).Must()
    w.Flush().Must()
    Assert(t.Str() == "\x1b[>1u\x1b[<u")
}

test "Term on a pseudo-terminal with KittyKeys on reads kitty CSI u keys and drops their releases" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    var keys: [5]str = ["\x1b[27u", "\x1b[97;5u", "\x1b[97;5:3u", "\x1b[13;5u", "q"]
    (code, text) := TmRun(sys, "kitty", keys[..], -1)
    Assert(code == 0)
    Assert(TmSame(sys, text, "key Escape\nkey Char a+c\nkey Enter+c\nkey Char q\n"))
}

test "a kitty key code in the private-use planes up to U+10FFFF is its key, and a code past U+10FFFF is dropped" (sys: &System) {
    a := sys.arena
    Assert(DecodeAll(a, "\x1b[1048576u".AsBytes()) == "Char[\u{100000}]/10 ")
    Assert(DecodeAll(a, "\x1b[1000000u\x1b[1114111;5u".AsBytes()) == "Char[\u{F4240}]/10 Char[\u{10FFFF}]+c/12 ")
    Assert(DecodeAll(a, "\x1b[1114112u\x1b[99999999u".AsBytes()) == "-/10 -/11 ")
}

// TmKittyChild is the child half of the kitty registry blocks below
// on t in raw mode; it never returns.
TmKittyChild(sys: &System, t: Term, mode: str, path: str): never {
    io := sys.io
    var term := t
    var buf: [256]u8 = [0; 256]
    var w := term.Writer(io, mut buf[..])
    if mode == "kexit" || mode == "konly" {
        // The main screen only: three pushes (one with other flags), a pop
        // of two, then one more push: two left.
        KittyKeys(mut w, true) else { proc.Exit(72) }
        w.Str("\x1b[>3u") else { proc.Exit(72) }
        KittyKeys(mut w, true) else { proc.Exit(72) }
        w.Str("\x1b[<2u") else { proc.Exit(72) }
        KittyKeys(mut w, true) else { proc.Exit(72) }
    } else {
        // One flag pushed on the main screen before the alternate screen
        // is entered, then one (kpanic) or two (kclose) on it.
        KittyKeys(mut w, true) else { proc.Exit(72) }
        AltScreen(mut w, true) else { proc.Exit(72) }
        KittyKeys(mut w, true) else { proc.Exit(72) }
        if mode == "kclose" {
            KittyKeys(mut w, true) else { proc.Exit(72) }
        }
    }
    w.Str("ready\r\n") else { proc.Exit(72) }
    w.Flush() else { proc.Exit(72) }
    // The Writer counted the flags per screen, and the runtime's record
    // holds them.
    want_alt := if mode == "kpanic" { 1 } else if mode == "kclose" { 2 } else { 0 }
    want_main := if mode == "kexit" || mode == "konly" { 2 } else { 1 }
    if term.kitty.main != want_main || term.kitty.alt != want_alt || term.reg < 0 {
        proc.Exit(83)
    }
    if mode == "kclose" {
        // Close pops what is left, and removes the record: proc.Exit
        // writes nothing more.
        term.Close(io)
        fs.WriteAll(io, path, "closed\n".AsBytes()) else { proc.Exit(74) }
        proc.Exit(0)
    }
    if mode == "kexit" {
        fs.WriteAll(io, path, "raw kitty\n".AsBytes()) else { proc.Exit(74) }
        proc.Exit(9)
    }
    if mode == "konly" {
        // Restore puts the mode back and leaves the flags; the record then
        // holds only them (no mode, the main screen), and proc.Exit pops
        // them. "again" is the mark the block counts. The block does not
        // compare where the pops fall against it (TmKittySame): two pops
        // in all show that Restore popped none, since it left the count at
        // 2 (checked below) and the exit pops that many.
        term.Restore(io) else { proc.Exit(84) }
        if term.reg < 0 || term.saved_len != 0 || term.alt || term.kitty.main != 2 {
            proc.Exit(85)
        }
        var again: [64]u8 = [0; 64]
        var w2 := term.Writer(io, mut again[..])
        w2.Str("again\r\n") else { proc.Exit(72) }
        w2.Flush() else { proc.Exit(72) }
        fs.WriteAll(io, path, "kitty only\n".AsBytes()) else { proc.Exit(74) }
        proc.Exit(6)
    }
    fs.WriteAll(io, path, "raw alt kitty\n".AsBytes()) else { proc.Exit(74) }
    Panic("std.term pty block: a panic with kitty flags pushed on both screens")
}

// TmDrainKitty is TmDrain that keeps the first 32 KiB the terminal showed
// and, after each read, stores their tokens (TmKittyCode) in tokens.
TmDrainKitty(io: Io, p: &proc.Pty, mark: str, tokens: &raw.Atomic[u64]) {
    var all: [32768]u8 = [0; 32768]
    var have := 0
    var b: [4096]u8 = [0; 4096]
    for {
        n := p.Read(io, mut b[..]) else {
            return
        }
        if n == 0 {
            return
        }
        for i in 0..n {
            if have < all.Len() {
                all[have] = b[i]
                have += 1
            }
        }
        tokens.Store(TmKittyCode(all[0..have], mark))
    }
}

// The tokens of TmKittyCode, 4 bits each: a flag-1 push (U), a pop (P),
// the alternate screen's enter (E) and leave (L), and the mark (R).
const TmKittyLetters: str = "-UPELR"

// TmKittyCode is the tokens in b, in order, 4 bits each, the first in the
// highest bits used, after a 1 that marks their start; all ones past 15.
TmKittyCode(b: []u8, mark: str): u64 {
    var code: u64 = 1
    var count := 0
    for i in 0..b.Len() {
        rest := b[i..]
        var t: u64 = 0
        if TmStarts(rest, "\x1b[>1u") {
            t = 1
        } else if TmStarts(rest, "\x1b[<u") {
            t = 2
        } else if TmStarts(rest, "\x1b[?1049h") {
            t = 3
        } else if TmStarts(rest, "\x1b[?1049l") {
            t = 4
        } else if TmStarts(rest, mark) {
            t = 5
        }
        if t != 0 {
            count += 1
            if count > 15 {
                return ~u64(0)
            }
            code = (code << 4) | t
        }
    }
    return code
}

// TmKittyName is code's tokens as letters (TmKittyLetters), or "many".
TmKittyName(a: &Arena, code: u64): str from a {
    if code == ~u64(0) {
        return "many"
    }
    var t := Text.New(a)
    var c := code
    var n := 0
    for c > 1 {
        c = c >> 4
        n += 1
    }
    letters := TmKittyLetters.AsBytes()
    var k := n
    for k > 0 {
        k -= 1
        t.PushChar(char(letters[Trunc[int]((code >> Cast[u64](k * 4)) & 15)])).Must()
    }
    return t.Done()
}

// TmStarts is whether b begins with s.
TmStarts(b: []u8, s: str): bool {
    p := s.AsBytes()
    if b.Len() < p.Len() {
        return false
    }
    for i in 0..p.Len() {
        if b[i] != p[i] {
            return false
        }
    }
    return true
}

// TmKittySame is whether got and want, tokens as TmKittyName gives them,
// have the same tokens other than the mark R, in the same order, and the
// same number of marks. Where the mark falls among the others is not
// compared: on a Windows pseudo-console the registry's pops have reached
// the terminal before text the child wrote earlier (the
// konly block saw UUUPPR for UUURPP), so the mark's place depends on
// timing. The order of the pushes, pops, enters and leaves is compared.
TmKittySame(got: str, want: str): bool {
    g := got.AsBytes()
    w := want.AsBytes()
    var i := 0
    var j := 0
    var rg := 0
    var rw := 0
    for {
        for i < g.Len() && g[i] == b'R' {
            rg += 1
            i += 1
        }
        for j < w.Len() && w[j] == b'R' {
            rw += 1
            j += 1
        }
        if i == g.Len() || j == w.Len() {
            break
        }
        if g[i] != w[j] {
            return false
        }
        i += 1
        j += 1
    }
    return i == g.Len() && j == w.Len() && rg == rw
}

// TmKittyAfter runs mode on a new pseudo-terminal between two probes, as
// TmModeAfter does, and returns the tokens (TmKittyCode, with mark, as
// letters) of what the terminal showed; it waits up to 2 s after the
// child ended for them to be want (TmKittySame). On Windows the probes'
// before == after shows nothing about the mode: no hold child keeps the
// pseudo-console, which resets its modes when its last client exits
// tests_pty.mk's hold-child blocks cover the mode's restore.
TmKittyAfter(sys: &System, mode: str, mark: str, want: str): (before: str, after: str, code: int, tokens: str) from sys {
    io := sys.io
    p := proc.Pty.Open(io, 80, 24).Must()
    before := TmOnPty(sys, &p, "probe")
    dir := fs.TempDir(sys.arena, io).Must()
    var r: [8]u8 = [0; 8]
    io.Random(mut r[..]).Must()
    tag := u64(r[0]) | (u64(r[1]) << 8) | (u64(r[2]) << 16) | (u64(r[3]) << 24) | (u64(r[4]) << 32)
    path := sys.arena.Fmt("{dir}/mkterm-{mode}-{tag}.txt", (dir, mode, tag)).Must()
    var a: [4]str = [sys.args[0], sys.args[1], mode, path]
    var c := proc.DefaultCommand
    c.args = a[..]
    c.terminal = some(&p)
    (child, stdin, stdout, stderr) := proc.Spawn(sys.arena, io, &c).Must()
    TmCloseEnds(stdin, stdout, stderr)
    var tokens := raw.Atomic[u64].New(1)
    var code := -1000
    scope s := io.Scope() {
        s.go TmDrainKitty(io, &p, mark, &tokens)
        st := child.Wait(io).Must()
        code = st.code else { -1000 }
        // What the child wrote before it ended is read by the drain within
        // 2 s, or never.
        for _ in 0..200 {
            if TmKittySame(TmKittyName(sys.arena, tokens.Load()), want) {
                break
            }
            io.Sleep(TmMs(10)).Must()
        }
        s.Cancel()
    }
    fs.Remove(io, path) else {}
    after := TmOnPty(sys, &p, "probe")
    p.Close()
    return (before, after, code, tokens: TmKittyName(sys.arena, tokens.Load()))
}

// The kitty keyboard flags a Writer left pushed are in the
// runtime's record, one count per screen. The blocks assert on the
// terminal's bytes, as the mouse blocks do, since no probe can read the
// flags: the
// tokens are U a flag-1 push, E and L the alternate screen's enter and
// leave, P a pop (CSI < u) and R the mark; pushes with other flags and
// pops with a count are written but not tokens. A Windows pseudo-console
// passes the protocol's sequences on (measured on this host: a push before
// the mark reached the pseudo-terminal's reader). The mark's place among
// the other tokens is not compared (TmKittySame), only that it came.
test "A panic with a kitty flag pushed on the main screen and one on the alternate screen pops the alternate screen's, leaves it, then pops the main screen's" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    (before, after, code, tokens) := TmKittyAfter(sys, "kpanic", "ready", "UEURPLP")
    Assert(code == 101)
    Assert(before.Len() > 0 && before == after)
    Assert(TmKittySame(tokens, "UEURPLP"))
}

test "proc.Exit with two kitty flags left pushed on the main screen pops both" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    (before, after, code, tokens) := TmKittyAfter(sys, "kexit", "ready", "UUURPP")
    Assert(code == 9)
    Assert(before.Len() > 0 && before == after)
    Assert(TmKittySame(tokens, "UUURPP"))
}

test "Term.Close pops the kitty flags left pushed on the alternate screen, leaves it, pops the main screen's, and the exit after pops none" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    (before, after, code, tokens) := TmKittyAfter(sys, "kclose", "ready", "UEUURPPLP")
    Assert(code == 0)
    Assert(before.Len() > 0 && before == after)
    Assert(TmKittySame(tokens, "UEUURPPLP"))
}

test "proc.Exit with only kitty flags left (Restore put the mode back and left them) pops them" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    (before, after, code, tokens) := TmKittyAfter(sys, "konly", "again", "UUURPP")
    Assert(code == 6)
    Assert(before.Len() > 0 && before == after)
    Assert(TmKittySame(tokens, "UUURPP"))
}

test "KittyScan counts pushes and pops on the screen in force, across writes split anywhere" (sys: &System) {
    // Main: a push. Alternate: pushes of flags 5 and 1, a pop. Main again:
    // a push with no flags, a pop of 0 (none), a pop of 9 (floored at 0),
    // a push. One flag is left on each screen.
    b := "\x1b[>1u\x1b[?1049h\x1b[>5u\x1b[>1u\x1b[<u\x1b[?1049l\x1b[>u\x1b[<0u\x1b[<9u\x1b[>1u".AsBytes()
    for cut in 0..b.Len() + 1 {
        k1 := KittyScan(KittyZero(), false, 0, b[0..cut], true)
        s := AltScan(false, 0, b[0..cut])
        k2 := KittyScan(k1, s.alt, s.at, b[cut..], true)
        Assert(k2.main == 1 && k2.alt == 1 && k2.at == 0)
    }
    // A pop with no number pops one entry; a pop of 0 pops none, as kitty
    // reads it (foot pops one: the count is then a pop too many).
    Assert(KittyScan(KittyZero(), false, 0, "\x1b[>1u\x1b[>1u\x1b[<u".AsBytes(), true).main == 1)
    Assert(KittyScan(KittyZero(), false, 0, "\x1b[>1u\x1b[<0u".AsBytes(), true).main == 1)
    Assert(KittyScan(KittyZero(), false, 0, "\x1b[>1u\x1b[>1u\x1b[<1u".AsBytes(), true).main == 1)
    // Not sequences: another final, an escape inside, a byte between the
    // digits; an escape restarts the match.
    k := KittyScan(KittyZero(), false, 0, "\x1b[>1x\x1b[>1\x1bu\x1b[>12a3u\x1b\x1b[>1u[>1u".AsBytes(), true)
    Assert(k.main == 1 && k.alt == 0)
    // A pop the scan does not take (the rest of a failed write) leaves the
    // count; a push there counts.
    p := KittyScan(k, false, 0, "\x1b[<u\x1b[>1u".AsBytes(), false)
    Assert(p.main == 2)
    // A pop's parameter saturates; the count stays at 0.
    q := KittyScan(p, false, 0, "\x1b[<99999999999999999999u".AsBytes(), true)
    Assert(q.main == 0 && q.n == KittyMax)
    Assert(KittyAdd(KittyMax, 1) == KittyMax && KittyAdd(0, -1) == 0)
}
