// time-httpdate, Mk: HTTP date format and parse. The task and generator are README.md's.
// Usage: time-httpdate <n> <rounds>.

import std.time

struct Gen {
    st: u64
}

// Next is splitmix64's next output.
Gen.Next(mut self): u64 {
    self.st = self.st +% 0x9E3779B97F4A7C15
    var z := self.st
    z = (z ^ (z >> 30)) *% 0xBF58476D1CE4E5B9
    z = (z ^ (z >> 27)) *% 0x94D049BB133111EB
    return z ^ (z >> 31)
}

// Instants makes n instants of 1970 to 2099 from g: whole seconds below
// 4,102,444,800, then nanoseconds below 10^9 (none when whole).
Instants(a: &Arena, g: mut Gen, n: int, whole: bool): OutOfMemory![]time.Time from a {
    var ts := try a.Slice[time.Time](n)
    for i in 0..n {
        secs := Cast[i64](g.Next() % 4_102_444_800)
        ns := if whole { 0 } else { Cast[i64](g.Next() % 1_000_000_000) }
        ts[i] = time.FromUnix(secs, ns)
    }
    return ts[..]
}

// Round formats and parses every instant once, in an arena freed at its end.
Round(h: &Heap, ts: []time.Time): ParseError|OutOfMemory!u64 {
    var a := try Arena.New(h)
    defer a.Free()
    var sum: u64 = 0
    for i in 0..ts.Len() {
        s := try time.FormatHttpDate(&a, ts[i])
        p := try time.ParseHttpDate(s)
        sum = sum +% Cast[u64](p.Unix()) +% Cast[u64](s.Len())
    }
    return sum
}

export Main(sys: &System): ParseError|WriteError|OutOfMemory!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var g := Gen(st: 1)
    ts := try Instants(sys.arena, mut g, n, true)
    var sum: u64 = 0
    for _ in 0..rounds {
        sum = sum +% try Round(sys.heap, ts)
    }
    try sys.Print("{sum}\n", (sum))
}
