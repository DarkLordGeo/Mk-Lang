// regex, Mk, idiomatic std: the task of ../README.md with std.regex
// (RE2 syntax, a Pike VM: time linear in the input). The pattern is
// compiled once with regex.Compile; each round splits the log with
// str.Lines and runs Regex.Find on every line, counting the matches and
// adding their lengths. Usage: regex <file> <rounds>. Prints the checksum.

import std.fs
import std.regex

const Pattern: str = "status=5[0-9][0-9] dur=[0-9]{4,}ms"

export Main(sys: &System): fs.Error|regex.Error|ParseError|WriteError!void {
    text := try fs.ReadText(sys.arena, sys.io, sys.args[1])
    rounds := Cast[int](try sys.args[2].ParseInt())
    re := try regex.Compile(sys.arena, Pattern)
    var total: i64 = 0
    for _ in 0..rounds {
        var lines := text.Lines()
        for line := lines.Next() {
            if m := re.Find(line) {
                total += 1_000_000 + Cast[i64](m.hi - m.lo)
            }
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
