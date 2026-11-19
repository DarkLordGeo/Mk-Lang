// mapstr, Mk, idiomatic std: the task of
// ../README.md with std's Map[str, u64] made by Map.New (the fold hash) in
// a sub-arena per round. The keys are views of one generated str (no copy
// per key), split by Words once, before the rounds. Usage: mapstr <n>
// <rounds> <seed> <minlen> <maxlen>. Prints the checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

// Gen is the n keys, each followed by a space, and their misses (the
// first letter in upper case, so never a key), in the same order: two
// passes of the same generator, the first for the length, then one fill
// of each byte array and one UTF-8 check.
Gen(a: &Arena, n: int, seed: u64, min_len: int, max_len: int): OutOfMemory!(keys: str, misses: str) from a {
    span := Cast[u64](max_len - min_len + 1)
    var st := seed
    var total := 0
    for _ in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        total += min_len + Cast[int](Mix(st) % span) + 1
    }
    var kb := try a.Slice[u8](total)
    var mb := try a.Slice[u8](total)
    st = seed
    var p := 0
    for _ in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        var z := Mix(st)
        l := min_len + Cast[int](z % span)
        for j in 0..l {
            if j % 8 == 0 {
                z = Mix(z +% Cast[u64](j))
            }
            c := Trunc[u8](0x61 + ((z >> Cast[u64](8 * (j % 8))) & 0xFF) % 26)
            kb[p] = c
            if j == 0 {
                mb[p] = c - 0x20
            } else {
                mb[p] = c
            }
            p += 1
        }
        kb[p] = 0x20
        mb[p] = 0x20
        p += 1
    }
    keys := str.From(kb[..]) else { return (keys: "", misses: "") }
    misses := str.From(mb[..]) else { return (keys: "", misses: "") }
    return (keys: keys, misses: misses)
}

// Round needs its into clause: the checker cannot tell the str
// keys copied out of ks from ks's own storage.
Round(h: &Arena, ks: &List[str], ms: &List[str], r: u64): OutOfMemory!u64 *ks into ks {
    var a := try h.Sub()
    defer a.Free()
    var m := Map[str, u64].New(&a)
    n := ks.Len()
    for i in 0..n {
        try m.Set(ks[i], Trunc[u64](i) +% r)
    }
    var sum: u64 = 0
    for i in 0..n {
        if v := m.Get(&ks[i]) {
            sum = sum +% *v
        }
        if m.Has(&ms[i]) {
            sum = sum +% 1
        }
        if i % 2 == 0 {
            _ = m.Remove(&ks[i])
        }
    }
    for i in 0..n {
        if m.Has(&ks[i]) {
            sum = sum +% 3
        }
    }
    return sum +% Trunc[u64](m.Len())
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    seed := Cast[u64](try sys.args[3].ParseInt())
    min_len := Cast[int](try sys.args[4].ParseInt())
    max_len := Cast[int](try sys.args[5].ParseInt())
    g := try Gen(sys.arena, n, seed, min_len, max_len)
    var ks := List[str].New(sys.arena)
    var ms := List[str].New(sys.arena)
    var kw := g.keys.Words()
    for w := kw.Next() {
        try ks.Push(w)
    }
    var mw := g.misses.Words()
    for w := mw.Next() {
        try ms.Push(w)
    }
    var total: u64 = 0
    for r in 0..rounds {
        total = total +% try Round(sys.arena, &ks, &ms, Trunc[u64](r))
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
