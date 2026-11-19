// Why: The web front end may be asked to listen on port 0, and the cases need
// the real port.
//
// What: WebAddress.
//
// How: Binds a listener to find a free port, closes it, and returns the address
// with that port.

import std.net

/// The address the web front end serves on: `addr`, or, when its port is 0,
/// the same host with a port that was free a moment ago. Fails when no port
/// can be had, or with `OutOfMemory`.
// A workaround: http.ServeWith binds the address itself and cannot
// report the port it bound, so port 0 is resolved here first.
WebAddress(a: &Arena, io: Io, addr: str): net.Error!str from a, addr {
    if !addr.EndsWith(":0") {
        return addr
    }
    l := try net.Listen(io, addr)
    port := l.Port()
    l.Close()
    host := addr[..addr.Len() - 2]
    return a.Fmt("{host}:{port}", (host, port)) else {
        fail OutOfMemory
    }
}
