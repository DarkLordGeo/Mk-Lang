// http-stream, Mk: n POST requests through one std.http.Client
// (DoStreaming, keep-alive), each streaming a 65,536-byte file as its
// body: chunked for an even request, with Content-Length for an odd one.
// The responder is a task in the same program, the Go column's code, with
// no HTTP library: it reads a head to its blank line, reads the body by
// the framing the head names (Content-Length, or chunked with no
// extensions or trailers), and answers with the body's byte count times
// 1,000,003 plus the sum of its bytes, in decimal. One arena, Reset after
// each request. Prints the checksum: the sum over every reply of its
// status and that number. Usage: http-stream <n>.

import std.fs
import std.http
import std.net
import std.url

const Size = 65536

// In is the responder's buffered view of its connection: rb[s..e] holds
// bytes read and not yet used.
struct In {
    rb: [16384]u8
    s: int
    e: int
}

// Fill reads more bytes into rb when none are left: false at the end.
Fill(io: Io, c: &net.Stream, b: mut In): bool {
    if b.s < b.e {
        return true
    }
    n := c.Read(io, mut b.rb[..]) else {
        return false
    }
    b.s = 0
    b.e = n
    return n > 0
}

// Line reads one line, its LF included, into l: its length, 0 at the end
// or for a line longer than l.
Line(io: Io, c: &net.Stream, b: mut In, l: mut [256]u8): int {
    var n := 0
    for {
        if !Fill(io, c, b) {
            return 0
        }
        x := b.rb[b.s]
        b.s += 1
        if n == 256 {
            return 0
        }
        l[n] = x
        n += 1
        if x == 10 {
            return n
        }
    }
}

// Body reads n bytes, adding them to count and sum: false at the end.
Body(io: Io, c: &net.Stream, b: mut In, n: int, count: mut u64, sum: mut u64): bool {
    var left := n
    for left > 0 {
        if !Fill(io, c, b) {
            return false
        }
        k := if b.e - b.s > left { left } else { b.e - b.s }
        var t: u64 = 0
        for x in b.rb[b.s..b.s + k] {
            t = t +% u64(x)
        }
        *sum = *sum +% t
        *count = *count +% Cast[u64](k)
        b.s += k
        left -= k
    }
    return true
}

// Starts: line begins with p, letters compared without case (each byte
// ORed with 32; the patterns hold only letters, "-", ":" and " ", which
// that leaves alone), since field names are case-insensitive and Zig's
// client sends them in lower case.
Starts(line: []u8, p: str): bool {
    pb := p.AsBytes()
    if line.Len() < pb.Len() {
        return false
    }
    for i in 0..pb.Len() {
        if (line[i] | 32) != (pb[i] | 32) {
            return false
        }
    }
    return true
}

// Number is the number in base (10 or 16) at the start of b.
Number(b: []u8, base: int): int {
    var v := 0
    for x in b {
        var d := -1
        if x >= 48 && x <= 57 {
            d = int(x - 48)
        } else if base == 16 && x >= 97 && x <= 102 {
            d = int(x - 87)
        } else if base == 16 && x >= 65 && x <= 70 {
            d = int(x - 55)
        }
        if d < 0 {
            return v
        }
        v = v * base + d
    }
    return v
}

