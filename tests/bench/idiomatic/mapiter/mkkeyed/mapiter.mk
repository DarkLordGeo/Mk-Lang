// mapiter, Mk keyed, idiomatic std: the mk column's program (../mk) with
// one change, the map: Map.NewKeyed under a key from HashKey.New, made once
// per process (SipHash-1-3 under 16 bytes of Io.Random), the
// DoS-resistant peer of Rust's RandomState and Go's per-map seed.
// Iteration does not hash; the one fill does. Usage: mapiter <n> <passes>
// <seed>. Prints the checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

export Main(sys: &System): Canceled|OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    passes := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    hk := try HashKey.New(sys.io)
    var m := Map[u64, u64].NewKeyed(sys.arena, hk)
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
