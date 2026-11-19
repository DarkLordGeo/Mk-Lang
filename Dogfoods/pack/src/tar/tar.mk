// Why: `pack untar` must read tar archives made by other tools, safely, from
// a stream.
//
// What: Walker (New, Next, Meta, Read, Skip), Entry, Kind, Parse and the
// octal field readers.
//
// How: one 512-byte header at a time, with the layout and long-name rules
// below.

// Reading tar archives (POSIX.1-1988 ustar, and the v7 layout before it):
// each entry is a 512-byte header, then its data padded to a multiple of
// 512 bytes, and two zero blocks end the archive.
//
//     0  name, 100 bytes             124  size, 12 octal digits
//   100  mode, 8 octal digits         136  modification time, 12 octal digits
//   108  owner and group ids          148  checksum, 8 octal digits
//   156  type flag                    157  link target, 100 bytes
//   257  "ustar" and a version        345  name prefix, 155 bytes (ustar only)
//
// The checksum is the sum of the header's bytes, unsigned, with the
// checksum field counted as eight spaces. Two kinds of header carry the
// next entry's name when it is longer than the name field holds: a pax
// extended header (type `x`, POSIX.1-2001), whose data is records
// "<length> <key>=<value>" each ended by a newline, of which `path` is
// read; and GNU's type `L`, whose data is the name. Pax global headers
// (`g`) and GNU long link names (`K`) are skipped, never returned. A number is octal digits after
// optional spaces, ended by a space or a zero byte; GNU's base-256 form is
// refused, since nothing pack reads is that large.
import src.archive

/// Bytes in a block.
export const BlockLen: int = 512

/// The most data a pax or GNU name header may hold, so a damaged size
/// cannot ask for gigabytes.
export const MaxMeta: int = 65536

/// What an entry is.
export enum Kind: u8 {
    File = 1
    Dir = 2
    Link = 3
    Other = 4
}

/// One entry's header.
export struct Entry {
    /// Relative, separated by `/`, checked by `archive.SafePath`.
    export path: str
    export kind: Kind
    /// The permission bits, `mode & 0o7777`.
    export mode: u32
    /// Bytes of data; 0 for anything but a file.
    export size: i64
    /// Seconds since the Unix epoch.
    export modified: i64
}

/// The header at `offset` is damaged: its checksum does not match, or a
/// number field is not octal.
export error BadHeader(offset: i64)
/// The input ends at `offset`, inside a header, an entry's data, or before
/// the two zero blocks that end an archive.
export error Cut(offset: i64)
/// The header at `offset` names a path that is not UTF-8, or that could
/// leave the extraction directory.
export error BadName(offset: i64)

/// The errors of reading a tar archive.
export type Error = BadHeader | Cut | BadName | ReadError | OutOfMemory

/// The reading state of one archive: where the input is, and what is left
/// of the current entry. The input itself is passed to each call, so the
/// caller keeps its `Reader` and can ask its decoder why it failed.
export struct Walker {
    /// Bytes of the input consumed.
    offset: i64
    /// Data bytes of the current entry not yet read.
    left: i64
    /// Padding after the current entry's data.
    pad: int
    block: [512]u8
}

/// A walker at the start of an archive.
export Walker.New(): Walker {
    return Walker(offset: 0, left: 0, pad: 0, block: [0; 512])
}

/// The next entry, or none after the end blocks. Skips what is left of
/// the previous entry's data, and entries whose path is the archive's root
/// (`./`). Fails with `Cut` when `r` ends early, `BadHeader` and `BadName`
/// for the header, and with `r`'s `ReadError`. O(the data skipped); the
/// path lives in `a`.
export Walker.Next(mut self, a: &Arena, r: mut Reader): Error!?Entry from a {
    var long: ?str = none
    for {
        try self.Skip(r)
        at := self.offset
        try self.Header(r)
        if Zero(self.block[..]) {
            try self.Header(r)
            if !Zero(self.block[..]) {
                fail BadHeader(offset: at)
            }
            return none
        }
        flag := self.block[156]
        (entry: e, stored) := try Parse(a, self.block[..], at, long)
        self.left = stored
        self.pad = Trunc[int]((i64(BlockLen) - stored % i64(BlockLen)) % i64(BlockLen))
        if flag == b'x' || flag == b'L' {
            long = try self.Meta(a, r, flag, at)
            continue
        }
        long = none
        if flag == b'g' || flag == b'K' {
            continue
        }
        if e.path.Len() > 0 {
            return some(e)
        }
    }
}

