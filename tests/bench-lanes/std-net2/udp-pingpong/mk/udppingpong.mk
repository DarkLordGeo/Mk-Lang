// udp-pingpong, Mk, std.net (net.Bind, Datagram.Send and Recv):
// two UDP sockets of one task on 127.0.0.1; each of n rounds
// sends 32 bytes (byte 0 is the round's low byte) from a to b, receives
// them on b, sends them back to their source and receives them on a.
// sum = 31 sum + byte 0 of each answer (wrapping). Prints n, sum and the
// failed calls. Usage: udppingpong <n>.

import std.net

export Main(sys: &System): ParseError|WriteError|net.Error!void {
    n := Cast[int](try sys.args[1].ParseInt())
    a := try net.Bind(sys.io, "127.0.0.1:0")
    b := net.Bind(sys.io, "127.0.0.1:0") else e {
        a.Close()
        fail e
    }
    var ip: [16]u8 = [0; 16]
    ip[0] = 127
    ip[3] = 1
    tb := net.Addr(ip: ip, port: Trunc[u16](b.Port()), v6: false)
    var msg: [32]u8 = [7; 32]
    var buf: [64]u8 = [0; 64]
    var sum: u64 = 0
    var fails := 0
    for i in 0..n {
        msg[0] = Trunc[u8](i)
        var ok := true
        a.Send(sys.io, msg[..], tb) else {
            ok = false
        }
        r1 := b.Recv(sys.io, mut buf[..]) else {
            fails += 1
            continue
        }
        b.Send(sys.io, buf[0..32], r1.peer) else {
            ok = false
        }
        r2 := a.Recv(sys.io, mut buf[..]) else {
            fails += 1
            continue
        }
        if !ok || r1.n != 32 || r2.n != 32 {
            fails += 1
        }
        sum = sum *% 31 +% u64(buf[0])
    }
    a.Close()
    b.Close()
    var out := sys.Stdout()
    try Print(mut out, "{n} {sum} {fails}\n", (n, sum, fails))
}
