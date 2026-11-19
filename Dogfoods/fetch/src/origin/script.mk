// Why: fetch's cases need a server whose answers they control, in the same
// process and with no network beyond loopback.
// What: Script, Route, Coding and Load, which reads an origin script.
// How: a script is lines of `route METHOD PATH`, each followed by the
// directives that shape its answer (status, header, body, gzip, chunked,
// stall, websocket and others).

import std.fs
import std.time

const NoBytes: []u8 = []

/// How a route's body is put on the wire.
export enum Coding {
    Identity
    Gzip
    Zlib
}

/// One scripted answer of the origin: the request it matches and how it
/// replies.
export struct Route {
    export method: str
    export path: str
    export status: int
    // the extra header lines, each ended by CR LF
    export headers: str
    export body: []u8
    export coding: Coding
    // the byte count after which the connection is closed, -1 for never
    export cut: i64
    // cut only a request that carries no Range header, so a resumed
    // download of the same route succeeds
    export cut_unranged: bool
    export stall: bool
    export echo: bool
    export ranges: bool
    export chunked: bool
    export modified: ?time.Time
    // answer the WebSocket handshake and echo frames
    export websocket: bool
}

/// A parsed origin script: its routes in file order.
export struct Script {
    export routes: []Route
}

/// A script line that is not a route or a directive with its value.
export error BadLine(line: int)
/// A directive the origin does not know.
export error BadDirective(line: int)

/// Why a script cannot be loaded.
export type ScriptError = BadLine | BadDirective | fs.Error | OutOfMemory

/// Reads the origin script at path: lines of `route METHOD PATH` followed
/// by the directives that shape that route's answer. A file named by `file`
/// is read relative to the script's directory. Fails with BadLine or
/// BadDirective at the first line it cannot read, and with the file
/// system's error. O(n) in the script and the files it names; allocates
/// them in a.
export Load(a: &Arena, io: Io, path: str): ScriptError!Script from a {
    text := try fs.ReadText(a, io, path)
    dir := DirOf(path)
    var routes := List[Route].New(a)
    // cur is the route being read; it joins routes at the next route line
    var cur := NewRoute("", "")
    var n := 0
    for raw in text.Lines() {
        n += 1
        line := raw.Trim()
        if line.Len() == 0 || line.StartsWith("#") {
            continue
        }
        (word, rest) := SplitWord(line)
        if word == "route" {
            (word: method, rest: target) := SplitWord(rest)
            if method.Len() == 0 || !target.StartsWith("/") {
                fail BadLine(line: n)
            }
            if cur.method.Len() > 0 {
                try routes.Push(cur)
            }
            cur = NewRoute(method, target)
            continue
        }
        if cur.method.Len() == 0 {
            fail BadLine(line: n)
        }
        try Apply(a, io, dir, mut cur, n, word, rest)
    }
    if cur.method.Len() > 0 {
        try routes.Push(cur)
    }
    return Script(routes: routes.Done()[..])
}

NewRoute(method: str, path: str): Route from method, path {
    return Route(
        method: method,
        path: path,
        status: 200,
        headers: "",
        body: NoBytes,
        coding: Coding.Identity,
        cut: -1,
        cut_unranged: false,
        stall: false,
        echo: false,
        ranges: false,
        chunked: false,
        modified: none,
        websocket: false,
    )
}

// Apply reads one directive line into r.
// Every piece of rest stored into r is copied into a first, so r holds
// loans of a alone.
Apply(a: &Arena into r, io: Io, dir: str, r: mut Route, n: int, word: str, rest: str): ScriptError!void {
    match word {
        "status" {
            r.status = int(rest.ParseInt() else { fail BadLine(line: n) })
        }
        "header" {
            colon := rest.Find(":") else { fail BadLine(line: n) }
            name := rest[..colon].Trim()
            value := rest[colon + 1..].Trim()
            prev := r.headers
            r.headers = try a.Fmt("{prev}{name}: {value}\r\n", (prev, name, value))
        }
        "body" {
            r.body = (try Unescape(a, rest)).AsBytes()
        }
        "file" {
            r.body = try fs.ReadBytes(a, io, try a.Fmt("{dir}{rest}", (dir, rest)))
        }
        "repeat" {
            (word: count, rest: unit) := SplitWord(rest)
            times := count.ParseInt() else { fail BadLine(line: n) }
            r.body = try Repeat(a, try Unescape(a, unit), times)
        }
        "gzip" {
            r.coding = Coding.Gzip
        }
        "zlib" {
            r.coding = Coding.Zlib
        }
        "cut" {
            r.cut = rest.ParseInt() else { fail BadLine(line: n) }
        }
        "cut-unranged" {
            r.cut = rest.ParseInt() else { fail BadLine(line: n) }
            r.cut_unranged = true
        }
        "stall" {
            r.stall = true
        }
        "echo" {
            r.echo = true
        }
        "ranges" {
            r.ranges = true
        }
        "chunked" {
            r.chunked = true
        }
        "websocket" {
            r.websocket = true
        }
        "modified" {
            r.modified = some(time.ParseHttpDate(rest) else { fail BadLine(line: n) })
        }
        _ {
            fail BadDirective(line: n)
        }
    }
}

// DirOf is the directory part of path with its separator, "" for a bare name.
DirOf(path: str): str {
    var end := 0
    for i in 0..path.Len() {
        b := path.Byte(i)
        if b == b'/' || b == b'\\' {
            end = i + 1
        }
    }
    return path[..end]
}

/// The first space-separated word of s and the trimmed rest.
export SplitWord(s: str): (word: str, rest: str) {
    i := s.Find(" ") else { return (word: s, rest: "") }
    return (word: s[..i], rest: s[i + 1..].Trim())
}

// Unescape reads the escapes \n, \r, \t and \\ of a script's text.
Unescape(a: &Arena, s: str): OutOfMemory!str from a {
    if !s.Has("\\") {
        return try a.Dup(s)
    }
    var t := Text.New(a)
    var i := 0
    for i < s.Len() {
        b := s.Byte(i)
        if b != b'\\' || i + 1 == s.Len() {
            try t.Push(s[i..i + 1])
            i += 1
            continue
        }
        next := s.Byte(i + 1)
        esc := match next {
            b'n' { "\n" }
            b'r' { "\r" }
            b't' { "\t" }
            _ { s[i + 1..i + 2] }
        }
        try t.Push(esc)
        i += 2
    }
    return t.Done()
}

// Repeat is unit written times times, for a large generated body.
Repeat(a: &Arena, unit: str, times: i64): OutOfMemory![]u8 from a {
    var t := Text.New(a)
    for _ in 0..times {
        try t.Push(unit)
    }
    return t.Done().AsBytes()
}
