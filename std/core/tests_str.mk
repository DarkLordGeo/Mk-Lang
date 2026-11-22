// Why: str is the prelude's UTF-8 text view; its checks and iterators must
// agree byte for byte with the UTF-8 rules.
// What: test blocks for str.From, Split, Words, Lines, Find, the comparisons
// and the other str functions.
// How: run by mk test --std=std.core with contracts on and the counting heap;
// the cases that panic are whole programs in std/std-text_regress_test.go.

// StrFromFails reports whether str.From refuses b.
StrFromFails(b: []u8): bool {
    _ = str.From(b) else {
        return true
    }
    return false
}

// StrSplitCount, StrWordCount and StrLineCount count what each iterator
// gives.
StrSplitCount(s: str, sep: str): int {
    var it := s.Split(sep)
    var n := 0
    for {
        _ = it.Next() else {
            return n
        }
        n += 1
    }
}

StrWordCount(s: str): int {
    var it := s.Words()
    var n := 0
    for {
        _ = it.Next() else {
            return n
        }
        n += 1
    }
}

StrLineCount(s: str): int {
    var it := s.Lines()
    var n := 0
    for {
        _ = it.Next() else {
            return n
        }
        n += 1
    }
}

test "Len counts bytes" {
    Assert("".Len() == 0)
    Assert("abc".Len() == 3)
    Assert("\u{E9}".Len() == 2)
    Assert("\u{20AC}".Len() == 3)
    Assert("\u{1F600}".Len() == 4)
}

test "Byte reads each offset" {
    s := "a\u{E9}z"
    Assert(s.Byte(0) == 0x61)
    Assert(s.Byte(1) == 0xC3)
    Assert(s.Byte(2) == 0xA9)
    Assert(s.Byte(3) == 0x7A)
}

test "AsBytes is the same bytes" {
    s := "h\u{E9}"
    b := s.AsBytes()
    Assert(b.Len() == 3)
    Assert(b[0] == 0x68 && b[1] == 0xC3 && b[2] == 0xA9)
    Assert("".AsBytes().Len() == 0)
}

test "Find gives the first byte offset" {
    Assert(("hello".Find("l") else { -1 }) == 2)
    Assert(("hello".Find("lo") else { -1 }) == 3)
    Assert(("hello".Find("") else { -1 }) == 0)
    Assert(("".Find("") else { -1 }) == 0)
    Assert("".Find("a").IsNone())
    Assert("hello".Find("hellos").IsNone())
    Assert(("aaaaaaaaab".Find("aaab") else { -1 }) == 6)
    Assert(("abababc".Find("ababc") else { -1 }) == 2)
    Assert(("caf\u{E9} caf\u{E9}".Find("\u{E9}") else { -1 }) == 3)
    Assert(("xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxy".Find("y") else { -1 }) == 40)
}

test "Has is Find is some" {
    Assert("hello".Has("ell"))
    Assert("hello".Has(""))
    Assert(!"hello".Has("elo"))
    Assert(!"".Has("a"))
}

test "StartsWith and EndsWith" {
    Assert("hello".StartsWith(""))
    Assert("hello".StartsWith("he"))
    Assert("hello".StartsWith("hello"))
    Assert(!"hello".StartsWith("hello!"))
    Assert(!"hello".StartsWith("eh"))
    Assert("hello".EndsWith(""))
    Assert("hello".EndsWith("llo"))
    Assert(!"hello".EndsWith("hhello"))
    Assert("".StartsWith("") && "".EndsWith(""))
    Assert("0123456789abcdefXYZ".StartsWith("0123456789abcdef"))
    Assert(!"0123456789abcdefXYZ".StartsWith("0123456789abcdeF"))
}

test "Trim removes White_Space at both ends" {
    Assert("  a b \t\n".Trim() == "a b")
    Assert("".Trim() == "")
    Assert(" \t\r\n ".Trim() == "")
    Assert("\u{A0}x\u{3000}".Trim() == "x")
    Assert("\u{2028}\u{85}y\u{200A}".Trim() == "y")
    Assert("\u{200B}z".Trim() == "\u{200B}z")
}

test "Split keeps empty pieces" {
    var it := "a,,b".Split(",")
    Assert((it.Next() else { "?" }) == "a")
    Assert((it.Next() else { "?" }) == "")
    Assert((it.Next() else { "?" }) == "b")
    Assert(it.Next().IsNone())
    var e := ",a,".Split(",")
    Assert((e.Next() else { "?" }) == "")
    Assert((e.Next() else { "?" }) == "a")
    Assert((e.Next() else { "?" }) == "")
    Assert(e.Next().IsNone())
    Assert(StrSplitCount("", ",") == 1)
    Assert(StrSplitCount("a<>b<>c", "<>") == 3)
    Assert(StrSplitCount("aaaa", "aa") == 3)
}

test "SplitOnce returns the first split and keeps empty sides" {
    for c in [(s: "name=a=b", sep: "=", left: "name", right: "a=b"),
        (s: "aaa", sep: "aa", left: "", right: "a"),
        (s: "x=", sep: "=", left: "x", right: ""),
        (s: "=", sep: "=", left: "", right: ""),
        (s: "é☃尾", sep: "☃", left: "é", right: "尾")] {
        parts := c.s.SplitOnce(c.sep).Must()
        Assert(parts.before == c.left && parts.after == c.right)
    }
    Assert("abc".SplitOnce("x").IsNone())
    Assert("".SplitOnce("x").IsNone())
    Assert("ab".SplitOnce("abc").IsNone())
}

test "SplitOnce releases the separator" (sys: &System) {
    var sep := Text.New(sys.arena)
    try sep.Push("::")
    parts := "a::b".SplitOnce(sep.Str()).Must()
    sep.Clear()
    Assert(parts.before == "a" && parts.after == "b")
}

