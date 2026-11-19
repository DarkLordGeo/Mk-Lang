// Why: the server side of the handshake must answer real clients and refuse
// bad requests.
// What: tests of Accept on RFC 6455 section 1.3's request, its subprotocol
// choice, and Dial against Accept.
// How: runs under std.sim with tests_conn.mk's loopback helpers; run by mk
// test --std=std.websocket and std/std-ws_blocks_test.go.

import std.http
import std.net
import std.sim
import std.url

// ProtoServer accepts one connection on l, reads the request head and
// answers with a 101 naming proto (no Sec-WebSocket-Protocol when proto
// is empty), then waits for the client's close.
ProtoServer(l: &net.Listener, io: Io, proto: str) {
    st := l.Accept(io) else {
        return
    }
    c := st.Conn()
    var buf: [2048]u8 = [0; 2048]
    var n := 0
    for HeadEnd(buf[0..n], 0).IsNone() {
        k := c.Read(io, mut buf[n..]) else {
            c.Abandon()
            return
        }
        if k == 0 {
            c.Abandon()
            return
        }
        n += k
    }
    var key: [24]u8 = [0; 24]
    tag := "Sec-WebSocket-Key: ".AsBytes()
    for i in 0..n - tag.Len() {
        if SameBytes(buf[i..i + tag.Len()], tag) {
            (mut key[..]).CopyFrom(buf[i + tag.Len()..i + tag.Len() + 24])
        }
    }
    var acc: [28]u8 = [0; 28]
    AcceptOf(key[..], mut acc)
    c.WriteAll(io, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ".AsBytes()) else {}
    c.WriteAll(io, acc[..]) else {}
    if proto.Len() > 0 {
        c.WriteAll(io, "\r\nSec-WebSocket-Protocol: ".AsBytes()) else {}
        c.WriteAll(io, proto.AsBytes()) else {}
    }
    c.WriteAll(io, "\r\n\r\n".AsBytes()) else {}
    var tail: [64]u8 = [0; 64]
    _ = c.Read(io, mut tail[..]) else { 0 }
    c.Abandon()
}

// DialProto dials u offering o's subprotocols against a ProtoServer that
// names proto: whether the Conn's Protocol is want (none when want is
// empty).
DialProto(sys: &System, io: Io, o: &Options, proto: str, want: str): Error|url.Error!bool {
    u := try url.Parse(sys.arena, "ws://127.0.0.1:7600/chat", none)
    l := try net.Listen(io, Addr)
    defer l.Close()
    var ok := false
    scope s := io.Scope() {
        s.go ProtoServer(&l, io, proto)
        c := try Dial(sys.heap, sys.arena, io, &u, o)
        ok = if p := c.Protocol() { p == want } else { want.Len() == 0 }
        c.Abandon()
    }
    return ok
}

test "Conn.Protocol is the subprotocol the server chose from those Dial offered, none when it chose none" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 23)
    defer sm.Free()
    io := sm.Io()
    var o := DefaultOptions
    offered: [2]str = ["chat", "superchat"]
    o.protocols = offered[..]
    Assert(try DialProto(sys, io, &o, "superchat", "superchat"))
    Assert(try DialProto(sys, io, &o, "chat", "chat"))
    Assert(try DialProto(sys, io, &o, "", ""))
    plain := DefaultOptions
    Assert(try DialProto(sys, io, &plain, "", ""))
}

// Offer is RFC 6455 1.3's request fields with version 13 and two
// Sec-WebSocket-Protocol fields p1 and p2; an empty one is sent as an
// X-Pad field instead, so it offers nothing.
Offer(a: &Arena, p1: str into a, p2: str into a): OutOfMemory![]http.Header from a {
    n1 := if p1.Len() > 0 { "Sec-WebSocket-Protocol" } else { "X-Pad" }
    n2 := if p2.Len() > 0 { "Sec-WebSocket-Protocol" } else { "X-Pad" }
    hs := try a.Put([http.Header(name: "Host", value: "server.example.com"), http.Header(name: "Upgrade", value: "websocket"),
        http.Header(name: "Connection", value: "Upgrade"), http.Header(name: "Sec-WebSocket-Key", value: "dGhlIHNhbXBsZSBub25jZQ=="),
        http.Header(name: "Sec-WebSocket-Version", value: "13"), http.Header(name: n1, value: p1), http.Header(name: n2, value: p2)])
    return hs[..]
}

