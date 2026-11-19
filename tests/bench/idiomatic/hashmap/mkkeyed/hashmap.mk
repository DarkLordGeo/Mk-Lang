// hashmap, Mk keyed, idiomatic std: the mk column's program (../mk) with
// one change, the map: Map.NewKeyed under a key from HashKey.New, made once
// per process (SipHash-1-3 under 16 bytes of Io.Random), the
// DoS-resistant peer of Rust's RandomState and Go's per-map seed.
// Usage: hashmap <n> <rounds> <seed>. Prints the checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Round(h: &Arena, n: int, seed: u64, key: HashKey): OutOfMemory!u64 {
    var a := try h.Sub()
    defer a.Free()
    var m := Map[u64, u64].NewKeyed(&a, key)
    var st := seed
    for i in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        try m.Set(Mix(st), Trunc[u64](i))
    }
    var sum: u64 = 0
    st = seed
    for i in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        k := Mix(st)
        if v := m.Get(&k) {
            sum = sum +% *v
        }
        miss := Mix(st ^ 0x5555555555555555)
        if m.Has(&miss) {
            sum = sum +% 1
        }
        if i % 2 == 0 {
            _ = m.Remove(&k)
        }
    }
    st = seed
    for _ in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        k := Mix(st)
        if m.Has(&k) {
            sum = sum +% 3
        }
    }
    return sum +% Trunc[u64](m.Len())
}

export Main(sys: &System): Canceled|OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    seed := Cast[u64](try sys.args[3].ParseInt())
    key := try HashKey.New(sys.io)
    var total: u64 = 0
    for r in 0..rounds {
        total = total +% try Round(sys.arena, n, seed +% Trunc[u64](r), key)
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
