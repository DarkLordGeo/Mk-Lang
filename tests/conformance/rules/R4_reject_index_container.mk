// verdict: reject E0501
// rule: R4
// rule: D-215
// note: `list[i]` on a `Slice` implementer is `list.Items()[i]`, so `r` holds a loan on the whole `list` by the call rule, and `Push` borrows `list` mut while `r` is live
Bad(list: mut List[int]): OutOfMemory!void {
    r := &list[0]
    try list.Push(1) // <- E0501
    Use(r)
}
