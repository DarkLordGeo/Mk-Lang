// hash-xx-short, Mk: std.hash.Xx (XXH3-64, seed 0) over 16-byte keys, the
// hash-table case: <mib> MiB of splitmix64 bytes from <seed> (each output
// little-endian) cut into 16-byte keys, every key hashed once per pass,
// <reps> passes; after each pass the byte at r % n is xored with the low
// byte of the running xor, so no pass can be hoisted. Prints the xor of
// every hash. Usage: hash-xx-short <mib> <reps> <seed>.

import std.hash

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
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
        var j := 0
        for j < n {
            sum = sum ^ hash.Xx(buf[j..j + 16])
            j += 16
        }
        buf[r % n] = buf[r % n] ^ Trunc[u8](sum)
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
