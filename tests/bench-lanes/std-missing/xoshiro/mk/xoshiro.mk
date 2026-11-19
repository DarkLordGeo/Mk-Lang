// xoshiro, Mk: the same-algorithm workload of ../README.md (xoshiro256**
// seeded through SplitMix64, Lemire's Below) through std.math.Rng, which is
// that algorithm, so this column measures std's code: RotL64,
// Mul128 and the wrapping operators as Mk compiles them. The sums wrap by
// design (+%). Usage: xoshiro <n> <seed>. Prints the checksum.

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
