// compress-unzstd, Mk: std.compress.ZstdDecoder reads a zstd frame (libzstd
// 1.5.7 level 19 with no content checksum, 524,288 bytes of text,
// std/testdata/zstd/real-l19nc.zst) rounds times. std has no Reader over
// memory, so each round opens the file and reads it through fs.File.Reader
// with a 64 KiB buffer (from the OS file cache after the first round), as
// the Zig column does. Each round makes a decoder (a ZstdDecoder's end of
// stream is sticky) with the default window limit (window_limit 0, 8 MiB,
// as ZstdContent and an idiomatic caller use it), in one arena
// reset after each round; New zero-fills the window and buffers (8.5 MiB)
// every round, a cost of Mk's API
// that the Zig column, whose window buffer is made once, does not pay. The
// output is read in 64 KiB chunks; the checksum adds every decoded byte
// and each round's length.
// Usage: compress-unzstd <file> <rounds>.

import std.compress
import std.fs

type UnzstdError = fs.Error | ReadError | compress.Error | OutOfMemory | ParseError | WriteError

// Round decodes the file once with a decoder in a and returns the sum of
// its bytes plus its length.
Round(sys: &System, a: &Arena, p: str, rbuf: mut []u8, out: mut []u8): fs.Error|ReadError|compress.Error!u64 {
    var z := compress.ZstdDecoder.New(a, 0)
    var f := try fs.Open(sys.io, p)
    onerr f.Abandon()
    var r := f.Reader(sys.io, rbuf)
    var sum: u64 = 0
    var n: u64 = 0
    for {
        k := try z.Read(mut r, out)
        if k == 0 {
            break
        }
        for c in out[0..k] {
            sum = sum +% u64(c)
        }
        n = n +% Cast[u64](k)
    }
    try f.Close(sys.io)
    return sum +% n
}

export Main(sys: &System): UnzstdError!void {
    p := sys.args[1]
    rounds := Cast[int](try sys.args[2].ParseInt())
    var rbuf: [65536]u8 = [0; 65536]
    var out: [65536]u8 = [0; 65536]
    var total: u64 = 0
    var a := try Arena.New(sys.heap)
    for _ in 0..rounds {
        match Round(sys, &a, p, mut rbuf[..], mut out[..]) {
            ok(s) {
                total = total +% s
            }
            err(e) {
                a.Free()
                fail e
            }
        }
        a.Reset()
    }
    a.Free()
    var w := sys.Stdout()
    try Print(mut w, "{total}\n", (total))
}
