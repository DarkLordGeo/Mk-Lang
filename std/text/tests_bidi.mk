// Why: the UCD's BidiTest.txt and BidiCharacterTest.txt check the rules
// (std/std-textlines_mk_test.go); the API contract and the cost bound need
// tests of their own.
// What: test blocks of text.BidiLevels, Levels, text.ParagraphBounds and
// text.Mirror, run by `mk test --std=std.text` (TestSTDTEXT_LinesTestBlocks,
// dev and release). Helper names start with Tb2.
// How: short texts show the contract, and nestings that would be
// quadratic if a first-strong scan did not jump over the isolates inside.

// Tb2Run reports whether r is the run start to end at level.
Tb2Run(r: Run, start: int, end: int, level: u8): bool {
    return r.start == start && r.end == end && r.level == level
}

// Tb2Split reports whether each paragraph of s (ParagraphBounds) has,
// inside BidiLevels of s with dir, the levels it has resolved alone with
// dir.
Tb2Split(a: &Arena, s: str, dir: Direction): OutOfMemory!bool {
    whole := try BidiLevels(a, s, dir)
    var prev := -1
    for q in ParagraphBounds(s) {
        if prev >= 0 {
            alone := try BidiLevels(a, s[prev..q], dir)
            for p in prev..q {
                if whole.At(p) != alone.At(p - prev) {
                    return false
                }
            }
        }
        prev = q
    }
    return true
}

// Tb2Rep is head then n copies of unit, then tail.
Tb2Rep(a: &Arena, head: str, unit: str, n: int, tail: str): OutOfMemory!str from a {
    var t := Text.New(a)
    try t.Push(head)
    for _ in 0..n {
        try t.Push(unit)
    }
    try t.Push(tail)
    return try a.Dup(t.Str())
}

test "left-to-right text is one run at level 0" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    s := "Hello, world"
    lv := try BidiLevels(&ar, s, Direction.Auto)
    Assert(lv.Base() == 0)
    runs := try lv.Line(&ar, 0, s.Len())
    Assert(runs.Len() == 1)
    Assert(Tb2Run(runs[0], 0, s.Len(), 0))
}

test "Auto takes the first strong character outside an isolate" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    Assert((try BidiLevels(&ar, "\u{5D0}x", Direction.Auto)).Base() == 1)
    Assert((try BidiLevels(&ar, "x\u{5D0}", Direction.Auto)).Base() == 0)
    Assert((try BidiLevels(&ar, "123", Direction.Auto)).Base() == 0)
    // LRI abc PDI alef: the isolate is skipped, so the paragraph is RTL.
    Assert((try BidiLevels(&ar, "\u{2066}abc\u{2069}\u{5D0}", Direction.Auto)).Base() == 1)
    Assert((try BidiLevels(&ar, "x", Direction.Rtl)).Base() == 1)
    Assert((try BidiLevels(&ar, "\u{5D0}", Direction.Ltr)).Base() == 0)
}

test "mixed text gives its runs left to right" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    s := "abc \u{5D0}\u{5D1}"
    lv := try BidiLevels(&ar, s, Direction.Auto)
    runs := try lv.Line(&ar, 0, s.Len())
    Assert(runs.Len() == 2)
    Assert(Tb2Run(runs[0], 0, 4, 0))
    Assert(Tb2Run(runs[1], 4, 8, 1))
}

test "a number in a right-to-left paragraph is a run of its own, left of the text before it" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    s := "\u{5D0} 12"
    lv := try BidiLevels(&ar, s, Direction.Auto)
    Assert(lv.Base() == 1)
    Assert(lv.At(0) == 1 && lv.At(1) == 1 && lv.At(2) == 1 && lv.At(3) == 2 && lv.At(4) == 2)
    runs := try lv.Line(&ar, 0, s.Len())
    Assert(runs.Len() == 2)
    Assert(Tb2Run(runs[0], 3, 5, 2))
    Assert(Tb2Run(runs[1], 0, 3, 1))
}