/// Reads the data of a pax extended header or a GNU long name header at
/// `at`, and gives the name it sets for the next entry, if any.
Walker.Meta(mut self, a: &Arena, r: mut Reader, flag: u8, at: i64): Error!?str from a {
    if self.left > i64(MaxMeta) {
        fail BadHeader(offset: at)
    }
    data := try a.Slice[u8](Trunc[int](self.left))
    n := try r.ReadAll(data)
    self.offset += i64(n)
    self.left = 0
    if n != data.Len() {
        fail Cut(offset: self.offset)
    }
    name := if flag == b'L' { some(Field(data[..])) } else { try PaxPath(data[..], at) }
    bytes := name else {
        return none
    }
    text := str.From(bytes) else {
        fail BadName(offset: at)
    }
    return some(text)
}

/// The value of the `path` record of pax extended header data, if it has
/// one. Fails with `BadHeader` for a record whose length or layout is
/// wrong.
PaxPath(data: []u8, at: i64): BadHeader!?[]u8 from data {
    var found: ?[]u8 = none
    var i := 0
    for i < data.Len() {
        var j := i
        var n := 0
        for j < data.Len() && data[j] >= b'0' && data[j] <= b'9' && n <= data.Len() {
            n = n * 10 + int(data[j] - b'0')
            j += 1
        }
        if j == i || j >= data.Len() || data[j] != b' ' || n <= j - i + 1 || i + n > data.Len() || data[i + n - 1] != b'\n' {
            fail BadHeader(offset: at)
        }
        record := data[j + 1..i + n - 1]
        var eq := 0
        for eq < record.Len() && record[eq] != b'=' {
            eq += 1
        }
        if eq == record.Len() {
            fail BadHeader(offset: at)
        }
        if (str.From(record[..eq]) else { "" }) == "path" {
            found = some(record[eq + 1..])
        }
        i += n
    }
    return found
}

/// Reads the current entry's next bytes into `buf`, 0 once its data is
/// read. Fails with `Cut` when `r` ends first, and with `r`'s `ReadError`.
/// O(n); allocates nothing.
export Walker.Read(mut self, r: mut Reader, buf: mut []u8): Cut|ReadError!int {
    if self.left == 0 || buf.Len() == 0 {
        return 0
    }
    want := if self.left < i64(buf.Len()) { Trunc[int](self.left) } else { buf.Len() }
    n := try r.Read(mut buf[..want])
    if n == 0 {
        fail Cut(offset: self.offset)
    }
    self.left -= i64(n)
    self.offset += i64(n)
    return n
}

/// Skips the rest of the current entry and its padding.
Walker.Skip(mut self, r: mut Reader): Cut|ReadError!void {
    for self.left > 0 {
        want := if self.left < i64(BlockLen) { Trunc[int](self.left) } else { BlockLen }
        n := try r.Read(mut self.block[..want])
        if n == 0 {
            fail Cut(offset: self.offset)
        }
        self.left -= i64(n)
        self.offset += i64(n)
    }
    if self.pad > 0 {
        n := try r.ReadAll(mut self.block[..self.pad])
        self.offset += i64(n)
        if n != self.pad {
            fail Cut(offset: self.offset)
        }
        self.pad = 0
    }
}

/// Reads one whole block into `self.block`.
Walker.Header(mut self, r: mut Reader): Cut|ReadError!void {
    n := try r.ReadAll(mut self.block[..])
    self.offset += i64(n)
    if n != BlockLen {
        fail Cut(offset: self.offset)
    }
}

/// Whether every byte of `b` is zero.
Zero(b: []u8): bool {
    for x in b {
        if x != 0 {
            return false
        }
    }
    return true
}

