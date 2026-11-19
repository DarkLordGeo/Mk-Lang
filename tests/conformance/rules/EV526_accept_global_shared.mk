// verdict: ok
// rule: EV526
// rule: R16
// note: a `var` global is reached only through `&`; `AtomicInt.Add` takes `&self`
import std.atomic

var Total: atomic.AtomicInt = atomic.AtomicInt.New(0)

Count(): i64 {
    return Total.Add(1)
}
