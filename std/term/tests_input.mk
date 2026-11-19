// Why: the input decoder sees every byte a user types, and a wrong or
// stuck decode breaks every terminal program.
// What: test blocks for input.mk's Step, Parse and Lossy over recorded streams.
// How: DecodeAll prints each step as text and the blocks compare it with
// the expected keys, drops and pastes.

// Test blocks for the input decoder (input.mk) over recorded byte
// streams: the sequences xterm sends for each key (ctlseqs, "PC-Style
// Function Keys"), with modifiers, alt prefixes, UTF-8, and malformed and
// truncated input. A Go test of package std checks the same decoder
// against a Go model over generated streams and fuzzes it.

// DecodeAll decodes all of b with Step (final), one step after another,
// the skip state carried, and returns the steps as text: "<key>[<ch>]<mods>/<n> "
// for a key (ch for Char only; mods +s, +a, +c), "P/<n> " for a paste
// start, "-/<n> " for a dropped sequence or part of one. A step of no
// bytes (a skipped sequence ended before an escape) prints nothing.
DecodeAll(a: &Arena, b: []u8): str from a {
    var t := Text.New(a)
    var i := 0
    var skip: u8 = 0
    for i < b.Len() {
        p := Step(b[i..], true, skip)
        was := skip
        skip = SkipOf(p.kind)
        Assert(p.n > 0 || skip != was)
        if p.n > 0 {
            t.Push(StepText(a, &p)).Must()
        }
        i += p.n
    }
    return t.Done()
}

// StepText is one step of DecodeAll.
StepText(a: &Arena, p: &Parsed): str from a {
    n := p.n
    if p.kind == PPaste {
        return a.Fmt("P/{n} ", (n)).Must()
    }
    if p.kind != PKey {
        return a.Fmt("-/{n} ", (n)).Must()
    }
    name := KeyName(p.key)
    ch := match p.key {
        .Char { a.Fmt("[{c}]", (c: p.ch)).Must() }
        _ { "" }
    }
    s := if p.mods.shift { "+s" } else { "" }
    al := if p.mods.alt { "+a" } else { "" }
    c := if p.mods.ctrl { "+c" } else { "" }
    return a.Fmt("{name}{ch}{s}{al}{c}/{n} ", (name, ch, s, al, c, n)).Must()
}

KeyName(k: Key): str {
    return match k {
        .Char { "Char" }
        .Enter { "Enter" }
        .Escape { "Escape" }
        .Backspace { "Bs" }
        .Tab { "Tab" }
        .Up { "Up" }
        .Down { "Down" }
        .Left { "Left" }
        .Right { "Right" }
        .Home { "Home" }
        .End { "End" }
        .PageUp { "PgUp" }
        .PageDown { "PgDn" }
        .Insert { "Ins" }
        .Delete { "Del" }
        .F(n) { ["F0", "F1", "F2", "F3", "F4", "F5", "F6", "F7", "F8", "F9", "F10", "F11", "F12", "F13", "F14", "F15",
            "F16", "F17", "F18", "F19", "F20"][n] }
    }
}

test "Parse: printable ASCII and control keys map as crossterm's Unix parser does" (sys: &System) {
    got := DecodeAll(sys.arena, "a\r\t\x7f\x00\x01\x08\x1c".AsBytes())
    Assert(got == "Char[a]/1 Enter/1 Tab/1 Bs/1 Char[ ]+c/1 Char[a]+c/1 Char[h]+c/1 Char[4]+c/1 ")
}

test "Parse: CSI cursor keys, with and without the modifier parameter" (sys: &System) {
    got := DecodeAll(sys.arena, "\x1b[A\x1b[1;5B\x1b[1;2C\x1b[1;3D\x1b[H\x1b[F".AsBytes())
    Assert(got == "Up/3 Down+c/6 Right+s/6 Left+a/6 Home/3 End/3 ")
}

test "Parse: SS3 keys of application mode and F1 to F4" (sys: &System) {
    got := DecodeAll(sys.arena, "\x1bOA\x1bOP\x1bOS\x1bOM".AsBytes())
    Assert(got == "Up/3 F1/3 F4/3 Enter/3 ")
}

