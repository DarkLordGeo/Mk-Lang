// hash-blake3-short, Mk: std.hash.Blake3 (BLAKE3, 32-byte output) over 64-byte messages, the short-input
// case (HMAC keys and blocks, Ed25519's hashes, tree nodes): <mib> MiB of
// splitmix64 bytes from <seed> (each output little-endian) cut into 64-byte
// messages, every message hashed once per pass, <reps> passes; after each
// pass the byte at r % n is xored with the low byte of the running xor, so
// no pass can be hoisted. Prints the xor of every digest's first 8 bytes,
// little-endian. Usage: hash-blake3-short <mib> <reps> <seed>.

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
        var j := 0
        for j < n {
            sum = sum ^ Lo64(hash.Blake3(buf[j..j + 64])[..])
            j += 64
        }
        buf[r % n] = buf[r % n] ^ Trunc[u8](sum)
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
