// verdict: ok
// case: H35
Bind(list: mut List[int]): OutOfMemory!void {
    if x := list.Get(0) {
        Use(x)
    } else {
        try list.Push(1)
    }
}