test "Parse: the VT220 editing keys and F5 to F12 by CSI n tilde" (sys: &System) {
    got := DecodeAll(sys.arena, "\x1b[2~\x1b[3~\x1b[5~\x1b[6~\x1b[15~\x1b[24~\x1b[3;5~".AsBytes())
    Assert(got == "Ins/4 Del/4 PgUp/4 PgDn/4 F5/5 F12/5 Del+c/6 ")
}

test "Parse: back-tab is Tab with shift" (sys: &System) {
    Assert(DecodeAll(sys.arena, "\x1b[Z".AsBytes()) == "Tab+s/3 ")
}

test "Parse: an escape before a key is alt, and two escapes before a sequence are alt with it" (sys: &System) {
    got := DecodeAll(sys.arena, "\x1bx\x1b\r\x1b\x1b[A".AsBytes())
    Assert(got == "Char[x]+a/2 Enter+a/2 Up+a/4 ")
}

test "Parse: an escape before a paste's start or a dropped sequence is the Escape key, then the sequence" (sys: &System) {
    got := DecodeAll(sys.arena, "\x1b\x1b[200~\x1b\x1b[?1;2c\x1b\x1bOx".AsBytes())
    Assert(got == "Escape/1 P/6 Escape/1 -/7 Escape/1 -/3 ")
}

test "Parse: a lone escape asks for more bytes until final, then is Escape" (sys: &System) {
    Assert(Parse("\x1b".AsBytes(), false).n == 0)
    Assert(Parse("\x1b[".AsBytes(), false).n == 0)
    Assert(Parse("\x1b[1;".AsBytes(), false).n == 0)
    p := Parse("\x1b[1;".AsBytes(), true)
    Assert(p.n == 1 && p.kind == PKey && IsEscape(p.key))
}

test "Parse: UTF-8 keys, and U+FFFD for each maximal ill-formed subpart" (sys: &System) {
    Assert(DecodeAll(sys.arena, "\u{e9}\u{20ac}\u{1F600}".AsBytes()) == "Char[\u{e9}]/2 Char[\u{20ac}]/3 Char[\u{1F600}]/4 ")
    var bad: [5]u8 = [0xE2, 0x82, 0x41, 0xC0, 0xF5]
    Assert(DecodeAll(sys.arena, bad[..]) == "Char[\u{FFFD}]/2 Char[A]/1 Char[\u{FFFD}]/1 Char[\u{FFFD}]/1 ")
    var cut: [2]u8 = [0xF0, 0x9F]
    Assert(Parse(cut[..], false).n == 0)
}

test "Parse: a bracketed paste's start, and unknown or private sequences are dropped whole" (sys: &System) {
    got := DecodeAll(sys.arena, "\x1b[200~\x1b[?1;2c\x1b[99~\x1b[5;7R\x1b[ q".AsBytes())
    Assert(got == "P/6 -/7 -/5 -/6 -/4 ")
}

test "Parse: a CSI with no final byte within 64 bytes is dropped, never waited on" (sys: &System) {
    var b: [100]u8 = [b'1'; 100]
    b[0] = 0x1B
    b[1] = b'['
    p := Parse(b[..], false)
    Assert(p.n == 64 && p.kind == PSkipCsi)
}

// Repeat is s n times.
Repeat(a: &Arena, s: str, n: int): str from a {
    var t := Text.New(a)
    for _ in 0..n {
        t.Push(s).Must()
    }
    return t.Done()
}

// Keys is how many key steps text (DecodeAll's) holds.
Keys(text: str): int {
    var n := 0
    for c in text.AsBytes() {
        if c == b'/' {
            n += 1
        }
    }
    return n - Drops(text)
}

// Drops is how many dropped steps text (DecodeAll's) holds.
Drops(text: str): int {
    b := text.AsBytes()
    var n := 0
    for i in 0..b.Len() {
        if b[i] == b'-' && i + 1 < b.Len() && b[i + 1] == b'/' && (i == 0 || b[i - 1] == b' ') {
            n += 1
        }
    }
    return n
}

