// verdict: ok
// rule: EV531
// note: a write through a `List` from a local arena; the walk stops at the arena
Local(h: &Heap, v: &i32): OutOfMemory!void {
    a := try Arena.New(h)
    defer a.Free()
    var out := List[&i32].New(&a)
    try out.Push(v)
    out[0] = v
}
