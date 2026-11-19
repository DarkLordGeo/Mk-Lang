// closuresort, Mk, the context-struct form: the same program as ../mk with
// the comparator's context in a struct passed explicitly, Closer(ctx, a, b)
// through SortWith (sortw/, a copy of std's former SortWith).
// This is the seven-line pattern a capturing literal replaces, and the
// one the compiler writes for it, so the two
// columns differ only in how the context reaches the comparator.
// Usage: closuresort <n> <rounds> <seed>. Prints the checksum.

import sortw

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
}

struct ByDistance {
    origin: i64
}

Closer(c: &ByDistance, a: &i64, b: &i64): bool {
    da := *a - c.origin
    db := *b - c.origin
    return da * da < db * db
}

export Main(sys: &System): OutOfMemory|ParseError|WriteError!void {
    n := Cast[int](try sys.args[1].ParseInt())
    rounds := Cast[int](try sys.args[2].ParseInt())
    seed := Cast[u64](try sys.args[3].ParseInt())
    src := try sys.arena.Slice[i64](n)
    var st := seed
    for i in 0..n {
        st = st +% 0x9e3779b97f4a7c15
        src[i] = Cast[i64](Mix(st) & 0xFFFFF)
    }
    xs := try sys.arena.Slice[i64](n)
    var total: u64 = 0
    for r in 0..rounds {
        origin := Cast[i64](Mix(seed +% Cast[u64](r)) & 0xFFFFF)
        (mut xs[..]).CopyFrom(src[..])
        ctx := ByDistance(origin: origin)
        sortw.SortWith(mut xs[..], &ctx, Closer)
        for i in 0..n {
            d := xs[i] - origin
            total = total +% Cast[u64](d * d) *% Cast[u64](i + 1)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
