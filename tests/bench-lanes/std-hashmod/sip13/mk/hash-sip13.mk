// hash-sip13, Mk: std.hash.Sip13 (SipHash-1-3) under the key (0, 0), Rust
// DefaultHasher::new()'s: <mib> MiB of splitmix64 bytes from <seed> (each
// output little-endian), hashed <reps> times; after each hash the byte at
// r % n is xored with the result's low byte, so no hash can be hoisted.
// Prints the xor of the results. The key is a zero-filled HashKey, which
// is the key (0, 0); it protects nothing, which a benchmark needs.
// Usage: hash-sip13 <mib> <reps> <seed>.

import std.hash

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    mib := Cast[int](try sys.args[1].ParseInt())
    reps := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    n := mib * 1048576
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
    keys := try sys.arena.Slice[HashKey](1)
    key := keys[0]
    var sum: u64 = 0
    for r in 0..reps {
        h := hash.Sip13(buf[..], key)
        sum = sum ^ h
        buf[r % n] = buf[r % n] ^ Trunc[u8](h)
    }
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
