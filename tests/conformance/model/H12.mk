// verdict: ok
// case: H12
struct GNode {
    next: Cell[?&GNode]
}

Recycle(h: &Heap): OutOfMemory!void {
    var a := try Arena.New(h)
    onerr a.Free()
    x := try a.Put(GNode(next: Cell.New(none)))
    y := try a.Put(GNode(next: Cell.New(none)))
    x.next.Store(some(&*y))
    y.next.Store(some(&*x))
    a.Reset()
    a.Free()
}
