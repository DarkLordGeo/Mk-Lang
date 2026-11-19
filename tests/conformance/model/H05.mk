// verdict: reject E0501
// case: H05
Bad(list: mut List[int]): OutOfMemory!void {
    r := list.Get(0)
    try list.Push(1) // <- E0501
    Use(&r)
}
