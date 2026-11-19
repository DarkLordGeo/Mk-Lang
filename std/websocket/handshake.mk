// Why: the opening handshake (RFC 6455 section 4) is the first untrusted
// input of a connection on both sides; it gets a strict reader of its own.
//
// What: the key and its accept value (section 4.2.2: SHA-1 of the key and
// the GUID, in base64), the client's request head, ReplyHead for the
// server's reply, and the checks of a client's request on the server.
//
// How: ReplyHead reads a head of at most HeadMax bytes in one pass, with no
// allocation and no recursion, as strictly as std.http reads a head (RFC
// 9112): CR LF line ends only, no obsolete folding, token field names with
// no space before the colon, no control byte in a value but HTAB. Field
// names compare without regard to ASCII case (RFC 9110 section 5.1).

import std.encoding
import std.hash
import std.http

// Guid is the value RFC 6455 4.2.2 appends to a key before hashing.
const Guid: str = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// HeadMax is the longest handshake head read, a reply on the client and
// the bytes past a request on the server: the read-ahead's size.
const HeadMax: int = 16384

// AcceptOf writes into out the Sec-WebSocket-Accept value of key: base64
// of the SHA-1 of key and Guid (4.2.2, step 5.4). O(key.Len()); allocates
// nothing.
AcceptOf(key: []u8, out: mut [28]u8) {
    var s := hash.Sha1State.New()
    s.Bytes(key)
    s.Bytes(Guid.AsBytes())
    sum := s.Sum()
    _ = encoding.Base64EncodeTo(mut out[..], sum[..], encoding.Base64)
}

// KeyOk reports whether a Sec-WebSocket-Key value is 16 bytes in base64
// (4.2.1, item 5): 24 characters of the standard alphabet, padded.
KeyOk(key: str): bool {
    if key.Len() != 24 {
        return false
    }
    var raw: [18]u8 = [0; 18]
    n := encoding.Base64DecodeTo(mut raw[..], key, encoding.Base64) else {
        return false
    }
    return n == 16
}

// Lower is b as an ASCII lowercase byte.
Lower(b: u8): u8 {
    if b >= 0x41 && b <= 0x5A {
        return b + 32
    }
    return b
}

// SameFold reports whether a and b are equal without regard to ASCII case.
SameFold(a: []u8, b: []u8): bool {
    if a.Len() != b.Len() {
        return false
    }
    for i in 0..a.Len() {
        if Lower(a[i]) != Lower(b[i]) {
            return false
        }
    }
    return true
}

// IsOws: a space or a tab (RFC 9110 5.6.3).
IsOws(b: u8): bool {
    return b == 0x20 || b == 0x09
}

// TrimOws is s without leading and trailing spaces and tabs.
TrimOws(s: []u8): []u8 from s {
    var i := 0
    var j := s.Len()
    for i < j && IsOws(s[i]) {
        i += 1
    }
    for j > i && IsOws(s[j - 1]) {
        j -= 1
    }
    return s[i..j]
}

// HasToken reports whether the comma-separated list value holds token,
// compared without regard to ASCII case (RFC 9110 5.6.1): Connection's
// "upgrade" and Upgrade's "websocket" are found this way.
HasToken(value: []u8, token: []u8): bool {
    var at := 0
    for at <= value.Len() {
        var end := at
        for end < value.Len() && value[end] != 0x2C {
            end += 1
        }
        if SameFold(TrimOws(value[at..end]), token) {
            return true
        }
        at = end + 1
    }
    return false
}

// IsTchar: a byte of an RFC 9110 5.6.2 token.
IsTchar(b: u8): bool {
    if (b >= 0x30 && b <= 0x39) || (b >= 0x41 && b <= 0x5A) || (b >= 0x61 && b <= 0x7A) {
        return true
    }
    return b == 0x21 || b == 0x23 || b == 0x24 || b == 0x25 || b == 0x26 || b == 0x27 || b == 0x2A || b == 0x2B ||
        b == 0x2D || b == 0x2E || b == 0x5E || b == 0x5F || b == 0x60 || b == 0x7C || b == 0x7E
}

// IsToken: one or more token bytes.
IsToken(s: []u8): bool {
    if s.Len() == 0 {
        return false
    }
    for b in s {
        if !IsTchar(b) {
            return false
        }
    }
    return true
}

// ValueOk: a field value with no control byte but HTAB, and no DEL (RFC
// 9110 5.5), so nothing a caller gives Dial can end a line of the head.
ValueOk(s: []u8): bool {
    for b in s {
        if (b < 0x20 && b != 0x09) || b == 0x7F {
            return false
        }
    }
    return true
}

