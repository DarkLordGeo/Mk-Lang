// Why: a Text's Writer judges each run as UTF-8, so a Logger's run must never
// end inside a character (log.mk, LineWrite), or a valid line fails.
// What: test blocks: every valid line, however its characters fall against
// the 256-byte runs, reaches a Text whole and as Print writes it after the
// level; bytes a Format wrote that are not UTF-8 fail the line.
// How: characters of 2, 3 and 4 bytes at every offset around bytes 256 and
// 512, lines made only of them, and timed lines, logged through IntoText.

// IntoText logs one line at Info into t through a new Logger over t's
// Writer (timed through io when timed is true); true when the call failed.
IntoText[A: Fields[Format] + Plain](io: Io, t: mut Text, timed: bool, const f: str, args: A): bool {
    var w := t.Writer()
    var failed := false
    if timed {
        var l := Logger.NewTimed(io, mut w, Level.Debug)
        l.Info(f, args) else {
            failed = true
        }
    } else {
        var l := Logger.New(mut w, Level.Debug)
        l.Info(f, args) else {
            failed = true
        }
    }
    return failed
}

// Cat is a, then b, then c, in ar.
Cat(ar: &Arena, a: str, b: str, c: str): str from ar {
    var t := Text.New(ar)
    t.Push(a) else {
        return ""
    }
    t.Push(b) else {
        return ""
    }
    t.Push(c) else {
        return ""
    }
    return t.Done()
}

// Reps is s n times, in ar.
Reps(ar: &Arena, s: str, n: int): str from ar {
    var t := Text.New(ar)
    for _ in 0..n {
        t.Push(s) else {
            return ""
        }
    }
    return t.Done()
}

// Straddles logs "INFO " + k bytes of x + c + "z" into a Text as one field
// and as three pieces (the field, the character, the literal), and checks
// each against Print's text.
Straddles(sys: &System, k: int, c: str) {
    xs := Xs(sys.arena, k)
    s := Cat(sys.arena, xs, c, "z")
    var t := Text.New(sys.arena)
    Assert(!IntoText(sys.io, mut t, false, "{s}", (s)))
    Assert(t.Len() == 5 + k + c.Len() + 2)
    Assert(t.Str() == Expected(sys.arena, Level.Info, "{s}", (s)))
    var u := Text.New(sys.arena)
    Assert(!IntoText(sys.io, mut u, false, "{a}{c}z", (a: xs, c)))
    Assert(u.Str() == Expected(sys.arena, Level.Info, "{a}{c}z", (a: xs, c)))
}

test "A 2, 3 or 4-byte character at every offset around bytes 256 and 512 goes into a Text whole, as Print writes it" (sys: &System) {
    // offsets 240 to 269 and 495 to 519, widened so
    // the character's first byte falls at every place from well before to
    // well after each boundary
    chars := ["é", "€", "😀", "\u{7FF}", "\u{800}", "\u{10000}", "\u{10FFFF}"]
    for c in chars {
        for k in 236..272 {
            Straddles(sys, k, c)
        }
        for k in 488..524 {
            Straddles(sys, k, c)
        }
    }
}

test "Lines made only of multi-byte characters, in one field and in many pieces, go into a Text whole" (sys: &System) {
    for c in ["é", "€", "😀"] {
        for n in [50, 85, 86, 127, 128, 129, 170, 171, 1250] {
            s := Reps(sys.arena, c, n)
            var t := Text.New(sys.arena)
            Assert(!IntoText(sys.io, mut t, false, "{s}", (s)))
            Assert(t.Str() == Expected(sys.arena, Level.Info, "{s}", (s)))
            // after 250 bytes of x the field's first part tops the buffer up
            // and its rest past the buffer goes straight through
            xs := Xs(sys.arena, 250)
            var u := Text.New(sys.arena)
            Assert(!IntoText(sys.io, mut u, false, "{a}{s}", (a: xs, s)))
            Assert(u.Str() == Expected(sys.arena, Level.Info, "{a}{s}", (a: xs, s)))
        }
    }
    // a slice of 400 strings, each written as its own piece between ", "
    var parts: [400]str = [""; 400]
    for i in 0..400 {
        parts[i] = if i % 3 == 0 { "é€😀" } else if i % 3 == 1 { "😀é" } else { "x€" }
    }
    var t := Text.New(sys.arena)
    Assert(!IntoText(sys.io, mut t, false, "v={v}", (v: parts[..])))
    want := Expected(sys.arena, Level.Info, "v={v}", (v: parts[..]))
    Assert(want.Len() > 2000)
    Assert(t.Str() == want)
}

