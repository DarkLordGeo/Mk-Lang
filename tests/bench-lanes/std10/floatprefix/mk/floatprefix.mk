// floatprefix, Mk: str.ParseFloatPrefix over 4,096 texts "<a>.<b>e<e>;"
// built from splitmix64 words (README.md), reps passes, pass r starting at
// text r: the prefix up to ';' is the float, the ';' ends it. Each result
// folds bits >> 32 of the value and the bytes used into a wrapping u64
// sum; every value is correctly rounded, so every column agrees.
// Usage: floatprefix <reps> <seed>.

const N: int = 4096

Mix(z0: u64): u64 {
    var z := z0
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    reps := Cast[int](try sys.args[1].ParseInt())
    var st := Cast[u64](try sys.args[2].ParseInt())
    var xs := List[str].New(sys.arena)
    for _ in 0..N {
        st = st +% 0x9E3779B97F4A7C15
        z := Mix(st)
        a := z % 1000000
        b := (z >> 20) % 10000000
        e := Cast[int]((z >> 50) % 41) - 20
        try xs.Push(try sys.arena.Fmt("{a}.{b}e{e};", (a, b, e)))
    }
    var acc: u64 = 0
    for r in 0..reps {
        for i in 0..N {
            p := xs[(i + r) & (N - 1)].ParseFloatPrefix()
            acc = acc +% (p.value.Bits() >> 32) +% Cast[u64](p.used)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{acc}\n", (acc))
}
