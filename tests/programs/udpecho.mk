// UDP echo, the server and its client in one program, run on both targets over
// IPv4 loopback.
// The server task binds 127.0.0.1:0 and answers every datagram with
// the same bytes until one reads "quit", which it answers "bye". The main task sends 1,000
// datagrams of 1 to 1,400 bytes, each answered before the next, checks every echo byte for
// byte with its source port and family, then sends "quit". Prints "1000 echoed" and "bye".

import std.net

Serve(d: net.Datagram, io: Io) {
    defer d.Close()
    var buf: [2048]u8 = [0; 2048]
    for {
        (n: n, peer: peer) := d.Recv(io, mut buf[..]) else {
            return
        }
        if Is(buf[0..n], "quit") {
            d.Send(io, "bye".AsBytes(), peer) else {
                return
            }
            return
        }
        d.Send(io, buf[0..n], peer) else {
            return
        }
    }
}

// Is: b holds exactly the bytes of s.
Is(b: []u8, s: str): bool {
    w := s.AsBytes()
    if b.Len() != w.Len() {
        return false
    }
    for i in 0..w.Len() {
        if b[i] != w[i] {
            return false
        }
    }
    return true
}

export Main(sys: &System): net.Error|WriteError|OutOfMemory!void {
    server := try net.Bind(sys.io, "127.0.0.1:0")
    p := server.Port()
    var out := sys.Stdout()
    scope s := sys.io.Scope() {
        s.go Serve(server, sys.io)
        to := try net.ParseAddr(try sys.arena.Fmt("127.0.0.1:{p}", (p)))
        d := try net.Bind(sys.io, "127.0.0.1:0")
        defer d.Close()
        out_buf := try sys.arena.Slice[u8](1_400)
        back := try sys.arena.Slice[u8](2_048)
        var good := 0
        for i in 0..1_000 {
            n := 1 + (i * 7) % 1_400
            for k in 0..n {
                out_buf[k] = Trunc[u8](i + k)
            }
            try d.Send(sys.io, out_buf[0..n], to)
            (n: m, peer: peer) := try d.Recv(sys.io, back)
            var same := m == n && peer.port == to.port && peer.v6 == to.v6
            for k in 0..m {
                if k < n && back[k] != out_buf[k] {
                    same = false
                }
            }
            if same {
                good += 1
            }
        }
        try Print(mut out, "{good} echoed\n", (good))
        try d.Send(sys.io, "quit".AsBytes(), to)
        (n: m, peer: _) := try d.Recv(sys.io, back)
        if Is(back[0..m], "bye") {
            try Print(mut out, "bye\n")
        }
    }
    try out.Flush()
}
