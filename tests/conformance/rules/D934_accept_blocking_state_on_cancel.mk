// verdict: ok
// rule: D-934
// note: runtime.Blocking's Canceled leaves the state with the caller, as Chan.Send's error edge does: a linear HeapList is freed on the error edge, and on success f's result is the caller's to free
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
}