/// The entry the header block `b` describes, read at `offset`.
/// The entry the header block `b` at `offset` describes, its name `long`
/// when a pax or GNU header gave one, and the bytes of data that follow
/// the header in the archive, whatever its kind.
Parse(a: &Arena, b: []u8, offset: i64, long: ?str): BadHeader|BadName|OutOfMemory!(entry: Entry, stored: i64) from a, long {
    var sum: u64 = 0
    for i, x in b {
        sum += if i >= 148 && i < 156 { u64(b' ') } else { u64(x) }
    }
    if try Octal(b[148..156], offset) != sum {
        fail BadHeader(offset: offset)
    }
    kind := match b[156] {
        b'0' | 0 | b'7' { Kind.File }
        b'5' { Kind.Dir }
        b'1' | b'2' { Kind.Link }
        _ { Kind.Other }
    }
    mode := Trunc[u32](try Octal(b[100..108], offset)) & 0o7777
    size := Cast[i64](try Octal(b[124..136], offset))
    modified := Cast[i64](try Octal(b[136..148], offset))
    name := Field(b[0..100])
    ustar := (str.From(b[257..262]) else { "" }) == "ustar"
    prefix := if ustar { Field(b[345..500]) } else { b[..0] }
    var full := if text := long {
        text
    } else if prefix.Len() > 0 {
        text := str.From(prefix) else {
            fail BadName(offset: offset)
        }
        rest := str.From(name) else {
            fail BadName(offset: offset)
        }
        try a.Fmt("{text}/{rest}", (text, rest))
    } else {
        text := str.From(name) else {
            fail BadName(offset: offset)
        }
        try a.Dup(text)
    }
    for full.StartsWith("./") {
        full = full[2..]
    }
    for full.EndsWith("/") {
        full = full[..full.Len() - 1]
    }
    if full != "." && full.Len() > 0 && !archive.SafePath(full) {
        fail BadName(offset: offset)
    }
    path := if full == "." { "" } else { full }
    entry := Entry(path: path, kind: kind, mode: mode, size: if kind == Kind.File { size } else { 0 }, modified: modified)
    return (entry, stored: size)
}

/// The bytes of a text field up to its first zero byte.
Field(b: []u8): []u8 from b {
    for i, x in b {
        if x == 0 {
            return b[..i]
        }
    }
    return b
}

/// An octal number field: optional leading spaces, octal digits, then
/// spaces or zero bytes to the end. An empty field is 0.
Octal(b: []u8, offset: i64): BadHeader!u64 {
    var i := 0
    for i < b.Len() && b[i] == b' ' {
        i += 1
    }
    var n: u64 = 0
    var digits := 0
    for i < b.Len() && b[i] >= b'0' && b[i] <= b'7' {
        if digits == 21 {
            fail BadHeader(offset: offset)
        }
        n = n * 8 + u64(b[i] - b'0')
        digits += 1
        i += 1
    }
    for i < b.Len() {
        if b[i] != b' ' && b[i] != 0 {
            fail BadHeader(offset: offset)
        }
        i += 1
    }
    return n
}

/// The nine permission letters of `mode`, as `ls -l` prints them.
export Letters(mode: u32): [9]u8 {
    names := "rwxrwxrwx"
    var out: [9]u8 = [b'-'; 9]
    for i in 0..9 {
        if mode & (Cast[u32](1) << Cast[u32](8 - i)) != 0 {
            out[i] = names.Byte(i)
        }
    }
    return out
}

test "an octal field reads with its spaces and zero bytes" {
    Assert(Octal(b"0000644 \x00", 0).Must() == 0o644)
    Assert(Octal(b"   17\x00\x00", 0).Must() == 15)
    Assert(Octal(b"\x00\x00\x00", 0).Must() == 0)
    var refused := false
    _ = Octal(b"12x4", 0) else {
        refused = true
        0
    }
    Assert(refused)
}

test "Letters spells the permission bits" {
    a := Letters(0o640)
    b := Letters(0o755)
    Assert(str.From(a[..]).Must() == "rw-r-----")
    Assert(str.From(b[..]).Must() == "rwxr-xr-x")
}
