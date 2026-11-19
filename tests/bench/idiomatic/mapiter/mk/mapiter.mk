// mapiter, Mk, idiomatic std: the task of ../README.md with std's hash
// map, Map[u64, u64] made by Map.New in sys.arena (the fixed fast hash),
// filled once with Set, then iterated <passes> times with
// `for (key, value) in m.Iter()`; the sum is order-free. Usage: mapiter <n>
// <passes> <seed>. Prints the checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    passes := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    var m := Map[u64, u64].New(sys.arena)
    for i in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        try m.Set(Mix(st), Cast[u64](i))
    }
    var total: u64 = 0
    for _ in 0..passes {
        for (key, value) in m.Iter() {
            total = total +% *key *% (*value +% 1)
        }
        total = total +% Cast[u64](m.Len())
    }
    try sys.Print("{total}\n", (total))
}
