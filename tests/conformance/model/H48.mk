// verdict: reject E0501
// case: H48
Keep(xs: mut []i32, h: &Heap): OutOfMemory!void {
    a := try Arena.New(h)
    defer a.Free()
    var out := List[mut i32].New(&a)
    for x in mut xs {
        try out.Push(x)
    }
    for x in mut xs { // <- E0501
        try out.Push(x)
    }
}