test "a CSI longer than 64 bytes is dropped whole, its tail never read as keys" (sys: &System) {
    a := sys.arena
    long := a.Fmt("\x1b[{d}A", (d: Repeat(a, "1", 70))).Must()
    Assert(DecodeAll(a, long.AsBytes()) == "-/64 -/9 ")
    // With an intermediate byte (DropTo), and with a key after it.
    inter := a.Fmt("\x1b[{d}qz", (d: Repeat(a, " ", 70))).Must()
    Assert(DecodeAll(a, inter.AsBytes()) == "-/64 -/9 Char[z]/1 ")
    // Every length from 60 to 300 between two keys: the two keys and
    // nothing else, whatever the decoder's model says.
    for k in 60..300 {
        s := a.Fmt("a\x1b[{d}~b", (d: Repeat(a, "9", k))).Must()
        got := DecodeAll(a, s.AsBytes())
        Assert(Keys(got) == 2)
        Assert(got.StartsWith("Char[a]/1 ") && got.EndsWith("Char[b]/1 "))
    }
    // A control byte ends the skipped sequence and is decoded afresh.
    ctl := a.Fmt("\x1b[{d}\r", (d: Repeat(a, "1", 70))).Must()
    Assert(DecodeAll(a, ctl.AsBytes()) == "-/64 -/8 Enter/1 ")
}

test "Skip consumes a long CSI's rest as it arrives, then ends at its final byte" (sys: &System) {
    p := Skip("1;2;3".AsBytes(), PSkipCsi, false)
    Assert(p.n == 5 && p.kind == PSkipCsi)
    q := Skip("9Ax".AsBytes(), PSkipCsi, false)
    Assert(q.n == 2 && q.kind == PNone)
    r := Skip("\x1b[A".AsBytes(), PSkipCsi, false)
    Assert(r.n == 0 && r.kind == PNone)
}

test "OSC, DCS and APC strings are dropped whole, ended by ST or BEL" (sys: &System) {
    a := sys.arena
    Assert(DecodeAll(a, "\x1b]11;rgb:0000/0000/0000\x1b\\q".AsBytes()) == "-/25 Char[q]/1 ")
    Assert(DecodeAll(a, "\x1b]10;rgb:ffff/ffff/ffff\x07q".AsBytes()) == "-/24 Char[q]/1 ")
    Assert(DecodeAll(a, "\x1bP1$r0m\x1b\\".AsBytes()) == "-/9 ")
    Assert(DecodeAll(a, "\x1b_Gi=1;OK\x1b\\".AsBytes()) == "-/11 ")
    Assert(DecodeAll(a, "\x1b^pm\x1b\\\x1bXsos\x1b\\".AsBytes()) == "-/6 -/7 ")
    // CAN aborts a string. (An escape that starts something else before
    // any end is no string: see the opener-then-keys block below.)
    Assert(DecodeAll(a, "\x1b]2;t\x18z".AsBytes()) == "-/6 Char[z]/1 ")
    // OSC 52 with a long payload: dropped whole, through Skip.
    clip := a.Fmt("\x1b]52;c;{d}\x07k", (d: Repeat(a, "QUFB", 500))).Must()
    got := DecodeAll(a, clip.AsBytes())
    Assert(Keys(got) == 1 && got.EndsWith("Char[k]/1 "))
}

test "an opener alone at the escape timeout is still the key typed with alt" (sys: &System) {
    a := sys.arena
    // Not final: wait for the rest of a possible string.
    Assert(Parse("\x1b]".AsBytes(), false).n == 0)
    Assert(Parse("\x1bPab".AsBytes(), false).n == 0)
    Assert(Parse("\x1b]2;t\x1b".AsBytes(), false).n == 0)
    // Final: Alt+] and Alt+P, and the bytes after are keys.
    Assert(DecodeAll(a, "\x1b]".AsBytes()) == "Char[]]+a/2 ")
    Assert(DecodeAll(a, "\x1bPab".AsBytes()) == "Char[P]+a/2 Char[a]/1 Char[b]/1 ")
    Assert(DecodeAll(a, "\x1b_".AsBytes()) == "Char[_]+a/2 ")
    // Two escapes before a string: the Escape key, then the string.
    Assert(DecodeAll(a, "\x1b\x1b]11;x\x07".AsBytes()) == "Escape/1 -/7 ")
}

