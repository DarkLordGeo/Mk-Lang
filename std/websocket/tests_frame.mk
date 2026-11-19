// Why: the frame codec and the handshake checks are pure functions that can
// be pinned against RFC 6455's own examples.
// What: tests of headers read and written, masking in pieces, close bodies,
// the accept value, a reply head and a client's request.
// How: examples from RFC 6455 sections 1.3, 4.2.2 and 5.7, and masking
// checked against a scalar model; run by mk test --std=std.websocket.

import std.http

// Bytes is s's bytes.
Bytes(s: str): []u8 from s {
    return s.AsBytes()
}

// HeadOf parses b as a client (masked false) or a server (true) with limit,
// and panics unless it is one whole header.
HeadOf(b: []u8, masked: bool, limit: int): Head {
    r := ParseHead(b, masked, limit) else {
        Panic("ParseHead failed")
    }
    h := r else {
        Panic("ParseHead wanted more bytes")
    }
    return h
}

// Refused reports ParseHead's error code for b: the Protocol code, 1 for
// TooLarge, 0 when it did not fail.
Refused(b: []u8, masked: bool, limit: int): int {
    _ = ParseHead(b, masked, limit) else e {
        return match e {
            Protocol(code) { int(code) }
            TooLarge { 1 }
        }
    }
    return 0
}

test "RFC 6455 5.7: a single-frame unmasked text message reads as Hello" {
    b: [7]u8 = [0x81, 0x05, 0x48, 0x65, 0x6C, 0x6C, 0x6F]
    h := HeadOf(b[..], false, 1 << 20)
    Assert(h.fin && h.op == OpText && !h.masked && h.len == 5 && h.size == 2)
    Assert(SameBytes(b[2..], Bytes("Hello")))
}

test "RFC 6455 5.7: a single-frame masked text message unmasks to Hello" {
    var b: [11]u8 = [0x81, 0x85, 0x37, 0xFA, 0x21, 0x3D, 0x7F, 0x9F, 0x4D, 0x51, 0x58]
    h := HeadOf(b[..], true, 1 << 20)
    Assert(h.fin && h.op == OpText && h.masked && h.len == 5 && h.size == 6)
    Assert(h.key[0] == 0x37 && h.key[3] == 0x3D)
    p := Mask(mut b[6..], h.key, 0)
    Assert(p == 1)
    Assert(SameBytes(b[6..], Bytes("Hello")))
}

test "RFC 6455 5.7: a fragmented unmasked text message is two frames, the first not final" {
    a: [5]u8 = [0x01, 0x03, 0x48, 0x65, 0x6C]
    b: [4]u8 = [0x80, 0x02, 0x6C, 0x6F]
    h1 := HeadOf(a[..], false, 100)
    h2 := HeadOf(b[..], false, 100)
    Assert(!h1.fin && h1.op == OpText && h1.len == 3)
    Assert(h2.fin && h2.op == OpContinue && h2.len == 2)
}

test "RFC 6455 5.7: an unmasked Ping and a masked Pong of Hello" {
    ping: [7]u8 = [0x89, 0x05, 0x48, 0x65, 0x6C, 0x6C, 0x6F]
    pong: [11]u8 = [0x8A, 0x85, 0x37, 0xFA, 0x21, 0x3D, 0x7F, 0x9F, 0x4D, 0x51, 0x58]
    Assert(HeadOf(ping[..], false, 10).op == OpPing)
    Assert(HeadOf(pong[..], true, 10).op == OpPong)
}

test "RFC 6455 5.7: 256 bytes take a 16-bit length and 64 KiB a 64-bit one" {
    b16: [4]u8 = [0x82, 0x7E, 0x01, 0x00]
    b64: [10]u8 = [0x82, 0x7F, 0, 0, 0, 0, 0, 1, 0, 0]
    Assert(HeadOf(b16[..], false, 1 << 20).len == 256)
    h := HeadOf(b64[..], false, 1 << 20)
    Assert(h.len == 65536 && h.size == 10)
    var out: [14]u8 = [0; 14]
    Assert(PutHead(mut out[..], OpBinary, 256, false, [0; 4]) == 4)
    Assert(SameBytes(out[0..4], b16[..]))
    Assert(PutHead(mut out[..], OpBinary, 65536, false, [0; 4]) == 10)
    Assert(SameBytes(out[0..10], b64[..]))
    Assert(PutHead(mut out[..], OpText, 5, true, [0x37, 0xFA, 0x21, 0x3D]) == 6)
    Assert(out[0] == 0x81 && out[1] == 0x85 && out[2] == 0x37 && out[5] == 0x3D)
}