test "a line's trailing whitespace and a segment separator take the paragraph level" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    s := "\u{5D0} \u{5D1}"
    lv := try BidiLevels(&ar, s, Direction.Ltr)
    // The space between two R letters resolves to R (N1)...
    Assert(lv.At(2) == 1)
    // ...but ends the line alef-space, so L1 sets it to 0.
    runs := try lv.Line(&ar, 0, 3)
    Assert(runs.Len() == 2)
    Assert(Tb2Run(runs[0], 0, 2, 1))
    Assert(Tb2Run(runs[1], 2, 3, 0))
    t := "\u{5D0}\t\u{5D1}"
    tl := try BidiLevels(&ar, t, Direction.Ltr)
    tr := try tl.Line(&ar, 0, t.Len())
    Assert(tr.Len() == 3)
    Assert(Tb2Run(tr[0], 0, 2, 1))
    Assert(Tb2Run(tr[1], 2, 3, 0))
    Assert(Tb2Run(tr[2], 3, 5, 1))
}

test "a bracket pair takes the direction of its content and context (N0)" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // RTL a(b)alef: the pair holds L and follows L, so both brackets are
    // L (level 2); N1 alone would give ')' the paragraph's R.
    lv := try BidiLevels(&ar, "a(b)\u{5D0}", Direction.Rtl)
    Assert(lv.At(1) == 2 && lv.At(3) == 2)
    Assert(lv.At(4) == 1)
}

test "an isolate's content gets its own level, and a removed control the level before it" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    iso := try BidiLevels(&ar, "\u{2067}abc\u{2069}", Direction.Ltr)
    Assert(iso.At(3) == 2)
    // a RLE b: X9 removes the RLE, which takes a's level; b is L at the
    // embedding's level 1, so 2.
    emb := try BidiLevels(&ar, "a\u{202B}b", Direction.Ltr)
    Assert(emb.At(0) == 0 && emb.At(1) == 0 && emb.At(4) == 2)
}

test "each paragraph inside the text resolves as it would alone, with the same direction" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // RLE a U+2029 b: a is L at the embedding's level 1, so 2; X8 closes
    // the embedding at the separator, which with b is at level 0.
    lv := try BidiLevels(&ar, "\u{202B}a\u{2029}b", Direction.Ltr)
    Assert(lv.At(3) == 2)
    Assert(lv.At(4) == 0 && lv.At(7) == 0)
    // RTL a ( U+2029 ) b: the brackets do not pair across the separator,
    // so '(' is R between a and the paragraph's end (1), not L (2).
    br := try BidiLevels(&ar, "a(\u{2029})b", Direction.Rtl)
    Assert(br.At(0) == 2 && br.At(1) == 1 && br.At(2) == 1 && br.At(5) == 1 && br.At(6) == 2)
    // LTR alef U+2029 1: W2 does not read back across the separator, so
    // 1 is EN, then L by W7 (0), not AN (2).
    w2 := try BidiLevels(&ar, "\u{627}\u{2029}1", Direction.Ltr)
    Assert(w2.At(0) == 1 && w2.At(2) == 0 && w2.At(5) == 0)
    // LTR alef U+2029 ! alef: N1 does not join '!' to the alef before the
    // separator; it lies between sos (L) and R, so N2 gives it 0.
    n1 := try BidiLevels(&ar, "\u{5D0}\u{2029}!\u{5D0}", Direction.Ltr)
    Assert(n1.At(0) == 1 && n1.At(2) == 0 && n1.At(5) == 0 && n1.At(6) == 1)
    // The same against each paragraph resolved alone, for those texts and
    // a separator first, CR LF, an embedding, an isolate, a bracket pair
    // and a removed control on either side of a separator.
    Assert(try Tb2Split(&ar, "a(\u{2029})b", Direction.Rtl))
    Assert(try Tb2Split(&ar, "\u{627}\u{2029}1", Direction.Ltr))
    Assert(try Tb2Split(&ar, "\u{5D0}\u{2029}!\u{5D0}", Direction.Ltr))
    Assert(try Tb2Split(&ar, "\u{2029}\u{5D0}a", Direction.Ltr))
    Assert(try Tb2Split(&ar, "\u{5D0}(\r\n)a 1", Direction.Rtl))
    Assert(try Tb2Split(&ar, "a\u{2029}\u{202B}b\u{202C}\u{5D0}", Direction.Ltr))
    Assert(try Tb2Split(&ar, "\u{2067}a\u{2029}b\u{2069}\u{5D0}", Direction.Ltr))
    Assert(try Tb2Split(&ar, "\u{5D0}[\u{2029}]a", Direction.Ltr))
    Assert(try Tb2Split(&ar, "1\u{200B}\u{2029}\u{200B}\u{627}2", Direction.Rtl))
    Assert(try Tb2Split(&ar, "\u{5D0}\u{2029}\u{2029}a\u{2029}", Direction.Ltr))
    // P2 reads up to the first separator only.
    Assert((try BidiLevels(&ar, "\u{2029}\u{5D0}", Direction.Auto)).Base() == 0)
    Assert((try BidiLevels(&ar, "1\u{5D0}\u{2029}a", Direction.Auto)).Base() == 1)
}

