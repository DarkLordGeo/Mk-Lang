// log-stderr, Mk: write R records of about 80 bytes, an access-log line
// with five integer fields, through log.Stderr, one task, then print the
// records and the sum of the bytes fields. Each record is formatted on the
// stack and written with one write under standard error's lock, after the
// bytes buffered on the stream. Usage: log-stderr <records>.

import std.log

export Main(sys: &System): ParseError|WriteError!void {
    records := Cast[int](try sys.args[1].ParseInt())
    sink := log.Stderr(log.Level.Info)
    var sum := 0
    for i in 0..records {
        p := i % 1000
        b := i * 7 % 100_000
        u := i % 977
        sink.Info(sys.io, "request id={i} path=/api/v1/items/{p} status=200 bytes={b} took_us={u}", (i, p, b, u)) else {
            break
        }
        sum += b
    }
    try sys.Print("{records} {sum}\n", (records, sum))
}
