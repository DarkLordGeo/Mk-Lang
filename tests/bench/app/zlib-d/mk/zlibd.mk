// zlib-d, Mk, std: the task of README.md with std.compress's streaming
// zlib Inflater (RFC 1950; it checks the header and the trailer's Adler-32
// inside the decompressor). The compressed file is read through
// fs.File.Reader with a 64 KiB buffer, the Inflater writes the decompressed
// stream into a 64 KiB buffer, and each buffer is written to the output
// file with File.WriteAll. The program prints the count of decompressed
// bytes.
// Usage: zlibd <in> <out>.

import std.compress
import std.fs

type UnzlibError = fs.Error | ReadError | compress.Error | WriteError

export Main(sys: &System): UnzlibError!void {
    var a := try Arena.New(sys.heap)
    defer a.Free()
    rbuf := try a.Slice[u8](65536)
    buf := try a.Slice[u8](65536)
    var src := try fs.Open(sys.io, sys.args[1])
    onerr src.Abandon()
    var dst := try fs.Create(sys.io, sys.args[2])
    onerr dst.Abandon()
    var r := src.Reader(sys.io, rbuf)
    var z := compress.Inflater.NewZlib(&a)
    var total: i64 = 0
    for {
        n := try z.Read(mut r, buf)
        if n == 0 {
            break
        }
        try dst.WriteAll(sys.io, buf[..n])
        total += i64(n)
    }
    try dst.Close(sys.io)
    try src.Close(sys.io)
    try sys.Print("{total}\n", (total))
}
