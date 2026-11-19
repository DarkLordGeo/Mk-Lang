// crypto-random, Mk: std.crypto.Random (Io.Random, the OS CSPRNG through
// std/sys: ProcessPrng on Windows) into one buffer of <size> bytes, <n>
// times. Prints n * size, the bytes drawn, which every column prints
// whatever the random bytes are (CRY-8). Usage: crypto-random <n> <size>.

import std.crypto

export Main(sys: &System): OutOfMemory|ParseError|WriteError|Canceled!void {
    n := Cast[int](try sys.args[1].ParseInt())
    size := Cast[int](try sys.args[2].ParseInt())
    buf := try sys.arena.Slice[u8](size)
    var total: u64 = 0
    for _ in 0..n {
        try crypto.Random(sys.io, mut buf[..])
        total = total +% Trunc[u64](buf.Len())
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
