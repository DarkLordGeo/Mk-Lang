// verdict: ok
// note: `*a into a` with `a` a reference to an arena stores what the arena's handle holds, the loan of the heap or allocator it was made with, into the arena itself; the arena holds that loan by its making, so it is no store into the arena's contents and a later call's store into a list in the arena does not reach the heap. Rejected, a helper that takes the arena's handle into itself refuses every later store into the arena's lists (E0505 into `*sys`); the content twin is ARN-handle-self-content, the by-value twin ARN-handle-self-byval
B1(a: &Arena): void *a into a {
}

B2(a: &Arena into o, o: mut List[str]): void {
}

Both(a: &Arena, b: &Arena): void *a into b {
}

Put(o: mut List[&i32], x: &i32 into o) {
}

Local(sys: &System): OutOfMemory!void {
    n: i32 = 1
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var o := List[str].New(&ar)
    B1(&ar)
    B2(&ar, mut o)
    try o.Push(try ar.Dup("x"))
    var l := List[&i32].New(&ar)
    Put(mut l, &n)
    Both(&ar, &ar)
    Put(mut l, &n)
    _ = o.Len()
}

OverHeap(h: &Heap): OutOfMemory!void {
    var ar := try Arena.New(h)
    defer ar.Free()
    var o := List[str].New(&ar)
    B1(&ar)
    B2(&ar, mut o)
    try o.Push(try ar.Dup("y"))
}

OverSub(sys: &System): OutOfMemory!void {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var sub := try ar.Sub()
    defer sub.Free()
    var o := List[str].New(&sub)
    B1(&sub)
    B2(&sub, mut o)
    try o.Push(try sub.Dup("z"))
}

export Main(sys: &System): OutOfMemory!void {
    try Local(sys)
    try OverHeap(sys.heap)
    try OverSub(sys)
}
