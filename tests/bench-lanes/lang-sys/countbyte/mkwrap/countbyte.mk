// countbyte, Mk, wrapping (the mk-wrap column): mk/countbyte.mk with
// the count's += written as the wrapping +% (n = n +% ..., in the vector
// loop and the tail), so no overflow check, and no nsw either (not C's
// semantics). Everything else, FromSlice's span check included, is the
// mk column's. The ratio mk / c holds the overflow check's price, mk-wrap
// / c what is left without it. Usage: countbyte <n> <reps> <seed>.

import std.simd

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

// CountByte is the reference block with its count wrapping.
CountByte(xs: []u8, b: u8): int {
    var n := 0
    var i := 0
    needle := simd.Vec[u8, 16].Splat(b)
    for i + 16 <= xs.Len() {
        v := simd.Vec[u8, 16].FromSlice(xs, i)
        n = n +% (v == needle).CountSet()
        i += 16
    }
    for i < xs.Len() {
        if xs[i] == b { n = n +% 1 }
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
