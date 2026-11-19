// Why: the server and the client read HTTP/1.1 heads and bodies from bytes
// an attacker controls, so one strict parser serves both, and its framing
// rules close request smuggling.
//
// What: ReadRequest; ParseHead and ReadHead, the head parser Serve, Fetch
// and the Client share; ReadBody, ReadChunked and ChunkStep for bodies;
// FieldEnd, the vectorized field-value scan; and the byte predicates
// IsTchar, IsAlpha, IsDigit and HexVal.
//
// How: picohttpparser's single pass over one buffer, every field a view of
// it, strict where llhttp's strict mode is (CRLF only, no obs-fold, no
// whitespace before a colon), with RFC 9110 and RFC 9112's syntax and
// framing. More below.

// RFC 9112 section 6.3's framing rules: Content-Length with
// Transfer-Encoding, differing Content-Lengths, and any coding but a lone
// chunked are refused.
//
// The bytes of a head are copied once into the request's arena and checked
// as UTF-8 once (rt.StrFrom, simd.ValidUtf8); the fields are views of that
// copy. A field value is scanned 16 bytes a step for its CR or a control
// byte (FieldEnd: picohttpparser's SSE4.2 findchar_fast, simdjson's byte
// classification); names and the request line stay byte loops (IsTchar is
// a predicate, and both are short). ReadHead takes a head one line per
// Reader.Until, and Serve finds a line's LF with simd.IndexByte (HeadScan,
// serve.mk).

import std.core
import std.raw
import std.rt
import std.simd

const CR: u8 = 0x0D
const LF: u8 = 0x0A
const SP: u8 = 0x20
const HTAB: u8 = 0x09
const Colon: u8 = 0x3A

// Head is a parsed request line or status line and its fields, as views of
// one head buffer. length is Content-Length, or -1 when there is none.
struct Head {
    method: str
    path: str
    query: str
    status: int
    headers: []Header
    minor: int
    length: int
    chunked: bool
    close: bool
}

// The chunked decoder's states (RFC 9112 7.1).
const CkSize = 0
const CkExt = 1
const CkSizeLF = 2
const CkData = 3
const CkDataCR = 4
const CkDataLF = 5
const CkTrailer = 6
const CkTrailerLine = 7
const CkTrailerLF = 8
const CkFinalLF = 9
const CkDone = 10
const CkSizeWS = 11
const CkTrailerName = 12

// Chunk is the resumable state of one chunked body: the state, the size
// being read or the data bytes left of the current chunk, how many hex
// digits were read, and how many bytes the body took so far (framing
// included), the offset errors report and the count limit bounds, and the
// bytes of its chunk extensions and trailers, which a head limit bounds.
struct Chunk {
    state: int
    size: int
    digits: int
    off: int
    meta: int
}

/// Reads one request from r into a: the head, then a Content-Length or
/// chunked body. Use it to read requests from a stream of your own; Serve
/// and ServeWith read theirs the same way. O(n) in the request's bytes;
/// the head and the body are allocated in a. A Content-Length body takes
/// buffers of 16 KiB and then doubling, up to about three times its size;
/// a chunked body grows by at most one chunk per buffer, so its cost grows
/// with the square of its number of chunks. The request's fields are views
/// of its head in a.
///
/// The head is read one line per Reader.Until, from the reader's buffer,
/// never a byte past the head's blank line, so no byte of a pipelined next
/// request is taken. The body grows into a buffer as its bytes arrive
/// (a Content-Length body from 16 KiB, doubling), so a declared length
/// commits no memory before its bytes come.
///
/// Errors: limit bounds the head plus the body's bytes, and one byte over
/// is TooLarge. End of input before the first byte is Closed, and end
/// of input inside the request, or a malformed request, is BadRequest at
/// its offset. A failed read of r is Canceled when io's scope is
/// cancelled, else ReadError.
export ReadRequest(a: &Arena, io: Io, r: mut Reader, limit: int): Error!Request from a {
    try CancelCheck(io)
    raw := try ReadHead(a, io, r, limit, 0)
    h := try ParseHead(a, raw.text, false, raw.skipped)
    used := raw.skipped + raw.text.Len()
    if h.chunked {
        body := try ReadChunked(a, io, r, limit - used, used)
        return Request(method: h.method, path: h.path, query: h.query, headers: h.headers, params: List[Header].New(a).Done()[..], body: body[..])
    }
    if h.length > limit - used {
        fail TooLarge
    }
    var length := h.length
    if length < 0 {
        // No Content-Length and no chunked: no body (RFC 9112 6.3 rule 7).
        length = 0
    }
    body := try ReadBody(a, io, r, length, used)
    return Request(method: h.method, path: h.path, query: h.query, headers: h.headers, params: List[Header].New(a).Done()[..], body: body[..])
}

