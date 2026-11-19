// verdict: ok
// case: X.SharedClone
// note: a clone outlives its source. `Shared` takes `&Heap`
import std.sync

Good(h: &Heap): OutOfMemory!void {
    x := try sync.Shared[i32].New(h, 1)
    y := x.Clone()
    x.Free(h)
    r := y.Ref()
    Use(r)
    y.Free(h)
}
