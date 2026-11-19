// encoding-base64, Mk: std.encoding's standard padded alphabet. n bytes of
// splitmix64 output (seed 1), rounds times: Base64Encode into an arena made
// before the loop and reset each round, then Base64DecodeTo into one buffer
// made before the loop. The checksum adds, per round, the text's length
// and one decoded byte at a moving index, so it is the same in every
// column and costs nothing beside the codec.
// Usage: encoding-base64 <n> <rounds>.

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
    out := try sys.arena.Slice[u8](n)
    var a := try Arena.New(sys.heap)
    defer a.Free()
    var total: u64 = 0
    for r in 0..rounds {
        text := try encoding.Base64Encode(&a, data[..], encoding.Base64)
        m := try encoding.Base64DecodeTo(mut out[..], text, encoding.Base64)
        total = total +% Cast[u64](text.Len()) +% Cast[u64](m) +% u64(out[(r * 7919) % n])
        a.Reset()
    }
    var w := sys.Stdout()
    try Print(mut w, "{total}\n", (total))
}