// BodyStep is the first buffer a body is read into; it doubles from there
// up to the length still to come (GrowFor).
const BodyStep = 16384

// ReadBody reads exactly n bytes from r into a buffer in a that grows as
// they arrive (GrowFor); fewer is BadRequest at base plus the bytes read.
ReadBody(a: &Arena, io: Io, r: mut Reader, n: int, base: int): Error![]u8 from a {
    var b := try a.Slice[u8](0)
    var bn := 0
    for bn < n {
        if bn == b.Len() {
            b = try GrowFor(a, b[0..bn], n - bn)
        }
        got := r.Read(mut b[bn..b.Len()]) else {
            try CancelCheck(io)
            fail ReadError
        }
        if got == 0 {
            fail BadRequest(offset: base + bn)
        }
        bn += got
    }
    return b[0..n]
}

// GrowFor is a new arena buffer holding old's bytes with room after them:
// twice old's length, at least BodyStep, but never more than old's length
// plus left, the bytes still to come. Each buffer is abandoned to the arena
// when the next is made, and arena is taken only for bytes that arrived. A
// caller that passes one chunk's size as left grows the buffer by that
// chunk, not by doubling.
GrowFor(a: &Arena, old: []u8, left: int): OutOfMemory!mut []u8 from a {
    var c := if old.Len() * 2 < BodyStep { BodyStep } else { old.Len() * 2 }
    if c > old.Len() + left {
        c = old.Len() + left
    }
    nb := try a.Slice[u8](c)
    (mut nb[0..old.Len()]).CopyFrom(old)
    return nb
}

// Raw is a head read whole: its text, which ends with the blank line, and
// the bytes of empty lines skipped before it.
struct Raw {
    text: str
    skipped: int
}

// ReadHead reads a head from r one line per Reader.Until, up to and
// with the first CRLF CRLF, never a byte past it, so a pipelined next
// request stays in r: empty lines before it are skipped (RFC 9112 2.2), a
// bare LF is BadRequest, and the head is checked as UTF-8 once
// (rt.StrFrom, simd.ValidUtf8). base is the offset of its first byte, for
// BadRequest; limit bounds the bytes read, and a byte past it is TooLarge.
// Each Until call is asked for at most one byte past the limit, so the
// verdicts are those of a byte-at-a-time read: only a line's last byte can
// be an LF or the byte past the limit (TestSTDVECTOR_HttpScansDiff).
ReadHead(a: &Arena, io: Io, r: mut Reader, limit: int, base: int): Error!Raw from a {
    var buf := try a.Slice[u8](512)
    var n := 0
    var skipped := 0
    for {
        if n == buf.Len() {
            buf = try GrowBytes(a, buf[0..n], n + 1)
        }
        // rem is what limit leaves; the clamp adds 1 only on the small
        // side, so a limit at the int maximum does not overflow.
        var want := buf.Len() - n
        rem := limit - skipped - n
        if want - 1 > rem {
            want = rem + 1
        }
        if want < 1 {
            want = 1
        }
        got := r.Until(LF, mut buf[n..n + want]) else {
            try CancelCheck(io)
            fail ReadError
        }
        if got == 0 {
            if n == 0 && skipped == 0 {
                fail Closed
            }
            fail BadRequest(offset: base + skipped + n)
        }
        if n == 0 && buf[0] == CR {
            // An empty line before the request line (RFC 9112 2.2); its LF
            // is not counted against the limit, as its CR is.
            if skipped >= limit {
                fail TooLarge
            }
            if got == 1 {
                var one: [1]u8 = [0; 1]
                got2 := r.Read(mut one[..]) else {
                    try CancelCheck(io)
                    fail ReadError
                }
                if got2 == 0 || one[0] != LF {
                    fail BadRequest(offset: base + skipped + 1)
                }
            } else if buf[1] != LF {
                fail BadRequest(offset: base + skipped + 1)
            }
            skipped += 2
            continue
        }
        e := n + got
        if skipped + e - 1 >= limit {
            fail TooLarge
        }
        n = e
        if buf[e - 1] == LF {
            if e < 2 || buf[e - 2] != CR {
                fail BadRequest(offset: base + skipped + e - 1)
            }
            if e >= 4 && buf[e - 3] == LF {
                break
            }
        }
    }
    head := buf[0..n]
    text := rt.StrFrom(head) else {
        fail BadRequest(offset: base + skipped + Utf8Bad(head))
    }
    return Raw(text: text, skipped: skipped)
}

