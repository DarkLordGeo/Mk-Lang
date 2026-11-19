// intsum, Mk, checked: the reference algorithm of ../README.md. The sum's +
// is checked (overflow class "never": no input of the stated sizes
// overflows), and every xs[i] is bounds checked. The fill uses the wrapping
// operators because splitmix64 wraps by design.
// Usage: intsum <n> <rounds> <seed>. Prints the checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    xs := try sys.arena.Slice[i64](n)
    for i in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        xs[i] = Cast[i64](Mix(st) & 1023)
    }
    var total: i64 = 0
    for r in 0..rounds {
        var s: i64 = 0
        for i in 0..n {
            s += xs[i]
        }
        total += s
        xs[r % n] = s & 1023
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
