// verdict: reject E0501
// case: X.LoopList
// note: R20 on a Slice implementer
Bad(list: mut List[int]) {
    for x in mut list {
        *x += 1
        list.Clear() // <- E0501
    }
}
