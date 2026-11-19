// sieve, Mk, wrapping: the reference algorithm of ../README.md with count, j
// and total advanced by the wrapping +% (no overflow check); i * i stays
// checked. Every flags[i] is still bounds checked.
// Usage: sieve <n> <rounds>. Prints the checksum.

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    flags := try sys.arena.Slice[u8](n + 1)
    var total := 0
    for _ in 0..rounds {
        for i in 0..n + 1 {
            flags[i] = 1
        }
        var count := 0
        for i in 2..n + 1 {
            if flags[i] != 0 {
                count = count +% 1
                var j := i * i
                for j <= n {
                    flags[j] = 0
                    j = j +% i
                }
            }
        }
        total = total +% count
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
