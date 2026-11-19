// regexreplace, Mk, idiomatic std: ../README.md's task with std.regex.
// regex.Compile once, then each round Regex.Replace of every match of
// (svc|req)=([a-z0-9]+) in the whole log by $2:$1 into an arena Reset
// after the round, adding the output's length and a sample of its bytes
// (every 4,096th byte, weighted by its position) to the checksum. Usage:
// regexreplace <file> <rounds>. Prints the checksum.

import std.fs
import std.regex

const Pattern: str = "(svc|req)=([a-z0-9]+)"
const Template: str = "$2:$1"

export Main(sys: &System): fs.Error|regex.Error|ParseError|WriteError!void {
    text := try fs.ReadText(sys.arena, sys.io, sys.args[1])
    rounds := Cast[int](try sys.args[2].ParseInt())
    re := try regex.Compile(sys.arena, Pattern)
    var a := try Arena.New(sys.heap)
    var total: i64 = 0
    for _ in 0..rounds {
        out := re.Replace(&a, text, Template) else {
            a.Free()
            fail OutOfMemory
        }
        b := out.AsBytes()
        total += Cast[i64](b.Len())
        var i := 0
        for i < b.Len() {
            total += Cast[i64](b[i]) * Cast[i64](i / 4096 % 251 + 1)
            i += 4096
        }
        a.Reset()
    }
    a.Free()
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
