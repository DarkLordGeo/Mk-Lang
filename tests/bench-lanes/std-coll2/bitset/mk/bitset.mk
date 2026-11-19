// bitset, Mk, std.coll.Bitset: the workload of ../README.md. n bits; every
// 3rd bit set, every 7th cleared, rounds times over the whole set with the
// offsets shifted by the round; then Count, and a Has scan summing the set
// indices. Usage: bitset <n> <rounds>.

import std.coll

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var b := try coll.Bitset.New(sys.arena, n)
    var total := 0
    for r in 0..rounds {
        var i := r % 3
        for i < n {
            b.Set(i)
            i += 3
        }
        i = r % 7
        for i < n {
            b.Remove(i)
            i += 7
        }
        total += b.Count()
    }
    var sum := 0
    for i in 0..n {
        if b.Has(i) {
            sum = sum +% i
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{total} {sum}\n", (total, sum))
}
