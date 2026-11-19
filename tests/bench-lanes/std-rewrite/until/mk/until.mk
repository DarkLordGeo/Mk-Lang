// until, Mk: split a file into records through fs.File.Reader with
// a 64 KiB buffer and Reader.Until into a 64 KiB record buffer,
// once on 0x0A and once on 0x20, r times; prints the records and the
// bytes taken (each record with its delimiter). Usage: until <file> <r>.

import std.fs

type UntilError = fs.Error | ReadError | ParseError | WriteError

Split(sys: &System, p: str, delim: u8, buf: mut []u8, rec: mut []u8): fs.Error|ReadError!(n: i64, bytes: i64) {
    var f := try fs.Open(sys.io, p)
    onerr f.Abandon()
    var r := f.Reader(sys.io, buf)
    var n: i64 = 0
    var bytes: i64 = 0
    for {
        k := try r.Until(delim, rec)
        if k == 0 {
            break
        }
        n += 1
        bytes += i64(k)
    }
    try f.Close(sys.io)
    return (n: n, bytes: bytes)
}

export Main(sys: &System): UntilError!void {
    p := sys.args[1]
    reps := try sys.args[2].ParseInt()
    var buf: [65536]u8 = [0; 65536]
    var rec: [65536]u8 = [0; 65536]
    var n: i64 = 0
    var bytes: i64 = 0
    for _ in 0..reps {
        for delim in [0x0A, 0x20] {
            s := try Split(sys, p, Trunc[u8](delim), mut buf[..], mut rec[..])
            n += s.n
            bytes += s.bytes
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{n} {bytes}\n", (n, bytes))
}
