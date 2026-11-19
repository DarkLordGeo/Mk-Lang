// verdict: reject E0301
// rule: EV532
// note: `Shared.New` takes `&Heap`, never `&Alloc`
import std.sync

Make(a: &Alloc): OutOfMemory!void {
    x := try sync.Shared[i32].New(a, 1) // <- E0301
    Use(x.Ref())
}
