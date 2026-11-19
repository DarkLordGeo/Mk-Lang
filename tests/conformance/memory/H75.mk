// verdict: ok
// rule: R2
// note: the reference's `Bounded.Push` and `Keep` example: `Push` fails with `Full` before it moves the item, so the caller still owns `r` on the error edge and its `onerr` closes it
error Full

struct Bounded[T] {
    capacity: int
    items: HeapList[T]
}

Bounded[T].Push(mut self, a: &Alloc, x: T into self): Full|OutOfMemory!void {
    if self.items.Len() >= self.capacity {
        fail Full
    }
    try self.items.Push(a, x)
}

Keep(a: &Alloc, b: mut Bounded[Res]): Full|OutOfMemory!void {
    r := Acquire("a")
    onerr r.Close()
    try b.Push(a, r)
}
