// Echo server: args are a port and a
// connection count. It prints "listening <port>" with the port it is bound to, so a
// server started on port 0 names the one the OS chose (Listener.Port), accepts
// exactly that many connections with one task each, echoes each until the client
// closes, and exits 0 after the scope joins. TestEchoServer (internal/corpus) is the client.

import std.net

Echo(conn: net.Stream, io: Io) {
    defer conn.Close()
    var buf: [4096]u8 = [0; 4096]
    for {
        n := conn.Read(io, mut buf[..]) else {
            return
        }
        if n == 0 {
            return
        }
        conn.WriteAll(io, buf[..n]) else {
            return
        }
    }
}

export Main(sys: &System): net.Error|ParseError|OutOfMemory|WriteError!void {
    port := try sys.args[1].ParseInt()
    var left := try sys.args[2].ParseInt()
    addr := try sys.arena.Fmt("127.0.0.1:{port}", (port))
    listener := try net.Listen(sys.io, addr)
    defer listener.Close()
    bound := listener.Port()
    var out := sys.Stdout()
    try Print(mut out, "listening {bound}\n", (bound))
    try out.Flush()
    scope s := sys.io.Scope() {
        for left > 0 {
            left -= 1
            conn := try listener.Accept(sys.io)
            s.go Echo(conn, sys.io)
        }
    }
}
