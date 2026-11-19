// verdict: ok
// case: H07.local
// note: list in a local arena
Keep(xs: mut []i32, h: &Heap): OutOfMemory!void {
    a := try Arena.New(h)
    defer a.Free()
    var out := List[mut i32].New(&a)
    for x in mut xs {
        try out.Push(x)
    }
}
