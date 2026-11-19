// Why: Lines to clients and names in the room table must fit fixed buffers.
//
// What: MaxLine, MaxName, Kind, Frame and Name.
//
// How: A Frame holds its bytes inline and is cut on a UTF-8 boundary when full;
// Name is a fixed-size nick or room name compared by value.

/// The longest line a client may send, without its newline.
export const MaxLine: int = 400

/// The longest nick and room name, in bytes.
export const MaxName: int = 24

/// What a frame asks of the session that writes it.
export enum Kind {
    /// An ordinary line for the client.
    Line
    /// The last line before the server closes the connection.
    Closing
}

/// One line of output for one or many clients, newline included. Frames are
/// plain so they can be shared by reference count across every member of a
/// room; text past the capacity is cut on a scalar boundary.
export struct Frame {
    export kind: Kind
    len: int
    data: [512]u8
}

/// An empty frame of `kind`. O(1); allocates nothing.
export Frame.Empty(kind: Kind): Frame {
    return Frame(kind: kind, len: 0, data: [0; 512])
}

/// A frame holding `s` and a newline. O(n); allocates nothing.
export Frame.Of(kind: Kind, s: str): Frame {
    var f := Frame.Empty(kind)
    f.Push(s)
    f.Push("\n")
    return f
}

/// Appends as much of `s` as fits, keeping room for a final newline and
/// cutting only between scalars. O(n); allocates nothing.
export Frame.Push(mut self, s: str) {
    var room := 511 - self.len
    if s == "\n" {
        room += 1
    }
    var n := s.Len()
    if n > room {
        n = room
        // back up to the start of a scalar: continuation bytes are 10xxxxxx
        for n > 0 && s.Byte(n) & 0xC0 == 0x80 {
            n -= 1
        }
    }
    for i in 0..n {
        self.data[self.len + i] = s.Byte(i)
    }
    self.len += n
}

/// The frame's bytes, newline included.
export Frame.Bytes(&self): []u8 from self {
    return self.data[..self.len]
}

/// The frame's text without its newline. Frames are built from `str` values
/// cut on scalar boundaries, so the bytes are UTF-8.
export Frame.Text(&self): str from self {
    var n := self.len
    if n > 0 && self.data[n - 1] == b'\n' {
        n -= 1
    }
    return str.From(self.data[..n]).Must()
}

/// A short name: a nick or a room name, kept inline so it is plain.
export struct Name {
    len: int
    data: [24]u8
}

/// The name `s`, which `ValidNick` or `ValidRoom` has accepted.
export Name.Of(s: str): Name {
    var n := Name(len: 0, data: [0; 24])
    for i in 0..s.Len() {
        n.data[i] = s.Byte(i)
    }
    n.len = s.Len()
    return n
}

/// The name as text. Names hold ASCII only, so the bytes are UTF-8.
export Name.Str(&self): str from self {
    return str.From(self.data[..self.len]).Must()
}

impl Eq for Name {
    Eq(&self, other: &Name): bool {
        return self.Str() == other.Str()
    }
}

IsNameByte(b: u8): bool {
    return b >= b'a' && b <= b'z' || b >= b'A' && b <= b'Z' || b >= b'0' && b <= b'9' || b == b'-' || b == b'_'
}

/// A nick is 1 to 24 letters, digits, `-` or `_`. O(n).
export ValidNick(s: str): bool {
    if s.Len() == 0 || s.Len() > MaxName {
        return false
    }
    for b in s.AsBytes() {
        if !IsNameByte(b) {
            return false
        }
    }
    return true
}

/// A room is `#` and then 1 to 23 letters, digits, `-` or `_`. O(n).
export ValidRoom(s: str): bool {
    if s.Len() < 2 || s.Len() > MaxName || s.Byte(0) != b'#' {
        return false
    }
    for b in s.AsBytes()[1..] {
        if !IsNameByte(b) {
            return false
        }
    }
    return true
}

test "Frame.Push cuts a long text on a scalar boundary and keeps the newline" {
    var long := Frame.Empty(Kind.Line)
    for _ in 0..300 {
        long.Push("é")
    }
    long.Push("\n")
    // 255 two-byte scalars fill 510 bytes; the 256th does not fit whole
    Assert(long.len == 511)
    Assert(long.Bytes()[long.len - 1] == b'\n')
    _ = long.Text()
}

test "room and nick names accept letters, digits, dash and underscore" {
    Assert(ValidNick("alice_2"))
    Assert(!ValidNick("al ice"))
    Assert(!ValidNick(""))
    Assert(ValidRoom("#general"))
    Assert(!ValidRoom("general"))
    Assert(!ValidRoom("#"))
}
