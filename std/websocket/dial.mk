// Why: a client opens a WebSocket connection with the handshake of RFC 6455
// section 4.1, over TCP for ws URLs and over the secure connector for wss.
//
// What: Dial, and its helpers CheckOptions, RequestHead, Connect, Opening
// and ReadReply.
//
// How: the request carries a fresh 16-byte key from io.Random, version 13,
// the subprotocols and fields of Options, and no extension. The reply is
// read into the connection's read-ahead and checked by ReplyHead, so frames
// the server sends right after its head stay there for the first Recv.

import std.encoding
import std.http
import std.net
import std.url

/// Opens a WebSocket connection to u, a ws or wss URL, with o's header
/// fields, subprotocols, connector and limits. The Conn keeps h, never u
/// or o.
///
/// Errors: net.InvalidAddress for a URL that is not ws or wss or has no
/// host; http.NoSecureTransport for wss with o.secure none; Handshake(0)
/// for a field or subprotocol of o that is not a token and value, a
/// subprotocol offered twice, or a field the handshake writes itself (each
/// before any I/O); the connection's net.Error, and its secure
/// connector's; Handshake(status)
/// for a reply other than a 101 that RFC 6455 4.1 accepts; TooLarge for a
/// reply head over 16 KiB; OutOfMemory. A Dial interrupted by a cancel
/// releases what it made. O(the request and the reply heads); allocates
/// the request and the resolved addresses in a, and the connection's
/// state (about 33 KiB) on h.
export Dial(h: &Heap, a: &Arena, io: Io, u: &url.Url, o: &Options): Error!Conn from h {
    scheme := u.Scheme()
    wss := scheme == "wss"
    if !wss && scheme != "ws" {
        fail net.InvalidAddress
    }
    if wss && o.secure.IsNone() {
        fail http.NoSecureTransport
    }
    host := u.Hostname()
    if host.Len() == 0 {
        fail net.InvalidAddress
    }
    try CheckOptions(o)
    var nonce: [16]u8 = [0; 16]
    try io.Random(mut nonce[..])
    var key: [24]u8 = [0; 24]
    _ = encoding.Base64EncodeTo(mut key[..], nonce[..], encoding.Base64)
    var want: [28]u8 = [0; 28]
    AcceptOf(key[..], mut want)
    head := try RequestHead(a, u, host, key[..], o)
    port := if p := u.Port() { int(p) } else if wss { 443 } else { 80 }
    bare := if host.Byte(0) == 0x5B { host[1..host.Len() - 1] } else { host }
    st := State.New(h, true, o, "".AsBytes())
    b := Box[State].New(h.Alloc(), st) else e {
        st.Drop(h)
        fail e
    }
    var conn := Connect(a, io, bare, port) else e {
        FreeState(b, h)
        fail e
    }
    if wss {
        sec := o.secure else {
            FreeState(b, h)
            conn.Abandon()
            fail http.NoSecureTransport
        }
        conn = sec.Connect(h, io, conn, bare) else e {
            FreeState(b, h)
            conn.Abandon()
            fail e
        }
    }
    chosen := Opening(b.Ref(), &conn, io, head, want[..], o.protocols) else e {
        FreeState(b, h)
        conn.Abandon()
        fail e
    }
    if chosen >= 0 {
        // The Conn keeps nothing of o: the chosen name is copied to h.
        var st2 := b
        st2.Mut().protocol.Extend(h.Alloc(), o.protocols[chosen].AsBytes()) else e {
            FreeState(st2, h)
            conn.Abandon()
            fail e
        }
        return Conn(conn: conn, heap: h, box: st2)
    }
    return Conn(conn: conn, heap: h, box: b)
}

