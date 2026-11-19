// Why: Every client command is one HTTP request with the same retry and
// decoding rules.
//
// What: Call, Answer, ProblemText, the error NotText and the limit MaxReply.
//
// How: Call retries a refused connection 20 ms apart, for a server still
// starting, and gunzips a gzip body before reading it as UTF-8.

import std.compress
import std.http
import std.json
import std.net
import std.time

/// The largest reply body the client accepts, compressed or not.
export const MaxReply: int = 4194304

/// A reply whose body is not UTF-8 text.
export error NotText

/// Why a call failed: the server could not be reached, or its reply could
/// not be read.
export type Error = NotText | http.Error | compress.Error

/// A reply with its body decompressed and read as text.
export struct Answer {
    export status: int
    export body: str
    export gzipped: bool
    export wire: int
}

/// Sends one request to `base` (`http://host:port`) and returns the reply,
/// gunzipping a gzip body. `attempts` connection refusals are retried 20 ms
/// apart, for a server that is still starting. The reply lives in `a`.
export Call(a: &Arena into a, io: Io, base: str, method: str, path: str, body: str, attempts: int): Error!Answer from a {
    url := try a.Fmt("{base}{path}", (base, path))
    headers := [
        http.Header(name: "Accept-Encoding", value: "gzip"),
        http.Header(name: "Content-Type", value: "application/json"),
    ]
    var left := attempts
    for {
        reply: http.Reply = http.Fetch(a, io, method, url, headers[..], body.AsBytes(), MaxReply) else e {
            if left > 1 && Refused(e) {
                left -= 1
                try io.Sleep(time.Millis(20))
                continue
            }
            fail e
        }
        var data := reply.body
        var gzipped := false
        for h in reply.headers {
            if h.name == "Content-Encoding" && h.value == "gzip" {
                data = try compress.Gunzip(a, reply.body, MaxReply)
                gzipped = true
            }
        }
        text := str.From(data) else { fail NotText }
        return Answer(status: reply.status, body: text, gzipped: gzipped, wire: reply.body.Len())
    }
}

// Whether e means nothing listens yet.
Refused(e: http.Error): bool {
    return match e {
        net.Refused { true }
        _ { false }
    }
}

/// The `message` member of a problem body, or the body itself when it is not one.
export ProblemText(a: &Arena, body: str into a): str from a, body {
    v := json.Parse(a, body) else { return body }
    m := v.Get("message") else { return body }
    s := m.Str() else { return body }
    return a.Dup(s) else { body }
}
