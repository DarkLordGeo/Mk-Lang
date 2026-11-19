// stat, Mk: the facts of one existing file, n times, through
// fs.Stat (following links, as Go's os.Stat); sum adds each answer's size.
// Prints n, sum and the failed calls. Usage: stat <path> <n>.

import std.fs

export Main(sys: &System): ParseError|WriteError!void {
    p := sys.args[1]
    n := Cast[int](try sys.args[2].ParseInt())
    var sum: i64 = 0
    var fails := 0
    for _ in 0..n {
        info := fs.Stat(sys.io, p) else {
            fails += 1
            continue
        }
        sum += info.size
    }
    var out := sys.Stdout()
    try Print(mut out, "{n} {sum} {fails}\n", (n, sum, fails))
}