test "ParseHead wants the whole header before it decides" {
    b: [10]u8 = [0x82, 0x7F, 0, 0, 0, 0, 0, 1, 0, 0]
    for n in 0..10 {
        r := ParseHead(b[0..n], false, 1 << 20) else {
            Panic("a prefix failed")
        }
        Assert(r.IsNone())
    }
    m: [6]u8 = [0x81, 0x85, 1, 2, 3, 4]
    for n in 0..6 {
        r := ParseHead(m[0..n], true, 10) else {
            Panic("a prefix failed")
        }
        Assert(r.IsNone())
    }
}

test "ParseHead fails reserved bits, reserved opcodes, bad control frames, a wrong mask bit and a 64-bit length's top bit" {
    rsv1: [2]u8 = [0xC1, 0x00]
    rsv3: [2]u8 = [0x91, 0x00]
    op3: [2]u8 = [0x83, 0x00]
    op11: [2]u8 = [0x8B, 0x00]
    frag_ping: [2]u8 = [0x09, 0x00]
    big_ping: [4]u8 = [0x89, 0x7E, 0x00, 0x7E]
    unmasked: [2]u8 = [0x81, 0x00]
    masked: [6]u8 = [0x81, 0x80, 1, 2, 3, 4]
    top: [10]u8 = [0x82, 0x7F, 0x80, 0, 0, 0, 0, 0, 0, 1]
    Assert(Refused(rsv1[..], false, 100) == 1002)
    Assert(Refused(rsv3[..], false, 100) == 1002)
    Assert(Refused(op3[..], false, 100) == 1002)
    Assert(Refused(op11[..], false, 100) == 1002)
    Assert(Refused(frag_ping[..], false, 100) == 1002)
    Assert(Refused(big_ping[..], false, 1000) == 1002)
    Assert(Refused(unmasked[..], true, 100) == 1002)
    Assert(Refused(masked[..], false, 100) == 1002)
    Assert(Refused(top[..], false, 1 << 20) == 1002)
    Assert(Refused(unmasked[..], false, 100) == 0)
}

test "ParseHead fails a payload over the frame limit before reading it, a 64-bit one included" {
    b16: [4]u8 = [0x82, 0x7E, 0x01, 0x00]
    huge: [10]u8 = [0x82, 0x7F, 0x7F, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF]
    Assert(Refused(b16[..], false, 255) == 1)
    Assert(Refused(b16[..], false, 256) == 0)
    Assert(Refused(huge[..], false, 1 << 24) == 1)
    Assert(Refused(huge[..], false, -1) == 1)
}

// MaskModel is RFC 6455 5.3's definition, one byte at a time.
MaskModel(b: mut []u8, key: [4]u8, phase: int) {
    for i in 0..b.Len() {
        b[i] = b[i] ^ key[(phase + i) % 4]
    }
}

test "Mask in pieces of every length and phase equals the byte-at-a-time model" {
    key: [4]u8 = [0x9A, 0x01, 0xFE, 0x37]
    var x: [80]u8 = [0; 80]
    var y: [80]u8 = [0; 80]
    for n in 0..80 {
        for phase in 0..4 {
            for i in 0..n {
                x[i] = Trunc[u8](i * 7 + n)
                y[i] = x[i]
            }
            MaskModel(mut y[0..n], key, phase)
            // In two pieces, split at every third length.
            cut := n / 3
            p := Mask(mut x[0..cut], key, phase)
            q := Mask(mut x[cut..n], key, p)
            Assert(q == (phase + n) % 4)
            Assert(SameBytes(x[0..n], y[0..n]))
        }
    }
}

test "CloseBody: empty is 1005, one byte and unsendable statuses are 1002, a reason that is not UTF-8 is 1007" {
    Assert(CloseBody(Bytes("")).Must() == 1005)
    ok: [4]u8 = [0x03, 0xE8, 0x68, 0x69]
    Assert(CloseBody(ok[..]).Must() == 1000)
    one: [1]u8 = [0x03]
    bad_utf8: [4]u8 = [0x03, 0xE8, 0xC0, 0x80]
    for code in [999, 1004, 1005, 1006, 1015, 1016, 2999, 5000] {
        var b: [2]u8 = [Trunc[u8](code >> 8), Trunc[u8](code)]
        _ = CloseBody(b[..]) else e {
            match e {
                Protocol(code: c) { Assert(c == 1002) }
            }
            continue
        }
        Panic("an unsendable status was accepted")
    }
    for code in [1000, 1001, 1002, 1003, 1007, 1011, 1012, 1014, 3000, 4999] {
        var b: [2]u8 = [Trunc[u8](code >> 8), Trunc[u8](code)]
        Assert(CloseBody(b[..]).Must() == Trunc[u16](code))
    }
    _ = CloseBody(one[..]) else e {
        match e {
            Protocol(code: c) { Assert(c == 1002) }
        }
        _ = CloseBody(bad_utf8[..]) else e2 {
            match e2 {
                Protocol(code: c) { Assert(c == 1007) }
            }
            return
        }
    }
    Panic("a bad close body was accepted")
}

