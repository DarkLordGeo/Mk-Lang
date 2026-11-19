// regexpatho, Mk, idiomatic std: ../README.md's task with std.regex. The
// pattern (a|aa)*c, on which a backtracking engine takes time exponential
// in the input, is compiled once with regex.Compile; each round runs
// Regex.Find on n bytes of 'a' (no match) and on the same bytes with a
// final 'c' (a match of the whole text). Usage: regexpatho <n> <rounds>.
// Prints the checksum: per round, n plus the second match's length.

import std.regex

const Pattern: str = "(a|aa)*c"

export Main(sys: &System): regex.Error|ParseError|OutOfMemory|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    buf := try sys.arena.Slice[u8](n + 1)
    for i in 0..n {
        buf[i] = b'a'
    }
    buf[n] = b'c'
    all := str.From(buf[..]).Must()
    as := all[0..n]
    re := try regex.Compile(sys.arena, Pattern)
    var total: i64 = 0
    for _ in 0..rounds {
        if m := re.Find(as) {
            total -= Cast[i64](m.hi - m.lo + 1)
        }
        if m := re.Find(all) {
            total += Cast[i64](n) + Cast[i64](m.hi - m.lo)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
