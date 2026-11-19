// Why: the UCD's LineBreakTest.txt checks the rules
// (std/std-textlines_mk_test.go); the iterator's contract and its cost bound
// need tests of their own.
// What: test blocks of text.LineBreaks, run by `mk test --std=std.text`
// (TestSTDTEXT_LinesTestBlocks, dev and release). Helper names start with Tl.
// How: plain texts show the contract, and a walk over a long input would
// be quadratic if a look-ahead rescanned its marks.

// TlAll is every opportunity of s, in a, as at * 2 + 1 when mandatory and
// at * 2 when allowed.
TlAll(a: &Arena, s: str): OutOfMemory!List[int] from a {
    var out := List[int].New(a)
    for br in LineBreaks(s) {
        try out.Push(if br.mandatory { br.at * 2 + 1 } else { br.at * 2 })
    }
    return out
}

// TlIs reports whether TlAll of s is exactly want, in order.
TlIs(a: &Arena, s: str, want: []int): OutOfMemory!bool {
    got := try TlAll(a, s)
    if got.Len() != want.Len() {
        return false
    }
    for i in 0..want.Len() {
        if got[i] != want[i] {
            return false
        }
    }
    return true
}

test "the empty text has no line-break opportunity" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    Assert((try TlAll(&ar, "")).Len() == 0)
}

test "a line may end after the spaces that follow a word, and at the end" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // Hello, |world|
    want: [2]int = [7 * 2, 12 * 2]
    Assert(try TlIs(&ar, "Hello, world", want[..]))
}

test "a line must end after LF and after CR LF, which is one break" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    want: [3]int = [3 * 2 + 1, 5 * 2 + 1, 6 * 2]
    Assert(try TlIs(&ar, "a\r\nb\nc", want[..]))
}

test "the end of a text that ends with a newline is mandatory" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    want: [1]int = [2 * 2 + 1]
    Assert(try TlIs(&ar, "a\n", want[..]))
}

test "no break before closing punctuation or inside a number with its signs" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // (12.5%) |x|
    want: [2]int = [8 * 2, 9 * 2]
    Assert(try TlIs(&ar, "(12.5%) x", want[..]))
}

test "ideographs break between any two, and a no-break space glues" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    want: [2]int = [3 * 2, 6 * 2]
    Assert(try TlIs(&ar, "\u{4E2D}\u{6587}", want[..]))
    // a NBSP b |c|
    glued: [2]int = [5 * 2, 6 * 2]
    Assert(try TlIs(&ar, "a\u{A0}b c", glued[..]))
}

test "a sign before an opening bracket and a number, and the look-aheads that skip marks" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // LB25 (25.11, 25.08): PR or PO x OP IS NU, so $(.5 and %(.5 are whole.
    end4: [1]int = [4 * 2]
    Assert(try TlIs(&ar, "$(.5", end4[..]))
    Assert(try TlIs(&ar, "%(.5", end4[..]))
    // LB9 joins U+0301 to the OP, and LB25's look-ahead skips it.
    end6: [1]int = [6 * 2]
    Assert(try TlIs(&ar, "$(\u{301}.5", end6[..]))
    // LB15b: no break before a closing quote (Pf) with its mark, before a
    // space (a 0, SP 1, U+201D 2, U+0301 5, SP 7, b 8).
    pf_sp: [2]int = [8 * 2, 9 * 2]
    Assert(try TlIs(&ar, "a \u{201D}\u{301} b", pf_sp[..]))
    // LB15c: a break after a space before IS NU, the mark joined to the IS.
    is_nu: [2]int = [2 * 2, 6 * 2]
    Assert(try TlIs(&ar, "a .\u{301}5", is_nu[..]))
}

test "a line may end after a hyphen, not before it" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    want: [2]int = [2 * 2, 6 * 2]
    Assert(try TlIs(&ar, "e-mail", want[..]))
}

test "an emoji ZWJ sequence and a flag stay whole" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // man ZWJ woman, then two flags (four regional indicators)
    s := "\u{1F468}\u{200D}\u{1F469}\u{1F1E9}\u{1F1EA}\u{1F1EB}\u{1F1F7}"
    want: [3]int = [11 * 2, 19 * 2, 27 * 2]
    Assert(try TlIs(&ar, s, want[..]))
}

test "marks after a closing quote are read once, so a long run is linear" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // x SP U+201D (QU, Pf) then 200,000 combining marks, then a letter:
    // LB15b looks past the marks once, and LB9 joins each mark.
    var t := Text.New(&ar)
    try t.Push("x \u{201D}")
    for _ in 0..200000 {
        try t.Push("\u{301}")
    }
    try t.Push("y")
    s := t.Str()
    var n := 0
    var last := 0
    for br in LineBreaks(s) {
        n += 1
        last = br.at
    }
    Assert(last == s.Len())
    Assert(n <= 3)
}

test "Next stays none after the last opportunity" (sys: &System) {
    var it := LineBreaks("one two")
    var n := 0
    for {
        _ = it.Next() else {
            break
        }
        n += 1
    }
    Assert(n == 2)
    if extra := it.Next() {
        _ = extra
        Panic("Next gave an opportunity after the last one")
    }
}
