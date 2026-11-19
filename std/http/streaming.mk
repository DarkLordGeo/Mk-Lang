// Why: a request body that comes from a file or another connection should
// not be read into memory before it is sent.
//
// What: Client.DoStreaming, a request whose body comes from a Reader.
//
// How: the body is sent with a Content-Length when its length is known and
// chunked otherwise (RFC 9112 6.1, 7.1), read and written in pieces of at
// most StreamPiece bytes through a buffer on the stack. Redirects follow.

// A Reader cannot be read twice, so a redirect that would send the body
// again (307, 308, and 301 or 302 for a method other than POST, RFC 9110
// 15.4) fails with BodyNotReplayable; one that drops the body (303, or 301
// and 302 after POST) is followed as Do follows it. The Stream parameter
// of Send, Exchange, LineWrite and WriteOn (client.mk, wire.mk) and
// RequestHead's streamed and slen carry the body to the wire.

import std.url

// StreamPiece is the most a streamed body's read takes, and so the
// largest chunk it sends: 16 KiB, as Go's chunked writer's buffer.
const StreamPiece = 16384

// SizeRoom is the room before a chunk's bytes for its size line: at most
// 4 hex digits for StreamPiece, then CRLF, in 8 bytes. StreamRoom holds
// the line, the piece and its CRLF.
const SizeRoom = 8
const StreamRoom = 16394

// Stream is a request body from a Reader: r, none for a body of bytes;
// len, the length the request states (none: chunked); took, whether a
// byte was read from r, after which the request cannot be retried.
view struct Stream {
    r: ?mut Reader
    len: ?i64
    took: bool
}

// NoStream is the Stream of a request whose body is bytes.
NoStream(): Stream {
    return Stream(r: none, len: none, took: false)
}

/// Sends req as Do does, with the request's body read from body instead of
/// req.body, which is not sent: for an upload that need not fit in memory.
/// With some(len), it sends len bytes with `Content-Length: len`; with
/// none, every byte up to body's end, in chunks of at most 16 KiB, with
/// `Transfer-Encoding: chunked`. O(bytes sent and read); the body passes
/// through a 16 KiB buffer on the stack, and it allocates as Do does.
///
/// A redirect that would send the body again (307, 308, and 301 or 302 for
/// a method other than POST) fails with BodyNotReplayable after its reply
/// is drained. One that drops the body (303, or 301 and 302 after POST) is
/// followed as Do follows it. When body fails, or ends before len bytes,
/// the call fails with ReadError and the connection is closed. A reply the
/// server sends before the whole body is sent is read only after it. A
/// pooled connection the server closed is retried on a new one only when
/// no byte was read from body. Otherwise it fails as Do does.
export Client.DoStreaming(&self, a: &Arena, io: Io, req: &ClientRequest, body: mut Reader, len: ?i64): Error!Exchange from a, self {
    if n := len {
        if n < 0 {
            fail BadUrl
        }
    }
    var st := Stream(r: some(body), len: len, took: false)
    var none_st := NoStream()
    var u := req.url
    var method := req.method
    var streamed := true
    var strip := false
    var hop := 0
    for {
        var ex := if streamed {
            try self.Send(a, io, method, u, req.headers, "".AsBytes(), strip, mut st)
        } else {
            try self.Send(a, io, method, u, req.headers, "".AsBytes(), strip, mut none_st)
        }
        if !IsRedirect(ex.status) || self.limits.redirects <= 0 {
            // A redirects limit of 0 follows none: the 3xx reply is the
            // caller's as it is.
            return ex
        }
        loc := Lookup(ex.headers, "location") else {
            return ex
        }
        // A copy in a: url.Parse stores its input into a, and loc
        // is a view of the reply, which holds self.
        dloc := a.Dup(loc) else {
            return ex
        }
        next := url.Parse(a, dloc, some(u)) else {
            return ex
        }
        if next.Scheme() != "http" && next.Scheme() != "https" {
            return ex
        }
        if hop >= self.limits.redirects {
            ex.Abandon()
            fail TooManyRedirects
        }
        hop += 1
        code := ex.status
        drops := code == 303 || (code == 301 || code == 302) && method == "POST"
        empty := if n := len { n == 0 } else { false }
        if streamed && !drops && !empty {
            Drain(ex, io)
            fail BodyNotReplayable
        }
        Drain(ex, io)
        if drops {
            if method != "HEAD" {
                method = "GET"
            }
            streamed = false
        }
        if !SameOrigin(u, &next) {
            strip = true
        }
        u = &*try a.Put(next)
    }
}

// StreamOut writes st's body to w and flushes it: st.len bytes as they
// are, or chunks of what st.r gives up to its end, then the last chunk.
// A chunk is read into buf after room for its size line and written with
// that line and its CRLF in one Bytes call, so a piece costs one write
// whatever w's buffer. st.r failing, or ending before st.len bytes, is
// ReadError; w failing is WriteError (Canceled under a cancelled scope).
StreamOut(w: mut Writer, io: Io, st: mut Stream): Error!void {
    var buf: [StreamRoom]u8 = [0; StreamRoom]
    match mut st.r {
        some(r) {
            if n := st.len {
                var left := n
                for left > 0 {
                    top := if left > i64(StreamPiece) { StreamPiece } else { int(left) }
                    st.took = true
                    k := (*r).Read(mut buf[0..top]) else {
                        fail ReadError
                    }
                    if k == 0 {
                        fail ReadError
                    }
                    try StreamPut(w, io, buf[0..k])
                    left -= i64(k)
                }
            } else {
                for {
                    st.took = true
                    k := (*r).Read(mut buf[SizeRoom..SizeRoom + StreamPiece]) else {
                        fail ReadError
                    }
                    if k == 0 {
                        break
                    }
                    // The size line, right-aligned before the piece: hex
                    // digits, CRLF; then the CRLF after the piece.
                    var j := SizeRoom - 2
                    var x := k
                    for {
                        j -= 1
                        buf[j] = HexDigit(x & 15)
                        x = x >> 4
                        if x == 0 {
                            break
                        }
                    }
                    buf[SizeRoom - 2] = CR
                    buf[SizeRoom - 1] = LF
                    buf[SizeRoom + k] = CR
                    buf[SizeRoom + k + 1] = LF
                    try StreamPut(w, io, buf[j..SizeRoom + k + 2])
                }
                w.Str("0\r\n\r\n") else {
                    try CancelCheck(io)
                    fail WriteError
                }
            }
        }
        none {}
    }
    w.Flush() else {
        try CancelCheck(io)
        fail WriteError
    }
}

// StreamPut writes b to w.
StreamPut(w: mut Writer, io: Io, b: []u8): Error!void {
    w.Bytes(b) else {
        try CancelCheck(io)
        fail WriteError
    }
}
