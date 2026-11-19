// fmt, Mk, idiomatic std: the task of ../README.md with std's formatting
// and parsing. Each value is printed with Print into a Text through its
// Writer ("{x}" for the i64; "{f}" for the f64, the shortest digits that
// round-trip), then read back with str.ParseInt and str.ParseFloat.
// Usage: fmt <n> <rounds> <seed>. Prints the checksum.

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

FmtInt(t: mut Text, x: i64): WriteError!void {
    t.Clear()
    var w := t.Writer()
    try Print(mut w, "{x}", (x))
}

FmtFloat(t: mut Text, f: f64): WriteError!void {
    t.Clear()
    var w := t.Writer()
    try Print(mut w, "{f}", (f))
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    var st := Cast[u64](try sys.args[3].ParseInt())
    var t := Text.New(sys.arena)
    var sum: i64 = 0
    var ok: i64 = 0
    for _ in 0..rounds {
        for _ in 0..n {
            st = st +% 0x9e3779b97f4a7c15
            z := Mix(st)
            var x := Cast[i64](z >> 1)
            if z & 1 == 1 {
                x = -x
            }
            st = st +% 0x9e3779b97f4a7c15
            f := Cast[f64](Mix(st) >> 11) * (1.0 / 9007199254740992.0) * 1e6
            try FmtInt(mut t, x)
            y := try t.Str().ParseInt()
            try FmtFloat(mut t, f)
            g := try t.Str().ParseFloat()
            sum = sum +% y +% Cast[i64](g)
            if g == f {
                ok += 1
            }
        }
    }
    total := sum +% ok
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
