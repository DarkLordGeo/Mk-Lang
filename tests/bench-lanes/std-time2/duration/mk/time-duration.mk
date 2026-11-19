// time-duration, Mk: Duration format and parse. The task and generator are README.md's.
// Usage: time-duration <n> <rounds>.

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

// Round formats and parses every duration once, in an arena freed at its
// end.
Round(h: &Heap, ds: []time.Duration): ParseError|OutOfMemory!u64 {
    var a := try Arena.New(h)
    defer a.Free()
    var sum: u64 = 0
    for i in 0..ds.Len() {
        s := try a.Fmt("{d}", (d: ds[i]))
        p := try time.ParseDuration(s)
        sum = sum +% Cast[u64](p.Nanos() & 0x7FFF_FFFF_FFFF_FFFF) +% Cast[u64](s.Len())
    }
    return sum
}

export Main(sys: &System): ParseError|WriteError|OutOfMemory!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var g := Gen(st: 1)
    var ds := try sys.arena.Slice[time.Duration](n)
    for i in 0..n {
        v := Trunc[i64](g.Next())
        k := Cast[int](g.Next() % 64)
        ds[i] = time.Nanos(v >> k)
    }
    var sum: u64 = 0
    for _ in 0..rounds {
        sum = sum +% try Round(sys.heap, ds[..])
    }
    try sys.Print("{sum}\n", (sum))
}
