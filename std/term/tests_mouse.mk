// Why: mouse and focus reports arrive in two encodings mixed into the key
// input, and each must decode to the right Event.
// What: test blocks for input_mouse.mk's decoder and MouseCapture's bytes.
// How: PointerAll prints each Parse step as text for comparison.

// Test blocks for the mouse and focus unit (input_mouse.mk): the reports over
// recorded byte streams, and MouseCapture's sequences. The pseudo-terminal
// block is tests_pty.mk's "mouse" mode; a Go test of package std checks the
// same decoder against its model.

// PointerAll decodes all of b with Parse (final) and returns the steps as
// text: "M<button>@<row>,<col><mods>/<n> " for a mouse report, "F<on>/<n> "
// for focus, "K/<n> " for a key, "P/<n> " for a paste start, "-/<n> " for
// a dropped sequence.
PointerAll(a: &Arena, b: []u8): str from a {
    var t := Text.New(a)
    var i := 0
    for i < b.Len() {
        p := Parse(b[i..], true)
        Assert(p.n > 0)
        n := p.n
        s := if p.mods.shift { "+s" } else { "" }
        al := if p.mods.alt { "+a" } else { "" }
        c := if p.mods.ctrl { "+c" } else { "" }
        step := if p.kind == PMouse {
            button := p.a
            row := p.b
            col := p.c
            a.Fmt("M{button}@{row},{col}{s}{al}{c}/{n} ", (button, row, col, s, al, c, n)).Must()
        } else if p.kind == PFocus {
            on := p.a
            a.Fmt("F{on}/{n} ", (on, n)).Must()
        } else if p.kind == PKey {
            a.Fmt("K/{n} ", (n)).Must()
        } else if p.kind == PPaste {
            a.Fmt("P/{n} ", (n)).Must()
        } else {
            a.Fmt("-/{n} ", (n)).Must()
        }
        t.Push(step).Must()
        i += p.n
    }
    return t.Done()
}

test "Parse: SGR mouse reports give the button, a release, the wheel, a drag, row and col from 0, and the modifiers" (sys: &System) {
    got := PointerAll(sys.arena, "\x1b[<0;10;5M\x1b[<2;1;1m\x1b[<64;3;4M\x1b[<65;3;4M\x1b[<32;7;8M\x1b[<20;300;200M\x1b[<9;1;1M".AsBytes())
    Assert(got == "M0@4,9/10 M3@0,0/9 M64@3,2/10 M65@3,2/10 M32@7,6/10 M0@199,299+s+c/14 M1@0,0+a/9 ")
}

test "Parse: an SGR report with other than three parameters is dropped, and a coordinate of 0 is 0" (sys: &System) {
    got := PointerAll(sys.arena, "\x1b[<0;10M\x1b[<0;1;2;3M\x1b[<0;0;0M".AsBytes())
    Assert(got == "-/8 -/11 M0@0,0/9 ")
}

test "Parse: X10 mouse reports, and one holding a control byte is dropped" (sys: &System) {
    got := PointerAll(sys.arena, "\x1b[M !!\x1b[M#*+\x1b[M\x01!!".AsBytes())
    // The dropped report leaves its three bytes: Ctrl+A, then ! and !.
    Assert(got == "M0@0,0/6 M3@10,9/6 -/3 K/1 K/1 K/1 ")
    Assert(Parse("\x1b[M !".AsBytes(), false).n == 0)
    Assert(Parse("\x1b[M !".AsBytes(), true).n == 5)
}

test "Parse: focus in and out, and CSI I or O with parameters are dropped" (sys: &System) {
    got := PointerAll(sys.arena, "\x1b[I\x1b[O\x1b[1I\x1b[?O".AsBytes())
    Assert(got == "F1/3 F0/3 -/4 -/4 ")
}

test "Parse: an escape before a report is the Escape key, then the report" (sys: &System) {
    got := PointerAll(sys.arena, "\x1b\x1b[<0;2;3M\x1b\x1b[I".AsBytes())
    Assert(got == "K/1 M0@2,1/9 K/1 F1/3 ")
}

test "MouseCapture writes DECSET 1000, 1002, 1006 and 1004, and resets them in reverse" (sys: &System) {
    var t := Text.New(sys.arena)
    var w := t.Writer()
    MouseCapture(mut w, true).Must()
    MouseCapture(mut w, false).Must()
    w.Flush().Must()
    Assert(t.Str() == "\x1b[?1000h\x1b[?1002h\x1b[?1006h\x1b[?1004h\x1b[?1004l\x1b[?1006l\x1b[?1002l\x1b[?1000l")
}

test "Term on a pseudo-terminal with MouseCapture: a press, its release, the wheel with ctrl and focus reach Read as Mouse and Focus events" (sys: &System) {
    if sys.args.Len() > 2 {
        TmChild(sys)
    }
    var keys: [6]str = ["\x1b[<0;10;5M", "\x1b[<0;10;5m", "\x1b[<81;1;2M", "\x1b[I", "\x1b[O", "q"]
    (code, text) := TmRun(sys, "mouse", keys[..], -1)
    Assert(code == 0)
    Assert(TmSame(sys, text, "mouse 0 4 9\nmouse 3 4 9\nmouse 65 1 0+c\nfocus true\nfocus false\nkey Char q\n"))
}

test "The Writer's watch follows MouseCapture's on and off sequences across writes split anywhere" (sys: &System) {
    on := MouseOn.AsBytes()
    off := MouseOff.AsBytes()
    w := MouseWatch.AsBytes()
    for cut in 0..on.Len() + 1 {
        (on: m1, at: a1) := DecScan(w, false, 0, on[0..cut])
        (on: m2, at: a2) := DecScan(w, m1, a1, on[cut..])
        Assert(m2)
        (on: m3, at: a3) := DecScan(w, m2, a2, off[0..cut])
        (on: m4, at: _) := DecScan(w, m3, a3, off[cut..])
        Assert(!m4)
    }
    // 1049 and 1002 are not 1000.
    (on: alt, at: _) := DecScan(w, false, 0, "\x1b[?1049h\x1b[?1002h\x1b[?10000h".AsBytes())
    Assert(!alt)
}
