// addrparse, Mk, std.net: Format and ParseAddr round trips of splitmix64
// addresses (README.md). Usage: addrparse <n>.

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

export Main(sys: &System): ParseError|WriteError|OutOfMemory|net.Error!void {
    n := Cast[int](try sys.args[1].ParseInt())
    var r := Rng(s: 1)
    var t := Text.New(sys.arena)
    var sum: u64 = 0
    for _ in 0..n {
        x := r.Next()
        var ip: [16]u8 = [0; 16]
        v6 := x & 1 == 1
        if !v6 {
            ip[0] = Trunc[u8](x >> 8)
            ip[1] = Trunc[u8](x >> 16)
            ip[2] = Trunc[u8](x >> 24)
            ip[3] = Trunc[u8](x >> 32)
        } else {
            y := r.Next()
            z := r.Next()
            zero := (x >> 8) & (x >> 16)
            for k in 0..8 {
                var w := y
                if k >= 4 {
                    w = z
                }
                var g := (w >> Trunc[u64](16 * (k % 4))) & 0xFFFF
                if (zero >> Trunc[u64](k)) & 1 == 1 {
                    g = 0
                }
                if k == 0 {
                    g = g | 0x2000
                }
                ip[2 * k] = Trunc[u8](g >> 8)
                ip[2 * k + 1] = Trunc[u8](g)
            }
        }
        a := net.Addr(ip: ip, port: Trunc[u16]((x >> 40) & 0xFFFF), v6: v6)
        t.Clear()
        var w := t.Writer()
        try a.Format(mut w)
        try w.Flush()
        s := t.Str()
        p := try net.ParseAddr(s)
        sum = sum *% 31 +% u64(p.port)
        var m := 4
        if p.v6 {
            m = 16
        }
        for k in 0..m {
            sum = sum *% 31 +% u64(p.ip[k])
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{n} {sum}\n", (n, sum))
}
