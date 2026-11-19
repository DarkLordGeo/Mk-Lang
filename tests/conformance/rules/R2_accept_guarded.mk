// verdict: ok
// rule: R2
// note: variant of the R2 `Guarded`/`Caller` example: the check fails before the move, so the caller's `onerr` still owns `r`
error Full

Guarded(a: &Alloc, list: mut HeapList[Res], r: Res, room: bool): Full|OutOfMemory!void {
    if !room {
        fail Full
    }
    try list.Push(a, r)
}

Caller(a: &Alloc, list: mut HeapList[Res]): Full|OutOfMemory!void {
    r := Acquire("b")
    onerr r.Close()
    try Guarded(a, list, r, Cond())
}
