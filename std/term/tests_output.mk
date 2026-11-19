// Why: an output sequence one byte off garbles the screen, so each one is
// pinned byte for byte.
// What: test blocks for screen.mk's and style.mk's writers and UseColor.
// How: each writes to a Writer over a Text and compares the bytes.

// Test blocks for the output functions (screen.mk, style.mk): each writes
// to a Writer over a Text, and the bytes are compared exactly with xterm's
// sequences (ctlseqs).

test "MoveTo writes CUP counted from 1, and clamps coordinates" (sys: &System) {
    var t := Text.New(sys.arena)
    var w := t.Writer()
    MoveTo(mut w, 0, 0).Must()
    MoveTo(mut w, 9, 79).Must()
    MoveTo(mut w, -5, 70_000).Must()
    w.Flush().Must()
    Assert(t.Str() == "\x1b[1;1H\x1b[10;80H\x1b[1;65535H")
}

test "Clear, AltScreen, ShowCursor and BracketedPaste write their DEC modes" (sys: &System) {
    var t := Text.New(sys.arena)
    var w := t.Writer()
    Clear(mut w).Must()
    AltScreen(mut w, true).Must()
    AltScreen(mut w, false).Must()
    ShowCursor(mut w, false).Must()
    ShowCursor(mut w, true).Must()
    BracketedPaste(mut w, true).Must()
    BracketedPaste(mut w, false).Must()
    w.Flush().Must()
    Assert(t.Str() == "\x1b[H\x1b[2J\x1b[?1049h\x1b[?1049l\x1b[?25l\x1b[?25h\x1b[?2004h\x1b[?2004l")
}

test "Sanitize replaces C0 controls but LF and tab, DEL and C1 controls, and keeps the rest" (sys: &System) {
    var t := Text.New(sys.arena)
    var w := t.Writer()
    Sanitize(mut w, "ok\x1b[2J\x07\r\nnext\tline\x7f\u{9b}31m\u{e9}\u{1F600}").Must()
    Sanitize(mut w, "").Must()
    Sanitize(mut w, "\x1b").Must()
    w.Flush().Must()
    Assert(t.Str() == "ok\u{FFFD}[2J\u{FFFD}\u{FFFD}\nnext\tline\u{FFFD}\u{FFFD}31m\u{e9}\u{1F600}\u{FFFD}")
}

test "Style.Format writes a reset then each attribute, in the 16-colour, 256-colour and direct forms" (sys: &System) {
    var t := Text.New(sys.arena)
    var w := t.Writer()
    plain := Style(fg: none, bg: none, bold: false, underline: false, reverse: false)
    plain.Format(mut w).Must()
    s1 := Style(fg: some(Color.Ansi(n: 1)), bg: some(Color.Ansi(n: 12)), bold: true, underline: true, reverse: true)
    s1.Format(mut w).Must()
    s2 := Style(fg: some(Color.Ansi(n: 208)), bg: some(Color.Rgb(r: 255, g: 0, b: 7)), bold: false, underline: false,
        reverse: false)
    s2.Format(mut w).Must()
    w.Flush().Must()
    Assert(t.Str() == "\x1b[0m\x1b[0;1;4;7;31;104m\x1b[0;38;5;208;48;2;255;0;7m")
}

test "UseColor is false for a stream that is no terminal" (sys: &System) {
    // The test runner's streams are pipes.
    Assert(!UseColor(sys.io, Std.Stdout) || IsTerminal(sys.io, Std.Stdout))
}

test "AltScan follows the alternate screen across writes, split anywhere" {
    var s := AltScan(false, 0, "x\x1b[?10".AsBytes())
    Assert(!s.alt && s.at == 5)
    s = AltScan(s.alt, s.at, "49hy".AsBytes())
    Assert(s.alt && s.at == 0)
    s = AltScan(s.alt, s.at, "\x1b\x1b[?1049".AsBytes())
    Assert(s.alt && s.at == 7)
    s = AltScan(s.alt, s.at, "l".AsBytes())
    Assert(!s.alt)
    s = AltScan(false, 0, "\x1b[?1048h\x1b[?1049x".AsBytes())
    Assert(!s.alt)
}

