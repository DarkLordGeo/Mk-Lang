// Why: std.crypto and std.x509 read keys and certificates in DER, and an
// encoding with one form per value must be parsed without recursion and
// without accepting a second form.
//
// What: DerValue, DerRead, DerIter and DerValue.Children, the typed readers
// DerValue.Integer, DerValue.BitString and DerValue.Boolean, and
// DerWriter (New, Begin, End, Primitive, Integer, Bytes): ITU-T X.690
// (02/2021) section 8's tag-length-value encoding under section 10's
// Distinguished Encoding Rules.
//
// How: the reader is lazy: DerRead takes one element and DerIter one child
// at a time, each a view of the input, so memory is O(1) whatever the
// input, and depth is a count each element carries, refused past MaxDepth.
// It enforces what makes DER one encoding per value: the low tag form for
// numbers below 31 and the high form with no leading zero group above
// (8.1.2), definite lengths only, the short form below 128 and the long
// form with no leading zero byte above (8.1.3, 10.1); the typed readers add
// the minimal integer (8.3.2), the canonical boolean (11.1) and a bit
// string's zero padding (11.2). The writer has the shape of BoringSSL's
// CBB: a constructed element reserves the longest length it allows and End
// moves its content back over what the real length does not use, so End
// allocates nothing.

/// DerValue is one DER element, a view of its input: its tag number, class
/// (0 universal, 1 application, 2 context-specific, 3 private), whether it
/// is constructed, and its content octets.
export struct DerValue {
    /// The tag number.
    export tag: u32
    /// The tag class: 0 universal, 1 application, 2 context-specific, 3
    /// private.
    export class: u8
    /// Whether the element is constructed.
    export constructed: bool
    /// The content octets, a view of the input.
    export content: []u8
    depth: int
}

/// DerIter yields the children of a constructed DerValue in order.
export view struct DerIter {
    data: []u8
    pos: int
    depth: int
    primitive: bool
}

// MaxDepth is how deep an element may nest: a top-level element is depth
// 0, and DerIter refuses a child below depth MaxDepth.
const MaxDepth: int = 32

// MaxTag is the largest tag number either side takes: 29 bits, so a
// writer's tag word (BoringSSL's CBS_ASN1_TAG layout) holds the class and
// the constructed bit above it.
const MaxTag: u32 = 0x1FFFFFFF

// ReadTlv reads one element at the front of data, at depth.
ReadTlv(data: []u8, depth: int): Error!(value: DerValue, rest: []u8) from data {
    n := data.Len()
    if n == 0 {
        fail BadInput(offset: 0)
    }
    id := data[0]
    var tag := u32(id & 0x1F)
    if tag != 0x1F && n >= 2 && data[1] < 0x80 && int(data[1]) <= n - 2 {
        // The short form: a one-byte identifier and a one-byte
        // length that fits have one encoding, which is DER (X.690 8.1.2.2,
        // 8.1.3.4), so most elements of a certificate take no more checks.
        end := 2 + int(data[1])
        value := DerValue(tag: tag, class: id >> 6, constructed: id & 0x20 != 0, content: data[2..end], depth: depth)
        return (value, rest: data[end..])
    }
    var i := 1
    if tag == 0x1F {
        // X.690 8.1.2.4: base 128, most significant group first, the first
        // group not zero; a number below 31 has the one-byte form.
        tag = 0
        for {
            if i >= n {
                fail BadInput(offset: n)
            }
            g := data[i]
            if i == 1 && g == 0x80 || tag > (MaxTag >> 7) {
                fail BadInput(offset: i)
            }
            tag = (tag << 7) | u32(g & 0x7F)
            i += 1
            if g & 0x80 == 0 {
                break
            }
        }
        if tag < 31 {
            fail BadInput(offset: 1)
        }
    }
    if i >= n {
        fail BadInput(offset: n)
    }
    at := i
    l0 := data[i]
    i += 1
    var len := int(l0)
    if l0 >= 0x80 {
        // X.690 8.1.3.5 and 10.1: definite, at most eight length bytes,
        // the first not zero, and a length below 128 has the short form.
        k := int(l0 & 0x7F)
        if k == 0 || k > 8 {
            fail BadInput(offset: at)
        }
        if n - i < k {
            fail BadInput(offset: n)
        }
        if data[i] == 0 {
            fail BadInput(offset: at)
        }
        var v: u64 = 0
        for j in 0..k {
            v = (v << 8) | u64(data[i + j])
        }
        i += k
        if v < 128 {
            fail BadInput(offset: at)
        }
        if v > Cast[u64](n - i) {
            fail BadInput(offset: n)
        }
        len = Cast[int](v)
    } else if len > n - i {
        fail BadInput(offset: n)
    }
    value := DerValue(tag: tag, class: id >> 6, constructed: id & 0x20 != 0, content: data[i..i + len], depth: depth)
    return (value, rest: data[i + len..])
}