test "a string with no end within 64 bytes is skipped to its end, its body never read as keys" (sys: &System) {
    a := sys.arena
    for k in 0..300 {
        s := a.Fmt("a\x1bP{d}\x1b\\b", (d: Repeat(a, "x", k))).Must()
        got := DecodeAll(a, s.AsBytes())
        Assert(Keys(got) == 2)
        Assert(got.StartsWith("Char[a]/1 ") && got.EndsWith("Char[b]/1 "))
    }
    // Unterminated and long: final or not, its body is dropped.
    long := a.Fmt("\x1b]{d}", (d: Repeat(a, "y", 100))).Must()
    Assert(Parse(long.AsBytes(), false).kind == PSkipStr)
    Assert(Keys(DecodeAll(a, long.AsBytes())) == 0)
}

test "Lossy: ill-formed UTF-8 in a paste becomes U+FFFD" (sys: &System) {
    var b: [4]u8 = [b'a', 0xFF, 0xE2, 0x82]
    Assert(Lossy(sys.arena, b[..]).Must() == "a\u{FFFD}\u{FFFD}")
}

IsEscape(k: Key): bool {
    return match k {
        .Escape { true }
        _ { false }
    }
}

test "a string opener followed by keys and then an escape that starts something else is the key typed with alt, and the keys after it are kept" (sys: &System) {
    a := sys.arena
    Assert(DecodeAll(a, "\x1bXab\x1b[A".AsBytes()) == "Char[X]+a/2 Char[a]/1 Char[b]/1 Up/3 ")
    Assert(DecodeAll(a, "\x1b]2;t\x1b[A".AsBytes()) == "Char[]]+a/2 Char[2]/1 Char[;]/1 Char[t]/1 Up/3 ")
    // Alt+] typed twice in one read: two keys, not a string.
    Assert(DecodeAll(a, "\x1b]\x1b]".AsBytes()) == "Char[]]+a/2 Char[]]+a/2 ")
    Assert(Parse("\x1b]\x1b]".AsBytes(), false).n == 2)
    // Final, with an escape last and no ST after it: the opener, the keys
    // and the Escape key.
    Assert(DecodeAll(a, "\x1bPab\x1b".AsBytes()) == "Char[P]+a/2 Char[a]/1 Char[b]/1 Escape/1 ")
    // A string the terminal sends ends in ST or BEL, and CAN aborts it:
    // still dropped whole.
    Assert(DecodeAll(a, "\x1b]2;t\x1b\\\x1b]2;t\x07\x1b]2;t\x18z".AsBytes()) == "-/7 -/6 -/6 Char[z]/1 ")
}

test "a CSI with a private marker after its first parameter byte is dropped whole, never read as keys" (sys: &System) {
    a := sys.arena
    Assert(DecodeAll(a, "\x1b[1<5u".AsBytes()) == "-/6 ")
    Assert(DecodeAll(a, "\x1b[2;?7~z".AsBytes()) == "-/7 Char[z]/1 ")
    Assert(DecodeAll(a, "\x1b[1;5>9;=A\x1b[B".AsBytes()) == "-/10 Down/3 ")
    // A control byte still ends the sequence and is decoded afresh.
    Assert(DecodeAll(a, "\x1b[1<\r".AsBytes()) == "-/4 Enter/1 ")
}

test "ESC O and ESC [ alone at the escape timeout are Alt+Shift+O and Alt+[" (sys: &System) {
    a := sys.arena
    Assert(DecodeAll(a, "\x1bO".AsBytes()) == "Char[O]+a/2 ")
    Assert(DecodeAll(a, "\x1b[".AsBytes()) == "Char[[]+a/2 ")
    // Not final: still the start of a sequence.
    Assert(Parse("\x1bO".AsBytes(), false).n == 0)
    Assert(Parse("\x1b[".AsBytes(), false).n == 0)
    // A longer unfinished sequence is still the Escape key.
    Assert(DecodeAll(a, "\x1b[1;".AsBytes()) == "Escape/1 Char[[]/1 Char[1]/1 Char[;]/1 ")
    // With an escape before it, the key keeps its alt.
    Assert(DecodeAll(a, "\x1b\x1b[".AsBytes()) == "Char[[]+a/3 ")
}
