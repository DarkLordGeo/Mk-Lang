// tcp-pingpong, Mk, std.net: one loopback TCP connection
// of one task, both ends with TCP_NODELAY; each of n rounds writes 32
// bytes (byte 0 is the round's low byte) at the dialled end, reads them
// whole at the accepted end, writes them back and reads them whole at the
// dialled end. sum = 31 sum + byte 0 of each answer (wrapping). Mode conn
// does every read and write through net.Conn (Stream.Conn: Conn.WriteAll
// and Conn.Read, two locks and one indirect call each); mode stream
// through net.Stream. Prints n, sum and the failed calls.
// Usage: tcppingpong <n> [conn|stream].

import std.net

// Full reads exactly buf.Len() bytes through read, false at an early end.
FullConn(io: Io, c: &net.Conn, buf: mut []u8): net.Error!bool {
    var got := 0
    for got < buf.Len() {
        k := try c.Read(io, mut buf[got..])
        if k == 0 {
            return false
        }
        got += k
    }
    return true
}

FullStream(io: Io, s: &net.Stream, buf: mut []u8): net.Error!bool {
    var got := 0
    for got < buf.Len() {
        k := try s.Read(io, mut buf[got..])
        if k == 0 {
            return false
        }
        got += k
    }
    return true
}

export Main(sys: &System): ParseError|WriteError|net.Error!void {
    n := Cast[int](try sys.args[1].ParseInt())
    var conn := true
    if sys.args.Len() > 2 && sys.args[2] == "stream" {
        conn = false
    }
    l := try net.Listen(sys.io, "127.0.0.1:0")
    defer l.Close()
    addr := sys.arena.Fmt("127.0.0.1:{p}", (p: l.Port())) else {
        Panic("no memory for an address")
    }
    d := try net.Dial(sys.io, addr)
    s := l.Accept(sys.io) else e {
        d.Close()
        fail e
    }
    // The option calls succeed on a native socket; a failure only slows
    // the run.
    d.SetNoDelay(true) else {
    }
    s.SetNoDelay(true) else {
    }
    var msg: [32]u8 = [7; 32]
    var buf: [32]u8 = [0; 32]
    var sum: u64 = 0
    var fails := 0
    if conn {
        dc := d.Conn()
        sc := s.Conn()
        for i in 0..n {
            msg[0] = Trunc[u8](i)
            dc.WriteAll(sys.io, msg[..]) else {
                fails += 1
            }
            a := FullConn(sys.io, &sc, mut buf[..]) else {
                fails += 1
                break
            }
            sc.WriteAll(sys.io, buf[..]) else {
                fails += 1
            }
            b := FullConn(sys.io, &dc, mut buf[..]) else {
                fails += 1
                break
            }
            if !a || !b {
                fails += 1
            }
            sum = sum *% 31 +% u64(buf[0])
        }
        dc.Abandon()
        sc.Abandon()
    } else {
        for i in 0..n {
            msg[0] = Trunc[u8](i)
            d.WriteAll(sys.io, msg[..]) else {
                fails += 1
            }
            a := FullStream(sys.io, &s, mut buf[..]) else {
                fails += 1
                break
            }
            s.WriteAll(sys.io, buf[..]) else {
                fails += 1
            }
            b := FullStream(sys.io, &d, mut buf[..]) else {
                fails += 1
                break
            }
            if !a || !b {
                fails += 1
            }
            sum = sum *% 31 +% u64(buf[0])
        }
        d.Close()
        s.Close()
    }
    var out := sys.Stdout()
    try Print(mut out, "{n} {sum} {fails}\n", (n, sum, fails))
}
