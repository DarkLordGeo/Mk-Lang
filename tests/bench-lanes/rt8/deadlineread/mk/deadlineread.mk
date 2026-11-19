// deadlineread, Mk: n round trips of one byte over a loopback TCP
// connection; an echo task reads each byte and writes it back, and the
// main task writes i % 251, then reads the echo. With deadline 1 each of
// the main task's reads runs in a scope of its own with a one-second
// deadline (Scope.CancelAfter, a one-operation timeout), which never
// fires; with 0 the read is bare. sum = 31 sum + the byte read (wrapping).
// Prints n, sum and the failed calls. Usage: deadlineread <n> <deadline>.

import std.net
import std.time

Echo(io: Io, l: &net.Listener, n: int) {
    s := l.Accept(io) else {
        return
    }
    var b: [1]u8 = [0]
    for _ in 0..n {
        k := s.Read(io, mut b[..]) else {
            break
        }
        if k != 1 {
            break
        }
        s.WriteAll(io, b[..]) else {
            break
        }
    }
    s.Close()
}

// Addr writes 127.0.0.1:port into b.
Addr(b: mut [32]u8, port: int): str from b {
    var n := 0
    for x in "127.0.0.1:".AsBytes() {
        b[n] = x
        n += 1
    }
    var d: [8]u8 = [0; 8]
    var k := 0
    var p := port
    for {
        d[k] = Trunc[u8](48 + p % 10)
        k += 1
        p = p / 10
        if p == 0 {
            break
        }
    }
    for k > 0 {
        k -= 1
        b[n] = d[k]
        n += 1
    }
    return str.From(b[0..n]).Must()
}

// ReadOne reads one byte of c, in a scope with a one-second deadline when
// timed: the byte, or -1 on a failure.
ReadOne(io: Io, c: &net.Stream, timed: bool): int {
    var b: [1]u8 = [0]
    var k := -1
    if timed {
        scope s := io.Scope() {
            s.CancelAfter(time.Seconds(1))
            k = c.Read(io, mut b[..]) else { -1 }
        }
    } else {
        k = c.Read(io, mut b[..]) else { -1 }
    }
    return if k == 1 { int(b[0]) } else { -1 }
}

export Main(sys: &System): ParseError|WriteError|net.Error!void {
    io := sys.io
    n := Cast[int](try sys.args[1].ParseInt())
    timed := (try sys.args[2].ParseInt()) == 1
    l := try net.Listen(io, "127.0.0.1:0")
    defer l.Close()
    var sum: u64 = 0
    var fails := 0
    scope s := io.Scope() {
        s.go Echo(io, &l, n)
        var ab: [32]u8 = [0; 32]
        c := net.Dial(io, Addr(mut ab, l.Port())) else e {
            s.Cancel()
            fail e
        }
        for i in 0..n {
            var w: [1]u8 = [Trunc[u8](i % 251)]
            c.WriteAll(io, w[..]) else {
                fails += 1
                break
            }
            v := ReadOne(io, &c, timed)
            if v < 0 {
                fails += 1
                break
            }
            sum = sum *% 31 +% Cast[u64](v)
        }
        c.Close()
    }
    var out := sys.Stdout()
    try Print(mut out, "{n} {sum} {fails}\n", (n, sum, fails))
}