test "RFC 6455 1.3 and 4.2.2: the accept value of the sample nonce" {
    var out: [28]u8 = [0; 28]
    AcceptOf(Bytes("dGhlIHNhbXBsZSBub25jZQ=="), mut out)
    Assert(SameBytes(out[..], Bytes("s3pPLMBiTxaQ9kYGzzhZRbK+xOo=")))
}

test "KeyOk takes 16 bytes of base64 and nothing else" {
    Assert(KeyOk("dGhlIHNhbXBsZSBub25jZQ=="))
    Assert(!KeyOk("dGhlIHNhbXBsZSBub25jZQ="))
    Assert(!KeyOk("dGhlIHNhbXBsZSBub25jZQ=x"))
    Assert(!KeyOk("dGhlIHNhbXBsZSBub25jZSE="))
    Assert(!KeyOk(""))
    Assert(!KeyOk("dGhlIHNhbXBsZSBub25jZQ==dGhl"))
}

test "HasToken finds a token in a comma list without regard to case" {
    Assert(HasToken(Bytes("keep-alive, Upgrade"), Bytes("upgrade")))
    Assert(HasToken(Bytes("UPGRADE"), Bytes("upgrade")))
    Assert(!HasToken(Bytes("upgrades"), Bytes("upgrade")))
    Assert(!HasToken(Bytes(""), Bytes("upgrade")))
    Assert(HasToken(Bytes(" ,\tupgrade\t, x"), Bytes("upgrade")))
}

// ReplyStatus is ReplyHead's verdict on h with the sample's accept value
// and offered: -1 when it accepts, else the Handshake status.
ReplyStatus(h: str, offered: []str): int {
    _ = ReplyHead(h.AsBytes(), Bytes("s3pPLMBiTxaQ9kYGzzhZRbK+xOo="), offered) else e {
        return match e {
            Handshake(status) { status }
        }
    }
    return -1
}

// ReplyChosen is the index ReplyHead gives for the subprotocol h names
// among offered, -2 when it refuses h.
ReplyChosen(h: str, offered: []str): int {
    return ReplyHead(h.AsBytes(), Bytes("s3pPLMBiTxaQ9kYGzzhZRbK+xOo="), offered) else {
        -2
    }
}

test "ReplyHead accepts RFC 6455 1.3's reply and refuses each way a reply breaks 4.1" {
    good := "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n"
    empty: [0]str = []
    none_ := empty[..]
    chat: [2]str = ["chat", "superchat"]
    Assert(ReplyStatus(good, none_) == -1)
    Assert(ReplyStatus("HTTP/1.1 101 OK\r\nupgrade: WebSocket\r\nconnection: keep-alive, upgrade\r\nsec-websocket-accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n", none_) == -1)
    Assert(ReplyStatus("HTTP/1.1 101 OK\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\nSec-WebSocket-Protocol: chat\r\n\r\n", chat[..]) == -1)
    // The index of the subprotocol chosen, -1 for none; a second one, or
    // one not offered, is refused.
    Assert(ReplyChosen(good, chat[..]) == -1)
    Assert(ReplyChosen("HTTP/1.1 101 OK\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\nSec-WebSocket-Protocol: superchat\r\n\r\n", chat[..]) == 1)
    Assert(ReplyChosen("HTTP/1.1 101 OK\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\nSec-WebSocket-Protocol: chat\r\nSec-WebSocket-Protocol: chat\r\n\r\n", chat[..]) == -2)
    Assert(ReplyChosen("HTTP/1.1 101 OK\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\nSec-WebSocket-Protocol: Chat\r\n\r\n", chat[..]) == -2)
    Assert(ReplyStatus("HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n", none_) == 403)
    Assert(ReplyStatus("HTTP/1.1 200 OK\r\n\r\n", none_) == 200)
    Assert(ReplyStatus("HTTP/1.1 101 OK\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n", none_) == 101)
    Assert(ReplyStatus("HTTP/1.1 101 OK\r\nUpgrade: websocket\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n", none_) == 101)
    Assert(ReplyStatus("HTTP/1.1 101 OK\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo\r\n\r\n", none_) == 101)
    Assert(ReplyStatus("HTTP/1.1 101 OK\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\nSec-WebSocket-Extensions: permessage-deflate\r\n\r\n", none_) == 101)
    Assert(ReplyStatus("HTTP/1.1 101 OK\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\nSec-WebSocket-Protocol: chat\r\n\r\n", none_) == 101)
    Assert(ReplyStatus("HTTP/1.1 101 OK\r\nUpgrade: websocket\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: s3pPLMBiTxaQ9kYGzzhZRbK+xOo=\r\n\r\n", none_) == 101)
    Assert(ReplyStatus("HTTP/1.1 101 OK\nUpgrade: websocket\r\n\r\n", none_) == 0)
    Assert(ReplyStatus("HTTP/1.1 101 OK\r\n Upgrade: websocket\r\n\r\n", none_) == 0)
    Assert(ReplyStatus("HTTP/1.1 101 OK\r\nUpgrade : websocket\r\n\r\n", none_) == 0)
    Assert(ReplyStatus("HTTP/1.0 101 OK\r\n\r\n", none_) == 0)
    Assert(ReplyStatus("HTTP/1.1 1x1 OK\r\n\r\n", none_) == 0)
    Assert(ReplyStatus("HTTP/1.1 101 OK\r\nX: a\x01b\r\n\r\n", none_) == 0)
}

