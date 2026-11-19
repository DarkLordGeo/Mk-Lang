// Why: reading CSV fast means not copying fields and not allocating per
// record, while still meeting RFC 4180 and surviving a cancelled read.
//
// What: Records with New and Next, and Record with Len, Field and Line; the
// scanner Bare, Quoted, Closed and AtEnd, and the buffer moves Fill,
// Compact and Unshift.
//
// How: an RFC 4180 reader over a Reader whose fields are views of the
// caller's buffer, reused from record to record.
//
// The grammar is RFC 4180 section 2 with encoding/csv's reading of it,
// which the differential tests hold it to: a CR LF and a lone LF both end
// a record; a CR elsewhere is data, and so is a CR LF inside a quoted
// field, which RFC 4180's escaped rule makes content (encoding/csv reads
// it as LF; here what WriteRecord wrote reads back exactly, as in Rust's
// csv and Python's csv); a blank line (empty, or a lone CR before LF or
// the end) is skipped; the last record may lack its line break, and a CR
// right before
// the end is dropped; a quote inside an unquoted field, or a closing quote
// followed by anything but the delimiter or a line break, is Quote, as an
// input that ends inside a quoted field is. Unlike encoding/csv it checks
// no field count across records (FieldsPerRecord -1), and an error other
// than ReadError stays: every later Next fails with it.
//
// Layout of buf. Bytes read from the Reader sit in buf[0..end], end <=
// lim; the record in progress starts at rec. buf[lim..] is the field
// index: for field k, two little-endian u32 at lim + 8k, its first byte
// and its end, both from rec. lim is buf.Len() - 8 * slots, where slots is
// record_fields capped at buf.Len() / 32, so the index takes at most a
// quarter of buf, and a record is at most lim bytes.
//
// Fields are not copied. An unquoted field is a view of its bytes where
// they were read. A quoted field's content starts after its opening quote
// and is shifted left in place by `shift` bytes, the count of "" pairs
// folded so far, so the unquoting writes nothing before the
// field's first escape. The scan is a state machine whose whole state is
// in Records, so a ReadError leaves it where it was and the next call
// resumes (Next is repeatable, over a repeatable Reader).
//
// Search: a run ends at the delimiter, the quote or LF (inside quotes, at
// the quote or LF), found through a 64-byte mask of those bytes built by
// std.simd's 16-lane compares (Stop, Block), so a record of short fields
// costs one vector pass over its bytes and one count of trailing zeros per
// field, the structural-index shape of simdjson and simdcsv. A CR is
// looked at only beside an LF or the end.
//
// Refill: when the scan reaches end, the record so far moves to the front
// of buf if rec > 0 and less than half of the data area is free, then one
// Read fills buf[end..lim]. Each byte is moved at most once per record that
// spans a refill, so the cost is O(n) in the input whatever the Reader's
// read sizes. A record whose bytes reach lim with rec = 0 is TooLarge
// unless the input ends there: a record, its line break included, holds at
// most lim bytes, and a last record with no line break may fill all lim.
//
// Every bound comes from buf and the options: no allocation, no recursion,
// stack O(1).

import std.raw
import std.rt
import std.simd

/// A reader of RFC 4180 records from r, using buf for the bytes read and
/// the field index. Make it with New and call Next until it returns none.
export view struct Records {
    r: mut Reader
    buf: mut []u8
    delim: u8
    qb: u8
    field_bytes: int
    // mask: bit k set when buf[mpos + k] is the delimiter, the quote or
    // LF, for mpos + k below mlim (Block).
    mask: u64
    mpos: int
    mlim: int
    slots: int
    lim: int
    end: int
    rec: int
    pos: int
    // nf: fields of the record closed so far; fs: the raw position of the
    // open field's first content byte.
    nf: int
    fs: int
    shift: int
    state: Scan
    line: i64
    line_start: int
    // prev_len: the length of the line before line_start, a CR LF counted
    // as one byte, for Quote's column at the end of input.
    prev_len: int
    rec_line: i64
    eof: bool
    // bad: 0, or 1 for Quote and 2 for TooLarge, which every later Next
    // repeats with bad_line and bad_column.
    bad: u8
    bad_line: i64
    bad_column: int
}

