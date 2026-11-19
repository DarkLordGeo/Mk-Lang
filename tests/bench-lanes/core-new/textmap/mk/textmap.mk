// textmap, Mk: the task of ../README.md with std's TextMap[u64] over the
// process heap (Set, then Replace), keys the decimal digits of a
// splitmix64 value modulo n / 4 + 1, so three Sets in four overwrite.
// The map copies a key only when it is new. Usage: textmap <n> <rounds>
// <seed>. Prints the checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

// Digits writes x in decimal at the end of buf and returns where it starts.
Digits(buf: mut []u8, x: u64): int {
    var i := buf.Len() - 1
    var v := x
    buf[i] = Trunc[u8](v % 10) + 0x30
    v = v / 10
    for v > 0 {
        i -= 1
        buf[i] = Trunc[u8](v % 10) + 0x30
        v = v / 10
    }
    return i
}

Round(a: &Alloc, n: int, seed: u64): OutOfMemory!u64 {
    var m := TextMap[u64].New(a)
    onerr m.Free(a)
    u := Trunc[u64](n / 4 + 1)
    var buf: [20]u8 = [0; 20]
    var st := seed
    for i in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        p := Digits(mut buf[..], Mix(st) % u)
        try m.Set(a, str.From(buf[p..]).Must(), Trunc[u64](i))
    }
    var sum: u64 = 0
    st = seed ^ 0x5555555555555555
    for i in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        p := Digits(mut buf[..], Mix(st) % u)
        old := try m.Replace(a, str.From(buf[p..]).Must(), Trunc[u64](i))
        if v := old {
            sum = sum +% v
        } else {
            sum = sum +% 1
        }
    }
    sum = sum +% Trunc[u64](m.Len())
    m.Free(a)
    return sum
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    seed := Cast[u64](try sys.args[3].ParseInt())
    var total: u64 = 0
    for r in 0..rounds {
        total = total +% try Round(sys.heap.Alloc(), n, seed +% Trunc[u64](r))
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
