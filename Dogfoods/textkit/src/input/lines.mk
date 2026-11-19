// Why: lines of any length, checked as UTF-8 as they arrive.
// What: LineSource and its helpers.
// How: chunks from Reader.Read; a line ends at `\n` with a `\r` before it
// dropped, and an invalid byte is reported with its line number.

/// Lines of one input of any length, read in chunks with `Reader.Read`.
/// A line ends at `\n`, and a `\r` before it is dropped; a last line with no
/// newline is still a line. Bytes are checked as UTF-8 as they arrive, so an
/// invalid byte is reported with its line number.
export view struct LineSource {
    reader: mut Reader
    chunk: mut []u8
    pending: Text
    // bytes of a scalar cut by the end of the last chunk, never more than 3
    carry: [4]u8
    carried: int
    eof: bool
    // the number of a line found not to be UTF-8, reported once the lines before it are read; 0 for none
    bad: int
    line: int
    bytes: int
    newlines: int
    returns: int
}

/// A source over `reader` with a read buffer of `size` bytes, at least 4.
/// Allocates the buffer and the pending text in `a`.
export LineSource.New(a: &Arena, reader: mut Reader, size: int): OutOfMemory!LineSource from a, reader {
    chunk := try a.Slice[u8](if size < 4 { 4 } else { size })
    return LineSource(
        reader: reader,
        chunk: chunk,
        pending: Text.New(a),
        carry: [0; 4],
        carried: 0,
        eof: false,
        bad: 0,
        line: 0,
        bytes: 0,
        newlines: 0,
        returns: 0,
    )
}

/// The next line, copied into `a`, or `none` at the end of the input.
/// Fails with `BadUtf8` on a line that is not UTF-8 and with `ReadError`
/// when the reader does. O(line length) amortised; allocates the line in `a`.
export LineSource.Next(mut self, a: &Arena): Error!?str from a {
    for {
        text := self.pending.Str()
        if i := text.Find("\n") {
            var line := text[..i]
            if line.EndsWith("\r") {
                line = line[..line.Len() - 1]
                self.returns += 1
            }
            copy := try a.Dup(line)
            self.pending.Drain(i + 1)
            self.line += 1
            self.newlines += 1
            return some(copy)
        }
        if self.bad > 0 {
            fail BadUtf8(line: self.bad)
        }
        if self.eof {
            if text.Len() == 0 {
                return none
            }
            copy := try a.Dup(text)
            self.pending.Clear()
            self.line += 1
            return some(copy)
        }
        try self.Fill()
    }
}

/// The number of the last line `Next` returned, from 1.
export LineSource.LineNumber(&self): int {
    return self.line
}

/// Counts of what has been read so far: bytes, `\n` bytes, and `\r` bytes
/// dropped before a `\n`.
export LineSource.Counts(&self): (bytes: int, newlines: int, returns: int) {
    return (bytes: self.bytes, newlines: self.newlines, returns: self.returns)
}

LineSource.Fill(mut self): Error!void {
    for i in 0..self.carried {
        self.chunk[i] = self.carry[i]
    }
    n := try self.reader.Read(mut self.chunk[self.carried..])
    if n == 0 {
        self.eof = true
        if self.carried > 0 {
            fail BadUtf8(line: self.line + 1 + Count(self.pending.Str(), b'\n'))
        }
        return
    }
    self.bytes += n
    total := self.carried + n
    keep := CutTail(self.chunk[..total])
    body := self.chunk[..total - keep]
    s := str.From(body) else {
        (lines, start) := BadLine(body)
        self.bad = self.line + 1 + Count(self.pending.Str(), b'\n') + lines
        self.eof = true
        // the lines before the bad one are UTF-8, so they are still returned
        try self.pending.Push(str.From(body[..start]) else { "" })
        return
    }
    try self.pending.Push(s)
    for i in 0..keep {
        self.carry[i] = self.chunk[total - keep + i]
    }
    self.carried = keep
}

/// How many bytes at the end of `bytes` begin a UTF-8 sequence that the
/// bytes after it do not complete: 0 to 3 (RFC 3629 section 3).
CutTail(bytes: []u8): int {
    n := bytes.Len()
    var back := 1
    for back <= 3 && back <= n {
        b := bytes[n - back]
        if b & 0xC0 != 0x80 {
            need := if b >= 0xF0 { 4 } else if b >= 0xE0 { 3 } else if b >= 0xC0 { 2 } else { 1 }
            return if need > back { back } else { 0 }
        }
        back += 1
    }
    return 0
}

/// Where the first line of `bytes` that is not UTF-8 starts: the number of
/// `\n` bytes before it, and its byte offset.
BadLine(bytes: []u8): (lines: int, start: int) {
    var start := 0
    var n := 0
    for i, b in bytes {
        if b == b'\n' {
            _ = str.From(bytes[start..i]) else { return (lines: n, start: start) }
            n += 1
            start = i + 1
        }
    }
    return (lines: n, start: start)
}

/// How many times `b` occurs in `s`.
Count(s: str, b: u8): int {
    var n := 0
    for x in s.AsBytes() {
        if x == b {
            n += 1
        }
    }
    return n
}
