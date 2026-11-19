// Why: a taken connection must close at once when abandoned.
// What: Taken.Abandon under a simulated Io.
// How: the peer reads the end of the stream right after Abandon. Run by mk
// test --std=std.http.

import std.net

// TkAbandon accepts one connection, wraps it in a Taken with rest bytes,
// checks rest, and abandons it: the dialling side then reads 0 bytes.
TkAbandon(io: Io, h: &Heap): bool {
    l := net.Listen(io, "127.0.0.1:7480") else {
        return false
    }
    c := net.Dial(io, "127.0.0.1:7480") else {
        l.Close()
        return false
    }
    st := l.Accept(io) else {
        c.Close()
        l.Close()
        return false
    }
    t := Taken(conn: st.Conn(), rest: "GET".AsBytes())
    ok := t.rest.Len() == 3 && t.rest[0] == 71
    t.Abandon()
    var b: [8]u8 = [0; 8]
    n := c.Read(io, mut b[..]) else {
        0
    }
    c.Close()
    l.Close()
    return ok && n == 0
}

test "Taken.Abandon closes the taken connection at once, so its peer reads the end of the stream" (sys: &System) {
    Assert(CtSim(sys, 7, TkAbandon))
}