/// DerRead reads the DER element at the front of data and returns it and
/// the bytes after it. Fails with BadInput at the first byte of an
/// identifier or length that is not DER (an indefinite or non-minimal
/// length, a non-minimal tag), or at data.Len() when data ends inside the
/// element. O(1); allocates nothing.
export DerRead(data: []u8): Error!(value: DerValue, rest: []u8) from data {
    return try ReadTlv(data, 0)
}

/// Children is an iterator over the elements of this value's content. It
/// fails at its first step, and every later one, when the value is
/// primitive, its content empty included. O(1); allocates nothing.
export DerValue.Children(&self): DerIter from self.content {
    return DerIter(data: self.content, pos: 0, depth: self.depth + 1, primitive: !self.constructed)
}

/// Next is the next child, or none after the last. Fails with BadInput as
/// DerRead does, at an offset into the parent's content, and with
/// TooLarge for a child nested deeper than 32. O(1); allocates nothing.
export DerIter.Next(mut self): Error!?DerValue from self.data {
    // A primitive value has no children, however short its content: an
    // empty one fails too, so `10 00` never reads as an empty SEQUENCE.
    if self.primitive {
        fail BadInput(offset: 0)
    }
    if self.pos == self.data.Len() {
        return none
    }
    if self.depth > MaxDepth {
        fail TooLarge
    }
    got := ReadTlv(self.data[self.pos..], self.depth) else e {
        match e {
            BadInput(offset) {
                fail BadInput(offset: self.pos + offset)
            }
            _ {
                fail e
            }
        }
    }
    (value, rest) := got
    self.pos = self.data.Len() - rest.Len()
    return some(value)
}

// Universal is whether v is the primitive universal element with tag
// number tag.
Universal(v: &DerValue, tag: u32): bool {
    return v.class == 0 && !v.constructed && v.tag == tag
}

/// Integer is the content of an INTEGER: its two's complement value, most
/// significant byte first. Fails with BadInput at 0 when this is not a
/// primitive universal INTEGER, or its encoding is empty or not minimal
/// (X.690 8.3.2). O(1); allocates nothing.
export DerValue.Integer(&self): Error![]u8 from self.content {
    c := self.content
    if !Universal(self, 2) || c.Len() == 0 {
        fail BadInput(offset: 0)
    }
    if c.Len() >= 2 && (c[0] == 0x00 && c[1] < 0x80 || c[0] == 0xFF && c[1] >= 0x80) {
        fail BadInput(offset: 0)
    }
    return c
}

/// BitString is the bits of a BIT STRING, first bit in the top of the
/// first byte; the bits past its length are zero, as DER makes them
/// (X.690 11.2.1), so a caller that needs only whole bytes or bit
/// positions (key usage) can read them as they are. Fails with BadInput at
/// 0 when this is not a primitive universal BIT STRING, its unused-bit
/// count is above 7, or the unused bits are not zero. O(1); allocates
/// nothing.
export DerValue.BitString(&self): Error![]u8 from self.content {
    c := self.content
    if !Universal(self, 3) || c.Len() == 0 {
        fail BadInput(offset: 0)
    }
    unused := c[0]
    if unused > 7 || c.Len() == 1 && unused != 0 {
        fail BadInput(offset: 0)
    }
    if unused != 0 && c[c.Len() - 1] & ((u8(1) << unused) - 1) != 0 {
        fail BadInput(offset: 0)
    }
    return c[1..]
}

/// Boolean is the value of a BOOLEAN. Fails with BadInput at 0 when this
/// is not a primitive universal BOOLEAN of one byte, 0x00 or 0xFF (X.690
/// 11.1). O(1); allocates nothing.
export DerValue.Boolean(&self): Error!bool {
    c := self.content
    if !Universal(self, 1) || c.Len() != 1 || c[0] != 0x00 && c[0] != 0xFF {
        fail BadInput(offset: 0)
    }
    return c[0] == 0xFF
}

/// DerWriter builds one DER encoding in an arena. A tag word holds the
/// class in bits 31 and 30, the constructed bit in bit 29, and the tag
/// number in bits 28 to 0: 0x10 is SEQUENCE (Begin sets the constructed
/// bit) and 0x80000000 is the context-specific [0].
export view struct DerWriter {
    buf: List[u8]
    open: [33]int
    depth: int
    over: int
    misuse: bool
    big: bool
}

/// New is an empty writer whose bytes live in a. O(1); allocates nothing
/// until the first write.
export DerWriter.New(a: &Arena): DerWriter from a {
    return DerWriter(buf: List[u8].New(a), open: [0; 33], depth: 0, over: 0, misuse: false, big: false)
}

// PutId writes the identifier octets of tag word tag.
DerWriter.PutId(mut self, tag: u32): OutOfMemory!void {
    head := Trunc[u8](((tag >> 30) << 6) | (((tag >> 29) & 1) << 5))
    number := tag & MaxTag
    if number < 31 {
        try self.buf.Push(head | Trunc[u8](number))
        return
    }
    try self.buf.Push(head | 0x1F)
    var shift := 28
    for shift > 0 && (number >> Trunc[u32](shift)) == 0 {
        shift -= 7
    }
    for shift > 0 {
        try self.buf.Push(0x80 | Trunc[u8](number >> Trunc[u32](shift)) & 0x7F)
        shift -= 7
    }
    try self.buf.Push(Trunc[u8](number & 0x7F))
}

