// gzip, Mk, idiomatic std: the task of README.md with std.compress's
// streaming gzip decompressor, Inflater.NewGzip (it checks each member's
// header and its trailer's CRC-32 and ISIZE inside the decompressor). The
// compressed file is read once into memory (fs.ReadBytes, into an arena
// kept for the run); each round inflates it from a Reader.Of over those
// bytes, as the Go column does from a bytes.Reader. One arena is
// made before the loop and reset after each round, so the Inflater's
// window and input buffer (taken from it at the first Read) are reused
// each round. The output is read in 64 KiB chunks; the checksum adds every
// decompressed byte and the length.
// Usage: gzip <file> <rounds>.

import std.compress
import std.fs

type GzipError = fs.Error | ReadError | compress.Error | OutOfMemory | ParseError | WriteError

// Round inflates gz once and returns the sum of its bytes plus its length.
Round(a: &Arena, gz: []u8, out: mut []u8): ReadError|compress.Error!u64 {
    var r := Reader.Of(gz)
    var z := compress.Inflater.NewGzip(a)
    var sum: u64 = 0
    for {
        n := try z.Read(mut r, out)
        if n == 0 {
            break
        }
        for c in out[0..n] {
            sum = sum +% u64(c)
        }
        sum = sum +% Cast[u64](n)
    }
    return sum
}

export Main(sys: &System): GzipError!void {
    p := sys.args[1]
    rounds := Cast[int](try sys.args[2].ParseInt())
    var keep := try Arena.New(sys.heap)
    defer keep.Free()
    gz := try fs.ReadBytes(&keep, sys.io, p)
    var out: [65536]u8 = [0; 65536]
    var total: u64 = 0
    var a := try Arena.New(sys.heap)
    for _ in 0..rounds {
        match Round(&a, gz, mut out[..]) {
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