// Scan is where the reader is in a record.
enum Scan {
    // Before a record: blank lines are skipped here.
    Line
    // Before a field.
    Field
    // In an unquoted field.
    Bare
    // In a quoted field's content.
    Quoted
    // Right after a quote inside a quoted field: an escape or the close.
    Closed
}

/// One record: its fields are views of the buffer Records reads into, valid
/// until the next Next.
export view struct Record {
    data: []u8
    // text: data as a str when all of it is UTF-8, checked once by Done,
    // so Field slices it; none sends Field to a check of its own bytes.
    text: ?str
    index: []u8
    n: int
    first: i64
}

/// A reader of the records in r, with buf for the bytes it reads and its
/// field index. Panics on options a reader cannot use (a delimiter or
/// quote that is CR or LF, the two equal, a negative field_bytes or
/// record_fields below 1): they come from the program. O(1); allocates
/// nothing.
///
/// The index takes 8 bytes per field slot at the end of buf, with
/// min(record_fields, buf.Len() / 32) slots; a record fits when it has at
/// most that many fields and its bytes, its line break included, fit in
/// the rest. So a buf of at least 32 * N and 4 * B / 3 bytes holds every
/// record of N fields and B bytes, and a record within the options that
/// does not fit buf is TooLarge too.
export Records.New(r: mut Reader, buf: mut []u8, o: &Options): Records from r, buf {
    CheckOptions(o)
    var slots := buf.Len() / 32
    if o.record_fields < slots {
        slots = o.record_fields
    }
    var lim := buf.Len() - 8 * slots
    // u32 offsets in the index: a record of 4 GiB or more is TooLarge.
    if lim > 0xFFFF_FFFF {
        lim = 0xFFFF_FFFF
    }
    return Records(r: r, buf: buf, delim: o.delimiter, qb: o.quote_byte, field_bytes: o.field_bytes, mask: 0, mpos: 0, mlim: 0, slots: slots, lim: lim, end: 0, rec: 0, pos: 0, nf: 0, fs: 0, shift: 0, state: Scan.Line, line: 1, line_start: 0, prev_len: 0, rec_line: 1, eof: false, bad: 0, bad_line: 0, bad_column: 0)
}

/// The next record, or none at the end of input. After Quote or TooLarge,
/// every later call fails with the same error. ReadError is r's; the reader
/// keeps its place, and calling again resumes. O(record bytes) amortized,
/// the bytes searched 16 at a time; allocates nothing.
export Records.Next(mut self): Error!?Record from self {
    if self.bad != 0 {
        try self.Repeat()
    }
    for {
        if self.pos == self.end {
            if self.eof {
                return try self.AtEnd()
            }
            try self.Fill()
            continue
        }
        match self.state {
            .Line {
                b := self.buf[self.pos]
                if b == 0x0A {
                    self.SkipLine(self.pos + 1)
                    continue
                }
                if b == 0x0D {
                    if self.pos + 1 == self.end && !self.eof {
                        try self.Fill()
                        continue
                    }
                    if self.pos + 1 == self.end {
                        // A lone CR before the end: a blank last line.
                        self.pos = self.end
                        self.rec = self.end
                        continue
                    }
                    if self.buf[self.pos + 1] == 0x0A {
                        self.SkipLine(self.pos + 2)
                        continue
                    }
                }
                self.rec_line = self.line
                self.state = Scan.Field
            }
            .Field {
                if self.nf == self.slots {
                    try self.Fail(2, self.rec_line, 0)
                }
                if self.buf[self.pos] == self.qb {
                    self.pos += 1
                    self.fs = self.pos
                    self.shift = 0
                    self.state = Scan.Quoted
                } else {
                    self.fs = self.pos
                    self.state = Scan.Bare
                }
            }
            .Bare {
                if try self.Bare() {
                    return some(self.Done())
                }
            }
            .Quoted {
                try self.Quoted()
            }
            .Closed {
                if r := try self.Closed() {
                    return some(r)
                }
            }
        }
    }
}

