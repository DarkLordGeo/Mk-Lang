// spawn, Mk, idiomatic std: the task of ../README.md with Mk's tasks: one
// scope, n tasks started by s.go, each adding its index to one
// atomic.AtomicInt the parent owns; the scope's exit joins them all. Tasks
// run on MK_CARRIERS carriers (tools/bench sets it to the row's thread
// count). Usage: spawn <n> <rounds>. Prints the checksum.

import std.atomic

Work(sum: &atomic.AtomicInt, i: i64) {
    _ = sum.Add(i)
}

export Main(sys: &System): ParseError|WriteError!void {
    n := try sys.args[1].ParseInt()
    rounds := try sys.args[2].ParseInt()
    var total: i64 = 0
    for _ in 0..rounds {
        sum := atomic.AtomicInt.New(0)
        scope s := sys.io.Scope() {
            for i in 0..n {
                s.go Work(&sum, i)
            }
        }
        total += sum.Load()
    }
    var out := sys.Stdout()
    try Print(mut out, "{total}\n", (total))
}
