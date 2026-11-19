// regexunicode, Mk, idiomatic std: ../README.md's task with std.regex.
// Three patterns over Unicode tables, \pL+ (a word of letters),
// \w+\s\pL and \p{Lu}\pL* (a capitalised word), each compiled once; each
// round runs Regex.FindAll of each over the whole log into an arena Reset
// after each pattern, adding 1,000,000 plus the length of every match.
// The classes are large UTF-8 DAGs, so the row measures the DFAs on them
// and their budgets. Usage: regexunicode <file> <rounds>.
// Prints the checksum.

import std.fs
import std.regex

const Patterns: []str = ["\\pL+", "\\w+\\s\\pL", "\\p{Lu}\\pL*"]

export Main(sys: &System): fs.Error|regex.Error|ParseError|WriteError!void {
    text := try fs.ReadText(sys.arena, sys.io, sys.args[1])
    rounds := Cast[int](try sys.args[2].ParseInt())
    var res := List[regex.Regex].New(sys.arena)
    for p in Patterns {
        re := try regex.Compile(sys.arena, p)
        try res.Push(re)
    }
    var a := try Arena.New(sys.heap)
    var total: i64 = 0
    for _ in 0..rounds {
        for re in &res {
            all := re.FindAll(&a, text) else {
                a.Free()
                fail OutOfMemory
            }
            for m in &all {
                total += 1_000_000 + Cast[i64](m.hi - m.lo)
            }
            a.Reset()
        }
    }
    a.Free()
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
