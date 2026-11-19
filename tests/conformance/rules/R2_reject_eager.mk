// verdict: reject E0411
// rule: R2
// note: variant of the R2 `Eager` example: the move comes before a check that can still fail
error Full

Eager(a: &Alloc, list: mut HeapList[Res], r: Res, room: bool): Full|OutOfMemory!void {
    try list.Push(a, r) // <- E0411
    if !room {
        fail Full
    }
}
