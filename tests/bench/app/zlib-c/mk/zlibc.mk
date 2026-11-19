// zlib-c, Mk, std: the task of README.md with std.compress's streaming
// encoder in its zlib framing (RFC 1950) at level 6. The input file is read
// through fs.File.Read in 64 KiB chunks, each chunk goes to the encoder,
// and the encoder writes through the output file's Writer, which has a
// 64 KiB buffer. Finish writes the final block and the Adler-32 trailer.
// The program prints the count of input bytes.
// Usage: zlibc <in> <out>.

import std.compress
import std.fs

type ZlibError = fs.Error | WriteError | OutOfMemory

export Main(sys: &System): ZlibError!void {
    var a := try Arena.New(sys.heap)
    defer a.Free()
    buf := try a.Slice[u8](65536)
    wbuf := try a.Slice[u8](65536)
    var src := try fs.Open(sys.io, sys.args[1])
    onerr src.Abandon()
    var dst := try fs.Create(sys.io, sys.args[2])
    onerr dst.Abandon()
    var w := dst.Writer(sys.io, wbuf)
    var enc := try compress.Encoder.New(&a, compress.Framing.Zlib, 6)
    onerr enc.Abandon()
    var total: i64 = 0
    for {
        n := try src.Read(sys.io, buf)
        if n == 0 {
            break
        }
        try enc.Write(mut w, buf[..n])
        total += i64(n)
    }
    try enc.Finish(mut w)
    try w.Flush()
    try dst.Close(sys.io)
    try src.Close(sys.io)
    try sys.Print("{total}\n", (total))
}
