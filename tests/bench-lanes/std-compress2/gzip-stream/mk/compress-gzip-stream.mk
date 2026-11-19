// compress-gzip-stream, Mk: the streaming std.compress.Encoder (gzip, its
// one level) over a 50 MB log text written in 64 KiB pieces, rounds times,
// each round's stream sent through one fs.File.Writer with a 64 KiB
// buffer to the null device (the path argument: NUL on Windows) and ended
// with Finish. The Encoder's buffers come from one arena made before the
// loop and reset after each round. The checksum adds every input byte and
// the input length once per round, so it does not depend on the
// compressor (TestCompress_EncoderRoundTrip checks the output).
// Usage: compress-gzip-stream <file> <out> <rounds>.

import std.compress
import std.fs

type StreamError = fs.Error | OutOfMemory | ParseError | WriteError

// Round compresses text once into w as one gzip member.
Round(a: &Arena, w: mut Writer, text: []u8): OutOfMemory|WriteError!void {
    var e := try compress.Encoder.New(a, compress.Framing.Gzip, 6)
    onerr e.Abandon()
    var p := 0
    n := text.Len()
    for p < n {
        k := if n - p > 65536 { 65536 } else { n - p }
        try e.Write(w, text[p..p + k])
        p += k
    }
    try e.Finish(w)
}

// Rounds runs the rounds through one Writer over f and flushes it; it
// returns the checksum.
Rounds(sys: &System, f: mut fs.File, a: mut Arena, wbuf: mut []u8, text: []u8, rounds: int, sum: u64): WriteError|OutOfMemory!u64 {
    var w := f.Writer(sys.io, wbuf)
    var total: u64 = 0
    for _ in 0..rounds {
        try Round(&*a, mut w, text)
        a.Reset()
        total = total +% sum
    }
    try w.Flush()
    return total
}

export Main(sys: &System): StreamError!void {
    text := try fs.ReadBytes(sys.arena, sys.io, sys.args[1])
    rounds := Cast[int](try sys.args[3].ParseInt())
    var sum: u64 = 0
    for c in text {
        sum = sum +% u64(c)
    }
    sum = sum +% Cast[u64](text.Len())
    var f := try fs.Create(sys.io, sys.args[2])
    onerr f.Abandon()
    var wbuf: [65536]u8 = [0; 65536]
    var a := try Arena.New(sys.heap)
    defer a.Free()
    total := try Rounds(sys, mut f, mut a, mut wbuf[..], text, rounds, sum)
    try f.Close(sys.io)
    var o := sys.Stdout()
    try Print(mut o, "{total}\n", (total))
}
