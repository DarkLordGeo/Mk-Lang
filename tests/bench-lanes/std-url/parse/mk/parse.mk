// url-parse, Mk: std.url's Parse. n URLs made from splitmix64 output (seed
// 1) by the generator README.md states (one in eight with an upper-case
// scheme and first host label, one in eight with an IPv4 host, the rest
// already in the form every parser here keeps), then rounds times: parse
// each into a fresh arena per round, freed at the round's end. The
// checksum adds, per URL and round, the lengths of the scheme, host, path,
// query and fragment, and the port.
// Usage: url-parse <n> <rounds>.

import std.url

const Alnum: str = "abcdefghijklmnopqrstuvwxyz0123456789"
const UpperAlnum: str = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
const PathChars: str = "abcdefghijklmnopqrstuvwxyz0123456789-_~"

struct Gen {
    st: u64
}

Gen.Next(mut self): u64 {
    self.st = self.st +% 0x9E3779B97F4A7C15
    var z := self.st
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

// Pick is a number below n.
Gen.Pick(mut self, n: int): int {
    return Cast[int](self.Next() % Cast[u64](n))
}

// Word pushes count characters drawn from chars.
Word(t: mut Text, g: mut Gen, chars: str, count: int): OutOfMemory!void {
    b := chars.AsBytes()
    for _ in 0..count {
        k := g.Pick(b.Len())
        try t.Push(chars[k..k + 1])
    }
}

MakeUrl(a: &Arena, g: mut Gen): OutOfMemory!str from a {
    z := g.Next()
    schemes: [5]str = ["http", "https", "ws", "wss", "ftp"]
    upper_schemes: [5]str = ["HTTP", "HTTPS", "WS", "WSS", "FTP"]
    ports: [4]int = [0, 8080, 8443, 3000]
    tlds: [4]str = ["com", "org", "net", "dev"]
    var t := Text.New(a)
    upper := (z >> 44) % 8 == 0
    try t.Push(if upper { upper_schemes[Cast[int](z % 5)] } else { schemes[Cast[int](z % 5)] })
    try t.Push("://")
    if (z >> 47) % 8 == 0 {
        x := g.Pick(256)
        y := g.Pick(256)
        w := g.Pick(256)
        try t.Push(try a.Fmt("10.{x}.{y}.{w}", (x, y, w)))
    } else {
        labels := 1 + Cast[int]((z >> 16) % 3)
        for k in 0..labels {
            try Word(mut t, g, if upper && k == 0 { UpperAlnum } else { Alnum }, 3 + g.Pick(8))
            try t.Push(".")
        }
        try t.Push(tlds[Cast[int]((z >> 24) % 4)])
    }
    port := ports[Cast[int]((z >> 8) % 4)]
    if port != 0 {
        try t.Push(try a.Fmt(":{port}", (port)))
    }
    try t.Push("/")
    segments := Cast[int]((z >> 32) % 6)
    for s in 0..segments {
        if s > 0 {
            try t.Push("/")
        }
        try Word(mut t, g, PathChars, 1 + g.Pick(12))
    }
    if segments > 0 && (z >> 40) & 1 == 1 {
        try t.Push("/")
    }
    if (z >> 41) & 1 == 1 {
        try t.Push("?")
        pairs := 1 + g.Pick(3)
        for p in 0..pairs {
            if p > 0 {
                try t.Push("&")
            }
            try Word(mut t, g, Alnum, 1 + g.Pick(6))
            try t.Push("=")
            try Word(mut t, g, Alnum, g.Pick(8))
        }
    }
    if (z >> 42) & 3 == 0 {
        try t.Push("#")
        try Word(mut t, g, Alnum, 1 + g.Pick(10))
    }
    return try a.Dup(t.Str())
}

export Main(sys: &System): url.Error|OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var g := Gen(st: 1)
    var urls := List[str].New(sys.arena)
    for _ in 0..n {
        try urls.Push(try MakeUrl(sys.arena, mut g))
    }
    var total: u64 = 0
    for _ in 0..rounds {
        // A fresh arena each round, freed at its end: Go and Zig get no
        // warm memory either.
        var a := try Arena.New(sys.heap)
        defer a.Free()
        for text in urls {
            u := try url.Parse(&a, text, none)
            port := if p := u.Port() { u64(p) } else { 0 }
            total = total +% Cast[u64](u.Scheme().Len() + u.Hostname().Len() + u.Path().Len() + u.Query().Len() + u.Fragment().Len()) +% port
        }
    }
    var w := sys.Stdout()
    try Print(mut w, "{total}\n", (total))
}
