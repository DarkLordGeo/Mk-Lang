// sieve, Mk, checked: the reference algorithm of ../README.md. A []u8 of
// n + 1 flags, every index bounds checked, every integer + checked
// (overflow class "never").
// Usage: sieve <n> <rounds>. Prints the checksum.

// tools/bench flips this line in a copy for the counting build.
const BenchCount: bool = false

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    flags := try sys.arena.Slice[u8](n + 1)
    var total := 0
    var marks := 0
    for _ in 0..rounds {
        for i in 0..n + 1 {
            flags[i] = 1
        }
        var count := 0
        for i in 2..n + 1 {
            if flags[i] != 0 {
                count += 1
                var j := i * i
                for j <= n {
                    flags[j] = 0
                    if BenchCount {
                        marks += 1
                    }
                    j += i
                }
            }
        }
        total += count
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
    if BenchCount {
        try Print(mut out, "counts marks={marks}\n", (marks))
    }
}
