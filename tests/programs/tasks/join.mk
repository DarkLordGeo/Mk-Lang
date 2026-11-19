// Scope join: 100 tasks each add their index to one AtomicInt;
// the scope block cannot exit until every task has finished, so the sum after it is 4950
// whatever order the tasks ran in.
import std.atomic

Add(total: &atomic.AtomicInt, i: int) {
    _ = total.Add(i64(i))
}

export Main(sys: &System): WriteError!void {
    total := atomic.AtomicInt.New(0)
    scope s := sys.io.Scope() {
        for i in 0..100 {
            s.go Add(&total, i)
        }
    }
    var out := sys.Stdout()
    try Print(mut out, "sum {sum}\n", (sum: total.Load()))
}
