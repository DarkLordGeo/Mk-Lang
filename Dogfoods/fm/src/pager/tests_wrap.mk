// Why: wrapping decides what the pager and `fm view` show.
// What: tests of Wrap.
// How: fitting lines, word breaks, wide characters, empty lines and CRLF.

Lines(a: &Arena, src: str, width: int): List[str] from a, src {
    return WrapAll(a, src, width).Must()
}

test "Wrap keeps a line that fits whole" (sys: &System) {
    l := Lines(sys.arena, "short line", 20)
    Assert(l.Len() == 1)
    Assert(l[0] == "short line")
}

test "Wrap breaks at a word boundary and drops the space at the break" (sys: &System) {
    l := Lines(sys.arena, "the quick brown fox", 10)
    Assert(l.Len() == 2)
    Assert(l[0] == "the quick")
    Assert(l[1] == "brown fox")
}

test "Wrap breaks a word wider than the line between clusters" (sys: &System) {
    l := Lines(sys.arena, "abcdefghij", 4)
    Assert(l.Len() == 3)
    Assert(l[0] == "abcd")
    Assert(l[2] == "ij")
}

test "Wrap counts a wide character as two columns" (sys: &System) {
    l := Lines(sys.arena, "漢字漢字", 5)
    Assert(l.Len() == 2)
    Assert(l[0] == "漢字")
}

test "Wrap keeps empty lines and starts no line after a final newline" (sys: &System) {
    l := Lines(sys.arena, "a\n\nb\n", 10)
    Assert(l.Len() == 3)
    Assert(l[1] == "")
    Assert(l[2] == "b")
}

test "Wrap strips a carriage return before a newline" (sys: &System) {
    l := Lines(sys.arena, "one\r\ntwo", 10)
    Assert(l[0] == "one")
    Assert(l[1] == "two")
}
