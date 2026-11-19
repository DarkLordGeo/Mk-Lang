// regexfindall, Mk, idiomatic std: ../README.md's task with std.regex.
// The pattern [a-z]+=5[0-9][0-9] has no literal prefix (an engine cannot
// skip to a fixed string), so it measures the automaton over the whole
// text: regex.Compile once, then each round Regex.FindAll over the whole
// log into an arena Reset after the round, adding 1,000,000 plus the
// length of every match. Usage: regexfindall <file> <rounds>. Prints the
// checksum.

import std.fs
import std.regex

const Pattern: str = "[a-z]+=5[0-9][0-9]"

export Main(sys: &System): fs.Error|regex.Error|ParseError|WriteError!void {
    text := try fs.ReadText(sys.arena, sys.io, sys.args[1])
    rounds := Cast[int](try sys.args[2].ParseInt())
    re := try regex.Compile(sys.arena, Pattern)
    var a := try Arena.New(sys.heap)
    var total: i64 = 0
    for _ in 0..rounds {
        all := re.FindAll(&a, text) else {
            a.Free()
            fail OutOfMemory
        }
        for m in &all {
            total += 1_000_000 + Cast[i64](m.hi - m.lo)
        }
        a.Reset()
    }
    a.Free()
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
