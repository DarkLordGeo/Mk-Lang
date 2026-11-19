// btree, Mk, std.coll.BTree: the workload of ../README.md. n keys from
// splitmix64(seed) inserted (key k, value k * 3), n lookups of keys from a
// second stream (half present), one full ascending iteration, then every
// inserted key removed. Usage: btree <n> <seed>. Prints the checksum.

import std.coll

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    seed := Cast[u64](try sys.args[2].ParseInt())
    var t := coll.BTree[u64, u64].New(sys.arena)
    var s := seed
    for _ in 0..n {
        s = s +% 0x9E3779B97F4A7C15
        k := Mix(s) >> 1
        try t.Set(k, k *% 3)
    }
    var sum: u64 = 0
    var q := seed
    for i in 0..n {
        q = q +% 0x9E3779B97F4A7C15
        var k := Mix(q) >> 1
        if i & 1 == 1 {
            k = Mix(q +% 7) >> 1
        }
        if v := t.Get(&k) {
            sum = sum +% *v
        }
    }
    var it := t.Iter()
    for e := it.Next() {
        sum = sum *% 31 +% *e.key
    }
    s = seed
    for _ in 0..n {
        s = s +% 0x9E3779B97F4A7C15
        k := Mix(s) >> 1
        if v := t.Remove(&k) {
            sum = sum +% v
        }
    }
    left := t.Len()
    var out := sys.Stdout()
    try Print(mut out, "{sum} {left}\n", (sum, left))
}
