// Codegen kernel intsum, Mk side, with the in-process harness
// (tests/bench/README.md "The codegen table"). Fill and Run are the
// algorithm of intsum.c; Main alternates the two sides and prints one line
// per round, which tools/bench reads (median, minimum and MAD per side).
// Usage: intsum <n> <passes> <seed> <rounds>.

import std.time
import ref

const Cap: int = 4194304

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

Fill(xs: mut []i64, seed: u64) {
    var st := seed
    for i in 0..xs.Len() {
        st = st +% 0x9e3779b97f4a7c15
        xs[i] = Cast[i64](Mix(st) & 1023)
    }
}

Run(xs: mut []i64, passes: int): i64 {
    n := xs.Len()
    var total: i64 = 0
    for r in 0..passes {
        var s: i64 = 0
        for i in 0..n {
            s += xs[i]
        }
        total += s
        xs[r % n] = s & 1023
    }
    return total
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    passes := Cast[int](try sys.args[2].ParseInt())
    seed := Cast[u64](try sys.args[3].ParseInt())
    rounds := Cast[int](try sys.args[4].ParseInt())
    var out := sys.Stdout()
    if n < 1 || n > Cap {
        try out.Str("n out of range\n")
        return
    }
    store := try sys.arena.Slice[i64](n + 8)
    off := ref.Align64(store[..])
    if ref.Addr(store[off..]) & 63 != 0 {
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
                Fill(mut store[off..off + n], seed)
                t0 := time.Now(sys.io)
                mk_sum = Run(mut store[off..off + n], passes)
                mk_ns = time.Now(sys.io).Since(t0).Nanos()
            } else {
                ref.CFill(seed, n)
                t0 := time.Now(sys.io)
                c_sum = ref.CRun(n, passes)
                c_ns = time.Now(sys.io).Since(t0).Nanos()
            }
        }
        try Print(mut out, "round {round} mk {mk_ns} {mk_sum} c {c_ns} {c_sum}\n", (round, mk_ns, mk_sum, c_ns, c_sum))
    }
}