test "HeadEnd finds the empty line, scanning each byte once across calls" {
    h := Bytes("HTTP/1.1 101 OK\r\nA: b\r\n\r\nrest")
    Assert(HeadEnd(h, 0).Must() == 25)
    Assert(HeadEnd(h[0..24], 0).IsNone())
    Assert(HeadEnd(h, 24).Must() == 25)
}

// Req is a request with fields hs.
Req(method: str, hs: []http.Header): http.Request from method, hs {
    return http.Request(method: method, path: "/chat", query: "", headers: hs, params: hs[0..0], body: "".AsBytes())
}

AllowAll(origin: str): bool {
    return true
}

AllowExample(origin: str): bool {
    return origin == "http://example.com"
}

// RefusedWith is CheckRequest's status for r, 0 when it accepts.
RefusedWith(r: &http.Request, const allow: fn(origin: str): bool): int {
    _ = CheckRequest(r, allow) else e {
        return match e {
            Refusal(status) { status }
        }
    }
    return 0
}

test "CheckRequest accepts RFC 6455 1.3's request and answers 400, 403 or 426 to each way a request breaks 4.2.1" {
    key := http.Header(name: "Sec-WebSocket-Key", value: "dGhlIHNhbXBsZSBub25jZQ==")
    up := http.Header(name: "Upgrade", value: "websocket")
    co := http.Header(name: "Connection", value: "keep-alive, Upgrade")
    v13 := http.Header(name: "Sec-WebSocket-Version", value: "13")
    v8 := http.Header(name: "Sec-WebSocket-Version", value: "8")
    origin := http.Header(name: "Origin", value: "http://example.com")
    evil := http.Header(name: "Origin", value: "http://evil.example")
    badkey := http.Header(name: "Sec-WebSocket-Key", value: "c2hvcnQ=")
    good: [5]http.Header = [key, up, co, v13, origin]
    r := Req("GET", good[..])
    Assert(RefusedWith(&r, AllowAll) == 0)
    Assert(CheckRequest(&r, AllowExample).Must() == "dGhlIHNhbXBsZSBub25jZQ==")
    bad_origin: [5]http.Header = [key, up, co, v13, evil]
    r2 := Req("GET", bad_origin[..])
    Assert(RefusedWith(&r2, AllowExample) == 403)
    r3 := Req("POST", good[..])
    Assert(RefusedWith(&r3, AllowAll) == 400)
    no_up: [3]http.Header = [key, co, v13]
    r4 := Req("GET", no_up[..])
    Assert(RefusedWith(&r4, AllowAll) == 400)
    no_co: [3]http.Header = [key, up, v13]
    r5 := Req("GET", no_co[..])
    Assert(RefusedWith(&r5, AllowAll) == 400)
    short: [4]http.Header = [badkey, up, co, v13]
    r6 := Req("GET", short[..])
    Assert(RefusedWith(&r6, AllowAll) == 400)
    two: [5]http.Header = [key, key, up, co, v13]
    r7 := Req("GET", two[..])
    Assert(RefusedWith(&r7, AllowAll) == 400)
    old: [4]http.Header = [key, up, co, v8]
    r8 := Req("GET", old[..])
    Assert(RefusedWith(&r8, AllowAll) == 426)
    none_v: [3]http.Header = [key, up, co]
    r9 := Req("GET", none_v[..])
    Assert(RefusedWith(&r9, AllowAll) == 426)
}

