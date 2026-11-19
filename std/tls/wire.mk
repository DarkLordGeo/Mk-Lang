// Why: every TLS structure is read from untrusted bytes; one bounds-checked
// reader keeps the parsers short and panic-free.
//
// What: Cursor, which reads big-endian integers and length-prefixed
// vectors, and Out, which writes the same encodings into a caller's buffer.
//
// How: RFC 8446 section 3's presentation language. Every read checks its
// bound before it indexes and fails with Protocol on any byte short or
// left over; nothing here allocates or recurses.

// Cursor reads b from at onward.
struct Cursor {
    b: []u8
    at: int
}

// Cursor.New is a cursor at the start of b.
Cursor.New(b: []u8): Cursor {
    return Cursor(b: b, at: 0)
}

// Cursor.Left is how many bytes remain.
Cursor.Left(&self): int {
    return self.b.Len() - self.at
}

// Cursor.Done fails with Protocol unless every byte was read.
Cursor.Done(&self): Protocol!void {
    if self.at != self.b.Len() {
        fail Protocol
    }
}

// Cursor.Take is the next n bytes.
Cursor.Take(mut self, n: int): Protocol![]u8 from self.b {
    if n < 0 || n > self.Left() {
        fail Protocol
    }
    s := self.b[self.at..self.at + n]
    self.at += n
    return s
}

// Cursor.U8 is the next byte.
Cursor.U8(mut self): Protocol!u8 {
    if self.Left() < 1 {
        fail Protocol
    }
    v := self.b[self.at]
    self.at += 1
    return v
}

// Cursor.U16 is the next big-endian u16.
Cursor.U16(mut self): Protocol!u16 {
    if self.Left() < 2 {
        fail Protocol
    }
    v := (u16(self.b[self.at]) << 8) | u16(self.b[self.at + 1])
    self.at += 2
    return v
}

// Cursor.U24 is the next big-endian 24-bit integer.
Cursor.U24(mut self): Protocol!int {
    if self.Left() < 3 {
        fail Protocol
    }
    v := (int(self.b[self.at]) << 16) | (int(self.b[self.at + 1]) << 8) | int(self.b[self.at + 2])
    self.at += 3
    return v
}

// Cursor.U32 is the next big-endian u32.
Cursor.U32(mut self): Protocol!u32 {
    if self.Left() < 4 {
        fail Protocol
    }
    var v: u32 = 0
    for i in 0..4 {
        v = (v << 8) | u32(self.b[self.at + i])
    }
    self.at += 4
    return v
}

// Cursor.Vec8 is a vector with a one-byte length, of min to max bytes.
Cursor.Vec8(mut self, min: int, max: int): Protocol![]u8 from self.b {
    n := int(try self.U8())
    if n < min || n > max {
        fail Protocol
    }
    return try self.Take(n)
}

// Cursor.Vec16 is a vector with a two-byte length, of min to max bytes.
Cursor.Vec16(mut self, min: int, max: int): Protocol![]u8 from self.b {
    n := int(try self.U16())
    if n < min || n > max {
        fail Protocol
    }
    return try self.Take(n)
}

// Cursor.Vec24 is a vector with a three-byte length, of min to max bytes.
Cursor.Vec24(mut self, min: int, max: int): Protocol![]u8 from self.b {
    n := try self.U24()
    if n < min || n > max {
        fail Protocol
    }
    return try self.Take(n)
}

// Out writes into b from at onward. A write past the end sets full and
// writes nothing more, so a caller checks once, at the end.
struct Out {
    b: mut []u8
    at: int
    full: bool
}

// Out.New is a writer at the start of b.
Out.New(b: mut []u8): Out from b {
    return Out(b: b, at: 0, full: false)
}

// Out.Room reports whether n more bytes fit, setting full when they do
// not.
Out.Room(mut self, n: int): bool {
    if self.full || n > self.b.Len() - self.at {
        self.full = true
        return false
    }
    return true
}

// Out.U8 writes one byte.
Out.U8(mut self, v: u8) {
    if self.Room(1) {
        self.b[self.at] = v
        self.at += 1
    }
}

// Out.U16 writes a big-endian u16.
Out.U16(mut self, v: u16) {
    if self.Room(2) {
        self.b[self.at] = Trunc[u8](v >> 8)
        self.b[self.at + 1] = Trunc[u8](v)
        self.at += 2
    }
}

// Out.U24 writes a big-endian 24-bit integer.
Out.U24(mut self, v: int) {
    if self.Room(3) {
        self.b[self.at] = Trunc[u8](v >> 16)
        self.b[self.at + 1] = Trunc[u8](v >> 8)
        self.b[self.at + 2] = Trunc[u8](v)
        self.at += 3
    }
}

// Out.U32 writes a big-endian u32.
Out.U32(mut self, v: u32) {
    if self.Room(4) {
        for i in 0..4 {
            self.b[self.at + i] = Trunc[u8](v >> Trunc[u32](24 - 8 * i))
        }
        self.at += 4
    }
}

// Out.Bytes writes b as it is.
Out.Bytes(mut self, b: []u8) {
    if self.Room(b.Len()) {
        (mut self.b[self.at..self.at + b.Len()]).CopyFrom(b)
        self.at += b.Len()
    }
}

// Out.Vec8 writes b with a one-byte length; longer than 255 bytes sets
// full.
Out.Vec8(mut self, b: []u8) {
    if b.Len() > 255 {
        self.full = true
        return
    }
    self.U8(Trunc[u8](b.Len()))
    self.Bytes(b)
}

// Out.Vec16 writes b with a two-byte length; longer than 65,535 bytes
// sets full.
Out.Vec16(mut self, b: []u8) {
    if b.Len() > 65535 {
        self.full = true
        return
    }
    self.U16(Trunc[u16](b.Len()))
    self.Bytes(b)
}

// Out.Vec24 writes b with a three-byte length.
Out.Vec24(mut self, b: []u8) {
    if b.Len() > 0xFFFFFF {
        self.full = true
        return
    }
    self.U24(b.Len())
    self.Bytes(b)
}

// Out.Open reserves an n-byte length (1, 2 or 3) to be patched by Close,
// and returns where the vector's length is.
Out.Open(mut self, n: int): int {
    at := self.at
    if self.Room(n) {
        for i in 0..n {
            self.b[self.at + i] = 0
        }
        self.at += n
    }
    return at
}

// Out.Close patches the n-byte length at at (from Open) with the length
// written since; a length over the field's range sets full.
Out.Close(mut self, at: int, n: int) {
    if self.full {
        return
    }
    len := self.at - at - n
    if len >= (1 << (8 * n)) {
        self.full = true
        return
    }
    for i in 0..n {
        self.b[at + i] = Trunc[u8](len >> (8 * (n - 1 - i)))
    }
}

// Out.Len is how many bytes were written.
Out.Len(&self): int {
    return self.at
}
