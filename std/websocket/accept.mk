// Why: a server upgrades an HTTP request to a WebSocket connection after its
// handler took the connection (http.Taken); the checks and the 101 reply of
// RFC 6455's server-side handshake (section 4.2) live here.
//
// What: Accept, and its helpers Switch, SwitchHead, Refuse and the limits
// SwitchFixed and ProtocolMax.
//
// How: CheckRequest checks the request; a refusal is answered with 400, 403
// or 426 (the last naming version 13, section 4.4) and leaves the Taken with
// the caller. On success the 101 names the subprotocol Choose picked, if
// any, and no extension: a permessage-deflate offer is declined by not
// answering it (RFC 7692 section 5).

import std.http
import std.net

/// Accepts the WebSocket handshake of req on the server side, over t, the
/// connection the HTTP handler took with Link.Takeover. The connection
/// uses DefaultOptions' limits. allow decides on the request's Origin
/// field ("" when absent).
///
/// The 101 reply names the first subprotocol the client offered (in the
/// order of its Sec-WebSocket-Protocol fields and of each field's list)
/// that protocols lists byte for byte, and none when they share none or
/// protocols is empty; Conn.Protocol then names it. An offered name that
/// is not a token, or is longer than 16,243 bytes, is never chosen.
///
/// On success the Conn holds t's connection and starts with the bytes t
/// had read past the request. It keeps t's loans, the response's arena
/// among them, so it lives inside the handler. On failure t stays the
/// caller's, to Abandon. Errors: Handshake(400) for a request RFC 6455
/// 4.2.1 refuses (not GET, no Upgrade of websocket, no Connection holding
/// upgrade, a key that is not 16 bytes in base64 or appears twice, more
/// read past the request than 16 KiB), Handshake(403) when allow refuses
/// the origin, Handshake(426) for a version other than 13, each after
/// answering the client with that status; the connection's net.Error;
/// OutOfMemory, before any byte of the 101 is written. After a cancel,
/// part of the reply may have been sent: abandon t. O(the request's
/// fields times protocols); allocates the connection's state (about 33
/// KiB) and the chosen name on h.
export Accept(h: &Heap, io: Io, req: &http.Request, t: http.Taken, protocols: []str, const allow: fn(origin: str): bool): Error!Conn from h, t {
    var status := 0
    key := CheckRequest(req, allow) else e {
        status = match e {
            Refusal(status: code) { code }
        }
        ""
    }
    if status == 0 && t.rest.Len() > ReadAhead {
        status = 400
    }
    if status != 0 {
        Refuse(&t.conn, io, status)
        fail Handshake(status: status)
    }
    chosen := Choose(req, protocols)
    o := DefaultOptions
    st := State.New(h, false, &o, t.rest)
    var b := Box[State].New(h.Alloc(), st) else e {
        st.Drop(h)
        fail e
    }
    if chosen >= 0 {
        // The Conn keeps nothing of protocols: the chosen name is copied
        // to h, before the 101, so OutOfMemory never follows a 101.
        b.Mut().protocol.Extend(h.Alloc(), protocols[chosen].AsBytes()) else e {
            FreeState(b, h)
            fail e
        }
    }
    Switch(b.Ref(), &t.conn, io, key.AsBytes()) else e {
        FreeState(b, h)
        fail e
    }
    http.Taken(conn, rest: _) := t
    return Conn(conn: conn, heap: h, box: b)
}

// Switch writes st's 101 for key, naming st's subprotocol when it has
// one, in one WriteAll from st's write buffer: no Conn exists yet, so the
// lock is free and taking it under NoCancel cannot wait.
Switch(st: &State, conn: &net.Conn, io: Io, key: []u8): net.Error!void {
    var g := st.wr.Lock(io.NoCancel()) else e {
        fail e
    }
    n := SwitchHead(key, st.protocol.Items(), mut g.Mut().buf[..])
    conn.WriteAll(io, g.Mut().buf[0..n]) else e {
        g.Unlock()
        fail e
    }
    g.Unlock()
}

// SwitchFixed is the length of a 101 with an empty subprotocol line: the
// status line, Upgrade, Connection, the accept value, the
// Sec-WebSocket-Protocol line's name and line end, and the empty line.
const SwitchFixed: int = 155

// ProtocolMax is the longest subprotocol name Choose picks, so a 101
// naming it fits a connection's write buffer: 16,243 bytes.
const ProtocolMax: int = MaxHead + WriteChunk - SwitchFixed

// SwitchHead writes the 101 reply for key into out (4.2.2, step 5) and
// returns its length: the status line, Upgrade, Connection, the accept
// value and, when proto is not empty, Sec-WebSocket-Protocol naming it.
// out holds at least SwitchFixed plus proto.Len() bytes.
SwitchHead(key: []u8, proto: []u8, out: mut []u8): int {
    lead := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ".AsBytes()
    var n := Put(out, 0, lead)
    var acc: [28]u8 = [0; 28]
    AcceptOf(key, mut acc)
    n = Put(out, n, acc[..])
    n = Put(out, n, "\r\n".AsBytes())
    if proto.Len() > 0 {
        n = Put(out, n, "Sec-WebSocket-Protocol: ".AsBytes())
        n = Put(out, n, proto)
        n = Put(out, n, "\r\n".AsBytes())
    }
    return Put(out, n, "\r\n".AsBytes())
}

// Put copies b into out at n and returns the index past it.
Put(out: mut []u8, n: int, b: []u8): int {
    (mut out[n..n + b.Len()]).CopyFrom(b)
    return n + b.Len()
}

// Refuse answers a refused handshake with status and no body, best effort
// within DefaultOptions' close limit: the connection is the caller's to
// release either way.
Refuse(conn: &net.Conn, io: Io, status: int) {
    head := if status == 403 {
        "HTTP/1.1 403 Forbidden\r\nConnection: close\r\nContent-Length: 0\r\n\r\n"
    } else if status == 426 {
        "HTTP/1.1 426 Upgrade Required\r\nSec-WebSocket-Version: 13\r\nConnection: close\r\nContent-Length: 0\r\n\r\n"
    } else {
        "HTTP/1.1 400 Bad Request\r\nConnection: close\r\nContent-Length: 0\r\n\r\n"
    }
    scope s := io.Scope() {
        s.CancelAfter(DefaultOptions.close)
        conn.WriteAll(io, head.AsBytes()) else {}
    }
}
