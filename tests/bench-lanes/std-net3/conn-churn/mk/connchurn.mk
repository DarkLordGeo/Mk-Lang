// conn-churn, Mk, std.net: n loopback TCP connections, one
// after another, of one task. Each round dials the listener, accepts the
// connection and reads the peer's address the accept gave
// (Stream.PeerAddr), writes two bytes (the round's low byte twice) at the
// accepted end, reads one at the dialled end, and closes the dialled end,
// then the accepted one. The dialled end closes with a byte unread, so its
// TCP resets the connection (RFC 1122 4.2.2.13) and no TIME_WAIT entry is
// left. sum = 31 sum + each byte read (wrapping); a round whose peer is not
// 127.0.0.1 with a port, or whose call failed, counts as failed. Prints n,
// sum and the failed rounds. Usage: connchurn <n>.

import std.net

export Main(sys: &System): ParseError|WriteError|net.Error!void {
    n := Cast[int](try sys.args[1].ParseInt())
    l := try net.Listen(sys.io, "127.0.0.1:0")
    defer l.Close()
    addr := sys.arena.Fmt("127.0.0.1:{p}", (p: l.Port())) else {
        Panic("no memory for an address")
    }
    var two: [2]u8 = [0, 0]
    var buf: [1]u8 = [0]
    var sum: u64 = 0
    var fails := 0
    for i in 0..n {
        d := net.Dial(sys.io, addr) else {
            fails += 1
            continue
        }
        s := l.Accept(sys.io) else {
            fails += 1
            d.Close()
            continue
        }
        p := s.PeerAddr()
        if p.v6 || p.ip[0] != 127 || p.ip[1] != 0 || p.ip[2] != 0 || p.ip[3] != 1 || p.port == 0 {
            fails += 1
        }
        two[0] = Trunc[u8](i)
        two[1] = Trunc[u8](i)
        buf[0] = 0
        s.WriteAll(sys.io, two[..]) else {
            fails += 1
        }
        k := d.Read(sys.io, mut buf[..]) else {
            fails += 1
            0
        }
        if k != 1 {
            fails += 1
        }
        sum = sum *% 31 +% u64(buf[0])
        d.Close()
        s.Close()
    }
    var out := sys.Stdout()
    try Print(mut out, "{n} {sum} {fails}\n", (n, sum, fails))
}