// Reserved reports whether a field name is one the handshake writes
// itself: a caller's copy would contradict or duplicate it.
Reserved(name: []u8): bool {
    if SameFold(name, "host".AsBytes()) || SameFold(name, "upgrade".AsBytes()) ||
        SameFold(name, "connection".AsBytes()) {
        return true
    }
    p := "sec-websocket-".AsBytes()
    return name.Len() >= p.Len() && SameFold(name[0..p.Len()], p)
}

// HeadEnd is the length of the head at the start of b, its final empty
// line included, or none when b holds no CR LF CR LF yet; from is how far
// an earlier call looked, so a head read in pieces is scanned once.
HeadEnd(b: []u8, from: int): ?int {
    var i := if from > 3 { from } else { 3 }
    for i < b.Len() {
        if b[i] == 0x0A && b[i - 1] == 0x0D && b[i - 2] == 0x0A && b[i - 3] == 0x0D {
            return some(i + 1)
        }
        i += 1
    }
    return none
}

// Reply is what a client takes from the server's 101 reply: the fields
// RFC 6455 4.1 checks, each seen at most once, and the index in the
// offered subprotocols of the one the server chose (-1 for none).
struct Reply {
    upgrade: bool
    connection: bool
    accept: bool
    protocol: int
    chosen: int
}

// ReplyHead reads the reply head h (HeadEnd's bytes) of a server asked
// with the accept value want and the subprotocols offered. Handshake(0)
// for a head that is not strict HTTP/1.1, Handshake(status) for any status
// but 101, and Handshake(101) for a 101 that breaks RFC 6455 4.1: no
// Upgrade of websocket, no Connection holding upgrade, an accept value
// other than want, any Sec-WebSocket-Extensions (none was offered), or a
// subprotocol that was not offered or a second one. Returns the index in
// offered of the subprotocol the server chose, -1 when it named none.
// O(h.Len() + offered); allocates nothing; no recursion.
ReplyHead(h: []u8, want: []u8, offered: []str): Handshake!int {
    // "HTTP/1.1 101" and at least the line's CR LF.
    if h.Len() < 14 || !SameBytes(h[0..9], "HTTP/1.1 ".AsBytes()) {
        fail Handshake(status: 0)
    }
    var status := 0
    for i in 9..12 {
        d := h[i]
        if d < 0x30 || d > 0x39 {
            fail Handshake(status: 0)
        }
        status = status * 10 + int(d - 0x30)
    }
    if h[12] != 0x20 && h[12] != 0x0D {
        fail Handshake(status: 0)
    }
    var at := try LineEnd(h, 12)
    var r := Reply(upgrade: false, connection: false, accept: false, protocol: 0, chosen: -1)
    for {
        end := try LineEnd(h, at)
        if end == at + 2 {
            break
        }
        line := h[at..end - 2]
        at = end
        colon := Colon(line) else {
            fail Handshake(status: 0)
        }
        name := line[0..colon]
        value := TrimOws(line[colon + 1..])
        if !IsToken(name) || !ValueOk(value) {
            fail Handshake(status: 0)
        }
        if status != 101 {
            continue
        }
        try r.Field(name, value, want, offered)
    }
    if status != 101 {
        fail Handshake(status: status)
    }
    if !r.upgrade || !r.connection || !r.accept {
        fail Handshake(status: 101)
    }
    return r.chosen
}

// Reply.Field checks one field of a 101 reply against RFC 6455 4.1's list.
Reply.Field(mut self, name: []u8, value: []u8, want: []u8, offered: []str): Handshake!void {
    if SameFold(name, "upgrade".AsBytes()) {
        if self.upgrade || !SameFold(value, "websocket".AsBytes()) {
            fail Handshake(status: 101)
        }
        self.upgrade = true
    } else if SameFold(name, "connection".AsBytes()) {
        if HasToken(value, "upgrade".AsBytes()) {
            self.connection = true
        }
    } else if SameFold(name, "sec-websocket-accept".AsBytes()) {
        if self.accept || !SameBytes(value, want) {
            fail Handshake(status: 101)
        }
        self.accept = true
    } else if SameFold(name, "sec-websocket-extensions".AsBytes()) {
        fail Handshake(status: 101)
    } else if SameFold(name, "sec-websocket-protocol".AsBytes()) {
        self.protocol += 1
        at := OfferedAt(value, offered)
        if self.protocol > 1 || at < 0 {
            fail Handshake(status: 101)
        }
        self.chosen = at
    }
}

