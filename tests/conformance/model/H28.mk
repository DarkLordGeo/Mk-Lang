// verdict: reject E0505
// case: H28
// note: the HeapList's storage lives in h, so the push stores &n into h, which outlives n
Late(h: &Alloc): OutOfMemory!void {
    var list := HeapList[&i32].New(h)
    defer list.Free(h)
    n: i32 = 1
    try list.Push(h, &n) // <- E0505
}
