// verdict: reject E0507
// rule: EV526
// rule: R16
// note: a `var` global is never assigned: a use of it is a place through a shared reference (14-borrow L8), so the write is E0507
import std.atomic

var Total: atomic.AtomicInt = atomic.AtomicInt.New(0)

Reset() {
    Total = atomic.AtomicInt.New(0) // <- E0507
}
