// closuresort, Mk, the capturing form: the comparator is a
// fn literal that reads the local origin, passed to sortw.SortWith
// (sortw/, the same sort as ../mkctx/sortw with the context argument taken
// out). The compiler gives the sort's instance one
// hidden record of shared references to the captured names and passes it
// to every comparator call. The task, the
// input and the checksum are README.md's; ../mkctx is the same program
// with the context passed explicitly, and ../mk the same capture through
// std's Slice[T].Sort. Usage: closuresort <n> <rounds> <seed>. Prints the
// checksum.

import sortw

Mix(x: u64): u64 {
    var z := x
    z = (z ^ (z >> 30)) *% 0xbf58476d1ce4e5b9
    z = (z ^ (z >> 27)) *% 0x94d049bb133111eb
    return z ^ (z >> 31)
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
        sortw.SortWith(mut xs[..], fn(a: &i64, b: &i64): bool {
            da := *a - origin
            db := *b - origin
            return da * da < db * db
        })
        for i in 0..n {
            d := xs[i] - origin
            total = total +% Cast[u64](d * d) *% Cast[u64](i + 1)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
