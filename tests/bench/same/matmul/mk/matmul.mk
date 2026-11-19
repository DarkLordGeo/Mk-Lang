// matmul, Mk: the reference algorithm of ../README.md. Three flat []f64 of
// n * n, row-major, the i-k-j loop, c[i*n+j] = c[i*n+j] + aik * b[k*n+j].
// Every index is bounds checked and every integer + and * is checked. The
// fill's splitmix64 uses the wrapping operators because it wraps by design.
// Usage: matmul <n> <rounds> <seed>. Prints the checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    a := try sys.arena.Slice[f64](n * n)
    b := try sys.arena.Slice[f64](n * n)
    c := try sys.arena.Slice[f64](n * n)
    for i in 0..n * n {
        st = st +% 0x9e3779b97f4a7c15
        a[i] = Cast[f64](Mix(st) & 255) / 256.0
        st = st +% 0x9e3779b97f4a7c15
        b[i] = Cast[f64](Mix(st) & 255) / 256.0
    }
    for _ in 0..rounds {
        for i in 0..n {
            for k in 0..n {
                aik := a[i * n + k]
                for j in 0..n {
                    c[i * n + j] = c[i * n + j] + aik * b[k * n + j]
                }
            }
        }
    }
    var total := 0.0
    for i in 0..n * n {
        total = total + c[i]
    }
    sum := Cast[i64](total * 65536.0)
    var out := sys.Stdout()
    try Print(mut out, "{sum}\n", (sum))
}
