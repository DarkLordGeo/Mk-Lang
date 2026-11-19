// verdict: reject E0401
// rule: D-925
// note: Chan.TrySend takes x into the channel: once it has not failed, x is moved, and a full channel gives it back only through the some(x) it returns, so a use of x itself after the call is use-after-move
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
    xs.Free(sys.heap.Alloc()) // <- E0401
}
