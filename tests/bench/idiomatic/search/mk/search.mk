// search, Mk, idiomatic std: the task of ../README.md with std's binary
// search, Slice[u64].Search(&k, Less): the first index whose element is
// not less than k (the comparator is a const fn argument, so the call is
// direct). The keys are distinct, so a found key has one index. Usage:
// search <n> <lookups> <seed>. Prints the checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Less(e: &u64, k: &u64): bool {
    return *e < *k
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    lookups := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    xs := try sys.arena.Slice[u64](n)
    for i in 0..n {
        xs[i] = Cast[u64](2 * i + 1)
    }
    span := Cast[u64](2 * n + 1)
    var total: u64 = 0
    for _ in 0..lookups {
        st = st +% 0x9e3779b97f4a7c15
        k := Mix(st) % span
        i := xs.Search(&k, Less)
        if i < n && xs[i] == k {
            total = total +% Cast[u64](i + 1)
        } else {
            total = total +% 1
        }
    }
    try sys.Print("{total}\n", (total))
}
