// encoding-utf16enc, Mk: std.encoding's UTF-16LE encoder. The input is the
// line of README.md repeated n times; rounds times it is encoded by
// Utf16Le.Encode into an arena reset each round. The checksum adds, per
// round, the encoding's length in bytes and one byte of it at a moving
// index.
// Usage: encoding-utf16enc <n> <rounds>.

import std.encoding

const Line: str = "Hello, w\u{f6}rld! \u{41f}\u{440}\u{438}\u{432}\u{435}\u{442} \u{43c}\u{438}\u{440}. \u{6c34}\u{306f}\u{51b7}\u{305f}\u{3044} \u{1F600}\n"

export Main(sys: &System): encoding.Error|OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var text := Text.New(sys.arena)
    for _ in 0..n {
        try text.Push(Line)
    }
    s := text.Str()
    var a := try Arena.New(sys.heap)
    defer a.Free()
    var total: u64 = 0
    for r in 0..rounds {
        b := try encoding.Utf16Le.Encode(&a, s)
        total = total +% Cast[u64](b.Len()) +% u64(b[(r * 7919) % b.Len()])
        a.Reset()
    }
    var w := sys.Stdout()
    try Print(mut w, "{total}\n", (total))
}