// ReadChunked decodes a chunked body from r, which never reads past its
// last byte: framing one byte per Read, data as it arrives into a buffer
// that grows with it (GrowFor), so a chunk-size line commits no memory
// before its bytes come. budget is what limit leaves for the body; base is
// the body's offset.
ReadChunked(a: &Arena, io: Io, r: mut Reader, budget: int, base: int): Error![]u8 from a {
    var st := Chunk(state: CkSize, size: 0, digits: 0, off: 0, meta: 0)
    var body := try a.Slice[u8](0)
    var bn := 0
    var one: [1]u8 = [0; 1]
    for st.state != CkDone {
        if st.state == CkData {
            if bn == body.Len() {
                body = try GrowFor(a, body[0..bn], st.size)
            }
            top := if body.Len() > bn + st.size { bn + st.size } else { body.Len() }
            got := r.Read(mut body[bn..top]) else {
                try CancelCheck(io)
                fail ReadError
            }
            if got == 0 {
                fail BadRequest(offset: base + st.off)
            }
            bn += got
            st.off += got
            st.size -= got
            if st.size == 0 {
                st.state = CkDataCR
            }
            continue
        }
        got := r.Read(mut one[..]) else {
            try CancelCheck(io)
            fail ReadError
        }
        if got == 0 {
            fail BadRequest(offset: base + st.off)
        }
        _ = try ChunkStep(mut st, one[..], budget, base, budget)
    }
    return body[0..bn]
}

// ChunkStep consumes the framing bytes at the front of win: it stops after
// the byte that enters CkData (the caller copies the data and sets
// CkDataCR), at CkDone, or at the end of win, and returns how many bytes it
// took. A chunk size is accumulated with the budget as its bound, so a
// size of any length of hex digits is TooLarge before it can overflow.
ChunkStep(st: mut Chunk, win: []u8, budget: int, base: int, meta_max: int): Error!int {
    var i := 0
    for i < win.Len() {
        if st.state == CkData || st.state == CkDone {
            return i
        }
        c := win[i]
        if st.off >= budget {
            fail TooLarge
        }
        s := st.state
        if s == CkSizeWS || s == CkExt || s >= CkTrailer && s <= CkTrailerLF || s == CkTrailerName {
            st.meta += 1
            if st.meta > meta_max {
                fail TooLarge
            }
        }
        if s == CkSize {
            d := HexVal(c)
            if d >= 0 {
                // off + size is what the body will hold when this chunk is
                // read; refusing it here keeps size * 16 + d below budget.
                if st.size > (budget - st.off - d) / 16 {
                    fail TooLarge
                }
                st.size = st.size * 16 + d
                st.digits += 1
            } else if st.digits == 0 {
                fail BadRequest(offset: base + st.off)
            } else if c == CR {
                st.state = CkSizeLF
            } else if c == 0x3B {
                st.state = CkExt
            } else if c == SP || c == HTAB {
                st.state = CkSizeWS
            } else {
                fail BadRequest(offset: base + st.off)
            }
        } else if s == CkSizeWS {
            // BWS before a chunk extension (RFC 9112 7.1.1); only ";" or
            // the line's end may follow it.
            if c == 0x3B {
                st.state = CkExt
            } else if c == CR {
                st.state = CkSizeLF
            } else if c != SP && c != HTAB {
                fail BadRequest(offset: base + st.off)
            }
        } else if s == CkExt {
            if c == CR {
                st.state = CkSizeLF
            } else if c != HTAB && (c < 0x20 || c == 0x7F) {
                fail BadRequest(offset: base + st.off)
            }
        } else if s == CkSizeLF {
            if c != LF {
                fail BadRequest(offset: base + st.off)
            }
            if st.size == 0 {
                st.state = CkTrailer
            } else {
                if st.size > budget - st.off - 1 {
                    fail TooLarge
                }
                st.state = CkData
            }
            st.digits = 0
        } else if s == CkDataCR {
            if c != CR {
                fail BadRequest(offset: base + st.off)
            }
            st.state = CkDataLF
        } else if s == CkDataLF {
            if c != LF {
                fail BadRequest(offset: base + st.off)
            }
            st.state = CkSize
            st.size = 0
        } else if s == CkTrailer {
            if c == CR {
                st.state = CkFinalLF
            } else if IsTchar(c) {
                st.state = CkTrailerName
            } else {
                fail BadRequest(offset: base + st.off)
            }
        } else if s == CkTrailerName {
            // A trailer field is a field (RFC 9112 7.1.2): a token, then
            // ":" with no whitespace before it.
            if c == Colon {
                st.state = CkTrailerLine
            } else if !IsTchar(c) {
                fail BadRequest(offset: base + st.off)
            }
        } else if s == CkTrailerLine {
            if c == CR {
                st.state = CkTrailerLF
            } else if c != HTAB && (c < 0x20 || c == 0x7F) {
                fail BadRequest(offset: base + st.off)
            }
        } else if s == CkTrailerLF {
            if c != LF {
                fail BadRequest(offset: base + st.off)
            }
            st.state = CkTrailer
        } else if s == CkFinalLF {
            if c != LF {
                fail BadRequest(offset: base + st.off)
            }
            st.state = CkDone
        }
        st.off += 1
        i += 1
    }
    return i
}

