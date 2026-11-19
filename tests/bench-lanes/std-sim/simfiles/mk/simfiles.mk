// simfiles, Mk, std.sim: the simulated namespace's lookups (SIM-R21). One
// Sim; n files f<i> created with i mod 7 + 1 bytes, then each statted and
// renamed to g<i>, then each g<i> statted. Every path operation looks the
// path up in the node table, so the row measures that lookup's cost as
// the file count grows. Usage: simfiles <files> <seed>. Prints the count
// and the sum of the sizes.

import std.fs
import std.sim

// Name writes prefix and i in decimal into buf; the name's length.
Name(prefix: u8, i: int, buf: mut [24]u8): int {
    var d: [20]u8 = [0; 20]
    var k := 0
    var x := i
    for {
        d[k] = b'0' + Trunc[u8](x % 10)
        k += 1
        x = x / 10
        if x == 0 {
            break
        }
    }
    buf[0] = prefix
    for j in 0..k {
        buf[1 + j] = d[k - 1 - j]
    }
    return k + 1
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    seed := Cast[u64](try sys.args[2].ParseInt())
    var out := sys.Stdout()
    var s := try sim.Sim.New(sys.arena, seed)
    io := s.Io()
    var a: [24]u8 = [0; 24]
    var b: [24]u8 = [0; 24]
    var data: [8]u8 = [b'x'; 8]
    var sum: i64 = 0
    var bad := 0
    for i in 0..n {
        k := Name(b'f', i, mut a)
        p := str.From(a[0..k]) else {
            bad += 1
            continue
        }
        fs.WriteAll(io, p, data[0..i % 7 + 1]) else {
            bad += 1
        }
    }
    for i in 0..n {
        k := Name(b'f', i, mut a)
        m := Name(b'g', i, mut b)
        p := str.From(a[0..k]) else {
            bad += 1
            continue
        }
        q := str.From(b[0..m]) else {
            bad += 1
            continue
        }
        info := fs.Stat(io, p) else {
            bad += 1
            continue
        }
        sum += info.size
        fs.Rename(io, p, q) else {
            bad += 1
        }
    }
    for i in 0..n {
        m := Name(b'g', i, mut b)
        q := str.From(b[0..m]) else {
            bad += 1
            continue
        }
        info := fs.Stat(io, q) else {
            bad += 1
            continue
        }
        sum += info.size
    }
    s.Free()
    try Print(mut out, "files {n} sum {sum} bad {bad}\n", (n, sum, bad))
}
