// crypto-ed25519-keygen, Mk: std.crypto.Ed25519.FromSeed of <n> 32-byte
// seeds. From <seed>, splitmix64 bytes (each output little-endian): the n
// seeds back to back. Prints the xor of every public key's bytes 0..8 and
// 24..32 (little-endian). Usage: crypto-ed25519-keygen <n> <seed>.

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
    n := Cast[int](try sys.args[1].ParseInt())
    var st := Cast[u64](try sys.args[2].ParseInt())
    seeds := try sys.arena.Slice[u8](32 * n)
    Fill(mut st, mut seeds[..])
    var sum: u64 = 0
    var seed: [32]u8 = [0; 32]
    for i in 0..n {
        for j in 0..32 {
            seed[j] = seeds[32 * i + j]
        }
        kp := crypto.Ed25519.FromSeed(&seed)
        sum = sum ^ Lo64(kp[32..40]) ^ Lo64(kp[56..64])
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
