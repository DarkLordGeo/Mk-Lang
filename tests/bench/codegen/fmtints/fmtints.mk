// Codegen kernel fmtints, Mk side, with the in-process harness
// (tests/bench/README.md "The codegen table"). Fill and Run are the
// algorithm of fmtints.c; Main alternates the two sides and prints one line
// per round, which tools/bench reads (median, minimum and MAD per side).
// Every index is bounds checked and every signed + is checked.
// Usage: fmtints <n> <seed> <rounds>.

import std.time
import ref

const Cap: int = 1048576

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
        xs[i] = Cast[i64](Mix(st) % 2000000001) - 1000000000
    }
}

Run(xs: []i64, buf: mut []u8): int {
    var tmp: [20]u8 = [0; 20]
    var pos := 0
    for i in 0..xs.Len() {
        x := xs[i]
        var u: u64 = 0
        if x < 0 {
            buf[pos] = 45
            pos += 1
            u = Cast[u64](-x)
        } else {
            u = Cast[u64](x)
        }
        var k := 20
        for {
            k -= 1
            tmp[k] = 48 + Cast[u8](u % 10)
            u = u / 10
            if u == 0 {
                break
            }
        }
        for j in k..20 {
            buf[pos] = tmp[j]
            pos += 1
        }
        buf[pos] = 10
        pos += 1
    }
    var sum := 0
    for i in 0..pos {
        sum += int(buf[i])
    }
    return sum
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    seed := Cast[u64](try sys.args[2].ParseInt())
    rounds := Cast[int](try sys.args[3].ParseInt())
    var out := sys.Stdout()
    if n < 1 || n > Cap {
        try out.Str("n out of range\n")
        return
    }
    sx := try sys.arena.Slice[i64](n + 8)
    sb := try sys.arena.Slice[u8](n * 12 + 64)
    ox := ref.Align64(sx[..])
    ob := ref.Align8(sb[..])
    if (ref.Addr64(sx[ox..]) | ref.Addr8(sb[ob..])) & 63 != 0 {
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
                Fill(mut sx[ox..ox + n], seed)
                t0 := time.Now(sys.io)
                mk_sum = Run(sx[ox..ox + n], mut sb[ob..ob + n * 12])
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
