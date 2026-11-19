// countbyte, Mk: doc/reference.md's CountByte block, a byte
// count in 16-lane steps with std.simd (Splat, FromSlice, ==, CountSet)
// and a scalar tail, over <n> bytes generated from <seed> (README.md),
// <reps> passes, pass r counting the byte r % 256; prints the wrapping sum
// of each pass's count times (r % 1000 + 1). The buffer is an arena slice,
// whose alignment Mk code cannot set without std.raw (BS-4's labelled
// exception, README.md). Usage: countbyte <n> <reps> <seed>.

import std.simd

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

// CountByte is the reference block, unchanged.
CountByte(xs: []u8, b: u8): int {
    var n := 0
    var i := 0
    needle := simd.Vec[u8, 16].Splat(b)
    for i + 16 <= xs.Len() {
        v := simd.Vec[u8, 16].FromSlice(xs, i)
        n += (v == needle).CountSet()
        i += 16
    }
    for i < xs.Len() {
        if xs[i] == b { n += 1 }
        i += 1
    }
    return n
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    seed := Cast[u64](try sys.args[3].ParseInt())
    buf := try sys.arena.Slice[u8](n)
    var st := seed
    for i in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        buf[i] = Trunc[u8](Mix(st))
    }
    var sum: u64 = 0
    for r in 0..reps {
        sum = sum +% Cast[u64](CountByte(buf[..], Trunc[u8](r))) *% Cast[u64](r % 1000 + 1)
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