// Chosen is Choose's index for a request whose only fields are a
// Sec-WebSocket-Protocol of v1 and, when v2 is not empty, a second of v2.
Chosen(v1: str, v2: str, protocols: []str): int {
    f1 := http.Header(name: "Sec-WebSocket-Protocol", value: v1)
    f2 := http.Header(name: "sec-websocket-protocol", value: v2)
    fs: [3]http.Header = [http.Header(name: "Upgrade", value: "websocket"), f1, f2]
    r := Req("GET", if v2.Len() > 0 { fs[..] } else { fs[0..2] })
    return Choose(&r, protocols)
}

test "Choose takes the client's first offered subprotocol that the server lists, byte for byte, across fields, skipping what is not a token" {
    sc: [1]str = ["superchat"]
    both: [2]str = ["superchat", "chat"]
    none_: [0]str = []
    // The client's order, not the server's.
    Assert(Chosen("chat, superchat", "", both[..]) == 1)
    Assert(Chosen("superchat, chat", "", both[..]) == 0)
    Assert(Chosen("chat, superchat", "", sc[..]) == 0)
    // Several fields are one list, in order; OWS and empty elements.
    Assert(Chosen("chat", "superchat", sc[..]) == 0)
    Assert(Chosen(",, \t superchat \t,", "", sc[..]) == 0)
    Assert(Chosen("x", "chat, superchat", both[..]) == 1)
    // None: no overlap, no list, case differs, not a token.
    Assert(Chosen("chat", "", sc[..]) == -1)
    Assert(Chosen("chat, superchat", "", none_[..]) == -1)
    Assert(Chosen("SuperChat", "", sc[..]) == -1)
    Assert(Chosen("super chat, superchat;x", "", sc[..]) == -1)
    Assert(Chosen("", "", sc[..]) == -1)
    // A server entry that is not a token is never matched.
    odd: [2]str = ["a b", "chat"]
    Assert(Chosen("a b, chat", "", odd[..]) == 1)
}
test "A 101 naming a ProtocolMax-byte subprotocol fills the write buffer exactly; Choose takes such a name and skips a longer one" (sys: &System) {
    name := try sys.arena.Slice[u8](ProtocolMax + 1)
    name.Fill(0x61)
    var out: [MaxHead + WriteChunk]u8 = [0; MaxHead + WriteChunk]
    key := "dGhlIHNhbXBsZSBub25jZQ==".AsBytes()
    Assert(SwitchHead(key, name[0..ProtocolMax], mut out[..]) == MaxHead + WriteChunk)
    Assert(SwitchHead(key, "".AsBytes(), mut out[..]) == 129)
    exact := str.From(name[0..ProtocolMax]) else { "" }
    long := str.From(name[..]) else { "" }
    both: [2]str = [exact, long]
    Assert(Chosen(exact, "", both[..]) == 0)
    Assert(Chosen(long, "", both[..]) == -1)
    Assert(Chosen(long, "chat", both[..]) == -1)
}
// OptsOk reports whether CheckOptions accepts o.
OptsOk(o: &Options): bool {
    CheckOptions(o) else {
        return false
    }
    return true
}

test "Reserved names the fields the handshake writes; CheckOptions refuses them, bad tokens and repeated subprotocols" {
    Assert(Reserved(Bytes("Sec-WebSocket-Extensions")) && Reserved(Bytes("HOST")) && Reserved(Bytes("connection")))
    Assert(!Reserved(Bytes("Origin")) && !Reserved(Bytes("Sec-Fetch-Mode")))
    var o := DefaultOptions
    CheckOptions(&o).Must()
    ext: [1]http.Header = [http.Header(name: "Sec-WebSocket-Extensions", value: "permessage-deflate")]
    o.headers = ext[..]
    Assert(!OptsOk(&o))
    crlf: [1]http.Header = [http.Header(name: "X-Token", value: "a\r\nInjected: 1")]
    o.headers = crlf[..]
    Assert(!OptsOk(&o))
    fine: [1]http.Header = [http.Header(name: "Origin", value: "http://example.com")]
    o.headers = fine[..]
    CheckOptions(&o).Must()
    twice: [2]str = ["chat", "chat"]
    o.protocols = twice[..]
    Assert(!OptsOk(&o))
    spaced: [1]str = ["a b"]
    o.protocols = spaced[..]
    Assert(!OptsOk(&o))
}
