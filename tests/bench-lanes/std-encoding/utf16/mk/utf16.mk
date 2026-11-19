// encoding-utf16, Mk: std.encoding's UTF-16LE decoder. The input is the
// UTF-16LE form of the line of README.md repeated n times, made with
// Utf16Le.Encode before the loop; rounds times it is decoded to UTF-8 by
// Utf16Le.Decode into an arena reset each round. The checksum adds, per
// round, the text's length and one byte of it at a moving index.
// Usage: encoding-utf16 <n> <rounds>.

import std.encoding

const Line: str = "Hello, w\u{f6}rld! \u{41f}\u{440}\u{438}\u{432}\u{435}\u{442} \u{43c}\u{438}\u{440}. \u{6c34}\u{306f}\u{51b7}\u{305f}\u{3044} \u{1F600}\n"

export Main(sys: &System): encoding.Error|OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var text := Text.New(sys.arena)
    for _ in 0..n {
        try text.Push(Line)
    }
    bytes := try encoding.Utf16Le.Encode(sys.arena, text.Str())
    var a := try Arena.New(sys.heap)
    defer a.Free()
    var total: u64 = 0
    for r in 0..rounds {
        s := try encoding.Utf16Le.Decode(&a, bytes)
        b := s.AsBytes()
        total = total +% Cast[u64](b.Len()) +% u64(b[(r * 7919) % b.Len()])
        a.Reset()
    }
    var w := sys.Stdout()
    try Print(mut w, "{total}\n", (total))
}
