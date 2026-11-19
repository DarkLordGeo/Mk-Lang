// Why: The API is served over TCP, or replayed from raw requests on standard
// input for the cases.
//
// What: Serve and Replay.
//
// How: Serve runs one task per connection and one arena per request until its
// scope is cancelled. Replay answers requests from a reader in order and stops
// at the first malformed or oversized one.

import std.http
import std.log

/// Serves the API on `addr`, one task per connection and one arena per
/// request, until the enclosing scope is cancelled. A request past `limit`
/// bytes gets 413.
export Serve(sys: &System, addr: str, limit: int, app: &App): http.Error!void {
    try http.Serve(sys, addr, limit, app, Handle)
}

/// Answers the HTTP/1.1 requests read from `r`, one after another, writing
/// each response to `w`, until the input ends. A request that is malformed or
/// past `limit` bytes is answered with 400 or 413 and ends the replay, since
/// the stream cannot be read past it. Returns how many requests were answered.
/// Each request gets a sub-arena of `a`, freed after its response.
export Replay(a: &Arena, io: Io, r: mut Reader, w: mut Writer, limit: int, app: &App): http.Error!int {
    var n := 0
    for {
        var scratch := try a.Sub()
        defer scratch.Free()
        req := http.ReadRequest(&scratch, io, r, limit) else e {
            match e {
                Closed {
                    // the end of the input before a request
                    return n
                }
                http.BadRequest {
                    try Refuse(app, &scratch, io, w, 400, "the request is malformed")
                    return n + 1
                }
                http.TooLarge {
                    try Refuse(app, &scratch, io, w, 413, "the request is larger than the limit")
                    return n + 1
                }
                _ {
                    fail e
                }
            }
        }
        var res := http.Response.New(&scratch)
        Handle(app, &scratch, io, &req, mut res) else e {
            // As Serve does: a Status failure is sent with what the handler
            // set before it, such as Dispatch's Allow header; anything else is 500.
            code := match e {
                http.Status(code) { code }
                _ { 500 }
            }
            res.Status(code)
        }
        try res.Send(io, w)
        n += 1
    }
}

// Answers a request that could not be read, and logs it.
Refuse(app: &App, a: &Arena, io: Io, w: mut Writer, status: int, message: str): http.Error!void {
    var res := http.Response.New(a)
    res.Status(status)
    try res.Header("Content-Type", "application/json")
    body := try a.Fmt("{{\"message\":\"{message}\"}}", (message))
    try res.Body(body.AsBytes())
    try res.Send(io, w)
    line := try a.Fmt("- - {status} {n}", (status, n: body.Len()))
    app.Note(a, io, log.Level.Warn, line)
}
