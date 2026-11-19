// log-sink, Mk: write R records of about 100 bytes, an access-log line
// with five integer fields, through log.ToFile into a new file, one task,
// then print the records and the file's size. Each record is formatted on
// the stack and written with one write under the file's mutex.
// Usage: log-sink <prefix> <records>; the file is <prefix>-mk.log.

import std.fs
import std.log

Run(sys: &System, path: str, records: int): fs.Error!i64 {
    f := try fs.Create(sys.io, path)
    m := Mutex[fs.File].New(f)
    sink := log.ToFile(&m, log.Level.Info)
    for i in 0..records {
        p := i % 1000
        b := i * 7 % 100_000
        u := i % 977
        sink.Info(sys.io, "request id={i} path=/api/v1/items/{p} status=200 bytes={b} took_us={u}", (i, p, b, u)) else {
            break
        }
    }
    var file := m.Into()
    onerr file.Abandon()
    try file.Close(sys.io)
    size := (try fs.Stat(sys.io, path)).size
    try fs.Remove(sys.io, path)
    return size
}

export Main(sys: &System): fs.Error|ParseError|WriteError|OutOfMemory!void {
    records := Cast[int](try sys.args[2].ParseInt())
    path := try sys.arena.Fmt("{p}-mk.log", (p: sys.args[1]))
    size := try Run(sys, path, records)
    try sys.Print("{records} {size}\n", (records, size))
}
