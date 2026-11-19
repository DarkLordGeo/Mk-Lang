// crypto-hmac384, Mk: std.crypto.Hmac384 of every 64-byte message of
// <mib> MiB of splitmix64 bytes from <seed> (each output little-endian;
// the key the next 48 bytes), <reps> passes; after each message the
// key's first byte is xored with the tag's, so no call can be hoisted.
// Prints the xor of every tag's first 8 bytes (little-endian). Usage:
// crypto-hmac384 <mib> <reps> <seed>.

import std.crypto

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

// Fill writes splitmix64 outputs from st, little-endian, over b.
Fill(st: mut u64, b: mut []u8) {
    var i := 0
    for i < b.Len() {
        *st = *st +% 0x9e3779b97f4a7c15
        x := Mix(*st)
        var k := 0
        for k < 8 && i < b.Len() {
            b[i] = Trunc[u8](x >> (8 * k))
            k += 1
            i += 1
        }
    }
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
    Fill(mut st, mut buf[..])
    var key: [48]u8 = [0; 48]
    Fill(mut st, mut key[..])
    var sum: u64 = 0
    for _ in 0..reps {
        for j in 0..n / 64 {
            t := crypto.Hmac384(key[..], buf[64 * j..64 * j + 64])
            sum = sum ^ Lo64(t[..])
            key[0] = key[0] ^ t[0]
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
