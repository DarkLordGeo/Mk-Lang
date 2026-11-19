// csv-read, Mk: read a log file (tools/bench gen kind log) as CSV records
// with the delimiter space, through csv.Records over fs.File.Reader with a
// 64 KiB buffer, r times; prints the records, the fields and the field
// bytes. Usage: csv-read <file> <r>.

import std.csv
import std.fs

export Main(sys: &System): fs.Error|OutOfMemory|ParseError|WriteError|csv.Error!void {
    reps := Cast[int](try sys.args[2].ParseInt())
    buf := try sys.arena.Slice[u8](1 << 16)
    // No inline buffer: Records reads straight into buf.
    rb := try sys.arena.Slice[u8](0)
    var o := csv.DefaultOptions
    o.delimiter = 0x20
    var records := 0
    var count := 0
    var bytes := 0
    for _ in 0..reps {
        var f := try fs.Open(sys.io, sys.args[1])
        onerr f.Abandon()
        var r := f.Reader(sys.io, mut rb[..])
        var rs := csv.Records.New(mut r, mut buf[..], &o)
        for rec := try rs.Next() {
            records += 1
            n := rec.Len()
            count += n
            for i in 0..n {
                bytes += (rec.Field(i) else { "" }).Len()
            }
        }
        try f.Close(sys.io)
    }
    try sys.Print("{records} {count} {bytes}\n", (records, count, bytes))
}
