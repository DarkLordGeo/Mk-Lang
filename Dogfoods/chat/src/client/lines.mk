// Why: Every test client reads and writes the line protocol; the framing lives
// in one place.
//
// What: ReadLine, SendLine, SendPaced, Final and the hex helper Unhex.
//
// How: Lines go out with a single newline, cut to 512 bytes; a reply ends at
// the first line starting with `OK` or `ERR`.

import std.net

/// Reads one line from `r` into `buf`, without its `\n` or `\r\n`. Returns
/// `none` at the end of the stream. A line longer than `buf` comes back cut.
/// Fails with the reader's error.
export ReadLine(r: mut Reader, buf: mut []u8): ReadError!?str from buf {
    n := try r.Until(b'\n', buf)
    if n == 0 {
        return none
    }
    var end := n
    if end > 0 && buf[end - 1] == b'\n' {
        end -= 1
    }
    if end > 0 && buf[end - 1] == b'\r' {
        end -= 1
    }
    s := str.From(buf[..end]) else {
        return some("(not UTF-8)")
    }
    return some(s)
}

/// Sends `line` and its newline. Fails with the stream's error.
export SendLine(conn: &net.Stream, io: Io, line: str): net.Error!void {
    var buf: [520]u8 = [0; 520]
    n := Frame(line, mut buf[..])
    try conn.WriteAll(io, buf[..n])
}

/// `line` and a newline in `buf`, cut to 512 bytes; returns the length.
Frame(line: str, buf: mut []u8): int {
    var n := line.Len()
    if n > 512 {
        n = 512
    }
    for i in 0..n {
        buf[i] = line.Byte(i)
    }
    buf[n] = b'\n'
    return n + 1
}

/// Decodes `hex`, two digits a byte, into `buf`. Returns the byte count, or
/// `none` when `hex` is not an even run of hex digits or does not fit in
/// `buf`. O(n); allocates nothing.
Unhex(hex: str, buf: mut []u8): ?int {
    n := hex.Len() / 2
    if hex.Len() % 2 != 0 || n > buf.Len() {
        return none
    }
    for i in 0..n {
        hi := HexDigit(hex.Byte(2 * i)) else {
            return none
        }
        lo := HexDigit(hex.Byte(2 * i + 1)) else {
            return none
        }
        buf[i] = hi << 4 | lo
    }
    return some(n)
}

/// The value of one hex digit, either case.
HexDigit(b: u8): ?u8 {
    return if b >= b'0' && b <= b'9' {
        some(b - b'0')
    } else if b >= b'a' && b <= b'f' {
        some(b - b'a' + 10)
    } else if b >= b'A' && b <= b'F' {
        some(b - b'A' + 10)
    } else {
        none
    }
}

test "Unhex decodes both cases and refuses odd or foreign digits" {
    var buf: [4]u8 = [0; 4]
    n := Unhex("4dfF", mut buf[..]) else {
        -1
    }
    Assert(n == 2 && buf[0] == 0x4d && buf[1] == 0xff)
    Assert(Unhex("abc", mut buf[..]).IsNone())
    Assert(Unhex("zz", mut buf[..]).IsNone())
    Assert(Unhex("0102030405", mut buf[..]).IsNone())
}

/// Sends `line` and its newline with single writes, each sending what the
/// stream takes, as a client that paces itself does. Fails with the stream's
/// error.
export SendPaced(conn: &net.Stream, io: Io, line: str): net.Error!void {
    var buf: [520]u8 = [0; 520]
    var n := line.Len()
    if n > 512 {
        n = 512
    }
    for i in 0..n {
        buf[i] = line.Byte(i)
    }
    buf[n] = b'\n'
    var sent := 0
    for sent < n + 1 {
        k := try conn.Write(io, buf[sent..n + 1])
        sent += k
    }
}

/// Whether `line` ends a reply: it starts with `OK` or `ERR`.
export Final(line: str): bool {
    return line.StartsWith("OK") || line.StartsWith("ERR")
}
