// Codegen kernel matmul, Mk side, with the in-process harness
// (tests/bench/README.md "The codegen table"). Fill and Run are the
// algorithm of matmul.c; Main alternates the two sides and prints one line
// per round, which tools/bench reads (median, minimum and MAD per side).
// Every index is bounds checked and every integer + and * is checked.
// Usage: matmul <n> <seed> <rounds>.

import std.time
import ref

const CapN: int = 512

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Fill(a: mut []f64, b: mut []f64, c: mut []f64, seed: u64) {
    var st := seed
    for i in 0..a.Len() {
        st = st +% 0x9e3779b97f4a7c15
        a[i] = Cast[f64](Mix(st) & 255) / 256.0
        st = st +% 0x9e3779b97f4a7c15
        b[i] = Cast[f64](Mix(st) & 255) / 256.0
        c[i] = 0.0
    }
}

Run(a: []f64, b: []f64, c: mut []f64, n: int): i64 {
    for i in 0..n {
        for k in 0..n {
            aik := a[i * n + k]
            for j in 0..n {
                c[i * n + j] = c[i * n + j] + aik * b[k * n + j]
            }
        }
    }
    var total := 0.0
    for i in 0..n * n {
        total = total + c[i]
    }
    return Cast[i64](total * 65536.0)
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    seed := Cast[u64](try sys.args[2].ParseInt())
    rounds := Cast[int](try sys.args[3].ParseInt())
    var out := sys.Stdout()
    if n < 1 || n > CapN {
        try out.Str("n out of range\n")
        return
    }
    nn := n * n
    sa := try sys.arena.Slice[f64](nn + 8)
    sb := try sys.arena.Slice[f64](nn + 8)
    sc := try sys.arena.Slice[f64](nn + 8)
    oa := ref.Align64(sa[..])
    ob := ref.Align64(sb[..])
    oc := ref.Align64(sc[..])
    if (ref.Addr(sa[oa..]) | ref.Addr(sb[ob..]) | ref.Addr(sc[oc..])) & 63 != 0 {
        try out.Str("buffer not aligned\n")
        return
    }
    for round in 0..rounds {
        var mk_ns: i64 = 0
        var c_ns: i64 = 0
        var mk_sum: i64 = 0
        var c_sum: i64 = 0
        for side in 0..2 {
            if (side + round) % 2 == 0 {
                Fill(mut sa[oa..oa + nn], mut sb[ob..ob + nn], mut sc[oc..oc + nn], seed)
                t0 := time.Now(sys.io)
                mk_sum = Run(sa[oa..oa + nn], sb[ob..ob + nn], mut sc[oc..oc + nn], n)
                mk_ns = time.Now(sys.io).Since(t0).Nanos()
            } else {
                ref.CFill(seed, n)
                t0 := time.Now(sys.io)
                c_sum = ref.CRun(n)
                c_ns = time.Now(sys.io).Since(t0).Nanos()
            }
        }
        try Print(mut out, "round {round} mk {mk_ns} {mk_sum} c {c_ns} {c_sum}\n", (round, mk_ns, mk_sum, c_ns, c_sum))
    }
}
