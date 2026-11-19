// verdict: ok
// rule: D-925
// note: Chan.TrySend(x: T into self) gives x back when the channel is full, some(x), and on Closed the caller still owns x: a linear value is freed on each of those edges, and on none it is the channel's
TrySendOne(sys: &System, c: &Chan[HeapList[int]]) {
    xs := HeapList[int].New(sys.heap.Alloc())
    back := c.TrySend(xs) else {
        xs.Free(sys.heap.Alloc())
        return
    }
    match back {
        some(ys) {
            ys.Free(sys.heap.Alloc())
        }
        none {}
    }
}
