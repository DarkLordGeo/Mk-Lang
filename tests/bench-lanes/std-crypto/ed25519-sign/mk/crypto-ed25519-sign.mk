// crypto-ed25519-sign, Mk: std.crypto.Ed25519.Sign of <n> 64-byte messages
// under one key. From <seed>, splitmix64 bytes (each output little-endian):
// first the key's 32-byte seed, then the n * 64 message bytes. The key is
// Ed25519.FromSeed of the seed (Go NewKeyFromSeed, Zig
// KeyPair.generateDeterministic). Prints the xor of every signature's bytes
// 0..8 and 32..40 (little-endian). Usage: crypto-ed25519-sign <n> <seed>.

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
    var seed: [32]u8 = [0; 32]
    Fill(mut st, mut seed[..])
    secret := crypto.Ed25519.FromSeed(&seed)
    msgs := try sys.arena.Slice[u8](64 * n)
    Fill(mut st, mut msgs[..])
    var sum: u64 = 0
    for i in 0..n {
        sig := crypto.Ed25519.Sign(&secret, msgs[64 * i..64 * i + 64])
        sum = sum ^ Lo64(sig[0..8]) ^ Lo64(sig[32..40])
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
