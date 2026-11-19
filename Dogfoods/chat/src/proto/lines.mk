// Why: A TCP stream delivers bytes in arbitrary pieces, and the server reads
// whole lines.
//
// What: LineBuf and Got.
//
// How: Bytes are read straight into the buffer's free tail (Space, then Fill),
// and Next hands out each complete line. A line longer than MaxLine is dropped
// whole and reported once as Overlong.

/// Splits a byte stream into lines. Bytes are appended at the tail with
/// `Space` and `Fill`, and `Next` hands out each complete line. A line longer
/// than `MaxLine` is dropped whole and reported once as `Overlong`.
export struct LineBuf {
    buf: [1024]u8
    start: int
    end: int
    skipping: bool
}

/// What `Next` found.
export enum Got {
    /// A complete line in `LineBuf.Line`.
    Line(len: int)
    /// A line past `MaxLine` bytes was dropped.
    Overlong
    /// No complete line yet: read more.
    More
}

/// An empty line buffer.
export LineBuf.New(): LineBuf {
    return LineBuf(buf: [0; 1024], start: 0, end: 0, skipping: false)
}

/// The free tail to read into, moving pending bytes to the front first.
export LineBuf.Space(mut self): mut []u8 from self {
    if self.start > 0 {
        n := self.end - self.start
        for i in 0..n {
            self.buf[i] = self.buf[self.start + i]
        }
        self.start = 0
        self.end = n
    }
    return mut self.buf[self.end..]
}

/// Records that `n` bytes were read into the slice `Space` returned.
export LineBuf.Fill(mut self, n: int) {
    self.end += n
}

/// Finds the next line. Its bytes stay valid until the next `Space`.
export LineBuf.Next(mut self): Got {
    for i in self.start..self.end {
        if self.buf[i] != b'\n' {
            continue
        }
        var stop := i
        if stop > self.start && self.buf[stop - 1] == b'\r' {
            stop -= 1
        }
        first := self.start
        self.start = i + 1
        if self.skipping {
            self.skipping = false
            return Got.Overlong
        }
        if stop - first > MaxLine {
            return Got.Overlong
        }
        // the caller reads the line from `first`, so move it to the front
        n := stop - first
        for k in 0..n {
            self.buf[k] = self.buf[first + k]
        }
        // keep the rest after the line, which may hold more lines
        rest := self.end - self.start
        for k in 0..rest {
            self.buf[n + 1 + k] = self.buf[self.start + k]
        }
        self.start = n + 1
        self.end = n + 1 + rest
        return Got.Line(len: n)
    }
    if self.end - self.start > MaxLine {
        // drop what is buffered of an overlong line and wait for its end
        self.start = 0
        self.end = 0
        self.skipping = true
    }
    return Got.More
}

/// The line `Next` returned as `Line(len)`, as text, or `none` when its
/// bytes are not UTF-8.
export LineBuf.Line(&self, len: int): ?str from self {
    s := str.From(self.buf[..len]) else {
        return none
    }
    return some(s)
}

test "LineBuf splits CRLF and LF lines across reads" {
    var lb := LineBuf.New()
    data := "NICK a\r\nJOIN #x\nMS"
    sp := lb.Space()
    for i in 0..data.Len() {
        sp[i] = data.Byte(i)
    }
    lb.Fill(data.Len())
    match lb.Next() {
        .Line(len) {
            Assert(lb.Line(len).Must() == "NICK a")
        }
        _ {
            Assert(false)
        }
    }
    match lb.Next() {
        .Line(len) {
            Assert(lb.Line(len).Must() == "JOIN #x")
        }
        _ {
            Assert(false)
        }
    }
    match lb.Next() {
        .More {}
        _ {
            Assert(false)
        }
    }
}
