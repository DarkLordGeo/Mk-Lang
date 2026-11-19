// verdict: reject E0706
// note: Unordered (memory model R19) is a load or store order only: a fence and every read-modify-write refuse it, a load and a store take it
import std.atomic

Use(a: &atomic.Atomic[int]): int {
    a.Store(1, atomic.Unordered)
    x := a.Load(atomic.Unordered)
    atomic.Fence(atomic.Unordered) // <- E0706
    _ = a.FetchAdd(1, atomic.Unordered) // <- E0706
    _ = a.Swap(2, atomic.Unordered) // <- E0706
    r := a.CompareExchange(0, 1, atomic.SeqCst, atomic.Unordered) // <- E0706
    s := a.CompareExchange(0, 1, atomic.Unordered, atomic.Relaxed) // <- E0706
    return x + r.value + s.value
}