// ParseHead parses text, a whole head that ends with its blank line: the
// request line (or the status line when reply), then the fields. base is
// the offset of text's first byte in the request, for BadRequest.
ParseHead(a: &Arena, text: str into a, reply: bool, base: int): Error!Head from a, text {
    b := text.AsBytes()
    n := b.Len()
    var method := ""
    var path := ""
    var query := ""
    var authority := ""
    var absolute := false
    var status := 0
    var minor := 1
    var i := 0
    if reply {
        minor = try Version(b, 0, base)
        i = 8
        if i >= n || b[i] != SP {
            fail BadRequest(offset: base + i)
        }
        i += 1
        if n - i < 3 || b[i] < 0x31 || b[i] > 0x39 || !IsDigit(b[i + 1]) || !IsDigit(b[i + 2]) {
            fail BadRequest(offset: base + i)
        }
        status = int(b[i] - 0x30) * 100 + int(b[i + 1] - 0x30) * 10 + int(b[i + 2] - 0x30)
        i += 3
        if i < n && b[i] == SP {
            i += 1
            for i < n && b[i] != CR {
                c := b[i]
                if c != HTAB && (c < 0x20 || c == 0x7F) {
                    fail BadRequest(offset: base + i)
                }
                i += 1
            }
        }
    } else {
        ms := i
        for i < n && IsTchar(b[i]) {
            i += 1
        }
        if i == ms || i >= n || b[i] != SP {
            fail BadRequest(offset: base + i)
        }
        method = text[ms..i]
        i += 1
        ts := i
        for i < n && b[i] != SP && b[i] != CR && b[i] != LF {
            i += 1
        }
        if i == ts || i >= n || b[i] != SP {
            fail BadRequest(offset: base + i)
        }
        tg := try ParseTarget(text, method, ts, i, base)
        path = tg.path
        query = tg.query
        authority = tg.authority
        absolute = tg.absolute
        i += 1
        minor = try Version(b, i, base)
        i += 8
    }
    if i + 1 >= n || b[i] != CR || b[i + 1] != LF {
        fail BadRequest(offset: base + i)
    }
    i += 2
    var hs := List[Header].New(a)
    var hosts := 0
    var flags := 0
    var length := -1
    var len_at := -1
    var chunked := false
    var te_at := -1
    for {
        if i >= n {
            fail BadRequest(offset: base + n)
        }
        if b[i] == CR {
            if i + 1 >= n || b[i + 1] != LF {
                fail BadRequest(offset: base + i + 1)
            }
            i += 2
            break
        }
        ns := i
        for i < n && IsTchar(b[i]) {
            i += 1
        }
        if i == ns || i >= n || b[i] != Colon {
            fail BadRequest(offset: base + i)
        }
        name := text[ns..i]
        i += 1
        for i < n && (b[i] == SP || b[i] == HTAB) {
            i += 1
        }
        vs := i
        i = FieldEnd(b, i)
        if i < n && b[i] != CR {
            fail BadRequest(offset: base + i)
        }
        var ve := i
        for ve > vs && (b[ve - 1] == SP || b[ve - 1] == HTAB) {
            ve -= 1
        }
        if i + 1 >= n || b[i + 1] != LF {
            fail BadRequest(offset: base + i + 1)
        }
        value := text[vs..ve]
        if EqFold(name, "content-length") {
            v := ParseLength(b[vs..ve])
            if v == -1 {
                fail BadRequest(offset: base + vs)
            }
            if v == -2 {
                fail TooLarge
            }
            if length >= 0 && length != v {
                fail BadRequest(offset: base + ns)
            }
            length = v
            len_at = ns
        } else if EqFold(name, "transfer-encoding") {
            if chunked || !EqFold(value, "chunked") {
                fail BadRequest(offset: base + vs)
            }
            chunked = true
            te_at = ns
        } else if EqFold(name, "host") {
            hosts += 1
            if hosts > 1 && !reply {
                fail BadRequest(offset: base + ns)
            }
            if !reply {
                _ = try CheckAuthority(b, vs, ve, base)
                if absolute && !EqFold(value, authority) {
                    // The absolute-form's authority and Host name one
                    // origin, or a proxy and this server would disagree
                    // on it.
                    fail BadRequest(offset: base + vs)
                }
            }
        } else if EqFold(name, "connection") {
            flags = flags | ConnFlags(value)
        }
        try hs.Push(Header(name: name, value: value))
        i += 2
    }
    if chunked && length >= 0 {
        if te_at > len_at {
            fail BadRequest(offset: base + te_at)
        }
        fail BadRequest(offset: base + len_at)
    }
    if chunked && minor == 0 {
        fail BadRequest(offset: base + te_at)
    }
    if !reply && minor == 1 && hosts == 0 {
        fail BadRequest(offset: base + n - 2)
    }
    // close wins over keep-alive in any version (RFC 9112 9.3, 9.6): a
    // close option ends the connection; HTTP/1.0 also ends it unless
    // keep-alive is asked for.
    close := flags & 1 != 0 || minor == 0 && flags & 2 == 0
    return Head(method: method, path: path, query: query, status: status, headers: hs.Done()[..], minor: minor, length: length, chunked: chunked, close: close)
}

