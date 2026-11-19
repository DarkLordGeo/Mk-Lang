// Why: some commands read records split by a delimiter byte, not lines.
// What: RecordSource.
// How: Reader.Until, joining reads for a record longer than the buffer.

/// Records of one input that each end at a delimiter byte, read with
/// `Reader.Until`, of any length: a record longer than the read buffer is
/// put together from several reads. A last record with no delimiter is
/// still a record.
export view struct RecordSource {
    reader: mut Reader
    chunk: mut []u8
    pending: List[u8]
    delim: u8
    record: int
    eof: bool
}

/// A source over `reader` that splits at `delim`, with a read buffer of
/// `size` bytes, at least 1. Allocates the buffer in `a`.
export RecordSource.New(a: &Arena, reader: mut Reader, delim: u8, size: int): OutOfMemory!RecordSource from a, reader {
    chunk := try a.Slice[u8](if size < 1 { 1 } else { size })
    return RecordSource(
        reader: reader,
        chunk: chunk,
        pending: List[u8].New(a),
        delim: delim,
        record: 0,
        eof: false,
    )
}

/// The next record without its delimiter, copied into `a`, or `none` at the
/// end of the input. Fails with `BadUtf8`, counting records as lines, on a
/// record that is not UTF-8. O(record length); allocates the record in `a`
/// and grows the pending buffer to the longest record.
export RecordSource.Next(mut self, a: &Arena): Error!?str from a {
    if self.eof {
        return none
    }
    self.pending.Clear()
    for {
        n := try self.reader.Until(self.delim, mut self.chunk[..])
        if n == 0 {
            self.eof = true
            if self.pending.Len() == 0 {
                return none
            }
            break
        }
        ended := self.chunk[n - 1] == self.delim
        end := if ended { n - 1 } else { n }
        for b in self.chunk[..end] {
            try self.pending.Push(b)
        }
        if ended {
            break
        }
    }
    self.record += 1
    s := str.From(self.pending.Items()) else {
        fail BadUtf8(line: self.record)
    }
    return some(try a.Dup(s))
}