// SanitizeRef is Sanitize one byte at a time, the model of its vector
// scan.
SanitizeRef(t: mut Text, s: str) {
    b := s.AsBytes()
    var run := 0
    var i := 0
    for i < b.Len() {
        c := b[i]
        var bad := 0
        if c < 0x20 && c != 0x0A && c != 0x09 || c == 0x7F {
            bad = 1
        } else if c == 0xC2 && i + 1 < b.Len() && b[i + 1] >= 0x80 && b[i + 1] <= 0x9F {
            bad = 2
        }
        if bad == 0 {
            i += 1
            continue
        }
        // A bad byte is a whole scalar (C0, DEL) or starts one (C1), so
        // the run before it is whole scalars.
        t.Push(str.From(b[run..i]).Must()).Must()
        t.Push("\u{FFFD}").Must()
        i += bad
        run = i
    }
    t.Push(str.From(b[run..]).Must()).Must()
}

test "Sanitize agrees with the byte-at-a-time model on 500 generated texts across the vector steps" (sys: &System) {
    pieces := ["a", "bc", "\n", "\t", "\x1b", "\x00", "\x1f", "\x7f", "\u{e9}", "\u{9b}", "\u{80}", "\u{a0}", "\u{20ac}",
        "0123456789abcdef", "\u{1F600}"]
    var st: u64 = 7
    for _ in 0..500 {
        var src := Text.New(sys.arena)
        st = st *% 6364136223846793005 +% 1442695040888963407
        n := Trunc[int]((st >> 33) % 40)
        for _ in 0..n {
            st = st *% 6364136223846793005 +% 1442695040888963407
            src.Push(pieces[Trunc[int]((st >> 33) % 15)]).Must()
        }
        var got := Text.New(sys.arena)
        var w := got.Writer()
        Sanitize(mut w, src.Str()).Must()
        w.Flush().Must()
        var want := Text.New(sys.arena)
        SanitizeRef(mut want, src.Str())
        Assert(got.Str() == want.Str())
    }
}

test "Sanitize agrees with the model on long texts across its 512-byte block, and on safe runs longer than the block" (sys: &System) {
    var long := Text.New(sys.arena)
    for _ in 0..40 {
        long.Push("0123456789abcdef").Must()
    }
    pieces := ["a", "\n", "\x1b", "\x07", "\u{9b}", "\u{e9}", "\u{1F600}", "0123456789abcdef", long.Str()]
    var st: u64 = 11
    for _ in 0..200 {
        var src := Text.New(sys.arena)
        st = st *% 6364136223846793005 +% 1442695040888963407
        n := Trunc[int]((st >> 33) % 600)
        for _ in 0..n {
            st = st *% 6364136223846793005 +% 1442695040888963407
            // The long run is rare: one piece in 64.
            k := Trunc[int]((st >> 33) % 64)
            j := if k == 63 { 8 } else { k % 8 }
            src.Push(pieces[j]).Must()
        }
        var got := Text.New(sys.arena)
        var w := got.Writer()
        Sanitize(mut w, src.Str()).Must()
        w.Flush().Must()
        var want := Text.New(sys.arena)
        SanitizeRef(mut want, src.Str())
        Assert(got.Str() == want.Str())
    }
}

test "NO_COLOR's decision: only a value that is set and not empty refuses colour" (sys: &System) {
    Assert(!ColorRefused(none))
    Assert(!ColorRefused(some("")))
    Assert(ColorRefused(some("1")))
    Assert(ColorRefused(some("0")))
}

test "NoColor reads NO_COLOR through rt.Env, with no arena, and agrees with System.Env" (sys: &System) {
    // Whatever this run's NO_COLOR is: the decision from System.Env's
    // value is NoColor's, and a refused colour is false on every stream.
    // The pty block "color" (tests_ptyread.mk) sets NO_COLOR unset, empty
    // and 1 in a child on a terminal.
    want := ColorRefused(sys.Env("NO_COLOR"))
    Assert(NoColor() == want)
    if want {
        Assert(!UseColor(sys.io, Std.Stdout))
        Assert(!UseColor(sys.io, Std.Stderr))
        Assert(!UseColor(sys.io, Std.Stdin))
    }
}
