// crypto-ed25519-verify, Mk: std.crypto.Ed25519.Verify, <n> times over 256
// signed 64-byte messages under one key. From <seed>, splitmix64 bytes
// (each output little-endian): first the key's 32-byte seed, then the
// 256 * 64 message bytes, each signed once. The key is Ed25519.FromSeed of
// the seed (Go NewKeyFromSeed, Zig KeyPair.generateDeterministic). Verify i
// checks message i % 256; message 255's signature has bit 0 of S flipped,
// so it fails. Prints the number of signatures that verified, xor the
// signatures' bytes 0..8 (little-endian). Usage: crypto-ed25519-verify <n>
// <seed>.

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
    var pk: [32]u8 = [0; 32]
    for i in 0..32 {
        pk[i] = secret[32 + i]
    }
    msgs := try sys.arena.Slice[u8](64 * 256)
    Fill(mut st, mut msgs[..])
    sigs := try sys.arena.Slice[[64]u8](256)
    var sum: u64 = 0
    for i in 0..256 {
        sigs[i] = crypto.Ed25519.Sign(&secret, msgs[64 * i..64 * i + 64])
        sum = sum ^ Lo64(sigs[i][0..8])
    }
    sigs[255][32] = sigs[255][32] ^ 1
    var ok: u64 = 0
    for i in 0..n {
        j := i % 256
        if crypto.Ed25519.Verify(&pk, msgs[64 * j..64 * j + 64], &sigs[j]) {
            ok += 1
        }
    }
    sum = sum ^ ok
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