// AcceptProto accepts req over a fresh loopback connection, supporting
// protocols: the 101 the client read, and whether the server Conn's
// Protocol is want (none when want is empty).
AcceptProto(sys: &System, req: &http.Request, protocols: []str, want: str): Error!(head: str, same: bool) from sys {
    var sm := try sim.Sim.New(sys.arena, 24)
    defer sm.Free()
    io := sm.Io()
    (c, s) := try Loop(io)
    onerr c.Abandon()
    t := http.Taken(conn: s, rest: "".AsBytes())
    sv := Accept(sys.heap, io, req, t, protocols, AllowAll) else e {
        t.Abandon()
        fail e
    }
    onerr sv.Abandon()
    same := if p := sv.Protocol() { p == want } else { want.Len() == 0 }
    var buf: [512]u8 = [0; 512]
    var n := 0
    for HeadEnd(buf[0..n], 0).IsNone() {
        k := try c.Read(io, mut buf[n..])
        if k == 0 {
            break
        }
        n += k
    }
    sv.Abandon()
    c.Abandon()
    return (head: try sys.arena.Dup(str.From(buf[0..n]) else { "?" }), same: same)
}

// OfferReq is the request of Offer(p1, p2).
OfferReq(a: &Arena, p1: str into a, p2: str into a): OutOfMemory!http.Request from a {
    hs := try Offer(a, p1, p2)
    return http.Request(method: "GET", path: "/chat", query: "", headers: hs, params: hs[0..0], body: "".AsBytes())
}

test "Accept answers with the first subprotocol the client offered that it supports, none when they share none or it supports none, and Conn.Protocol names it" (sys: &System) {
    plain := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n"
    superchat := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\nSec-WebSocket-Protocol: superchat\r\n\r\n"
    chat := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\nSec-WebSocket-Protocol: chat\r\n\r\n"
    two := try OfferReq(sys.arena, "chat, superchat", "")
    second: [1]str = ["superchat"]
    // The client offers two; the server supports the second.
    (head: h1, same: s1) := try AcceptProto(sys, &two, second[..], "superchat")
    Assert(s1)
    Assert(h1 == superchat)
    // The client's order wins over the server's.
    both: [2]str = ["superchat", "chat"]
    (head: h2, same: s2) := try AcceptProto(sys, &two, both[..], "chat")
    Assert(s2)
    Assert(h2 == chat)
    // Two fields are one list (RFC 6455 11.3.4).
    split := try OfferReq(sys.arena, "chat", " superchat ")
    (head: h3, same: s3) := try AcceptProto(sys, &split, second[..], "superchat")
    Assert(s3)
    Assert(h3 == superchat)
    // No overlap, an empty list and no offer: none, and no field.
    mqtt: [1]str = ["mqtt"]
    (head: h4, same: s4) := try AcceptProto(sys, &two, mqtt[..], "")
    Assert(s4)
    Assert(h4 == plain)
    nothing: [0]str = []
    (head: h5, same: s5) := try AcceptProto(sys, &two, nothing[..], "")
    Assert(s5)
    Assert(h5 == plain)
    bare := try OfferReq(sys.arena, "", "")
    (head: h6, same: s6) := try AcceptProto(sys, &bare, second[..], "")
    Assert(s6)
    Assert(h6 == plain)
    // Byte-exact: "Chat" is not "chat".
    upper := try OfferReq(sys.arena, "Chat", "")
    chatonly: [1]str = ["chat"]
    (head: h7, same: s7) := try AcceptProto(sys, &upper, chatonly[..], "")
    Assert(s7)
    Assert(h7 == plain)
}

