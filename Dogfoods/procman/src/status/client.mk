// Why: `procman status` asks a running procman over its status port.
//
// What: Get, Parse, Answer and the NoReply and BadReply errors.
//
// How: one HTTP/1.0 request to the port named in the run directory.

import std.net

/// The server closed the connection, or reset it, before answering.
export error NoReply
/// The answer is not HTTP/1.x.
export error BadReply
/// What asking a status worker can fail with.
export type ClientError = NoReply | BadReply | net.Error | OutOfMemory

/// An answer from a status worker.
export struct Answer {
    export code: int
    export body: str
}

/// Asks `addr` for `path` and returns the answer, read whole, in `a`. Fails
/// with `NoReply` when the connection ends or fails before a byte arrives,
/// which is what a worker that dies mid-request gives, and with `BadReply` past
/// `limit` bytes or on an answer that is not HTTP. One connection.
export Get(a: &Arena, io: Io, addr: str, path: str, limit: int): ClientError!Answer from a {
    conn := try net.Dial(io, addr)
    defer conn.Close()
    request := try a.Fmt("GET {path} HTTP/1.0\r\nHost: procman\r\n\r\n", (path))
    try conn.WriteAll(io, request.AsBytes())
    buf := try a.Slice[u8](limit)
    var n := 0
    for n < limit {
        // A worker that dies mid-request resets the connection on one OS
        // and closes it on another, so any failure to read ends the answer;
        // with nothing read, that is NoReply below.
        got := conn.Read(io, mut buf[n..]) else {
            break
        }
        if got == 0 {
            break
        }
        n += got
    }
    if n == 0 {
        fail NoReply
    }
    if n == limit {
        fail BadReply
    }
    text := str.From(buf[..n]) else {
        fail BadReply
    }
    return try Parse(text)
}

// Splits an answer into its status code and body.
Parse(text: str): BadReply!Answer from text {
    if !text.StartsWith("HTTP/1.") || text.Len() < 12 {
        fail BadReply
    }
    code := text[9..12].ParseInt() else {
        fail BadReply
    }
    end := text.Find("\r\n\r\n") else {
        fail BadReply
    }
    return Answer(code: Trunc[int](code), body: text[end + 4..])
}

test "Parse splits the status code from the body" {
    r := try Parse("HTTP/1.0 404 Not Found\r\nContent-Length: 3\r\n\r\nno\n")
    Assert(r.code == 404)
    Assert(r.body == "no\n")
}

test "Parse refuses an answer that is not HTTP" {
    r := Parse("SSH-2.0 hello\r\n\r\n") else {
        Answer(code: 0, body: "refused")
    }
    Assert(r.body == "refused")
}
