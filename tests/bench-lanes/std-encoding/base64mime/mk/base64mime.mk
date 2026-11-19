// encoding-base64mime, Mk: std.encoding's forgiving decoder (the WHATWG
// Infra Standard's forgiving-base64, which skips ASCII whitespace). n bytes
// of splitmix64 output (seed 1) are encoded once with the standard padded
// alphabet and broken into lines of cols characters, each ending in CRLF
// (cols 0: one line, no break); rounds times the text is decoded by
// Base64DecodeTo with Base64Forgiving into one buffer made before the loop.
// The checksum adds, per round, the decoded length and one decoded byte at
// a moving index.
// Usage: encoding-base64mime <n> <rounds> <cols>.

import std.encoding

export Main(sys: &System): encoding.Error|OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    cols := Cast[int](try sys.args[3].ParseInt())
    data := try sys.arena.Slice[u8](n)
    var st: u64 = 1
    var i := 0
    for i < n {
        st = st +% 0x9E3779B97F4A7C15
        var z := st
        z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
        z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
        z = z ^ (z >> 31)
        var k := 0
        for k < 8 && i < n {
            data[i] = Trunc[u8](z >> Trunc[u64](8 * k))
            k += 1
            i += 1
        }
    }
    plain := try encoding.Base64Encode(sys.arena, data[..], encoding.Base64)
    var text := Text.New(sys.arena)
    if cols == 0 {
        try text.Push(plain)
    } else {
        var at := 0
        for at < plain.Len() {
            end := if plain.Len() - at > cols { at + cols } else { plain.Len() }
            try text.Push(plain[at..end])
            try text.Push("\r\n")
            at = end
        }
    }
    s := text.Str()
    out := try sys.arena.Slice[u8](n)
    var total: u64 = 0
    for r in 0..rounds {
        m := try encoding.Base64DecodeTo(mut out[..], s, encoding.Base64Forgiving)
        total = total +% Cast[u64](m) +% u64(out[(r * 7919) % n])
    }
    var w := sys.Stdout()
    try Print(mut w, "{total}\n", (total))
}