// CheckOptions refuses, with Handshake(0), a field of o whose name is not
// a token, whose value could end a line, or that the handshake writes
// itself, and a subprotocol that is not a token or is offered twice (RFC
// 6455 4.1, item 10).
CheckOptions(o: &Options): Handshake!void {
    for f in o.headers {
        if !IsToken(f.name.AsBytes()) || !ValueOk(f.value.AsBytes()) || Reserved(f.name.AsBytes()) {
            fail Handshake(status: 0)
        }
    }
    for i in 0..o.protocols.Len() {
        p := o.protocols[i]
        if !IsToken(p.AsBytes()) {
            fail Handshake(status: 0)
        }
        for j in 0..i {
            if o.protocols[j] == p {
                fail Handshake(status: 0)
            }
        }
    }
}

// RequestHead is the request of RFC 6455 4.1: GET of u's path and query,
// Host (with the port when it is not the default), Upgrade, Connection,
// the key, version 13, the subprotocols offered and o's fields.
RequestHead(a: &Arena, u: &url.Url, host: str, key: []u8, o: &Options): OutOfMemory![]u8 from a {
    var t := Text.New(a)
    try t.Push("GET ")
    path := u.Path()
    try t.Push(if path.Len() == 0 { "/" } else { path })
    q := u.Query()
    if q.Len() > 0 {
        try t.Push("?")
        try t.Push(q)
    }
    try t.Push(" HTTP/1.1\r\nHost: ")
    try t.Push(host)
    if p := u.Port() {
        try t.Push(try a.Fmt(":{p}", (p)))
    }
    try t.Push("\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: ")
    // The key is base64, so it is ASCII.
    try t.Push(str.From(key) else { "" })
    try t.Push("\r\nSec-WebSocket-Version: 13\r\n")
    if o.protocols.Len() > 0 {
        try t.Push("Sec-WebSocket-Protocol: ")
        for i in 0..o.protocols.Len() {
            if i > 0 {
                try t.Push(", ")
            }
            try t.Push(o.protocols[i])
        }
        try t.Push("\r\n")
    }
    for f in o.headers {
        try t.Push(f.name)
        try t.Push(": ")
        try t.Push(f.value)
        try t.Push("\r\n")
    }
    try t.Push("\r\n")
    return t.Done().AsBytes()
}

// Connect resolves host and dials its addresses in order at port: the
// first that answers, else the last dial's error (Refused when host
// resolved to none).
Connect(a: &Arena, io: Io, host: str, port: int): Error!net.Conn from io {
    addrs := try net.Resolve(a, io, host)
    var last: Error = net.Refused
    for x in addrs.Items() {
        var ad := x
        ad.port = Trunc[u16](port)
        st := net.Dial(io, try a.Fmt("{ad}", (ad))) else e {
            match e {
                Canceled {
                    fail e
                }
                _ {
                    last = e
                    continue
                }
            }
        }
        return st.Conn()
    }
    fail last
}

// Opening sends the request head and reads the reply into the read-ahead, whose
// bytes past the head stay there as the first frames' (conn.mk's rule
// Repeatable Recv): the index in offered of the subprotocol the server chose,
// -1 for none.
Opening(hv: &State, conn: &net.Conn, io: Io, head: []u8, want: []u8, offered: []str): Error!int {
    try conn.WriteAll(io, head)
    var g := try hv.rd.Lock(io)
    chosen := ReadReply(g.Mut(), conn, io, want, offered) else e {
        g.Unlock()
        fail e
    }
    g.Unlock()
    return chosen
}

// ReadReply reads until the read-ahead holds the reply's head (TooLarge
// past HeadMax, Handshake(0) when the server closes first), checks it,
// and drops it from the read-ahead: ReplyHead's chosen index. Each byte is
// scanned once.
ReadReply(r: mut ReadHalf, conn: &net.Conn, io: Io, want: []u8, offered: []str): Error!int {
    var scanned := 0
    for {
        if end := HeadEnd(r.buf[0..r.have], scanned) {
            chosen := try ReplyHead(r.buf[0..end], want, offered)
            r.Consume(end)
            return chosen
        }
        scanned = r.have
        if r.have == ReadAhead {
            fail TooLarge
        }
        k := try conn.Read(io, mut r.buf[r.have..])
        if k == 0 {
            fail Handshake(status: 0)
        }
        r.have += k
    }
}