// LenBytes is how many bytes the long form of length n needs after its
// first byte, 0 for the short form.
LenBytes(n: int): int {
    if n < 128 {
        return 0
    }
    var k := 1
    for k < 8 && n >> (8 * k) != 0 {
        k += 1
    }
    return k
}

// PutLen writes the minimal length octets of n.
DerWriter.PutLen(mut self, n: int): OutOfMemory!void {
    k := LenBytes(n)
    if k == 0 {
        try self.buf.Push(Trunc[u8](n))
        return
    }
    try self.buf.Push(0x80 | Trunc[u8](k))
    var j := k - 1
    for j >= 0 {
        try self.buf.Push(Trunc[u8](n >> (8 * j)))
        j -= 1
    }
}

// Reserved is how many length bytes Begin reserves: 0x84 and four bytes,
// the longest length End writes (content below 4 GiB).
const Reserved: int = 5

/// Begin opens a constructed element with tag word tag; its content is what
/// is written until the matching End. A Begin past depth 32 makes End and
/// Bytes fail with TooLarge. O(1); allocates up to ten bytes in the arena.
export DerWriter.Begin(mut self, tag: u32): OutOfMemory!void {
    if self.depth > MaxDepth {
        self.over += 1
        return
    }
    try self.PutId(tag | 0x20000000)
    for _ in 0..Reserved {
        try self.buf.Push(0)
    }
    self.open[self.depth] = self.buf.Len()
    self.depth += 1
}

/// End closes the element the last open Begin opened, writing its length.
/// Fails with BadInput at the writer's length when no element is open,
/// and with TooLarge for an element opened past depth 32 or with 4 GiB of
/// content or more. O(n) in the element's content, which moves back over
/// the length bytes it does not need; allocates nothing.
export DerWriter.End(mut self): Error!void {
    if self.over > 0 {
        self.over -= 1
        self.big = true
        fail TooLarge
    }
    if self.depth == 0 {
        fail BadInput(offset: self.buf.Len())
    }
    self.depth -= 1
    start := self.open[self.depth]
    n := self.buf.Len() - start
    if n >> 32 != 0 {
        self.big = true
        fail TooLarge
    }
    k := LenBytes(n)
    at := start - Reserved
    var p := at
    if k == 0 {
        self.buf[p] = Trunc[u8](n)
        p += 1
    } else {
        self.buf[p] = 0x80 | Trunc[u8](k)
        p += 1
        var j := k - 1
        for j >= 0 {
            self.buf[p] = Trunc[u8](n >> (8 * j))
            p += 1
            j -= 1
        }
    }
    gap := start - p
    if gap == 0 {
        return
    }
    for i in start..self.buf.Len() {
        self.buf[i - gap] = self.buf[i]
    }
    for _ in 0..gap {
        _ = self.buf.Pop()
    }
}

/// Primitive writes a primitive element with tag word tag (its constructed
/// bit clear) and content. A tag word with the constructed bit set makes
/// Bytes fail with BadInput. O(n); allocates the element's length in the
/// arena.
export DerWriter.Primitive(mut self, tag: u32, content: []u8): OutOfMemory!void {
    if tag & 0x20000000 != 0 {
        self.misuse = true
        return
    }
    try self.PutId(tag)
    try self.PutLen(content.Len())
    for x in content {
        try self.buf.Push(x)
    }
}

/// Integer writes the INTEGER whose value is magnitude, an unsigned number
/// most significant byte first, in its minimal form: leading zero bytes
/// dropped and one added when the top bit is set, so it reads back as
/// positive; an empty magnitude is zero. O(n); allocates the element's
/// length in the arena.
export DerWriter.Integer(mut self, magnitude: []u8): OutOfMemory!void {
    var m := magnitude
    for m.Len() > 0 && m[0] == 0 {
        m = m[1..]
    }
    pad := m.Len() == 0 || m[0] >= 0x80
    try self.PutId(2)
    extra := if pad { 1 } else { 0 }
    try self.PutLen(m.Len() + extra)
    if pad {
        try self.buf.Push(0)
    }
    for x in m {
        try self.buf.Push(x)
    }
}

/// Bytes ends the writer and is its encoding, in the writer's arena. Fails
/// with BadInput at the writer's length while an element is open or after a
/// Primitive with the constructed bit, and with TooLarge while a Begin past
/// depth 32 is open or after an End failed with TooLarge; on an error the
/// writer stays with the caller. O(1); allocates nothing.
export DerWriter.Bytes(self): Error![]u8 {
    if self.over > 0 || self.big {
        fail TooLarge
    }
    if self.misuse || self.depth > 0 {
        fail BadInput(offset: self.buf.Len())
    }
    DerWriter(buf, ..) := self
    return buf.Done()[..]
}
