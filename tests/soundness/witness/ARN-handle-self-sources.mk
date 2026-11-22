// verdict: reject E0501
// note: arenas made by Arena.NewIn over a FixedHeap and over a Fallback hold the source's loan by its making; `*a into a` drops that loan from the store into the arena alone, so resetting the source under a live list of the arena is still refused. Accepted, the list would read bytes the source handed out again
import std.mem

B1(a: &Arena): void *a into a {
}

B2(a: &Arena into o, o: mut List[str]): void {
}

OverFixedHeap(): OutOfMemory!void {
    var buf: [8192]u8 = [0; 8192]
    var fh := mem.FixedHeap.New(mut buf[..])
    var ar := try Arena.NewIn(fh.Alloc())
    onerr ar.Free()
    var o := List[str].New(&ar)
    B1(&ar)
    B2(&ar, mut o)
    try o.Push(try ar.Dup("x"))
    fh.Reset() // <- E0501
    _ = o.Len()
    ar.Free()
}

OverFallback(sys: &System): OutOfMemory!void {
    var buf: [4096]u8 = [0; 4096]
    var fb := mem.Fallback.New(sys.heap.Alloc(), mut buf[..])
    var ar := try Arena.NewIn(fb.Alloc())
    onerr ar.Free()
    var o := List[str].New(&ar)
    B1(&ar)
    B2(&ar, mut o)
    try o.Push(try ar.Dup("x"))
    fb.Reset() // <- E0501
    _ = o.Len()
    ar.Free()
}

export Main(sys: &System): OutOfMemory!void {
    try OverFixedHeap()
    try OverFallback(sys)
}
