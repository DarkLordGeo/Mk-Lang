// encoding-hex, Mk: std.encoding's hex. n bytes of splitmix64 output (seed
// 1), rounds times: HexEncode into an arena made before the loop and reset
// each round, then HexDecode into the same arena. The checksum adds, per
// round, the text's length, the decoded length and one decoded byte at a
// moving index.
// Usage: encoding-hex <n> <rounds>.

import std.encoding

export Main(sys: &System): encoding.Error|OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
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
    var a := try Arena.New(sys.heap)
    defer a.Free()
    var total: u64 = 0
    for r in 0..rounds {
        text := try encoding.HexEncode(&a, data[..])
        out := try encoding.HexDecode(&a, text)
        total = total +% Cast[u64](text.Len()) +% Cast[u64](out.Len()) +% u64(out[(r * 7919) % n])
        a.Reset()
    }
    var w := sys.Stdout()
    try Print(mut w, "{total}\n", (total))
}
