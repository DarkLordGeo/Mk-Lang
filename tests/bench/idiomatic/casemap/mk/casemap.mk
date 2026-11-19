// casemap, Mk, idiomatic std: the task of ../README.md with std's lower-case
// mapping, str.ToLower(a) (one scalar to one), on each line of the
// generated mixed-script text (str.Lines), each result in one Arena made
// before the rounds and reset after each. The text is built once with
// Text.PushChar from splitmix64. Usage: casemap <kib> <rounds> <seed>.
// Prints the checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

// The alphabet: ASCII, Latin-1, Greek without sigma, and Cyrillic, upper
// and lower case, as inclusive code point ranges (../README.md).
const Ranges: [11](lo: u32, hi: u32) = [(lo: 0x41, hi: 0x5A), (lo: 0x61, hi: 0x7A), (lo: 0xC0, hi: 0xD6), (lo: 0xD8, hi: 0xDE), (lo: 0xE0, hi: 0xF6), (lo: 0xF8, hi: 0xFE), (lo: 0x391, hi: 0x3A1), (lo: 0x3A4, hi: 0x3A9), (lo: 0x3B1, hi: 0x3C1), (lo: 0x3C4, hi: 0x3C9), (lo: 0x410, hi: 0x44F)]

Gen(a: &Arena, bytes: int, seed: u64): OutOfMemory!str from a {
    var table := List[u32].New(a)
    for r in Ranges {
        var c := r.lo
        for c <= r.hi {
            try table.Push(c)
            c += 1
        }
    }
    tab := table.Done()
    var t := Text.New(a)
    var st := seed
    for t.Str().Len() < bytes {
        st = st +% 0x9e3779b97f4a7c15
        x := Mix(st)
        letters := Cast[int](x % 12) + 1
        for _ in 0..letters {
            st = st +% 0x9e3779b97f4a7c15
            y := Mix(st)
            try t.PushChar(Cast[char](tab[Cast[int](y % Cast[u64](tab.Len()))]))
        }
        st = st +% 0x9e3779b97f4a7c15
        if Mix(st) % 12 == 0 {
            try t.Push("\n")
        } else {
            try t.Push(" ")
        }
    }
    try t.Push("\n")
    return t.Done()
}

Round(a: &Arena, text: str): OutOfMemory!u64 {
    var h: u64 = 0xcbf29ce484222325
    var n: u64 = 0
    var lines := text.Lines()
    for line := lines.Next() {
        lo := try line.ToLower(a)
        for c in lo.AsBytes() {
            h = (h ^ u64(c)) *% 0x100000001b3
        }
        n = n +% Cast[u64](lo.Len())
    }
    return h +% n
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    kib := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    seed := Cast[u64](try sys.args[3].ParseInt())
    text := try Gen(sys.arena, kib * 1024, seed)
    var a := try Arena.New(sys.heap)
    defer a.Free()
    var total: u64 = 0
    for _ in 0..rounds {
        total = total +% try Round(&a, text)
        a.Reset()
    }
    try sys.Print("{total}\n", (total))
}
