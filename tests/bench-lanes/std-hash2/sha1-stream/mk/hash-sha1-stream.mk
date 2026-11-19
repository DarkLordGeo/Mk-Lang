// hash-sha1-stream, Mk: std.hash.Sha1State (SHA-1) fed in 1,500-byte pieces (one
// Ethernet payload each; 1,500 is not a multiple of the block, so every
// piece but the first meets a partial block): <mib> MiB of splitmix64
// bytes from <seed> (each output little-endian), one fresh state per pass
// fed every piece in order (the last piece is the remainder,
// 1,048,576 * mib mod 1,500 bytes), then Sum; <reps> passes; after each
// pass the byte at r % n is xored with the digest's low byte, so no pass
// can be hoisted. Prints the xor of every digest's first 8 bytes,
// little-endian. Usage: hash-sha1-stream <mib> <reps> <seed>.

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
        var s := hash.Sha1State.New()
        var j := 0
        for j < n {
            var e := j + 1500
            if e > n {
                e = n
            }
            s.Bytes(buf[j..e])
            j = e
        }
        h := Lo64(s.Sum()[..])
        sum = sum ^ h
        buf[r % n] = buf[r % n] ^ Trunc[u8](h)
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