// Version reads "HTTP/1.0" or "HTTP/1.1" at b[i..i + 8] and returns the
// minor version; anything else is BadRequest at the first byte that differs.
Version(b: []u8, i: int, base: int): Error!int {
    lit := "HTTP/1.".AsBytes()
    for k in 0..7 {
        if i + k >= b.Len() || b[i + k] != lit[k] {
            fail BadRequest(offset: base + i + k)
        }
    }
    if i + 7 >= b.Len() || b[i + 7] != 0x30 && b[i + 7] != 0x31 {
        fail BadRequest(offset: base + i + 7)
    }
    return int(b[i + 7] - 0x30)
}

// SchemeEnd is the index after "scheme://" of an absolute-form target in
// b[s..e] (RFC 3986 3.1: ALPHA *( ALPHA / DIGIT / "+" / "-" / "." )), or -1.
SchemeEnd(b: []u8, s: int, e: int): int {
    var i := s
    for i < e && (IsAlpha(b[i]) || i > s && (IsDigit(b[i]) || b[i] == 0x2B || b[i] == 0x2D || b[i] == 0x2E)) {
        i += 1
    }
    if i == s || e - i < 3 || b[i] != Colon || b[i + 1] != 0x2F || b[i + 2] != 0x2F {
        return -1
    }
    return i + 3
}

// ParseLength is a Content-Length value, 1*DIGIT (RFC 9110 8.6): -1 when it
// is not, -2 when it does not fit an int.
ParseLength(v: []u8): int {
    if v.Len() == 0 {
        return -1
    }
    var x := 0
    for i in 0..v.Len() {
        c := v[i]
        if !IsDigit(c) {
            return -1
        }
        d := int(c - 0x30)
        if x > (9223372036854775807 - d) / 10 {
            return -2
        }
        x = x * 10 + d
    }
    return x
}

// ConnFlags reads a Connection field's tokens: bit 0 for close, bit 1 for
// keep-alive (RFC 9112 9.3, 9.6).
ConnFlags(v: str): int {
    b := v.AsBytes()
    var f := 0
    var i := 0
    for i < b.Len() {
        for i < b.Len() && (b[i] == SP || b[i] == HTAB || b[i] == 0x2C) {
            i += 1
        }
        s := i
        for i < b.Len() && b[i] != 0x2C && b[i] != SP && b[i] != HTAB {
            i += 1
        }
        if i > s {
            t := v[s..i]
            if EqFold(t, "close") {
                f = f | 1
            } else if EqFold(t, "keep-alive") {
                f = f | 2
            }
        }
    }
    return f
}