// Respond answers the requests of one connection until it ends.
Respond(io: Io, l: &net.Listener) {
    c := l.Accept(io) else {
        return
    }
    var b := In(rb: [0; 16384], s: 0, e: 0)
    var lb: [256]u8 = [0; 256]
    var wb: [256]u8 = [0; 256]
    for {
        var chunked := false
        var length := -1
        for {
            n := Line(io, &c, mut b, mut lb)
            if n == 0 {
                c.Close()
                return
            }
            if n == 2 {
                break
            }
            if Starts(lb[0..n], "Transfer-Encoding: chunked") {
                chunked = true
            } else if Starts(lb[0..n], "Content-Length: ") {
                length = Number(lb[16..n], 10)
            }
        }
        var count: u64 = 0
        var sum: u64 = 0
        var ok := true
        if chunked {
            for ok {
                n := Line(io, &c, mut b, mut lb)
                if n == 0 {
                    ok = false
                    break
                }
                k := Number(lb[0..n], 16)
                if k == 0 {
                    ok = Line(io, &c, mut b, mut lb) == 2
                    break
                }
                ok = Body(io, &c, mut b, k, mut count, mut sum) && Line(io, &c, mut b, mut lb) == 2
            }
        } else if length > 0 {
            ok = Body(io, &c, mut b, length, mut count, mut sum)
        }
        if !ok {
            c.Close()
            return
        }
        // The reply: the head, then the decimal value.
        var digits: [24]u8 = [0; 24]
        var d := 24
        var x := count *% 1000003 +% sum
        for {
            d -= 1
            digits[d] = Trunc[u8](48 + x % 10)
            x = x / 10
            if x == 0 {
                break
            }
        }
        dn := 24 - d
        head := "HTTP/1.1 200 OK\r\nContent-Length: ".AsBytes()
        (mut wb[0..head.Len()]).CopyFrom(head)
        var w := head.Len()
        if dn >= 10 {
            wb[w] = Trunc[u8](48 + dn / 10)
            w += 1
        }
        wb[w] = Trunc[u8](48 + dn % 10)
        w += 1
        (mut wb[w..w + 4]).CopyFrom("\r\n\r\n".AsBytes())
        w += 4
        (mut wb[w..w + dn]).CopyFrom(digits[d..24])
        w += dn
        c.WriteAll(io, wb[0..w]) else {
            c.Close()
            return
        }
    }
}

// Run streams the file at path n times with one client: the checksum.
Run(h: &Heap, io: Io, port: int, path: str, n: int): http.Error|url.Error|fs.Error|OutOfMemory|ParseError!u64 {
    c := http.Client.New(h, http.DefaultClientLimits)
    defer c.Close()
    var a := try Arena.New(h)
    defer a.Free()
    var total: u64 = 0
    hs: [0]http.Header = []
    var fb: [16384]u8 = [0; 16384]
    for i in 0..n {
        text := try a.Fmt("http://127.0.0.1:{port}/up/{i}", (port, i))
        u := try url.Parse(&a, text, none)
        req := http.ClientRequest(method: "POST", url: &u, headers: hs[..], body: "".AsBytes())
        var f := try fs.Open(io, path)
        onerr f.Abandon()
        len: ?i64 = if i % 2 == 1 { some(i64(Size)) } else { none }
        var r := f.Reader(io, mut fb[..])
        var ex := try c.DoStreaming(&a, io, &req, mut r, len)
        onerr ex.Abandon()
        var vb: [32]u8 = [0; 32]
        var none_buf: [0]u8 = []
        var body := ex.Body(mut none_buf[..])
        var got := 0
        for {
            k := try body.Read(mut vb[got..32])
            if k == 0 {
                break
            }
            got += k
        }
        status := ex.status
        try ex.Close(io)
        try f.Close(io)
        v := try str.From(vb[0..got]).Must().ParseInt()
        total = total +% Cast[u64](status) +% Cast[u64](v)
        a.Reset()
    }
    return total
}

export Main(sys: &System): http.Error|url.Error|fs.Error|ParseError|OutOfMemory|WriteError!void {
    if sys.args.Len() != 2 {
        var e := sys.Stderr()
        try Print(mut e, "usage: http-stream <n>\n")
        fail ParseError
    }
    n := Cast[int](try sys.args[1].ParseInt())
    io := sys.io
    var data := try sys.arena.Slice[u8](Size)
    for i in 0..Size {
        data[i] = Trunc[u8](97 + i % 26)
    }
    dir := try fs.TempDir(sys.arena, io)
    (file: f0, path: path) := try fs.CreateTemp(sys.arena, io, dir, "http-stream-")
    var f := f0
    onerr f.Abandon()
    try f.WriteAll(io, data[..])
    try f.Close(io)
    defer fs.Remove(io, path) else {}
    l := try net.Listen(io, "127.0.0.1:0")
    defer l.Close()
    port := l.Port()
    var total: u64 = 0
    // Run's client closes its connection at the end, which ends Respond.
    scope top := io.Scope() {
        top.go Respond(io, &l)
        total = try Run(sys.heap, io, port, path, n)
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
    try out.Flush()
}
