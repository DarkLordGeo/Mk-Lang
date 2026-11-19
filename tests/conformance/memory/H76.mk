// verdict: reject E0411
// rule: R2
// note: the reference's `Register` example: the item is moved into the list, then a later check fails; `ok` is a parameter hiding the universe name, so no E0102
error Full

Register(a: &Alloc, list: mut HeapList[Res], r: Res, ok: bool): Full|OutOfMemory!void {
    try list.Push(a, r) // <- E0411
    if !ok {
        fail Full
    }
}
