// verdict: ok
// case: H11
struct GNode {
    next: Cell[?&GNode]
}

Cycle(h: &Heap): OutOfMemory!void {
    a := try Arena.New(h)
    onerr a.Free()
    x := try a.Put(GNode(next: Cell.New(none)))
    y := try a.Put(GNode(next: Cell.New(none)))
    x.next.Store(some(&*y))
    y.next.Store(some(&*x))
    a.Free()
}
