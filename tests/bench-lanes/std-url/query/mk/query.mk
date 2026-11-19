// url-query, Mk: std.url's ParseQuery. n queries made from splitmix64
// output (seed 1) by the generator README.md states, then rounds times:
// parse each into a fresh arena per round, freed at the round's end. The
// checksum adds, per query and round, the length of every name and every
// value.
// Usage: url-query <n> <rounds>.

import std.url

const Pieces: [48]str = ["a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k", "l", "m", "n", "o", "p", "q", "r", "s", "t", "u", "v", "w", "x", "y", "z", "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "-", ".", "_", "~", "+", "+", "+", "+", "%20", "%2B", "%C3%A9", "%26"]

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

MakeQuery(a: &Arena, g: mut Gen): OutOfMemory!str from a {
    var t := Text.New(a)
    pairs := 1 + g.Pick(8)
    for p in 0..pairs {
        if p > 0 {
            try t.Push("&")
        }
        z := g.Next()
        k := if z % 4 == 0 { 48 } else { 40 }
        name := 1 + Cast[int]((z >> 8) % 8)
        for _ in 0..name {
            try t.Push(Pieces[g.Pick(k)])
        }
        try t.Push("=")
        value := Cast[int]((z >> 16) % 12)
        for _ in 0..value {
            try t.Push(Pieces[g.Pick(k)])
        }
    }
    return try a.Dup(t.Str())
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var g := Gen(st: 1)
    var queries := List[str].New(sys.arena)
    for _ in 0..n {
        try queries.Push(try MakeQuery(sys.arena, mut g))
    }
    var total: u64 = 0
    for _ in 0..rounds {
        // A fresh arena each round, freed at its end: Go gets no warm
        // memory either.
        var a := try Arena.New(sys.heap)
        defer a.Free()
        for q in queries {
            pairs := try url.ParseQuery(&a, q)
            for (name, value) in pairs {
                total = total +% Cast[u64](name.Len() + value.Len())
            }
        }
    }
    var w := sys.Stdout()
    try Print(mut w, "{total}\n", (total))
}
