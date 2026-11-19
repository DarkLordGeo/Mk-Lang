// line-echo, Mk, std.net: a buffered line echo through
// net.Conn's Reader and Writer (Conn.Reader and Conn.Writer over
// 4096-byte buffers). Each of n rounds writes 16 lines through the
// client's Writer and flushes, reads each line at the server with
// Reader.Until and writes it back through the server's Writer, flushes,
// and reads the 16 lines back at the client with Reader.Until. A line is
// 32 bytes: byte 0 is 32 + k mod 64 for the k-th line sent, then 30 'x'
// and a newline. sum = 31 sum + byte 0 + 256 * length of each line read
// back at the client (wrapping). Mode tcp runs over one loopback TCP
// connection (Stream.Conn at both ends, both with TCP_NODELAY); mode
// inmem over two in-memory pipes of 4096 bytes, each under its own Mutex,
// one Conn per end made by net.NewConn. The in-memory transport is picked
// at run time from the mode, as in every cell: End, or Null for mode null
// (every read is the end of the stream, so every round fails), so no
// optimiser sees one transport type at the loop. Prints n, sum
// and the failed calls. Usage: lineecho <n> tcp|inmem|null.

import std.math
import std.net

const Lines = 16

// Pipe holds the bytes written to one direction and not yet read.
struct Pipe {
    data: [4096]u8
    start: int
    end: int
}

NewPipe(): Mutex[Pipe] {
    return Mutex[Pipe].New(Pipe(data: [0; 4096], start: 0, end: 0))
}

// End is one end of the in-memory duplex: it reads rx and writes tx.
linear view struct End {
    rx: &Mutex[Pipe]
    tx: &Mutex[Pipe]
}

impl net.Transport for End {
    Read(&self, io: Io, buf: mut []u8): net.Error!int {
        var g := try self.rx.Lock(io)
        defer g.Unlock()
        p := g.Mut()
        n := math.Min(p.end - p.start, buf.Len())
        (mut buf[0..n]).CopyFrom(p.data[p.start..p.start + n])
        p.start += n
        if p.start == p.end {
            p.start = 0
            p.end = 0
        }
        return n
    }
    WriteAll(&self, io: Io, data: []u8): net.Error!void {
        var g := try self.tx.Lock(io)
        defer g.Unlock()
        p := g.Mut()
        n := data.Len()
        if n > 4096 - p.end {
            // A round sends 512 bytes and reads them all before the next.
            Panic("lineecho: a pipe overflowed")
        }
        (mut p.data[p.end..p.end + n]).CopyFrom(data)
        p.end += n
    }
    Shutdown(&self, io: Io, how: net.Shutdown): net.Error!void {
    }
    PeerAddr(&self): ?net.Addr {
        return none
    }
    Close(&self, io: Io): net.Error!void {
    }
    Abandon(self) {
        End(rx: _, tx: _) := self
    }
}

// Null reads the end of the stream and drops what is written.
linear struct Null {
    mark: u8
}

impl net.Transport for Null {
    Read(&self, io: Io, buf: mut []u8): net.Error!int {
        return 0
    }
    WriteAll(&self, io: Io, data: []u8): net.Error!void {
    }
    Shutdown(&self, io: Io, how: net.Shutdown): net.Error!void {
    }
    PeerAddr(&self): ?net.Addr {
        return none
    }
    Close(&self, io: Io): net.Error!void {
    }
    Abandon(self) {
        Null(mark: _) := self
    }
}

// Open makes one end's Conn over the transport the mode picked: Null, or
// the End that reads rx and writes tx.
Open(h: &Heap, null: bool, rx: &Mutex[Pipe], tx: &Mutex[Pipe]): OutOfMemory!net.Conn from h, rx, tx {
    if null {
        z := Null(mark: 0)
        return net.NewConn(h, z) else e {
            z.Abandon()
            fail e
        }
    }
    t := End(rx: rx, tx: tx)
    return net.NewConn(h, t) else e {
        t.Abandon()
        fail e
    }
}

struct Result {
    sum: u64
    fails: int
}

// Echo runs the n rounds between the client c and the server s.
Echo(io: Io, n: int, c: &net.Conn, s: &net.Conn): Result {
    var cwb: [4096]u8 = [0; 4096]
    var crb: [4096]u8 = [0; 4096]
    var swb: [4096]u8 = [0; 4096]
    var srb: [4096]u8 = [0; 4096]
    var cw := c.Writer(io, mut cwb[..])
    var cr := c.Reader(io, mut crb[..])
    var sw := s.Writer(io, mut swb[..])
    var sr := s.Reader(io, mut srb[..])
    var line: [32]u8 = [0x78; 32]
    line[31] = 0x0A
    var got: [64]u8 = [0; 64]
    var sum: u64 = 0
    var fails := 0
    var k := 0
    for _ in 0..n {
        for _ in 0..Lines {
            line[0] = Trunc[u8](32 + k % 64)
            k += 1
            cw.Bytes(line[..]) else {
                fails += 1
            }
        }
        cw.Flush() else {
            fails += 1
        }
        for _ in 0..Lines {
            m := sr.Until(0x0A, mut got[..]) else {
                fails += 1
                0
            }
            sw.Bytes(got[0..m]) else {
                fails += 1
            }
        }
        sw.Flush() else {
            fails += 1
        }
        for _ in 0..Lines {
            m := cr.Until(0x0A, mut got[..]) else {
                fails += 1
                0
            }
            if m != 32 {
                fails += 1
            }
            sum = sum *% 31 +% (u64(got[0]) +% 256 * Trunc[u64](m))
        }
    }
    return Result(sum: sum, fails: fails)
}

export Main(sys: &System): ParseError|WriteError|OutOfMemory|net.Error!void {
    n := Cast[int](try sys.args[1].ParseInt())
    var r := Result(sum: 0, fails: 0)
    mode := if sys.args.Len() > 2 { sys.args[2] } else { "tcp" }
    if mode == "inmem" || mode == "null" {
        up := NewPipe()
        down := NewPipe()
        c := try Open(sys.heap, mode == "null", &down, &up)
        s := Open(sys.heap, mode == "null", &up, &down) else e {
            c.Abandon()
            fail e
        }
        r = Echo(sys.io, n, &c, &s)
        c.Abandon()
        s.Abandon()
    } else {
        l := try net.Listen(sys.io, "127.0.0.1:0")
        defer l.Close()
        addr := sys.arena.Fmt("127.0.0.1:{p}", (p: l.Port())) else {
            Panic("no memory for an address")
        }
        d := try net.Dial(sys.io, addr)
        a := l.Accept(sys.io) else e {
            d.Close()
            fail e
        }
        // The option calls succeed on a native socket; a failure only
        // slows the run.
        d.SetNoDelay(true) else {
        }
        a.SetNoDelay(true) else {
        }
        c := d.Conn()
        s := a.Conn()
        r = Echo(sys.io, n, &c, &s)
        c.Abandon()
        s.Abandon()
    }
    var out := sys.Stdout()
    try Print(mut out, "{n} {sum} {fails}\n", (n, sum: r.sum, fails: r.fails))
}