// OfferedAt is value's index among the subprotocols offered, -1 when it
// is none of them.
OfferedAt(value: []u8, offered: []str): int {
    for i in 0..offered.Len() {
        if SameBytes(value, offered[i].AsBytes()) {
            return i
        }
    }
    return -1
}

// SameBytes reports whether a and b hold the same bytes.
SameBytes(a: []u8, b: []u8): bool {
    if a.Len() != b.Len() {
        return false
    }
    for i in 0..a.Len() {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}

// LineEnd is the index past the CR LF that ends the line holding h[at]:
// Handshake(0) for a bare CR or LF, or a line with no end in h.
LineEnd(h: []u8, at: int): Handshake!int {
    var i := at
    for i < h.Len() {
        b := h[i]
        if b == 0x0D {
            if i + 1 < h.Len() && h[i + 1] == 0x0A {
                return i + 2
            }
            fail Handshake(status: 0)
        }
        if b == 0x0A {
            fail Handshake(status: 0)
        }
        i += 1
    }
    fail Handshake(status: 0)
}

// Colon is the index of the first colon of a field line, none when it has
// none or the line starts with a space or tab (obsolete folding).
Colon(line: []u8): ?int {
    if line.Len() == 0 || IsOws(line[0]) {
        return none
    }
    for i in 0..line.Len() {
        if line[i] == 0x3A {
            return some(i)
        }
    }
    return none
}

// Refusal is why Accept refused a request: the status it answers with.
error Refusal(status: int)

// CheckRequest checks a client's opening handshake (4.2.1) and returns its
// key: Refusal(400) for a method other than GET, an Upgrade without
// websocket, a Connection without upgrade, or a key that is not 16 bytes in
// base64, or that appears twice; Refusal(426) for a version other than 13
// (4.4); Refusal(403) when allow refuses the Origin (absent is ""). O(the
// request's fields); allocates nothing.
CheckRequest(req: &http.Request, const allow: fn(origin: str): bool): Refusal!str from req {
    if req.method != "GET" {
        fail Refusal(status: 400)
    }
    var key: ?str = none
    var keys := 0
    var version: ?str = none
    var versions := 0
    var upgrade := false
    var connection := false
    for f in req.headers {
        name := f.name.AsBytes()
        if SameFold(name, "sec-websocket-key".AsBytes()) {
            keys += 1
            key = some(f.value)
        } else if SameFold(name, "sec-websocket-version".AsBytes()) {
            versions += 1
            version = some(f.value)
        } else if SameFold(name, "upgrade".AsBytes()) {
            if HasToken(f.value.AsBytes(), "websocket".AsBytes()) {
                upgrade = true
            }
        } else if SameFold(name, "connection".AsBytes()) {
            if HasToken(f.value.AsBytes(), "upgrade".AsBytes()) {
                connection = true
            }
        }
    }
    if !upgrade || !connection || keys != 1 {
        fail Refusal(status: 400)
    }
    k := key else {
        fail Refusal(status: 400)
    }
    if !KeyOk(k) {
        fail Refusal(status: 400)
    }
    v := version else {
        fail Refusal(status: 426)
    }
    if versions != 1 || v != "13" {
        fail Refusal(status: 426)
    }
    origin := req.Header("origin") else { "" }
    if !allow(origin) {
        fail Refusal(status: 403)
    }
    return k
}

// Choose is the index in protocols of the first subprotocol the client
// offered that protocols lists byte for byte, -1 when they share none.
// The offer is every Sec-WebSocket-Protocol field of req, in order, each a
// comma-separated list (RFC 6455 4.2.1 item 8; several fields are one list,
// 11.3.4). An element that is not a token, or is longer than ProtocolMax,
// is skipped, never refused: the name the 101 echoes is a token that fits
// the write buffer. O(the fields' bytes times protocols.Len()); allocates
// nothing; no recursion.
Choose(req: &http.Request, protocols: []str): int {
    if protocols.Len() == 0 {
        return -1
    }
    for f in req.headers {
        if !SameFold(f.name.AsBytes(), "sec-websocket-protocol".AsBytes()) {
            continue
        }
        v := f.value.AsBytes()
        var at := 0
        for at <= v.Len() {
            var end := at
            for end < v.Len() && v[end] != 0x2C {
                end += 1
            }
            e := TrimOws(v[at..end])
            if e.Len() <= ProtocolMax && IsToken(e) {
                i := OfferedAt(e, protocols)
                if i >= 0 {
                    return i
                }
            }
            at = end + 1
        }
    }
    return -1
}
