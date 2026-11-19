// verdict: reject E0301
// note: `Shared.New` takes `&Heap`, so an arena's `&Alloc` is a type error, and the clone cannot outlive `Reset`
import std.sync

Bad(h: &Heap): OutOfMemory!void {
    var a := try Arena.New(h)
    defer a.Free()
    x := try sync.Shared[i32].New(a.Alloc(), 1) // <- E0301
    y := x.Clone()
    x.Free(h)
    a.Reset()
    Use(y.Ref())
    y.Free(h)
}
