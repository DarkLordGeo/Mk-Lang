// Why: fetch's WebSocket mode needs a server to talk to in its cases.
// What: Upgrade, EchoFrames, Frame and ReadFull.
// How: a small RFC 6455 echo server: the handshake's accept key from the
// GUID and SHA-1, then each client frame unmasked and echoed back.

import std.encoding
import std.hash
import std.http

// RFC 6455 section 1.3: the GUID appended to the key before hashing
const WebSocketGuid = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// the largest frame payload the origin reads
const FrameLimit: i64 = 1048576

// the size of the message the `big` command answers with
const BigMessage = 70000

error BadFrame

// Upgrade answers a WebSocket handshake (RFC 6455 section 4.2) and then
// serves the connection as an echo endpoint until either side closes it.
// It is false when the connection must close, which is always, since the
// connection is no longer HTTP.
Upgrade(a: &Arena, req: &http.Request, r: mut Reader, w: mut Writer): bool {
    key := req.Header("Sec-WebSocket-Key") else {
        Respond(w, 400, "", "not a WebSocket handshake\n".AsBytes(), 27, false) else {}
        return false
    }
    digest := hash.Sha1((a.Fmt("{key}{WebSocketGuid}", (key, WebSocketGuid)) else { return false }).AsBytes())
    accept := encoding.Base64Encode(a, digest[..], encoding.Base64) else {
        return false
    }
    Print(w, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: {accept}\r\n", (accept)) else {
        return false
    }
    // the first subprotocol the client offers is the one chosen (RFC 6455 section 4.2.2)
    if offered := req.Header("Sec-WebSocket-Protocol") {
        first := offered.Find(",") else { offered.Len() }
        chosen := offered[..first].Trim()
        Print(w, "Sec-WebSocket-Protocol: {chosen}\r\n", (chosen)) else {
            return false
        }
    }
    w.Str("\r\n") else {
        return false
    }
    w.Flush() else {
        return false
    }
    EchoFrames(a, r, w) else {}
    return false
}

// EchoFrames reads client frames and answers each: a text message with "echo: "
// before it, a binary one as it came, a Ping with a Pong, a Close with a
// Close. The text commands `big`, `bye` and `drop` answer with a 70,000-byte
// message, a Close of the origin's own, and a dropped connection.
EchoFrames(a: &Arena, r: mut Reader, w: mut Writer): BadFrame|ReadError|WriteError|OutOfMemory!void {
    for {
        var head: [2]u8 = [0; 2]
        try ReadFull(r, mut head[..])
        opcode := head[0] & 0x0F
        masked := head[1] & 0x80 != 0
        var len := i64(head[1] & 0x7F)
        if len == 126 {
            var ext: [2]u8 = [0; 2]
            try ReadFull(r, mut ext[..])
            len = i64(ext[0]) << 8 | i64(ext[1])
        } else if len == 127 {
            var ext: [8]u8 = [0; 8]
            try ReadFull(r, mut ext[..])
            len = 0
            for b in ext {
                len = len << 8 | i64(b)
            }
        }
        // a client masks every frame (RFC 6455 section 5.3); the origin reads no more than its limit
        if !masked || len > FrameLimit {
            fail BadFrame
        }
        var key: [4]u8 = [0; 4]
        try ReadFull(r, mut key[..])
        payload := try a.Slice[u8](int(len))
        try ReadFull(r, payload)
        for i in 0..payload.Len() {
            payload[i] = payload[i] ^ key[i & 3]
        }
        match opcode {
            0x1 {
                text := str.From(payload[..]) else { fail BadFrame }
                if text == "drop" {
                    return
                }
                if text == "bye" {
                    // status 1000, then the reason (RFC 6455 section 5.5.1)
                    try Frame(w, 0x8, b"\x03\xe8bye")
                    return
                }
                if text == "big" {
                    big := try a.Slice[u8](BigMessage)
                    big.Fill(b'x')
                    try Frame(w, 0x1, big[..])
                    continue
                }
                try Frame(w, 0x1, (try a.Fmt("echo: {text}", (text))).AsBytes())
            }
            0x2 { try Frame(w, 0x2, payload[..]) }
            0x8 {
                try Frame(w, 0x8, payload[..])
                return
            }
            0x9 { try Frame(w, 0xA, payload[..]) }
            0xA {}
            _ { fail BadFrame }
        }
    }
}

// Frame writes one unmasked, final frame (RFC 6455 section 5.2) and flushes it.
Frame(w: mut Writer, opcode: u8, payload: []u8): WriteError!void {
    n := payload.Len()
    if n < 126 {
        var head: [2]u8 = [0x80 | opcode, u8(0)]
        head[1] = Cast[u8](n)
        try w.Bytes(head[..])
    } else if n < 65536 {
        var head: [4]u8 = [0x80 | opcode, 126, 0, 0]
        head[2] = Cast[u8](n >> 8)
        head[3] = Cast[u8](n & 0xFF)
        try w.Bytes(head[..])
    } else {
        var head: [10]u8 = [0; 10]
        head[0] = 0x80 | opcode
        head[1] = 127
        for i in 0..8 {
            head[9 - i] = Cast[u8]((n >> (8 * i)) & 0xFF)
        }
        try w.Bytes(head[..])
    }
    try w.Bytes(payload)
    try w.Flush()
}

// ReadFull fills buf from r, or fails at the end of input.
ReadFull(r: mut Reader, buf: mut []u8): BadFrame|ReadError!void {
    var got := 0
    for got < buf.Len() {
        n := try r.Read(mut buf[got..])
        if n == 0 {
            fail BadFrame
        }
        got += n
    }
}
