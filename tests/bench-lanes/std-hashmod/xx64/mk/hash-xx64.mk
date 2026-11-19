// hash-xx64, Mk: std.hash over XXH64, seed 0: <mib> MiB of splitmix64 bytes from
// <seed> (each output little-endian), hashed <reps> times; after each hash
// the byte at r % n is xored with the result's low byte, so no hash can be
// hoisted. Prints the xor of the results (a digest's first 8 bytes,
// little-endian). Usage: hash-xx64 <mib> <reps> <seed>.

import std.hash

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Lo64(d: []u8): u64 {
    var x: u64 = 0
    for k in 0..8 {
        x = x | (u64(d[k]) << (8 * k))
    }
    return x
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    mib := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    n := mib * 1048576
    buf := try sys.arena.Slice[u8](n)
    var i := 0
    for i < n {
        st = st +% 0x9e3779b97f4a7c15
        x := Mix(st)
        for k in 0..8 {
            buf[i + k] = Trunc[u8](x >> (8 * k))
        }
        i += 8
    }
    var sum: u64 = 0
    for r in 0..reps {
        h := hash.Xx64(buf[..], 0)
        sum = sum ^ h
        buf[r % n] = buf[r % n] ^ Trunc[u8](h)
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
