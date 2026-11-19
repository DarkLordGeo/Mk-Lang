// http-parse, Mk: std.http.ReadRequest over a Reader with a 4,096-byte
// inline buffer on a loopback TCP stream, the same n requests as the Go
// column (a splitmix64 stream, README.md), reps times, in batches of at
// most 32,768 bytes written and then read by the one task, into an arena
// Reset after each request. Prints the checksum: the sum over every
// request of its method, path, query, body and field lengths plus its
// field count. Usage: http-parse <n> <reps>.

import std.http
import std.net

struct Rng {
    s: u64
}

Rng.Next(mut self): u64 {
    self.s = self.s +% 0x9E3779B97F4A7C15
    var z := self.s
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

const Fixed = " HTTP/1.1\r\nHost: bench.example.com\r\nUser-Agent: Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36\r\nAccept: text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8\r\nAccept-Language: en-US,en;q=0.9\r\nAccept-Encoding: gzip, deflate, br\r\n"

const Letters = "abcdefghijklmnopqrstuvwxyz"

// Build appends the n requests back to back to all, and the offset of
// each, then the end, to starts.
Build(a: &Arena, n: int, all: mut Text, starts: mut List[int]): WriteError|OutOfMemory!void {
    var g := Rng(s: 1)
    var one := Text.New(a)
    for _ in 0..n {
        try starts.Push(all.Str().Len())
        one.Clear()
        var w := one.Writer()
        r := g.Next()
        post := r % 4 == 0
        id := (r >> 8) % 1_000_000
        if post {
            try Print(mut w, "POST /api/v1/items/{id}", (id))
        } else {
            try Print(mut w, "GET /api/v1/items/{id}", (id))
        }
        if (r >> 40) & 1 == 1 {
            try Print(mut w, "?page={page}&sort=asc", (page: (r >> 41) % 100))
        }
        try w.Str(Fixed)
        try Print(mut w, "Cookie: session={c}\r\nConnection: keep-alive\r\n", (c: g.Next()))
        if post {
            l := 16 + Cast[int]((r >> 20) % 240)
            try Print(mut w, "Content-Type: application/json\r\nContent-Length: {l}\r\n\r\n", (l))
            for k in 0..l {
                try w.Str(Letters[k % 26..k % 26 + 1])
            }
        } else {
            try w.Str("\r\n")
        }
        try all.Push(one.Str())
    }
    try starts.Push(all.Str().Len())
}

// Sum is the request's share of the checksum.
Sum(req: &http.Request): u64 {
    var s := req.method.Len() + req.path.Len() + req.query.Len() + req.body.Len() + req.headers.Len()
    for k in 0..req.headers.Len() {
        s += req.headers[k].name.Len() + req.headers[k].value.Len()
    }
    return Cast[u64](s)
}

export Main(sys: &System): http.Error|ParseError!void {
    if sys.args.Len() != 3 {
        var e := sys.Stderr()
        try Print(mut e, "usage: http-parse <n> <reps>\n")
        fail ParseError
    }
    n := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    io := sys.io
    var all := Text.New(sys.arena)
    var starts := List[int].New(sys.arena)
    try Build(sys.arena, n, mut all, mut starts)
    data := all.Str().AsBytes()
    l := try net.Listen(io, "127.0.0.1:0")
    defer l.Close()
    addr := try sys.arena.Fmt("127.0.0.1:{port}", (port: l.Port()))
    out := try net.Dial(io, addr)
    defer out.Close()
    inn := try l.Accept(io)
    defer inn.Close()
    var inline: [4096]u8 = [0; 4096]
    var r := inn.Reader(io, mut inline[..])
    var a := try Arena.New(sys.heap)
    defer a.Free()
    var total: u64 = 0
    for _ in 0..reps {
        var i := 0
        for i < n {
            var j := i
            for j < n && starts[j + 1] - starts[i] <= 32768 {
                j += 1
            }
            try out.WriteAll(io, data[starts[i]..starts[j]])
            for _ in i..j {
                req := try http.ReadRequest(&a, io, mut r, 65536)
                total = total +% Sum(&req)
                a.Reset()
            }
            i = j
        }
    }
    var o := sys.Stdout()
    try Print(mut o, "{total}\n", (total))
}
