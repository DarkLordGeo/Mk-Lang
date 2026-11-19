// regexgroup, Mk, idiomatic std: ../README.md's task with std.regex.
// regex.Compile of ([a-z]{1,64})=([0-9]+) once; each round runs
// Regex.FindAll over the whole log into an arena Reset after the round,
// then Regex.Group(text, m, 1) on every match, adding 1,000,000 plus the
// length of group 1. The counted repeat makes a program of about 130
// instructions, so the row measures Group's cost on short matches of a
// mid-sized program. Usage: regexgroup <file>
// <rounds>. Prints the checksum.

import std.fs
import std.regex

const Pattern: str = "([a-z]{1,64})=([0-9]+)"

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
            if g := re.Group(text, *m, 1) {
                total += 1_000_000 + Cast[i64](g.Len())
            }
        }
        a.Reset()
    }
    a.Free()
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
