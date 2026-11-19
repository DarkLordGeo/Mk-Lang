// verdict: reject E0401
// rule: D-934
// note: on success runtime.Blocking consumes its state: f's result is what the caller holds, so a use of the state after the call is use-after-move
import std.runtime

Grow(xs: HeapList[int]): HeapList[int] {
    return xs
}

Run(sys: &System) {
    xs := HeapList[int].New(sys.heap.Alloc())
    ys := runtime.Blocking(sys.io, xs, Grow) else {
        xs.Free(sys.heap.Alloc())
        return
    }
    ys.Free(sys.heap.Alloc())
    xs.Free(sys.heap.Alloc()) // <- E0401
}
