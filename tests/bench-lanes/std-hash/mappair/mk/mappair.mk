// mappair, Mk, idiomatic std: the task of
// ../README.md with std's Map[Pt, u64] made by Map.New (the fold hash) in
// a sub-arena per round. Pt is two u64 fields with the Hash impl a derive
// writes (each field's Hash in order), so the key takes HashOf's Hash impl
// path. Usage: mappair <n> <rounds> <seed>. Prints the checksum.

struct Pt {
    a: u64
    b: u64
}

impl Eq for Pt {
    Eq(&self, other: &Pt): bool {
        return self.a == other.a && self.b == other.b
    }
}

impl Hash for Pt {
    Hash(&self, h: mut Hasher) {
        self.a.Hash(h)
        self.b.Hash(h)
    }
}

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Round(h: &Arena, n: int, seed: u64): OutOfMemory!u64 {
    var a := try h.Sub()
    defer a.Free()
    var m := Map[Pt, u64].New(&a)
    var st := seed
    for i in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        try m.Set(Pt(a: Mix(st), b: st), Trunc[u64](i))
    }
    var sum: u64 = 0
    st = seed
    for i in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        k := Pt(a: Mix(st), b: st)
        if v := m.Get(&k) {
            sum = sum +% *v
        }
        miss := Pt(a: Mix(st), b: st ^ 1)
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
        k := Pt(a: Mix(st), b: st)
        if m.Has(&k) {
            sum = sum +% 3
        }
    }
    return sum +% Trunc[u64](m.Len())
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    seed := Cast[u64](try sys.args[3].ParseInt())
    var total: u64 = 0
    for r in 0..rounds {
        total = total +% try Round(sys.arena, n, seed +% Trunc[u64](r))
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
