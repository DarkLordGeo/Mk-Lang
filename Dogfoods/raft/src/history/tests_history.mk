// Why: a saved history must read back as written, and a bad one must say
// where it fails.
// What: tests of Parse and Write.
// How: FailedAt reports the line Parse stopped at.

// The line Parse stopped at, or 0 when it read the whole text.
FailedAt(a: &Arena, text: str): int {
    _ = Parse(a, text) else e {
        return match e {
            BadLine(line) { line }
            BadTime(line) { line }
            TooManyKeys(line) { line }
            _ { -1 }
        }
    }
    return 0
}

test "Write prints what Parse reads back" (sys: &System) {
    text := """
        # raft history v1: client call return op key args -> result
        0 10 20 get k0 -> 5
        1 15 - put k1 7
        2 30 40 cas k0 5 6 -> ok
        2 50 - cas k1 1 2 -> ?
        1 60 - get k0 -> ?

        """
    h := try Parse(sys.arena, text)
    Assert(h.ops.Len() == 5 && h.keys.Len() == 2)
    var out := Text.New(sys.arena)
    var w := out.Writer()
    try h.Write(mut w)
    Assert(out.Str() == text[..text.Len() - 1])
}

test "Parse names the line of a malformed operation" (sys: &System) {
    Assert(FailedAt(sys.arena, "0 1 2 put x 1\n0 3 4 pop x 1\n") == 2)
    Assert(FailedAt(sys.arena, "0 1 2 get x 1\n") == 1)
    Assert(FailedAt(sys.arena, "0 1 2 put x one\n") == 1)
    Assert(FailedAt(sys.arena, "# only a comment\n\n0 5 4 put x 1\n") == 3)
    Assert(FailedAt(sys.arena, "0 1 2 cas x 1 2 -> maybe\n") == 1)
    Assert(FailedAt(sys.arena, "0 1 2 put x 1\n") == 0)
}
