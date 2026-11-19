// crypto-equal, Mk: std.crypto.Equal of two copies of <mib> MiB of
// splitmix64 bytes from <seed> (each output little-endian), <reps> passes;
// on an odd pass r the byte at (r * 7919) % n of the second copy is flipped
// before the compare and restored after it, so half the compares are
// false. Prints the number of true compares times 1000003, xor reps. Usage:
// crypto-equal <mib> <reps> <seed>.

import std.crypto

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
    a := try sys.arena.Slice[u8](n)
    b := try sys.arena.Slice[u8](n)
    var i := 0
    for i < n {
        st = st +% 0x9e3779b97f4a7c15
        x := Mix(st)
        for k in 0..8 {
            a[i + k] = Trunc[u8](x >> (8 * k))
            b[i + k] = a[i + k]
        }
        i += 8
    }
    var count: u64 = 0
    for r in 0..reps {
        j := (r * 7919) % n
        if r % 2 == 1 {
            b[j] = b[j] ^ 1
        }
        if crypto.Equal(a[..], b[..]) {
            count += 1
        }
        if r % 2 == 1 {
            b[j] = b[j] ^ 1
        }
    }
    sum := (count *% 1000003) ^ Trunc[u64](reps)
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