// AcceptPeer accepts one connection on l, reads the request head into an
// http.Request, accepts it supporting protocols, asserts that the Conn's
// Protocol is want (none when empty), and waits for the client's Close.
AcceptPeer(l: &net.Listener, h: &Heap, io: Io, protocols: []str, want: str) {
    st := l.Accept(io) else {
        return
    }
    c := st.Conn()
    var buf: [2048]u8 = [0; 2048]
    var n := 0
    for HeadEnd(buf[0..n], 0).IsNone() {
        k := c.Read(io, mut buf[n..]) else {
            c.Abandon()
            return
        }
        if k == 0 {
            c.Abandon()
            return
        }
        n += k
    }
    var fs: [16]http.Header = [http.Header(name: "", value: ""); 16]
    var nf := 0
    hd := buf[0..n]
    var at := LineEnd(hd, 0) else {
        c.Abandon()
        return
    }
    for nf < 16 {
        end := LineEnd(hd, at) else {
            break
        }
        if end == at + 2 {
            break
        }
        line := hd[at..end - 2]
        at = end
        colon := Colon(line) else {
            break
        }
        fs[nf] = http.Header(name: str.From(line[0..colon]) else { "" }, value: str.From(TrimOws(line[colon + 1..])) else { "" })
        nf += 1
    }
    req := http.Request(method: "GET", path: "/chat", query: "", headers: fs[0..nf], params: fs[0..0], body: "".AsBytes())
    t := http.Taken(conn: c, rest: "".AsBytes())
    ws := Accept(h, io, &req, t, protocols, AllowAll) else {
        t.Abandon()
        Assert(false)
        return
    }
    Assert(if p := ws.Protocol() { p == want } else { want.Len() == 0 })
    var a := Arena.New(h) else {
        ws.Abandon()
        return
    }
    Assert(RecvCode(&ws, &a, io) == 1000)
    a.Free()
    ws.Abandon()
}

// DialAccept dials, offering offered, a Mk server that accepts supporting
// protocols: whether the client Conn's Protocol is want (the server
// asserts the same of its own).
DialAccept(sys: &System, io: Io, offered: []str, protocols: []str, want: str): Error|url.Error!bool {
    u := try url.Parse(sys.arena, "ws://127.0.0.1:7600/chat", none)
    l := try net.Listen(io, Addr)
    defer l.Close()
    var o := DefaultOptions
    o.protocols = offered
    var ok := false
    scope s := io.Scope() {
        s.go AcceptPeer(&l, sys.heap, io, protocols, want)
        c := try Dial(sys.heap, sys.arena, io, &u, &o)
        ok = if p := c.Protocol() { p == want } else { want.Len() == 0 }
        c.Close(io, 1000, "") else {
            c.Abandon()
            ok = false
        }
    }
    return ok
}

test "Dial against Accept: both Conns name the subprotocol Accept chose from Dial's offer, or none" (sys: &System) {
    var sm := try sim.Sim.New(sys.arena, 25)
    defer sm.Free()
    io := sm.Io()
    offered: [2]str = ["chat", "superchat"]
    second: [1]str = ["superchat"]
    Assert(try DialAccept(sys, io, offered[..], second[..], "superchat"))
    both: [2]str = ["superchat", "chat"]
    Assert(try DialAccept(sys, io, offered[..], both[..], "chat"))
    mqtt: [1]str = ["mqtt"]
    Assert(try DialAccept(sys, io, offered[..], mqtt[..], ""))
    nothing: [0]str = []
    Assert(try DialAccept(sys, io, nothing[..], second[..], ""))
}
// DialFails dials u, which must fail before any I/O: 1 for
// NoSecureTransport, 2 for InvalidAddress.
DialFails(sys: &System, io: Io, u: &url.Url, o: &Options): int {
    c := Dial(sys.heap, sys.arena, io, u, o) else e {
        return match e {
            http.NoSecureTransport { 1 }
            net.InvalidAddress { 2 }
            _ { 0 }
        }
    }
    c.Abandon()
    return -1
}

