// regexnoprefix, Mk, idiomatic std: ../README.md's task with std.regex.
// The log filter of tests/bench/idiomatic/regex with the literal prefix
// taken out of the pattern, [a-z]+=5[0-9]{2} [a-z]+=[0-9]{4,}ms, so no
// engine can skip to a fixed string and the row measures the automata.
// regex.Compile once; each round splits the log with str.Lines and runs
// Regex.Find on every line, adding 1,000,000 plus the match's length.
// Usage: regexnoprefix <file> <rounds>. Prints the checksum.

import std.fs
import std.regex

const Pattern: str = "[a-z]+=5[0-9]{2} [a-z]+=[0-9]{4,}ms"

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