// Bare scans unquoted fields from pos: to the end of what was read, or to
// a delimiter, where it closes the field and goes on with the next one
// while that one is unquoted too, or to LF, where it closes the field and
// the record and Bare returns true: Next then returns Done's record, built
// once in Next's result rather than copied up through one of Bare's. A
// quote in it is Quote at the quote's column.
//
// The per-field steps are written out here rather than called: Stop's
// first test (a call that returned ?int through memory once per field) and
// Close, with the same checks in the same order.
Records.Bare(mut self): Error!bool {
    for {
        // Stop's first step: the mask's next bit at or after pos, when the
        // mask covers pos. There 0 <= pos - mpos < 64 (Block sets mlim to
        // at most mpos + 64), so the test of d below 64 never decides
        // anything; it bounds the shift for the compiler. Else Stop.
        at := self.pos
        var p := -1
        d := at - self.mpos
        if d >= 0 && d < 64 && at < self.mlim {
            m := self.mask & (~Cast[u64](0) << d)
            if m != 0 {
                p = self.mpos + raw.Ctz64(m)
            }
        }
        if p < 0 {
            p = self.Stop(at) else {
                // One byte of slack: a CR that an LF then drops. Written as
                // a subtraction: field_bytes may be the largest int (csv-1).
                if self.end - self.fs - 1 > self.field_bytes {
                    try self.Fail(2, self.rec_line, 0)
                }
                self.pos = self.end
                return false
            }
        }
        b := self.buf[p]
        if b == self.qb {
            try self.Fail(1, self.line, p - self.line_start + 1)
        }
        var e := p
        if b == 0x0A && e > self.fs && self.buf[e - 1] == 0x0D {
            e -= 1
        }
        // Close(e).
        if e - self.fs > self.field_bytes {
            try self.Fail(2, self.rec_line, 0)
        }
        at8 := self.lim + 8 * self.nf
        PutU32(mut self.buf[..], at8, self.fs - self.rec)
        PutU32(mut self.buf[..], at8 + 4, e - self.rec)
        self.nf += 1
        self.pos = p + 1
        if b != self.delim {
            self.NewLine(p, e)
            return true
        }
        // The next field, when it is read and unquoted, without a pass
        // through Next's dispatch.
        if self.pos == self.end || self.buf[self.pos] == self.qb || self.nf == self.slots {
            self.state = Scan.Field
            return false
        }
        self.fs = self.pos
    }
}

// Stop is the first position from `from` on, below end, of the delimiter,
// the quote or LF, or none. It reads them from a mask of the 64 bytes
// from mpos, built with four 16-lane compares per byte class (std.simd's
// baseline width; no cpu dispatch, so no unsafe) and kept between calls,
// so a run of short fields costs one count of trailing zeros each. Bytes
// below the scan never change a mask's bits at or above it (the reader
// writes only below pos), and Compact clears the mask.
Records.Stop(mut self, from: int): ?int {
    var at := from
    if at < self.mpos || at >= self.mlim {
        self.Block(at)
    }
    for {
        m := self.mask & (~Cast[u64](0) << (at - self.mpos))
        if m != 0 {
            return some(self.mpos + raw.Ctz64(m))
        }
        if self.mlim >= self.end {
            return none
        }
        at = self.mlim
        self.Block(at)
    }
}

// Block builds the stop mask of buf[b..min(b + 64, end)].
Records.Block(mut self, b: int) {
    self.mpos = b
    var m: u64 = 0
    if b + 64 <= self.end {
        d := simd.Vec[u8, 16].Splat(self.delim)
        q := simd.Vec[u8, 16].Splat(self.qb)
        l := simd.Vec[u8, 16].Splat(0x0A)
        for k in 0..4 {
            v := simd.Vec[u8, 16].FromSlice(self.buf[..], b + 16 * k)
            m |= ((v == d) | (v == q) | (v == l)).Bits() << (16 * k)
        }
        self.mlim = b + 64
    } else {
        for k in 0..self.end - b {
            c := self.buf[b + k]
            if c == self.delim || c == self.qb || c == 0x0A {
                m |= Cast[u64](1) << k
            }
        }
        self.mlim = self.end
    }
    self.mask = m
}

