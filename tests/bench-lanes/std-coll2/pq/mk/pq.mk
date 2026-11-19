// pq, Mk, std.coll.PriorityQueue: the workload of ../README.md. n values
// from splitmix64(seed) pushed, then every value popped (least first); the
// checksum folds the pops in order. Usage: pq <n> <seed>.

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
    var q := coll.PriorityQueue[u64].New(sys.arena)
    var s := seed
    for _ in 0..n {
        s = s +% 0x9E3779B97F4A7C15
        try q.Push(Mix(s))
    }
    var sum: u64 = 0
    for x := q.Pop() {
        sum = sum *% 31 +% x
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
