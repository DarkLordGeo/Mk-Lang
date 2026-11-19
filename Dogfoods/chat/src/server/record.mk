// Why: With `--record`, the log shows every byte that crossed each socket.
//
// What: Recorder, a net.Transport wrapper, and the byte escaper Escape.
//
// How: The wrapper owns the connection, adds no buffering, and logs one record
// per read or write, showing up to 120 bytes and counting the rest.

import std.log
import std.net

/// The most bytes of one read or write a record shows; the rest is counted.
const RecordShown: int = 120

/// A transport that reports every byte its connection reads and writes to
/// the hub's log, one record per read or write, for `--record`. It owns the
/// connection it wraps and adds no buffering, so what it reports is what
/// crossed the socket.
struct Recorder {
    inner: net.Conn
    id: int
    sink: log.Sink
}

/// A recorder over `inner`, reporting as connection `id` to `sink`.
Recorder.Of(inner: net.Conn, id: int, sink: log.Sink): Recorder from inner, sink {
    return Recorder(inner: inner, id: id, sink: sink)
}

impl net.Transport for Recorder {
    Read(&self, io: Io, buf: mut []u8): net.Error!int {
        n := try self.inner.Read(io, buf)
        if n == 0 {
            self.sink.Info(io, "conn {id} < end of stream", (id: self.id)) else {}
            return 0
        }
        Note(&self.sink, io, self.id, "<", buf[..n])
        return n
    }

    WriteAll(&self, io: Io, data: []u8): net.Error!void {
        try self.inner.WriteAll(io, data)
        Note(&self.sink, io, self.id, ">", data)
    }

    Shutdown(&self, io: Io, how: net.Shutdown): net.Error!void {
        try self.inner.Shutdown(io, how)
    }

    PeerAddr(&self): ?net.Addr {
        return self.inner.PeerAddr()
    }

    Close(&self, io: Io): net.Error!void {
        // a TCP close only releases the socket, which Abandon does
        self.sink.Info(io, "conn {id} closed", (id: self.id)) else {}
    }

    Abandon(self) {
        Recorder(inner, ..) := self
        inner.Abandon()
    }
}

/// Reports `data` as one record: `conn <id> <dir> <bytes>`, with `\n`, `\r`,
/// `\t` and `\\` escaped, other bytes outside printable ASCII as `\xHH`, and
/// what passes `RecordShown` bytes counted. A record that cannot be written is
/// dropped, as a log line may be.
Note(sink: &log.Sink, io: Io, id: int, dir: str, data: []u8) {
    var buf: [512]u8 = [0; 512]
    var n := 0
    shown := if data.Len() > RecordShown { RecordShown } else { data.Len() }
    for b in data[..shown] {
        n = Escape(b, mut buf[..], n)
    }
    // Escape writes printable ASCII only, so the text is valid UTF-8
    text := str.From(buf[..n]).Must()
    if shown < data.Len() {
        sink.Info(io, "conn {id} {dir} {text} (+{more} bytes)", (id, dir, text, more: data.Len() - shown)) else {}
        return
    }
    sink.Info(io, "conn {id} {dir} {text}", (id, dir, text)) else {}
}

/// Writes byte `b` at `buf[n..]` in its escaped form and returns the new
/// length; `buf` has room for four bytes past `n`.
Escape(b: u8, buf: mut []u8, n: int): int {
    hex := "0123456789abcdef"
    var c: u8 = 0
    if b == b'\n' {
        c = b'n'
    } else if b == b'\r' {
        c = b'r'
    } else if b == b'\t' {
        c = b't'
    } else if b == b'\\' {
        c = b'\\'
    }
    if c != 0 {
        buf[n] = b'\\'
        buf[n + 1] = c
        return n + 2
    }
    if b >= 0x20 && b < 0x7f {
        buf[n] = b
        return n + 1
    }
    buf[n] = b'\\'
    buf[n + 1] = b'x'
    buf[n + 2] = hex.Byte(int(b >> 4))
    buf[n + 3] = hex.Byte(int(b & 0xf))
    return n + 4
}

test "Escape keeps printable ASCII and escapes the rest" {
    var buf: [16]u8 = [0; 16]
    var n := Escape(b'a', mut buf[..], 0)
    n = Escape(b'\n', mut buf[..], n)
    n = Escape(0x01, mut buf[..], n)
    n = Escape(b'\\', mut buf[..], n)
    Assert(str.From(buf[..n]).Must() == "a\\n\\x01\\\\")
}
