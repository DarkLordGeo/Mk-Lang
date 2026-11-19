// fmtints, Mk: the reference algorithm of ../README.md. The textbook
// conversion written out (one digit per division by 10 into a 20-byte
// temporary, copied out), not std's formatter: that is the idiomatic fmt
// row's. A []u8 output buffer and a [20]u8 temporary, every index bounds
// checked, every signed + checked. splitmix64 wraps by design.
// Usage: fmtints <n> <rounds> <seed>. Prints the sum of all bytes written.

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
    xs := try sys.arena.Slice[i64](n)
    for i in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        xs[i] = Cast[i64](Mix(st) % 2000000001) - 1000000000
    }
    buf := try sys.arena.Slice[u8](n * 12)
    var tmp: [20]u8 = [0; 20]
    var total := 0
    for _ in 0..rounds {
        var pos := 0
        for i in 0..n {
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
        total += sum
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