test "explicit levels stop at 125" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    s := try Tb2Rep(&ar, "", "\u{202B}", 200, "x")
    lv := try BidiLevels(&ar, s, Direction.Ltr)
    Assert(lv.At(s.Len() - 1) == 126)
}

test "deeply nested first-strong isolates resolve in linear time" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // 20,000 FSIs, a, 20,000 PDIs: each FSI's scan jumps over the isolate
    // inside it to its own PDI, so each is LTR; 62 of them fit under 125.
    var t := Text.New(&ar)
    for _ in 0..20000 {
        try t.Push("\u{2068}")
    }
    try t.Push("a")
    for _ in 0..20000 {
        try t.Push("\u{2069}")
    }
    s := t.Str()
    lv := try BidiLevels(&ar, s, Direction.Auto)
    Assert(lv.Base() == 0)
    Assert(lv.At(3 * 20000) == 124)
    runs := try lv.Line(&ar, 0, s.Len())
    Assert(runs.Len() > 0)
}

test "a line's runs cover it, and an empty line has none" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    s := "ab \u{5D0}\u{5D1} 12 cd"
    lv := try BidiLevels(&ar, s, Direction.Rtl)
    Assert((try lv.Line(&ar, 2, 2)).Len() == 0)
    runs := try lv.Line(&ar, 3, s.Len())
    var bytes := 0
    for r in runs {
        Assert(r.start >= 3 && r.end <= s.Len() && r.start < r.end)
        bytes += r.end - r.start
    }
    Assert(bytes == s.Len() - 3)
}

test "a line inside its paragraph gets L1 and L2 of its own bytes only" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // RTL a b SP alef bet SP 1 2 SP c d: ab and cd are 2, 12 is 2, the
    // rest 1. The line from alef to the end reverses each level-2 run,
    // then the whole line: cd, SP, 12, then alef bet SP from the right.
    s := "ab \u{5D0}\u{5D1} 12 cd"
    lv := try BidiLevels(&ar, s, Direction.Rtl)
    runs := try lv.Line(&ar, 3, s.Len())
    Assert(runs.Len() == 4)
    Assert(Tb2Run(runs[0], 11, 13, 2))
    Assert(Tb2Run(runs[1], 10, 11, 1))
    Assert(Tb2Run(runs[2], 8, 10, 2))
    Assert(Tb2Run(runs[3], 3, 8, 1))
    // LTR a SP alef SP bet SP b: the SP between alef and bet is 1 (N1),
    // but it ends the line alef-SP, so L1 sets it to 0; the next line,
    // which starts there, keeps it at 1.
    t := "a \u{5D0} \u{5D1} b"
    tl := try BidiLevels(&ar, t, Direction.Ltr)
    Assert(tl.At(4) == 1)
    first := try tl.Line(&ar, 2, 5)
    Assert(first.Len() == 2)
    Assert(Tb2Run(first[0], 2, 4, 1))
    Assert(Tb2Run(first[1], 4, 5, 0))
    next := try tl.Line(&ar, 4, 9)
    Assert(next.Len() == 2)
    Assert(Tb2Run(next[0], 4, 7, 1))
    Assert(Tb2Run(next[1], 7, 9, 0))
}

test "ParagraphBounds splits after each paragraph separator, CR LF as one" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var got := List[int].New(&ar)
    for p in ParagraphBounds("a\r\nb\u{2029}c") {
        try got.Push(p)
    }
    Assert(got.Len() == 4)
    Assert(got[0] == 0 && got[1] == 3 && got[2] == 7 && got[3] == 8)
    var n := 0
    for p in ParagraphBounds("") {
        _ = p
        n += 1
    }
    Assert(n == 0)
}

test "Mirror gives the mirrored glyph, and none for a scalar without one" (sys: &System) {
    Assert((Mirror('(') else { 'x' }) == ')')
    Assert((Mirror('[') else { 'x' }) == ']')
    Assert((Mirror('\u{226A}') else { 'x' }) == '\u{226B}')
    if m := Mirror('a') {
        _ = m
        Panic("a has no mirroring glyph")
    }
}
