// Why: a CSV writer must quote exactly where readers need it, so what it
// writes reads back field for field.
//
// What: WriteRecord and NeedsQuotes.
//
// How: one RFC 4180 record. A field is quoted only where RFC 4180 section 2
// needs it: when it holds the delimiter, the quote, CR or LF (rules 6 and
// 7), or when it is the record's only field and empty, which would
// otherwise write a blank line that readers skip. A quote inside a quoted
// field is doubled. Records end with CR LF (rule 1). Unlike
// encoding/csv's Writer, a field with a leading space or the text \. is
// written bare, and a CR or LF inside a field is written as it is.

import std.simd

/// Writes values as one record to w, then CR LF, quoting a field only
/// where RFC 4180 needs it. WriteError is w's, and after it w holds an
/// unknown prefix of the record. Panics on options a reader refuses too
/// (see Records.New). O(total bytes), each field searched once; allocates
/// nothing.
export WriteRecord(w: mut Writer, values: []str, o: &Options): WriteError!void {
    CheckOptions(o)
    var qb: [1]u8 = [o.quote_byte]
    if values.Len() == 1 && values[0].Len() == 0 {
        var empty: [4]u8 = [o.quote_byte, o.quote_byte, 0x0D, 0x0A]
        try w.Bytes(empty[..])
        return
    }
    var delim: [1]u8 = [o.delimiter]
    var stops: [3]u8 = [o.delimiter, o.quote_byte, 0x0A]
    for i in 0..values.Len() {
        if i > 0 {
            try w.Bytes(delim[..])
        }
        b := values[i].AsBytes()
        if !NeedsQuotes(b, stops[..]) {
            try w.Bytes(b)
            continue
        }
        try w.Bytes(qb[..])
        var at := 0
        for at < b.Len() {
            k := simd.IndexByte(b[at..], o.quote_byte) else {
                try w.Bytes(b[at..])
                break
            }
            // Through the quote, then the quote again.
            try w.Bytes(b[at..at + k + 1])
            try w.Bytes(qb[..])
            at += k + 1
        }
        try w.Bytes(qb[..])
    }
    var crlf: [2]u8 = [0x0D, 0x0A]
    try w.Bytes(crlf[..])
}

// NeedsQuotes reports whether field b holds a byte of stops (the
// delimiter, the quote and LF) or a CR.
NeedsQuotes(b: []u8, stops: []u8): bool {
    if simd.IndexAny(b, stops).IsSome() {
        return true
    }
    return simd.IndexByte(b, 0x0D).IsSome()
}