// Quoted scans a quoted field's content from pos to the end of what was
// read or to the next quote or LF, shifting it left by shift. An LF counts
// a line and stays content, as a CR before it does.
Records.Quoted(mut self): Error!void {
    var at := self.pos
    var p := 0
    // The mask holds the delimiter too: inside quotes it is content.
    for {
        k := self.Stop(at) else {
            self.Unshift(self.pos, self.end)
            try self.CheckField(self.end - self.shift - self.fs)
            self.pos = self.end
            return
        }
        if self.buf[k] != self.delim {
            p = k
            break
        }
        at = k + 1
    }
    self.Unshift(self.pos, p)
    if self.buf[p] == self.qb {
        try self.CheckField(p - self.shift - self.fs)
        self.pos = p + 1
        self.state = Scan.Closed
        return
    }
    // p is an LF, which is content, as a CR before it is: RFC 4180's
    // escaped field holds CR and LF as data, so a CR LF that WriteRecord
    // wrote reads back as it was (csv-3). The raw byte before p is
    // untouched by Unshift, which writes below p - shift; it is looked at
    // only for the line length that Quote's column at the end needs.
    var cr := 0
    if p > self.fs && self.buf[p - 1] == 0x0D {
        cr = 1
    }
    self.buf[p - self.shift] = 0x0A
    self.NewLine(p, p - cr)
    self.pos = p + 1
    try self.CheckField(p - self.shift + 1 - self.fs)
}

// Closed reads the byte after a quote inside a quoted field (at pos - 1):
// a second quote is one quote of content; the delimiter, LF, or CR LF
// close the field (and the record at a line break); anything else is Quote
// at the quote's column.
Records.Closed(mut self): Error!?Record from self {
    q := self.pos - 1
    b := self.buf[self.pos]
    if b == self.qb {
        self.buf[q - self.shift] = self.qb
        self.shift += 1
        self.pos += 1
        self.state = Scan.Quoted
        return none
    }
    if b == self.delim {
        try self.Close(q - self.shift)
        self.pos += 1
        self.state = Scan.Field
        return none
    }
    if b == 0x0A {
        try self.Close(q - self.shift)
        self.NewLine(self.pos, self.pos)
        self.pos += 1
        return some(self.Done())
    }
    if b == 0x0D {
        if self.pos + 1 == self.end && !self.eof {
            try self.Fill()
            return none
        }
        if self.pos + 1 == self.end {
            // CR before the end: dropped, the field and record close.
            try self.Close(q - self.shift)
            self.pos = self.end
            return some(self.Done())
        }
        if self.buf[self.pos + 1] == 0x0A {
            try self.Close(q - self.shift)
            self.NewLine(self.pos + 1, self.pos)
            self.pos += 2
            return some(self.Done())
        }
    }
    try self.Fail(1, self.line, q - self.line_start + 1)
    return none
}

// AtEnd is Next at the end of input: none before a record, else the last
// record closed as its state allows, or Quote inside a quoted field.
Records.AtEnd(mut self): Error!?Record from self {
    match self.state {
        .Line {
            return none
        }
        .Field {
            // After a delimiter: one more empty field.
            if self.nf == self.slots {
                try self.Fail(2, self.rec_line, 0)
            }
            self.fs = self.pos
            try self.Close(self.pos)
        }
        .Bare {
            var e := self.end
            if e > self.fs && self.buf[e - 1] == 0x0D {
                e -= 1
            }
            try self.Close(e)
        }
        .Quoted {
            // encoding/csv's position: just past the last line that has a
            // byte, a dropped final CR not counted.
            var n := self.end - self.line_start
            if n > 0 && self.buf[self.end - 1] == 0x0D {
                n -= 1
            }
            if n == 0 {
                try self.Fail(1, self.line - 1, self.prev_len + 1)
            }
            try self.Fail(1, self.line, n + 1)
        }
        .Closed {
            try self.Close(self.pos - 1 - self.shift)
        }
    }
    return some(self.Done())
}

