// Why: recovery depends on telling a torn record from a corrupt one.
// What: tests of Records over one-record logs.
// How: a whole record, a record cut short, and a damaged one with bytes
// after it.

import src.store

/// A log of one header and one record, `SET k v`, in `out`; returns its length.
OneRecord(out: mut []u8): int {
    head := Header(9)
    start := RecordStart(store.Op.Set, "k", "v")
    var n := 0
    for b in head {
        out[n] = b
        n += 1
    }
    for b in start {
        out[n] = b
        n += 1
    }
    out[n] = b'k'
    out[n + 1] = b'v'
    return n + 2
}

/// How a scan of `data` ends, and how many records it reads first.
Scan(data: []u8): (records: int, end: End) {
    var it := Records.New(data)
    var n := 0
    for r := it.Next() {
        _ = r
        n += 1
    }
    return (records: n, end: it.End())
}

test "a whole record reads back, and its header says generation 9" {
    var buf: [64]u8 = [0; 64]
    n := OneRecord(mut buf[..])
    Assert(ReadHeader(buf[..n]).Must().generation == 9)
    (records, end) := Scan(buf[..n])
    Assert(records == 1)
    Assert(CorruptAt(end).IsNone())
}

test "a record cut short at the end is torn, not corrupt" {
    var buf: [64]u8 = [0; 64]
    n := OneRecord(mut buf[..])
    (records, end) := Scan(buf[..n - 1])
    Assert(records == 0)
    match end {
        .Torn(offset, bytes) {
            Assert(offset == 24 && bytes == i64(n - 1 - 24))
        }
        _ {
            Assert(false)
        }
    }
}

test "a damaged record with bytes after it is corrupt" {
    var buf: [64]u8 = [0; 64]
    n := OneRecord(mut buf[..])
    buf[n - 1] = b'w'
    (records, end) := Scan(buf[..n + 3])
    Assert(records == 0)
    Assert(CorruptAt(end).Must() == 24)
}