// Upgrade is RFC 6455 1.3's request fields, with version v.
Upgrade(a: &Arena, v: str into a): OutOfMemory![]http.Header from a {
    hs := try a.Put([http.Header(name: "Host", value: "server.example.com"), http.Header(name: "Upgrade", value: "websocket"),
        http.Header(name: "Connection", value: "Upgrade"), http.Header(name: "Sec-WebSocket-Key", value: "dGhlIHNhbXBsZSBub25jZQ=="),
        http.Header(name: "Sec-WebSocket-Version", value: v)])
    return hs[..]
}

// AcceptOver accepts req over a fresh loopback connection whose Taken
// holds rest, and returns the head the raw client read and, when Accept
// succeeded, the first message the server received ("" after a refusal).
AcceptOver(sys: &System, seed: u64, req: &http.Request, rest: []u8, frame: []u8): Error!(head: str, msg: str) from sys {
    var sm := try sim.Sim.New(sys.arena, seed)
    defer sm.Free()
    io := sm.Io()
    (c, s) := try Loop(io)
    onerr c.Abandon()
    t := http.Taken(conn: s, rest: rest)
    var buf: [256]u8 = [0; 256]
    sv := Accept(sys.heap, io, req, t, DefaultOptions.protocols, AllowAll) else e {
        t.Abandon()
        var n := 0
        for HeadEnd(buf[0..n], 0).IsNone() {
            k := try c.Read(io, mut buf[n..])
            if k == 0 {
                break
            }
            n += k
        }
        c.Abandon()
        _ = CodeOf(e)
        return (head: try sys.arena.Dup(str.From(buf[0..n]) else { "?" }), msg: "")
    }
    onerr sv.Abandon()
    var n := 0
    for HeadEnd(buf[0..n], 0).IsNone() {
        k := try c.Read(io, mut buf[n..])
        if k == 0 {
            break
        }
        n += k
    }
    try c.WriteAll(io, frame)
    m := try sv.Recv(sys.arena, io)
    sv.Abandon()
    c.Abandon()
    return (head: try sys.arena.Dup(str.From(buf[0..n]) else { "?" }), msg: try sys.arena.Dup(str.From(m.data) else { "?" }))
}

test "Accept answers RFC 6455 1.3's request with its 101 and accept value, keeps the bytes read past the request, and refuses version 8 with 426 naming 13" (sys: &System) {
    hs := try Upgrade(sys.arena, "13")
    req := http.Request(method: "GET", path: "/chat", query: "", headers: hs, params: hs[0..0], body: "".AsBytes())
    hello: [11]u8 = [0x81, 0x85, 0x37, 0xFA, 0x21, 0x3D, 0x7F, 0x9F, 0x4D, 0x51, 0x58]
    (head, msg) := try AcceptOver(sys, 30, &req, "".AsBytes(), hello[..])
    Assert(head == "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n")
    Assert(msg == "Hello")
    // The client's first frame read with the request, then a second.
    (head: _, msg: m2) := try AcceptOver(sys, 31, &req, hello[..], hello[0..0])
    Assert(m2 == "Hello")
    old := try Upgrade(sys.arena, "8")
    r8 := http.Request(method: "GET", path: "/chat", query: "", headers: old, params: old[0..0], body: "".AsBytes())
    (head: h8, msg: m8) := try AcceptOver(sys, 32, &r8, "".AsBytes(), hello[..])
    Assert(h8 == "HTTP/1.1 426 Upgrade Required\r\nSec-WebSocket-Version: 13\r\nConnection: close\r\nContent-Length: 0\r\n\r\n")
    Assert(m8 == "")
}
