// keywords, Mk, wrapping (the mk-wrap column): mk/keywords.mk with the
// timed loop's checked arithmetic written wrapping: offs[i +% 1] for
// offs[i + 1], i % 1000 +% 1 for i % 1000 + 1, and Trunc[u64] (low bits)
// for the checked Cast[u64]; so no overflow check and no conversion check
// per word. The bounds checks, the range check and the UTF-8 boundary
// checks of text[a..b] are the mk column's. Everything else is mk/'s.
// Usage: keywords <n> <rounds> <seed>. Prints the checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

const Keywords: [16]str = ["if", "else", "for", "while", "return", "break", "continue", "fn",
    "let", "var", "const", "match", "struct", "enum", "import", "export"]

// Classify is 1 + the index of the keyword w equals, or 0.
Classify(w: str): u64 {
    if w == "if" { return 1 }
    if w == "else" { return 2 }
    if w == "for" { return 3 }
    if w == "while" { return 4 }
    if w == "return" { return 5 }
    if w == "break" { return 6 }
    if w == "continue" { return 7 }
    if w == "fn" { return 8 }
    if w == "let" { return 9 }
    if w == "var" { return 10 }
    if w == "const" { return 11 }
    if w == "match" { return 12 }
    if w == "struct" { return 13 }
    if w == "enum" { return 14 }
    if w == "import" { return 15 }
    if w == "export" { return 16 }
    return 0
}

export Main(sys: &System): Utf8Error|OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    buf := try sys.arena.Slice[u8](n * 8)
    offs := try sys.arena.Slice[int](n + 1)
    var p := 0
    for i in 0..n {
        offs[i] = p
        st = st +% 0x9e3779b97f4a7c15
        z := Mix(st)
        kind := z % 4
        if kind < 2 {
            k := Keywords[Cast[int]((z >> 8) % 16)].AsBytes()
            for j in 0..k.Len() {
                buf[p] = k[j]
                p += 1
            }
            if kind == 1 {
                buf[p - 1] = Trunc[u8](0x61 + (z >> 16) % 26)
            }
        } else {
            l := 1 + Cast[int]((z >> 8) % 8)
            for _ in 0..l {
                st = st +% 0x9e3779b97f4a7c15
                buf[p] = Trunc[u8](0x61 + Mix(st) % 26)
                p += 1
            }
        }
    }
    offs[n] = p
    text := try str.From(buf[0..p])
    var total: u64 = 0
    for _ in 0..rounds {
        for i in 0..n {
            total = total +% Classify(text[offs[i]..offs[i +% 1]]) *% Trunc[u64](i % 1000 +% 1)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
