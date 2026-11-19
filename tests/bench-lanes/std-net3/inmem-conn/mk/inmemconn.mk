// inmem-conn, Mk, std.net: a connection over an
// in-memory transport, as a protocol layer sees one. The transport keeps
// one 32-byte message under its own Mutex (Send + Share, as net.Transport
// asks); each of n rounds writes 32 bytes (byte 0 is the round's low
// byte) through the connection and reads them back. sum = 31 sum + byte 0
// of each read (wrapping). Mode conn goes through net.NewConn (the
// Conn's read or write lock, one indirect call, the box's branch); mode
// direct calls the transport itself, the cost the Conn adds being the
// difference. The Conn's transport is picked at run time from the mode,
// as a server makes its connection from what it accepted: Loop, or Null
// for mode null (every read is the end of the stream, so every round
// fails), the same choice every cell makes, so no optimiser sees one
// transport type at the loop. Prints n, sum and the failed
// calls. Usage: inmemconn <n> [conn|direct|null].

import std.math
import std.net

struct Slot {
    data: [32]u8
    len: int
}

// Loop holds the last message written until it is read.
linear struct Loop {
    held: Mutex[Slot]
}

impl net.Transport for Loop {
    Read(&self, io: Io, buf: mut []u8): net.Error!int {
        var g := try self.held.Lock(io)
        defer g.Unlock()
        s := g.Mut()
        n := math.Min(s.len, buf.Len())
        (mut buf[0..n]).CopyFrom(s.data[0..n])
        s.len = 0
        return n
    }
    WriteAll(&self, io: Io, data: []u8): net.Error!void {
        var g := try self.held.Lock(io)
        defer g.Unlock()
        s := g.Mut()
        n := math.Min(data.Len(), 32)
        (mut s.data[0..n]).CopyFrom(data[0..n])
        s.len = n
    }
    Shutdown(&self, io: Io, how: net.Shutdown): net.Error!void {
    }
    PeerAddr(&self): ?net.Addr {
        return none
    }
    Close(&self, io: Io): net.Error!void {
    }
    Abandon(self) {
        Loop(held) := self
        _ = held.Into()
    }
}

NewLoop(): Loop {
    return Loop(held: Mutex[Slot].New(Slot(data: [0; 32], len: 0)))
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

// Open makes the Conn over the transport the mode picked.
Open(h: &Heap, null: bool): OutOfMemory!net.Conn from h {
    if null {
        z := Null(mark: 0)
        return net.NewConn(h, z) else e {
            z.Abandon()
            fail e
        }
    }
    t := NewLoop()
    return net.NewConn(h, t) else e {
        t.Abandon()
        fail e
    }
}

export Main(sys: &System): ParseError|WriteError|OutOfMemory|net.Error!void {
    n := Cast[int](try sys.args[1].ParseInt())
    mode := if sys.args.Len() > 2 { sys.args[2] } else { "conn" }
    conn := mode != "direct"
    var msg: [32]u8 = [7; 32]
    var buf: [32]u8 = [0; 32]
    var sum: u64 = 0
    var fails := 0
    if conn {
        c := try Open(sys.heap, mode == "null")
        for i in 0..n {
            msg[0] = Trunc[u8](i)
            c.WriteAll(sys.io, msg[..]) else {
                fails += 1
            }
            k := c.Read(sys.io, mut buf[..]) else {
                fails += 1
                0
            }
            if k != 32 {
                fails += 1
            }
            sum = sum *% 31 +% u64(buf[0])
        }
        c.Abandon()
    } else {
        t := NewLoop()
        for i in 0..n {
            msg[0] = Trunc[u8](i)
            t.WriteAll(sys.io, msg[..]) else {
                fails += 1
            }
            k := t.Read(sys.io, mut buf[..]) else {
                fails += 1
                0
            }
            if k != 32 {
                fails += 1
            }
            sum = sum *% 31 +% u64(buf[0])
        }
        t.Abandon()
    }
    var out := sys.Stdout()
    try Print(mut out, "{n} {sum} {fails}\n", (n, sum, fails))
}
