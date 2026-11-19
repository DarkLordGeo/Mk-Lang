// crypto-aesgcm256-short, Mk: std.crypto.AesGcm (AES-256-GCM) over <mib>
// MiB of splitmix64 bytes from <seed> (each output little-endian; the key
// the next 32 bytes) in 64-byte records (a short TLS record: the cost per
// call), <reps> passes, one key object for the run: each record is sealed
// under the nonce le64(pass * records + record) || 0000 with a 13-byte aad
// (le64 of the record index, then zeros), then opened back. Prints the xor
// of every tag's first 8 bytes (little-endian) and of every opened
// record's first byte. Usage: crypto-aesgcm256-short <mib> <reps> <seed>.

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
    var key: [32]u8 = [0; 32]
    Fill(mut st, mut key[..])
    g := try crypto.AesGcm.New(key[..])
    sealed := try sys.arena.Slice[u8](64 + 16)
    back := try sys.arena.Slice[u8](64)
    recs := n / 64
    var nonce: [12]u8 = [0; 12]
    var aad: [13]u8 = [0; 13]
    var sum: u64 = 0
    for r in 0..reps {
        for j in 0..recs {
            ctr := Trunc[u64](r * recs + j)
            for k in 0..8 {
                nonce[k] = Trunc[u8](ctr >> (8 * k))
                aad[k] = Trunc[u8](Trunc[u64](j) >> (8 * k))
            }
            _ = try g.Seal(&nonce, aad[..], buf[64 * j..64 * j + 64], mut sealed[..])
            sum = sum ^ Lo64(sealed[64..80])
            _ = try g.Open(&nonce, aad[..], sealed[..], mut back[..])
            sum = sum ^ u64(back[0])
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
