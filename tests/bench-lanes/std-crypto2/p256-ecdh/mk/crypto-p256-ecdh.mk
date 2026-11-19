// crypto-p256-ecdh, Mk: std.crypto P-256 ECDH (crypto.EcdhShared). From
// <seed>'s splitmix64 bytes (each output little-endian): <keys> secrets of
// 32 bytes, the first byte masked to 7 bits, and their public points.
// <reps> passes compute, for every i, the shared secret of secret i and
// the public point of secret i + 1 (mod keys). Prints sum = sum * 31 +
// each shared secret's first 8 bytes (little-endian, wrapping). Usage:
// crypto-p256-ecdh <keys> <reps> <seed>.

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

export Main(sys: &System): OutOfMemory|ParseError|WriteError|crypto.Error!void {
    keys := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    secrets := try sys.arena.Slice[u8](32 * keys)
    Fill(mut st, mut secrets[..])
    pubs := try sys.arena.Slice[u8](65 * keys)
    for i in 0..keys {
        secrets[32 * i] = secrets[32 * i] & 0x7f
        s := try crypto.EcSecret.FromBytes(crypto.Curve.P256, secrets[32 * i..32 * i + 32])
        _ = try s.Public(mut pubs[65 * i..65 * i + 65])
    }
    var shared: [32]u8 = [0; 32]
    var sum: u64 = 0
    for _ in 0..reps {
        for i in 0..keys {
            j := (i + 1) % keys
            s := try crypto.EcSecret.FromBytes(crypto.Curve.P256, secrets[32 * i..32 * i + 32])
            try crypto.EcdhShared(crypto.Curve.P256, &s, pubs[65 * j..65 * j + 65], mut shared[..])
            sum = sum *% 31 +% Lo64(shared[..])
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
