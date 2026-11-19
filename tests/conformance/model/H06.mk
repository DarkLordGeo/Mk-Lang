// verdict: reject E0501
// case: H06
// note: `for x in list` copies each element
Bad(list: mut List[int]): OutOfMemory!void {
    for x in list {
        try list.Push(x) // <- E0501
    }
}