// FieldEnd is the index of the first byte of b from i0 on that is a control
// byte other than HTAB (below 0x20, or 0x7F), or b.Len(): a field value
// ends at its CR, and any other such byte refuses it (RFC 9110 5.5). Sixteen
// bytes a step, one unsigned compare and two equalities per vector, then the
// first set lane (picohttpparser's findchar_fast with SSE4.2; simdjson's
// classification); the tail is a byte loop. TestSTDVECTOR_HttpScansDiff
// holds FieldEnd to a plain byte loop. Vec runs lane by lane in the
// evaluator, so it holds at compile time too.
FieldEnd(b: []u8, i0: int): int {
    n := b.Len()
    var i := i0
    if i + 16 <= n {
        sp := simd.Vec[u8, 16].Splat(0x20)
        del := simd.Vec[u8, 16].Splat(0x7F)
        tab := simd.Vec[u8, 16].Splat(HTAB)
        for i + 16 <= n {
            v := simd.Vec[u8, 16].FromSlice(b, i)
            m := (((v < sp) & (v != tab)) | (v == del)).Bits()
            if m != 0 {
                return i + raw.Ctz64(m)
            }
            i += 16
        }
    }
    for i < n {
        c := b[i]
        if c != HTAB && (c < 0x20 || c == 0x7F) {
            return i
        }
        i += 1
    }
    return n
}

// GrowBytes is a new arena buffer of at least need bytes, max(256, twice
// old's length, need), holding old's bytes; the old buffer is abandoned to
// the arena.
GrowBytes(a: &Arena, old: []u8, need: int): OutOfMemory!mut []u8 from a {
    var c := if old.Len() * 2 < 256 { 256 } else { old.Len() * 2 }
    if c < need {
        c = need
    }
    nb := try a.Slice[u8](c)
    (mut nb[0..old.Len()]).CopyFrom(old)
    return nb
}

// Utf8Bad is the index of the first byte of b that does not start a
// well-formed UTF-8 sequence (Unicode 15, table 3-7), or b.Len(). Only the
// error path calls it, after str.From refused b.
Utf8Bad(b: []u8): int {
    n := b.Len()
    var i := 0
    for i < n {
        c := b[i]
        if c < 0x80 {
            i += 1
            continue
        }
        var k := 0
        var lo: u8 = 0x80
        var hi: u8 = 0xBF
        if c >= 0xC2 && c <= 0xDF {
            k = 1
        } else if c == 0xE0 {
            k = 2
            lo = 0xA0
        } else if c >= 0xE1 && c <= 0xEC || c == 0xEE || c == 0xEF {
            k = 2
        } else if c == 0xED {
            k = 2
            hi = 0x9F
        } else if c == 0xF0 {
            k = 3
            lo = 0x90
        } else if c >= 0xF1 && c <= 0xF3 {
            k = 3
        } else if c == 0xF4 {
            k = 3
            hi = 0x8F
        } else {
            return i
        }
        if i + k >= n || b[i + 1] < lo || b[i + 1] > hi {
            return i
        }
        for j in 2..k + 1 {
            if b[i + j] < 0x80 || b[i + j] > 0xBF {
                return i
            }
        }
        i += k + 1
    }
    return n
}

// IsTchar: a token character (RFC 9110 5.6.2).
IsTchar(c: u8): bool {
    if IsAlpha(c) || IsDigit(c) {
        return true
    }
    return c == 0x21 || c >= 0x23 && c <= 0x27 || c == 0x2A || c == 0x2B || c == 0x2D || c == 0x2E || c == 0x5E || c == 0x5F || c == 0x60 || c == 0x7C || c == 0x7E
}

IsAlpha(c: u8): bool {
    return c >= 0x61 && c <= 0x7A || c >= 0x41 && c <= 0x5A
}

IsDigit(c: u8): bool {
    return c >= 0x30 && c <= 0x39
}

// HexVal is c's value as a hex digit, or -1.
HexVal(c: u8): int {
    if IsDigit(c) {
        return int(c - 0x30)
    }
    if c >= 0x61 && c <= 0x66 {
        return int(c - 0x61) + 10
    }
    if c >= 0x41 && c <= 0x46 {
        return int(c - 0x41) + 10
    }
    return -1
}

// CancelCheck is the cancellation point for a call that may make no
// runtime call before it waits: std's one check, core.SyncCancel,
// as net's CancelPoint. Canceled in a cancelled scope that io's mask does
// not cover; a check that fails otherwise (an Io with no OS) is not a
// cancellation, and the call goes on.
CancelCheck(io: Io): Error!void {
    core.SyncCancel(io) else {
        fail Canceled
    }
}
