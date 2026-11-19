// Why: the status port must keep answering even if a worker crashes.
//
// What: Serve, Respond, Reply and Crash.
//
// How: a small HTTP/1.0 server that answers from the run directory alone.
// It runs in worker processes under `proc.Supervise`, so a worker that dies
// takes one connection with it and the pool goes on.

import std.net
import src.rundir

/// A request that is not `METHOD PATH VERSION` and headers.
export error BadRequest
/// What serving the status port can fail with.
export type Error = BadRequest | net.Error | rundir.Error | ReadError | WriteError | OutOfMemory

/// The longest request line or header line a worker reads.
export const MaxLine: int = 1024

/// Serves `l` until accepting fails, which cancellation does. Each request
/// gets one answer and the connection closes; a request that fails is
/// dropped and the next one served. One arena, reset after each request.
export Serve(h: &Heap, io: Io, l: &net.Listener, dir: rundir.RunDir, debug: bool): Error!void {
    var arena := try Arena.New(h)
    defer arena.Free()
    for {
        conn := try l.Accept(io)
        Respond(&arena, io, &conn, dir, debug) else {}
        conn.Close()
        arena.Reset()
    }
}

// Reads one request from `conn` and writes its answer.
Respond(a: &Arena, io: Io, conn: &net.Stream, dir: rundir.RunDir, debug: bool): Error!void {
    buf := try a.Slice[u8](MaxLine)
    var r := conn.Reader(io, buf)
    first := try r.Line(a)
    line := first else {
        fail BadRequest
    }
    for {
        header := try r.Line(a)
        h := header else {
            break
        }
        if h.Len() == 0 {
            break
        }
    }
    var words := line.Words()
    method := words.Next() else {
        fail BadRequest
    }
    path := words.Next() else {
        fail BadRequest
    }
    if method != "GET" {
        try Reply(a, io, conn, 405, "method not allowed\n")
        return
    }
    if path == "/health" {
        try Reply(a, io, conn, 200, "ok\n")
    } else if path == "/status" {
        try Reply(a, io, conn, 200, try StatusText(a, io, dir))
    } else if path == "/panic" && debug {
        Crash()
    } else {
        try Reply(a, io, conn, 404, "not found\n")
    }
}

// Every process's state line, in name order.
StatusText(a: &Arena, io: Io, dir: rundir.RunDir): rundir.Error!str from a {
    names := try dir.List(a, io, ".state")
    if names.Len() == 0 {
        return "no processes\n"
    }
    var out := Text.New(a)
    for name in names {
        text := try dir.Read(a, io, name)
        if t := text {
            try out.Push(t)
        }
    }
    return out.Done()
}

Reply(a: &Arena, io: Io, conn: &net.Stream, code: int, body: str): Error!void {
    reason := match code {
        200 { "OK" }
        404 { "Not Found" }
        405 { "Method Not Allowed" }
        _ { "Error" }
    }
    head := try a.Fmt("HTTP/1.0 {code} {reason}\r\nContent-Type: text/plain\r\nContent-Length: {n}\r\nConnection: close\r\n\r\n", (code, reason, n: body.Len()))
    try conn.WriteAll(io, head.AsBytes())
    try conn.WriteAll(io, body.AsBytes())
}

// `/panic` with `--status-debug`: shows that the pool keeps serving when a
// worker dies. Kept last in the file, so its line in the panic report holds.
Crash() {
    Panic("status worker stopped by /panic")
}
