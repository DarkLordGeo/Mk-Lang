// encoding-utf8valid, Mk: std.encoding's UTF-8 decoder on valid text. The
// input is the line of README.md repeated n times; rounds times it is
// decoded by Utf8.Decode into a new arena each round, freed at the
// round's end (a fresh allocation, as every column makes), which checks it
// once and copies it. The checksum adds, per round, the text's length and
// one byte of it at a moving index.
// Usage: encoding-utf8valid <n> <rounds>.

import std.encoding

const Line: str = "Hello, w\u{f6}rld! \u{41f}\u{440}\u{438}\u{432}\u{435}\u{442} \u{43c}\u{438}\u{440}. \u{6c34}\u{306f}\u{51b7}\u{305f}\u{3044} \u{1F600}\n"

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var text := Text.New(sys.arena)
    for _ in 0..n {
        try text.Push(Line)
    }
    src := text.Str().AsBytes()
    bytes := try sys.arena.Slice[u8](src.Len())
    bytes.CopyFrom(src)
    var total: u64 = 0
    for r in 0..rounds {
        // A new arena each round, freed at its end: the text is a fresh
        // allocation each round, as every column's is.
        var a := try Arena.New(sys.heap)
        defer a.Free()
        s := try encoding.Utf8.Decode(&a, bytes[..])
        b := s.AsBytes()
        total = total +% Cast[u64](b.Len()) +% u64(b[(r * 7919) % b.Len()])
    }
    var w := sys.Stdout()
    try Print(mut w, "{total}\n", (total))
}