test "A timed line with a character across byte 256 goes into a Text whole" (sys: &System) {
    for k in 230..260 {
        s := Cat(sys.arena, Xs(sys.arena, k), "😀", "z")
        var t := Text.New(sys.arena)
        Assert(!IntoText(sys.io, mut t, true, "{s}", (s)))
        Assert(StampedRest(t.Str()) == Expected(sys.arena, Level.Info, "{s}", (s)))
    }
}

// SwallowBad writes a, then two bytes that are not UTF-8, ignoring that
// write's failure, then b.
struct SwallowBad {
    n: int
}

impl Format for SwallowBad {
    Format(&self, w: mut Writer): WriteError!void {
        try w.Str("a")
        bad: [2]u8 = [0xFF, 0xFE]
        w.Bytes(bad[..]) else {}
        try w.Str("b")
    }
}

// SplitChar writes λ (CE BB) in two writes of one byte.
struct SplitChar {
    n: int
}

impl Format for SplitChar {
    Format(&self, w: mut Writer): WriteError!void {
        b1: [1]u8 = [0xCE]
        b2: [1]u8 = [0xBB]
        try w.Bytes(b1[..])
        try w.Bytes(b2[..])
    }
}

test "Into a Text, bytes that are not UTF-8 a Format wrote and ignored fail the line, and a character written in two pieces arrives whole" (sys: &System) {
    // the bad bytes join the line's run, and the Text refuses the run: the
    // call fails and the Text keeps nothing (each piece a write of its own,
    // only the bad one would fail, inside the Format, which ignores it)
    var t := Text.New(sys.arena)
    Assert(IntoText(sys.io, mut t, false, "x{f}y", (f: SwallowBad(n: 0))))
    Assert(t.Len() == 0)
    Assert(!IntoText(sys.io, mut t, false, "next", ()))
    Assert(t.Str() == "INFO next\n")
    // the two bytes reach the Text in one run (each piece a write of its
    // own, the Text would refuse the first byte alone)
    var u := Text.New(sys.arena)
    Assert(!IntoText(sys.io, mut u, false, "x{f}y", (f: SplitChar(n: 0))))
    Assert(u.Str() == "INFO xλy\n")
    // a lone lead at the line's end is handed on, and refused
    var v := Text.New(sys.arena)
    Assert(IntoText(sys.io, mut v, false, "{f}", (f: LoneLead(n: 0))))
    Assert(v.Len() == 0)
}

// LoneLead writes a lead byte that no continuation follows, at the end of
// the message, and ignores nothing.
struct LoneLead {
    n: int
}

impl Format for LoneLead {
    Format(&self, w: mut Writer): WriteError!void {
        b: [1]u8 = [0xE2]
        try w.Bytes(b[..])
    }
}

test "Incomplete and LeadLen judge the end of a run as UTF-8 does" (sys: &System) {
    e := "é".AsBytes()
    euro := "€".AsBytes()
    face := "😀".AsBytes()
    Assert(Incomplete(e[..0]) == 0)
    Assert(Incomplete(e[..1]) == 1)
    Assert(Incomplete(e) == 0)
    Assert(Incomplete(euro[..1]) == 1 && Incomplete(euro[..2]) == 2 && Incomplete(euro) == 0)
    Assert(Incomplete(face[..1]) == 1 && Incomplete(face[..2]) == 2 && Incomplete(face[..3]) == 3)
    Assert(Incomplete(face) == 0)
    // continuation bytes with no lead within three, and ASCII, are handed on
    cont: [4]u8 = [0x80, 0x80, 0x80, 0x80]
    Assert(Incomplete(cont[..]) == 0)
    Assert(Incomplete("abc".AsBytes()) == 0)
    // an invalid lead needs nothing more
    bad: [2]u8 = [0x61, 0xFF]
    Assert(Incomplete(bad[..]) == 0)
    Assert(LeadLen(0x41) == 1 && LeadLen(0xC1) == 1 && LeadLen(0xC2) == 2 && LeadLen(0xDF) == 2)
    Assert(LeadLen(0xE0) == 3 && LeadLen(0xEF) == 3 && LeadLen(0xF0) == 4 && LeadLen(0xF4) == 4 && LeadLen(0xF5) == 1)
}
