// rng, Mk: the workload of ../README.md with std.math.Rng (xoshiro256**
// seeded through SplitMix64). The sums wrap by design (+%).
// Usage: rng <n> <seed>. Prints the checksum.

import std.math

export Main(sys: &System): ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    seed := Cast[u64](try sys.args[2].ParseInt())
    var r := math.Rng.New(seed)
    var total: u64 = 0
    for _ in 0..n {
        total = total +% r.U64()
        total = total +% r.Below(1000)
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
