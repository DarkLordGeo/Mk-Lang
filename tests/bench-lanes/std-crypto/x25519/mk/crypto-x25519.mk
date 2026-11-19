// crypto-x25519, Mk: one X25519 key exchange per step, std.crypto.X25519
// twice: the public key X25519(k, 9) and the shared secret X25519(k, u),
// chained as RFC 7748 5.2's iteration. k is 32 splitmix64 bytes from
// <seed> (each output little-endian), u is 9, then <n> times: pub =
// X25519(k, 9), acc ^= pub's first 8 bytes, (k, u) = (X25519(k, u), k).
// Two ladders per step on every side, because Go's crypto/ecdh has no
// single-ladder form: NewPrivateKey computes the public key (CRY-1). Prints
// k's first 8 bytes xor acc, both little-endian u64, so a column that
// skipped the public key would print another checksum. Usage:
// crypto-x25519 <n> <seed>.

import std.crypto

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Low64(b: &[32]u8): u64 {
    var s: u64 = 0
    for i in 0..8 {
        s = s | (u64(b[i]) << (8 * i))
    }
    return s
}

export Main(sys: &System): ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    var st := Cast[u64](try sys.args[2].ParseInt())
    var k: [32]u8 = [0; 32]
    for w in 0..4 {
        st = st +% 0x9e3779b97f4a7c15
        x := Mix(st)
        for b in 0..8 {
            k[8 * w + b] = Trunc[u8](x >> (8 * b))
        }
    }
    var nine: [32]u8 = [0; 32]
    nine[0] = 9
    var u: [32]u8 = [0; 32]
    u[0] = 9
    var acc: u64 = 0
    for _ in 0..n {
        pub := crypto.X25519(&k, &nine)
        acc = acc ^ Low64(&pub)
        r := crypto.X25519(&k, &u)
        u = k
        k = r
    }
    sum := Low64(&k) ^ acc
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
