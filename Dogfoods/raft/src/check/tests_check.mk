// Why: the checker must accept every linearizable history and refuse others.
// What: tests on small hand-made histories.
// How: each builds a history with Linear and compares the verdict.

import src.history

Linear(a: &Arena, text: str): bool {
    h := history.Parse(a, text).Must()
    return Check(a, &h).Must().ok
}

test "a write then a read of it is linearizable" (sys: &System) {
    Assert(Linear(sys.arena, """
        0 10 20 put x 5
        1 30 40 get x -> 5
        """))
}

test "a read that misses a write finished before it is not linearizable" (sys: &System) {
    Assert(!Linear(sys.arena, """
        0 10 20 put x 5
        1 30 40 get x -> 0
        """))
}

test "overlapping operations may take effect in either order" (sys: &System) {
    Assert(Linear(sys.arena, """
        0 10 50 put x 1
        1 20 60 put x 2
        2 70 80 get x -> 1
        """))
}

test "an unanswered write may take effect or not" (sys: &System) {
    Assert(Linear(sys.arena, """
        0 10 - put x 7
        1 30 40 get x -> 7
        2 50 60 get x -> 7
        """))
    Assert(Linear(sys.arena, """
        0 10 - put x 7
        1 30 40 get x -> 0
        """))
}

test "a compare-and-swap succeeds only on the value it expects" (sys: &System) {
    Assert(Linear(sys.arena, """
        0 10 20 put x 3
        1 30 40 cas x 3 4 -> ok
        2 50 60 cas x 3 9 -> fail
        0 70 80 get x -> 4
        """))
    Assert(!Linear(sys.arena, """
        0 10 20 put x 3
        1 30 40 cas x 2 4 -> ok
        """))
}

test "registers are checked one at a time" (sys: &System) {
    Assert(Linear(sys.arena, """
        0 10 20 put x 1
        1 10 20 put y 2
        0 30 40 get y -> 2
        1 30 40 get x -> 1
        """))
}

test "Check refuses a register with more operations than it tracks" (sys: &System) {
    var t := Text.New(sys.arena)
    for i in 0..MaxOps + 1 {
        line := try sys.arena.Fmt("0 {a} {b} put x {i}\n", (a: i * 10, b: i * 10 + 5, i))
        try t.Push(line)
    }
    h := try history.Parse(sys.arena, t.Str())
    var refused := false
    _ = Check(sys.arena, &h) else {
        refused = true
    }
    Assert(refused)
}
