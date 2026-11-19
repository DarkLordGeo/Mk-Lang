// read, Mk: read a file through fs.File.Reader with a 64 KiB
// buffer by Reader.Read calls of want bytes each, r times; prints the bytes
// read and the wrapping sum of each call's first byte. A want of at least
// the buffer reads straight into the caller's bytes (core_rw.mk, Go
// bufio's rule). Usage: read <file> <want> <r>.

import std.fs

type ReadRowError = fs.Error | ReadError | ParseError | WriteError

Pass(sys: &System, p: str, buf: mut []u8, d: mut []u8): fs.Error|ReadError!(bytes: i64, sum: u64) {
    var f := try fs.Open(sys.io, p)
    onerr f.Abandon()
    var r := f.Reader(sys.io, buf)
    var bytes: i64 = 0
    var sum: u64 = 0
    for {
        k := try r.Read(d)
        if k == 0 {
            break
        }
        bytes += i64(k)
        sum = sum +% u64(d[0])
    }
    try f.Close(sys.io)
    return (bytes: bytes, sum: sum)
}

export Main(sys: &System): ReadRowError!void {
    p := sys.args[1]
    want := Cast[int](try sys.args[2].ParseInt())
    reps := try sys.args[3].ParseInt()
    var buf: [65536]u8 = [0; 65536]
    var d: [131072]u8 = [0; 131072]
    var bytes: i64 = 0
    var sum: u64 = 0
    for _ in 0..reps {
        s := try Pass(sys, p, mut buf[..], mut d[0..want])
        bytes += s.bytes
        sum = sum +% s.sum
    }
    var out := sys.Stdout()
    try Print(mut out, "{bytes} {sum}\n", (bytes, sum))
}
