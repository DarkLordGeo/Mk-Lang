// verdict: ok
// rule: R4
// rule: D-215
// note: the twin of R4_reject_index_container: the element's loan ends at its last use, before `Push`
Good(list: mut List[int]): OutOfMemory!void {
    r := &list[0]
    Use(r)
    try list.Push(1)
}
