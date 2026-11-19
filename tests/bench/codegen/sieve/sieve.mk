// Codegen kernel sieve, Mk side, with the in-process harness
// (tests/bench/README.md "The codegen table"). Fill and Run are the
// algorithm of sieve.c; Main alternates the two sides and prints one line
// per round, which tools/bench reads (median, minimum and MAD per side).
// Every flags[j] is bounds checked and every integer + and * is checked.
// Usage: sieve <n> <rounds>.

import std.time
import ref

const Cap: int = 16777216

Fill(flags: mut []u8) {
    for i in 0..flags.Len() {
        flags[i] = 1
    }
}

Run(flags: mut []u8, n: int): int {
    var count := 0
    for i in 2..n + 1 {
        if flags[i] != 0 {
            count += 1
            var j := i * i
            for j <= n {
                flags[j] = 0
                j += i
            }
        }
    }
    return count
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var out := sys.Stdout()
    if n < 2 || n > Cap {
        try out.Str("n out of range\n")
        return
    }
    store := try sys.arena.Slice[u8](n + 1 + 64)
    off := ref.Align64(store[..])
    if ref.Addr(store[off..]) & 63 != 0 {
        try out.Str("buffer not aligned\n")
        return
    }
    for round in 0..rounds {
        var mk_ns: i64 = 0
        var c_ns: i64 = 0
        var mk_sum := 0
        var c_sum := 0
        for side in 0..2 {
            if (side + round) % 2 == 0 {
                Fill(mut store[off..off + n + 1])
                t0 := time.Now(sys.io)
                mk_sum = Run(mut store[off..off + n + 1], n)
                mk_ns = time.Now(sys.io).Since(t0).Nanos()
            } else {
                ref.CFill(n)
                t0 := time.Now(sys.io)
                c_sum = ref.CRun(n)
                c_ns = time.Now(sys.io).Since(t0).Nanos()
            }
        }
        try Print(mut out, "round {round} mk {mk_ns} {mk_sum} c {c_ns} {c_sum}\n", (round, mk_ns, mk_sum, c_ns, c_sum))
    }
}
