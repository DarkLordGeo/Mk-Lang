// sortstr, Mk: the task of ../../README.md (the row "sortstr") with std's
// Slice.Sort over a List[str] of 16-byte views of one buffer, compared by
// str's byte order. Usage: sortstr <n> <rounds> <seed>. Prints the
// checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

const Hex = "0123456789abcdef"

// Unhex is the u64 a key's 16 lowercase hex digits spell.
Unhex(s: str): u64 {
    var v: u64 = 0
    for i in 0..16 {
        c := s.Byte(i)
        d := if c <= b'9' { c - b'0' } else { c - b'a' + 10 }
        v = (v << 4) | Cast[u64](d)
    }
    return v
}

// Fold samples the sorted keys: every 1,024th as a number times its index,
// and the last.
Fold(xs: &List[str]): u64 {
    n := xs.Len()
    var sum: u64 = Unhex(xs[n - 1])
    var i := 0
    for i < n {
        sum = sum +% Unhex(xs[i]) *% Cast[u64](i)
        i += 1024
    }
    return sum
}

// Round writes the n keys from st0, sorts them in a sub-arena of h and
// returns their fold.
Round(h: &Arena, n: int, st0: u64): OutOfMemory!u64 {
    var a := try h.Sub()
    defer a.Free()
    var buf := try a.Slice[u8](n * 16)
    var st := st0
    for i in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        z := Mix(st)
        for j in 0..16 {
            buf[i * 16 + j] = Hex.Byte(Cast[int]((z >> Cast[u64](60 - 4 * j)) & 15))
        }
    }
    var xs := List[str].New(&a)
    for i in 0..n {
        s := str.From(buf[i * 16..i * 16 + 16]) else {
            return 0
        }
        try xs.Push(s)
    }
    xs.Sort(fn(p: &str, q: &str): bool { return *p < *q })
    return Fold(&xs)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    seed := Cast[u64](try sys.args[3].ParseInt())
    var total: u64 = 0
    for r in 0..rounds {
        st := seed +% Cast[u64](r) *% 0x5555555555555555
        total = total +% try Round(sys.arena, n, st)
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