// Fill reads more input into buf[end..lim], first moving the record so far
// to the front when rec > 0 and less than half the data area is free.
// When the record already fills the data area, one byte is read into a
// stack byte to tell the end of input, where the record closes at lim
// bytes, from more of it, which is TooLarge (csv-12).
Records.Fill(mut self): Error!void {
    if self.end == self.lim && self.rec == 0 {
        var probe: [1]u8 = [0]
        got := try self.r.Read(mut probe[..])
        if got == 0 {
            self.eof = true
            return
        }
        try self.Fail(2, self.rec_line, 0)
    }
    if self.rec > 0 && (self.end == self.lim || self.lim - self.end < self.lim / 2) {
        self.Compact()
    }
    n := try self.r.Read(mut self.buf[self.end..self.lim])
    if n == 0 {
        self.eof = true
        return
    }
    self.end += n
}

// Compact moves buf[rec..end] to the front and every position with it; the
// index holds offsets from rec, so it stays.
Records.Compact(mut self) {
    d := self.rec
    MoveDown(mut self.buf[..], 0, d, self.end - d)
    self.end -= d
    self.pos -= d
    self.fs -= d
    self.line_start -= d
    self.rec = 0
    self.mpos = 0
    self.mlim = 0
}

// Unshift moves the quoted content buf[from..to] down by shift, when shift
// is above 0.
Records.Unshift(mut self, from: int, to: int) {
    if self.shift > 0 {
        MoveDown(mut self.buf[..], from - self.shift, from, to - from)
    }
}

// CheckField is TooLarge when the open quoted field, n bytes so far, holds
// more than field_bytes.
Records.CheckField(mut self, n: int): Error!void {
    if n > self.field_bytes {
        try self.Fail(2, self.rec_line, 0)
    }
}

// Close ends the open field at e (raw, exclusive): checks its length and
// writes its index entry.
Records.Close(mut self, e: int): Error!void {
    if e - self.fs > self.field_bytes {
        try self.Fail(2, self.rec_line, 0)
    }
    at := self.lim + 8 * self.nf
    PutU32(mut self.buf[..], at, self.fs - self.rec)
    PutU32(mut self.buf[..], at + 4, e - self.rec)
    self.nf += 1
}

// NewLine counts the line break whose LF is at p; e is where the line's
// content ends (the CR's position for a CR LF), for prev_len.
Records.NewLine(mut self, p: int, e: int) {
    self.prev_len = e - self.line_start + 1
    self.line += 1
    self.line_start = p + 1
}

// SkipLine skips a blank line that ends just before next.
Records.SkipLine(mut self, next: int) {
    self.NewLine(next - 1, next - 1)
    self.pos = next
    self.rec = next
    self.rec_line = self.line
}

// Done returns the record that ends at pos and starts the next one there.
Records.Done(mut self): Record from self {
    rec := self.rec
    n := self.nf
    line := self.rec_line
    self.rec = self.pos
    self.nf = 0
    self.state = Scan.Line
    self.rec_line = self.line
    data := self.buf[rec..self.pos]
    // One vector check of the whole record (rt.StrFrom, std.simd's
    // validator), so a field of it costs two byte tests and a slice. Bytes
    // a quoted field's unquoting left behind may fail it when the field
    // is not ASCII; Field then checks each field alone.
    return Record(data: data, text: TextOf(data), index: self.buf[self.lim..self.lim + 8 * n], n: n, first: line)
}

// Fail records the error (kind 1 Quote, 2 TooLarge) so that every later
// Next repeats it, and fails with it.
Records.Fail(mut self, kind: u8, line: i64, column: int): Error!void {
    self.bad = kind
    self.bad_line = line
    self.bad_column = column
    try self.Repeat()
}

