// verdict: ok
// rule: EV532
// note: `Shared` takes `&Heap`; each handle is freed, and each read holds the handle it came from
import std.sync

Two(h: &Heap): OutOfMemory!void {
    x := try sync.Shared[Point].New(h, MakePoint())
    y := x.Clone()
    Use(y.Ref())
    y.Free(h)
    Use(x.Ref())
    x.Free(h)
}
