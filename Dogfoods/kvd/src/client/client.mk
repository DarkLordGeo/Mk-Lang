// Why: every client mode of kvd (client, bench, demo, record, sim) speaks
// the same protocol and needs one place that connects and reads replies.
// What: Lookup, Connect (with retries), ConnectAdmin, and Call, which sends
// one request line and reads its whole reply.
// How: a reply is read line by line; an array header says how many value
// lines follow, and a connection closed before the reply is Disconnected.

import std.net
import std.time
import src.proto

export error Disconnected

/// What a call can fail with. `Disconnected` is a server that closed the
/// connection before replying.
export type Error = net.Error | ReadError | WriteError | OutOfMemory | Canceled | proto.BadReply | Disconnected

/// The address to dial for `addr`, `host:port`: `addr` itself when the host
/// is an IP address, else the first address `net.Resolve` finds for it.
/// Fails with `InvalidAddress` for an address with no port or a bad one.
export Lookup(a: &Arena, io: Io, addr: str): net.Error|OutOfMemory!str from a, addr {
    _ = net.ParseAddr(addr) else {
        var i := addr.Len() - 1
        for i >= 0 && addr.Byte(i) != b':' {
            i -= 1
        }
        if i <= 0 {
            fail net.InvalidAddress
        }
        port := addr[i + 1..].ParseInt() else {
            fail net.InvalidAddress
        }
        if port < 0 || port > 65535 {
            fail net.InvalidAddress
        }
        found := try net.Resolve(a, io, addr[..i])
        if found.Len() == 0 {
            fail net.InvalidAddress
        }
        at := net.Addr(ip: found[0].ip, port: Cast[u16](port), v6: found[0].v6)
        var t := Text.New(a)
        var tw := t.Writer()
        at.Format(mut tw) else {
            fail OutOfMemory
        }
        return t.Done()
    }
    return addr
}

/// Dials `addr`, trying `attempts` times 20 ms apart while it is refused,
/// as a client started next to a server that is still starting does.
export Connect(io: Io, addr: str, attempts: int): net.Error|Canceled!net.Stream {
    var left := attempts
    for {
        conn := net.Dial(io, addr) else e {
            left -= 1
            if left <= 0 {
                fail e
            }
            match e {
                net.Refused | net.Reset {
                    try io.Sleep(time.Millis(20))
                    continue
                }
                _ {
                    fail e
                }
            }
        }
        Tune(&conn)
        return conn
    }
}

/// Sets a TCP stream to send each request at once and to probe a server
/// that went quiet. Best effort: where the system has no such options
/// (std.sim), or one fails, the stream works as it is.
Tune(conn: &net.Stream) {
    conn.SetNoDelay(true) else {
        return
    }
    conn.SetKeepAlive(some(time.Seconds(30))) else {}
}

/// Connects to the admin channel at `path`, a Unix-domain socket.
export ConnectAdmin(io: Io, path: str): net.Error!net.Stream {
    return try net.DialUnix(io, path)
}

/// Sends one request and reads its reply: one line, or for an array its
/// header and its items. The lines live in `a`. O(reply).
export Call(a: &Arena, r: mut Reader, w: mut Writer, request: str): Error!List[str] from a {
    try w.Str(request)
    try w.Str("\n")
    try w.Flush()
    var lines := List[str].New(a)
    first := try ReadLine(a, r)
    try lines.Push(first)
    reply := try proto.ParseReply(first)
    if n := Items(&reply) {
        for _ in 0..n {
            try lines.Push(try ReadLine(a, r))
        }
    }
    return lines
}

/// How many item lines follow a reply line.
Items(reply: &proto.Reply): ?int {
    return match *reply {
        .Array(n) { some(n) }
        _ { none }
    }
}

/// One reply line; `Disconnected` at the end of the stream.
ReadLine(a: &Arena, r: mut Reader): ReadError|OutOfMemory|Disconnected!str from a {
    got := try r.Line(a)
    line := got else {
        fail Disconnected
    }
    return line
}