// Repeat fails with the recorded error.
Records.Repeat(&self): Error!void {
    if self.bad == 1 {
        fail Quote(line: self.bad_line, column: self.bad_column)
    }
    fail TooLarge(line: self.bad_line)
}

/// The number of fields. O(1); allocates nothing.
export Record.Len(&self): int {
    return self.n
}

/// Field i, unquoted, as a view of the reader's buffer; none when i is not
/// below Len() or the field's bytes are not UTF-8 (tell the two apart by
/// i < Len()). O(1) when the record's bytes are UTF-8 as a whole, which
/// Next checked once; else O(field length) for the field's own check;
/// allocates nothing.
export Record.Field(&self, i: int): ?str from self {
    if i < 0 || i >= self.n {
        return none
    }
    s := GetU32(self.index, 8 * i)
    e := GetU32(self.index, 8 * i + 4)
    if s == e {
        return some("")
    }
    if t := self.text {
        // In valid UTF-8 an offset is a scalar boundary exactly when its
        // byte is not a continuation byte (or it is the end), and a range
        // between two boundaries is UTF-8: the slice cannot panic, and a
        // field that starts or ends inside a scalar is not UTF-8 alone.
        if Inside(self.data, s) || Inside(self.data, e) {
            return none
        }
        return some(t[s..e])
    }
    v := str.From(self.data[s..e]) else {
        return none
    }
    return some(v)
}

// TextOf is b as a str when it is UTF-8, else none.
TextOf(b: []u8): ?str {
    t := rt.StrFrom(b) else {
        return none
    }
    return some(t)
}

// Inside is whether offset k of b falls inside a scalar: b[k] is a UTF-8
// continuation byte. The end is never inside.
Inside(b: []u8, k: int): bool {
    return k < b.Len() && b[k] & 0xC0 == 0x80
}

/// The line the record starts on, from 1, counting LF bytes. O(1);
/// allocates nothing.
export Record.Line(&self): i64 {
    return self.first
}

// MoveDown copies buf[src..src + n] to buf[dst..dst + n], dst <= src: one
// copy when the two ranges are apart, else forward byte by byte, which is
// right for an overlap below the source.
MoveDown(buf: mut []u8, dst: int, src: int, n: int) {
    if src - dst >= n {
        (left, right) := buf.SplitAt(src)
        (mut left[dst..dst + n]).CopyFrom(right[0..n])
        return
    }
    for k in 0..n {
        buf[dst + k] = buf[src + k]
    }
}

// PutU32 writes v, 0 <= v < 2^32, little-endian at buf[at..at + 4]. One
// range test bounds the four byte stores, so they carry no check of their
// own (at +% k, k below 4, cannot wrap under it). Its callers write half
// of field nf's index entry, at lim + 8 * nf or 4 bytes on, for a field
// opened while nf < slots (Next's Field step fails at nf == slots, Bare
// goes on to a next field only below it, and nf moves only once the field
// is closed), so at + 4 <= lim + 8 * slots <= buf.Len() and the test never
// fails. Were it to, it panics with the bounds code (2) that the stores'
// own checks had.
PutU32(buf: mut []u8, at: int, v: int) {
    if at < 0 || buf.Len() - at < 4 {
        raw.Fail(2, "csv: field index entry out of range")
    }
    buf[at] = Trunc[u8](v)
    buf[at +% 1] = Trunc[u8](v >> 8)
    buf[at +% 2] = Trunc[u8](v >> 16)
    buf[at +% 3] = Trunc[u8](v >> 24)
}

// GetU32 reads PutU32's value at b[at..at + 4], with the same one range
// test for its four loads. Field reads entry i at 8 * i and 8 * i + 4 of
// the record's index, 8 * Len() bytes, for i below Len(), so the test
// never fails; were it to, it panics with the bounds code (2), as the
// loads' own checks did.
GetU32(b: []u8, at: int): int {
    if at < 0 || b.Len() - at < 4 {
        raw.Fail(2, "csv: field index entry out of range")
    }
    return int(b[at]) | (int(b[at +% 1]) << 8) | (int(b[at +% 2]) << 16) | (int(b[at +% 3]) << 24)
}
