// sha256-4k, Mk: std.hash.Sha256 (SHA-256) over 4096-byte messages held in
// L1, the block loop's case (TLS records, file chunks), where an
// instruction path shows: <kib> KiB of splitmix64 bytes from <seed> (each
// output little-endian) cut into 4096-byte messages, every message hashed
// once per pass, <reps> passes; after each pass the byte at r % n is xored
// with the low byte of the running xor, so no pass can be hoisted. Prints
// the xor of every digest's first 8 bytes, little-endian. Usage: sha256-4k
// <kib> <reps> <seed>.

import std.hash

const Msg = 4096

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
    kib := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    n := kib * 1024
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
        for j + Msg <= n {
            sum = sum ^ Lo64(hash.Sha256(buf[j..j + Msg])[..])
            j += Msg
        }
        buf[r % n] = buf[r % n] ^ Trunc[u8](sum)
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
