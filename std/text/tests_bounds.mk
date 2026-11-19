// Why: the UCD's test files and the Go models check the rules; the
// iterators' contract needs tests of its own.
// What: test blocks of text.WordBounds and text.SentenceBounds, run by
// `mk test --std=std.text` (TestSTDTEXT_WordsTestBlocks, dev and release).
// How: each block lists every boundary of a plain text (TbAll) and
// compares it with the expected list. Helper names start with Tb.

// TbAll is every boundary of s, in a: its sentence boundaries when
// sentences is set, else its word boundaries.
TbAll(a: &Arena, s: str, sentences: bool): OutOfMemory!List[int] from a {
    var out := List[int].New(a)
    var it := if sentences { SentenceBounds(s) } else { WordBounds(s) }
    for b in it {
        try out.Push(b)
    }
    return out
}

// TbSame reports whether got holds exactly want, in order.
TbSame(got: &List[int], want: []int): bool {
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

test "the empty text has no boundary" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    Assert((try TbAll(&ar, "", false)).Len() == 0)
    Assert((try TbAll(&ar, "", true)).Len() == 0)
}

test "words keep contractions and decimals whole, and split punctuation and spaces" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    // Hello|,| |world|.| |It's| |3.14|!
    want: [11]int = [0, 5, 6, 7, 12, 13, 14, 18, 19, 23, 24]
    got := try TbAll(&ar, "Hello, world. It's 3.14!", false)
    Assert(TbSame(&got, want[..]))
}

test "sentences end after a terminator and the spaces after it" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    want: [4]int = [0, 5, 10, 16]
    got := try TbAll(&ar, "One. Two? Three!", true)
    Assert(TbSame(&got, want[..]))
}

test "a full stop before a lower-case word does not end a sentence" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    want: [2]int = [0, 14]
    got := try TbAll(&ar, "e.g. this one.", true)
    Assert(TbSame(&got, want[..]))
}

test "the first offset is 0, the last the text's length, and Next stays none after it" (sys: &System) {
    s := "\u{1F1E6}\u{1F1E8}\u{1F1E6} \u{4E2D}\u{6587}"
    var it := WordBounds(s)
    var last := -1
    var n := 0
    for {
        b := it.Next() else {
            break
        }
        if n == 0 {
            Assert(b == 0)
        }
        Assert(b > last)
        last = b
        n += 1
    }
    Assert(last == s.Len())
    if extra := it.Next() {
        _ = extra
        Panic("Next gave a boundary after the last one")
    }
}

test "a paragraph separator ends a sentence without a terminator" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    want: [3]int = [0, 7, 13]
    got := try TbAll(&ar, "first\r\nsecond", true)
    Assert(TbSame(&got, want[..]))
}
