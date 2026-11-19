// mk test: every test here passes; `mk test` exits 0 on this file alone.
struct Span {
    lo: int
    hi: int
    invariant lo <= hi
}

Span.Len(&self): int
    ensures result >= 0
{
    return self.hi - self.lo
}

test "len" {
    Assert(Span(lo: 1, hi: 4).Len() == 3)
}

test "with memory" (sys: &System) {
    xs := HeapList[int].New(sys.heap.Alloc())
    defer xs.Free(sys.heap.Alloc())
    Assert(xs.Len() == 0)
}

test "push and free" (sys: &System) {
    var xs := HeapList[int].New(sys.heap.Alloc())
    defer xs.Free(sys.heap.Alloc())
    try xs.Push(sys.heap.Alloc(), 7)
    Assert(xs.Len() == 1)
}
