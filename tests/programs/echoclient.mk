// The echo client run inside WSL when loopback forwarding from Windows is unavailable
// to a server there. Args: port, connections, bytes per connection.
// Each task sends its bytes in writes of varying size, reads them back, and compares;
// the program prints "ok <connections>" when every echo matched, else exits 1.

import std.net
import std.atomic

error Mismatch

Pattern(i: int, k: int): u8 {
    return Trunc[u8](i *% 31 +% k *% 7)
}

Client(io: Io, addr: str, k: int, size: int, bad: &atomic.AtomicInt) {
    conn := net.Dial(io, addr) else {
        _ = bad.Add(1)
        return
    }
    defer conn.Close()
    var chunk: [1024]u8 = [0; 1024]
    var back: [1024]u8 = [0; 1024]
    var sent := 0
    for sent < size {
        var n := 1 + (sent *% 13 +% k) % 1024
        if n > size - sent {
            n = size - sent
        }
        for j in 0..n {
            chunk[j] = Pattern(sent + j, k)
        }
        conn.WriteAll(io, chunk[..n]) else {
            _ = bad.Add(1)
            return
        }
        var got := 0
        for got < n {
            m := conn.Read(io, mut back[got..n]) else {
                _ = bad.Add(1)
                return
            }
            if m == 0 {
                _ = bad.Add(1)
                return
            }
            got += m
        }
        for j in 0..n {
            if back[j] != chunk[j] {
                _ = bad.Add(1)
                return
            }
        }
        sent += n
    }
}

export Main(sys: &System): Mismatch|OutOfMemory|ParseError|WriteError!void {
    port := try sys.args[1].ParseInt()
    count := try sys.args[2].ParseInt()
    size := try sys.args[3].ParseInt()
    addr := try sys.arena.Fmt("127.0.0.1:{port}", (port))
    bad := atomic.AtomicInt.New(0)
    scope s := sys.io.Scope() {
        for k in 0..Trunc[int](count) {
            s.go Client(sys.io, addr, k, Trunc[int](size), &bad)
        }
    }
    if bad.Load() != 0 {
        fail Mismatch
    }
    var out := sys.Stdout()
    try Print(mut out, "ok {count}\n", (count))
}
