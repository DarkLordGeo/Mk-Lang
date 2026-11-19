// verdict: ok
// case: R13.Grow
// also: H03
Grow(list: mut List[int]): OutOfMemory!void {
    try list.Push(list.Len())
}
