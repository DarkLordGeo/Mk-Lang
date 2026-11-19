// encoding-single, Mk: std.encoding's windows-1252 decoder. The input is
// the windows-1252 line of README.md repeated n times; rounds times it is
// decoded to UTF-8 by Windows1252.Decode into an arena reset each round.
// The checksum adds, per round, the text's length and one byte of it at a
// moving index.
// Usage: encoding-single <n> <rounds>.

import std.encoding

const Line: []u8 = [0x43, 0x61, 0x66, 0xE9, 0x20, 0x63, 0x72, 0xE8, 0x6D, 0x65, 0x20, 0x62, 0x72, 0xFB, 0x6C, 0xE9,
    0x65, 0x2C, 0x20, 0x6E, 0x61, 0xEF, 0x76, 0x65, 0x20, 0x93, 0x71, 0x75, 0x6F, 0x74, 0x65, 0x64,
    0x94, 0x20, 0x96, 0x20, 0x80, 0x20, 0x35, 0x2C, 0x20, 0xA9, 0x20, 0x32, 0x30, 0x32, 0x36, 0x0A]

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    m := Line.Len()
    bytes := try sys.arena.Slice[u8](n * m)
    for k in 0..n {
        (mut bytes[k * m..(k + 1) * m]).CopyFrom(Line)
    }
    var a := try Arena.New(sys.heap)
    defer a.Free()
    var total: u64 = 0
    for r in 0..rounds {
        s := try encoding.Windows1252.Decode(&a, bytes[..])
        b := s.AsBytes()
        total = total +% Cast[u64](b.Len()) +% u64(b[(r * 7919) % b.Len()])
        a.Reset()
    }
    var w := sys.Stdout()
    try Print(mut w, "{total}\n", (total))
}