test "Lines, Words and Scalars start at the text" {
    Assert(StrLineCount("") == 0)
    Assert(StrWordCount("") == 0)
    var sc := "".Scalars()
    Assert(sc.Next().IsNone())
}

test "From rejects overlongs, surrogates and truncation" {
    good: [11]u8 = [0x61, 0xC3, 0xA9, 0xE2, 0x82, 0xAC, 0xF0, 0x9F, 0x98, 0x80, 0x7A]
    s := str.From(good[..]) else {
        Assert(false)
        return
    }
    Assert(s.Len() == 11)
    overlong: [2]u8 = [0xC0, 0xAF]
    Assert(StrFromFails(overlong[..]))
    surrogate: [3]u8 = [0xED, 0xA0, 0x80]
    Assert(StrFromFails(surrogate[..]))
    above: [4]u8 = [0xF4, 0x90, 0x80, 0x80]
    Assert(StrFromFails(above[..]))
    cut: [2]u8 = [0xE2, 0x82]
    Assert(StrFromFails(cut[..]))
    lone: [1]u8 = [0x80]
    Assert(StrFromFails(lone[..]))
}

test "ToLower and ToUpper map non-ASCII" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    Assert(try "Hello, World".ToLower(&ar) == "hello, world")
    Assert(try "Hello, World".ToUpper(&ar) == "HELLO, WORLD")
    Assert(try "\u{C9}COLE \u{394}\u{3A3}".ToLower(&ar) == "\u{E9}cole \u{3B4}\u{3C3}")
    Assert(try "stra\u{DF}e \u{3C9}".ToUpper(&ar) == "STRA\u{DF}E \u{3A9}")
    Assert(try "\u{23A}".ToLower(&ar) == "\u{2C65}")
    Assert(try "\u{2C65}".ToUpper(&ar) == "\u{23A}")
    Assert(try "\u{10400}".ToLower(&ar) == "\u{10428}")
    Assert(try "".ToUpper(&ar) == "")
}

test "Lines strips newlines and ends at a final one" {
    var a := "a\n\n".Lines()
    Assert((a.Next() else { "?" }) == "a")
    Assert((a.Next() else { "?" }) == "")
    Assert(a.Next().IsNone())
    var b := "a\r\nb".Lines()
    Assert((b.Next() else { "?" }) == "a")
    Assert((b.Next() else { "?" }) == "b")
    Assert(b.Next().IsNone())
    var c := "a\rb\r".Lines()
    Assert((c.Next() else { "?" }) == "a\rb\r")
    Assert(c.Next().IsNone())
    Assert(StrLineCount("a") == 1)
    Assert(StrLineCount("a\n") == 1)
    Assert(StrLineCount("\n") == 1)
}

test "Split over a multi-byte separator" {
    var it := "x--y----z".Split("--")
    Assert((it.Next() else { "?" }) == "x")
    Assert((it.Next() else { "?" }) == "y")
    Assert((it.Next() else { "?" }) == "")
    Assert((it.Next() else { "?" }) == "z")
    Assert(it.Next().IsNone())
}

test "Words splits on Unicode White_Space" {
    var w := "  one\u{A0}two\t\u{2003}three\u{3000}".Words()
    Assert((w.Next() else { "?" }) == "one")
    Assert((w.Next() else { "?" }) == "two")
    Assert((w.Next() else { "?" }) == "three")
    Assert(w.Next().IsNone())
    Assert(StrWordCount(" \t\n ") == 0)
    Assert(StrWordCount("a\u{200B}b") == 1)
}

test "Scalars decodes every length" {
    var it := "a\u{E9}\u{20AC}\u{1F600}".Scalars()
    Assert((it.Next() else { ' ' }) == 'a')
    Assert((it.Next() else { ' ' }) == '\u{E9}')
    Assert((it.Next() else { ' ' }) == '\u{20AC}')
    Assert((it.Next() else { ' ' }) == '\u{1F600}')
    Assert(it.Next().IsNone())
}

test "Split over a computed separator" (sys: &System) {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var tmp := Text.New(&ar)
    try tmp.Push(";")
    try tmp.Push(";")
    var it := "a;;b;;;;c".Split(tmp.Str())
    Assert((it.Next() else { "?" }) == "a")
    Assert((it.Next() else { "?" }) == "b")
    Assert((it.Next() else { "?" }) == "")
    Assert((it.Next() else { "?" }) == "c")
    Assert(it.Next().IsNone())
}

// StrAt copies b to offset k of a 96-byte buffer and gives From's verdict
// and the word count, or -1 when From fails.
StrAt(b: []u8, k: int): int {
    var buf: [96]u8 = [0; 96]
    for i in 0..b.Len() {
        buf[k + i] = b[i]
    }
    s := str.From(buf[k..k + b.Len()]) else {
        return -1
    }
    return StrWordCount(s)
}

test "From at every alignment" {
    text := "ab cd\u{E9}f \u{20AC}\u{20AC} ghijklmnop\u{A0}q \u{1F600}rstu vwxyz0123456789"
    b := text.AsBytes()
    want := StrWordCount(text)
    for k in 0..8 {
        Assert(StrAt(b, k) == want)
    }
    // One bad byte at every position, at every offset.
    var bad: [40]u8 = [0x61; 40]
    for p in 0..40 {
        bad[p] = 0xFF
        for k in 0..8 {
            Assert(StrAt(bad[..], k) == -1)
        }
        bad[p] = 0x61
        for k in 0..8 {
            Assert(StrAt(bad[..], k) == 1)
        }
    }
}
