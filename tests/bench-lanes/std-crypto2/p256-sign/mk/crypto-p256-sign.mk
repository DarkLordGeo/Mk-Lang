// crypto-p256-sign, Mk: std.crypto ECDSA P-256 signing with RFC 6979
// nonces (crypto.EcdsaSign, DER form, as Go's Sign returns it). From
// <seed>'s splitmix64 bytes (each output little-endian): <keys> secrets of
// 32 bytes, the first byte masked to 7 bits so each is below the order,
// then one 32-byte digest per secret; <reps> passes sign every digest under
// its secret, and after each signature the digest's first byte is xored
// with the signature's last byte (s's lowest). Prints the xor of every
// DER signature's first 8 and last 8 bytes (little-endian). Usage:
// crypto-p256-sign <keys> <reps> <seed>.

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
    for i in 0..keys {
        secrets[32 * i] = secrets[32 * i] & 0x7f
    }
    digests := try sys.arena.Slice[u8](32 * keys)
    Fill(mut st, mut digests[..])
    var sig: [72]u8 = [0; 72]
    var sum: u64 = 0
    for _ in 0..reps {
        for i in 0..keys {
            s := try crypto.EcSecret.FromBytes(crypto.Curve.P256, secrets[32 * i..32 * i + 32])
            n := try crypto.EcdsaSign(&s, digests[32 * i..32 * i + 32], crypto.SigFormat.Der, mut sig[..])
            sum = sum ^ Lo64(sig[0..8]) ^ Lo64(sig[n - 8..n])
            digests[32 * i] = digests[32 * i] ^ sig[n - 1]
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
