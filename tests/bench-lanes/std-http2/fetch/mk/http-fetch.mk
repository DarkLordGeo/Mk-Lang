// http-fetch, Mk: n GET requests through one std.http.Client (Fetch,
// keep-alive, so every request after the first reuses one connection) to
// a responder in a task of the same program, over loopback. The responder
// is the same in both columns and uses no HTTP library: it reads a
// request's head to its blank line, takes the id from the target and
// writes a fixed head with a body of 16 + id % 1024 letters. The targets
// come from a splitmix64 stream (seed 1, README.md). One arena, Reset
// after each request. Prints the checksum: the sum over every reply of
// its status, its body's length and its first and last bytes. Usage:
// http-fetch <n>.

import std.http
import std.net
import std.url

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

const Letters = "abcdefghijklmnopqrstuvwxyz"

// HeadEnd is the offset just past the first CRLF CRLF in b, -1 when b
// holds none.
HeadEnd(b: []u8): int {
    if b.Len() < 4 {
        return -1
    }
    for i in 0..b.Len() - 3 {
        if b[i] == 13 && b[i + 1] == 10 && b[i + 2] == 13 && b[i + 3] == 10 {
            return i + 4
        }
    }
    return -1
}

// Id is the decimal number after "/items/" in the request line of h.
Id(h: []u8): int {
    var i := 0
    for i < h.Len() && h[i] != 47 {
        i += 1
    }
    i += 7
    var id := 0
    for i < h.Len() && h[i] >= 48 && h[i] <= 57 {
        id = id * 10 + int(h[i] - 48)
        i += 1
    }
    return id
}

// Respond answers the requests of one connection until it ends.
Respond(io: Io, l: &net.Listener) {
    c := l.Accept(io) else {
        return
    }
    var rb: [16384]u8 = [0; 16384]
    var wb: [2048]u8 = [0; 2048]
    var e := 0
    for {
        var end := HeadEnd(rb[0..e])
        for end < 0 {
            n := c.Read(io, mut rb[e..rb.Len()]) else {
                c.Close()
                return
            }
            if n == 0 {
                c.Close()
                return
            }
            e += n
            end = HeadEnd(rb[0..e])
        }
        size := 16 + Id(rb[0..end]) % 1024
        head := "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\nContent-Length: ".AsBytes()
        (mut wb[0..head.Len()]).CopyFrom(head)
        var w := head.Len()
        var digits: [8]u8 = [0; 8]
        var d := 8
        var x := size
        for {
            d -= 1
            digits[d] = Trunc[u8](48 + x % 10)
            x = x / 10
            if x == 0 {
                break
            }
        }
        (mut wb[w..w + 8 - d]).CopyFrom(digits[d..8])
        w += 8 - d
        (mut wb[w..w + 4]).CopyFrom("\r\n\r\n".AsBytes())
        w += 4
        lb := Letters.AsBytes()
        for k in 0..size {
            wb[w + k] = lb[k % 26]
        }
        w += size
        c.WriteAll(io, wb[0..w]) else {
            c.Close()
            return
        }
        k := e - end
        for y in 0..k {
            rb[y] = rb[end + y]
        }
        e = k
    }
}

// Run fetches the n targets with one client: the checksum.
Run(h: &Heap, io: Io, port: int, n: int): http.Error|url.Error|OutOfMemory!u64 {
    c := http.Client.New(h, http.DefaultClientLimits)
    defer c.Close()
    var a := try Arena.New(h)
    defer a.Free()
    var g := Rng(s: 1)
    var total: u64 = 0
    hs: [0]http.Header = []
    for _ in 0..n {
        id := g.Next() % 1_000_000
        text := try a.Fmt("http://127.0.0.1:{port}/items/{id}", (port, id))
        u := try url.Parse(&a, text, none)
        req := http.ClientRequest(method: "GET", url: &u, headers: hs[..], body: "".AsBytes())
        r := try c.Fetch(&a, io, &req)
        b := r.body
        total = total +% Cast[u64](r.status) +% Cast[u64](b.Len())
        if b.Len() > 0 {
            total = total +% u64(b[0]) +% u64(b[b.Len() - 1])
        }
        a.Reset()
    }
    return total
}

export Main(sys: &System): http.Error|url.Error|ParseError|OutOfMemory|WriteError!void {
    if sys.args.Len() != 2 {
        var e := sys.Stderr()
        try Print(mut e, "usage: http-fetch <n>\n")
        fail ParseError
    }
    n := Cast[int](try sys.args[1].ParseInt())
    io := sys.io
    l := try net.Listen(io, "127.0.0.1:0")
    defer l.Close()
    port := l.Port()
    var total: u64 = 0
    // Run's client closes its connection at the end, which ends Respond.
    scope top := io.Scope() {
        top.go Respond(io, &l)
        total = try Run(sys.heap, io, port, n)
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
    try out.Flush()
}
