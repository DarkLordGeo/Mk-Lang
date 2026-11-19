// crypto-hkdf256, Mk: std.crypto HKDF-SHA256 (RFC 5869) over <mib> MiB of
// splitmix64 bytes from <seed> (each output little-endian; the salt the
// next 32 bytes): for every 32-byte input key, HkdfExtract with the salt,
// then HkdfExpand of 42 bytes with a 12-byte info (TLS 1.3's AES-128-GCM
// key and IV sizes), <reps> passes; after each
// derivation the salt's first byte is xored with the output's. Prints the
// xor of every output's first 8 bytes (little-endian). Usage:
// crypto-hkdf256 <mib> <reps> <seed>.

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
    mib := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    n := mib * 1048576
    buf := try sys.arena.Slice[u8](n)
    Fill(mut st, mut buf[..])
    var salt: [32]u8 = [0; 32]
    Fill(mut st, mut salt[..])
    info := "tls13 key iv".AsBytes()
    var okm: [42]u8 = [0; 42]
    var sum: u64 = 0
    for _ in 0..reps {
        for j in 0..n / 32 {
            prk := crypto.HkdfExtract(salt[..], buf[32 * j..32 * j + 32], crypto.HashKind.Sha256)
            try crypto.HkdfExpand(&prk, info, mut okm[..])
            sum = sum ^ Lo64(okm[..])
            salt[0] = salt[0] ^ okm[0]
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
