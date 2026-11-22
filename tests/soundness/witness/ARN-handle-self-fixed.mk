// verdict: reject E0501
// note: an arena made over a local Fixed by Arena.NewIn holds the Fixed's loan by its making; `*a into a` drops that loan from the store into the arena, but the arena and the list made in it still hold it, so resetting the Fixed under the live list is refused. Accepted, the list would read bytes the Fixed handed out again
import std.mem

B1(a: &Arena): void *a into a {
}

B2(a: &Arena into o, o: mut List[str]): void {
}

export Main(sys: &System): OutOfMemory!void {
    var buf: [4096]u8 = [0; 4096]
    var fixed := mem.Fixed.New(mut buf[..])
    var ar := try Arena.NewIn(fixed.Alloc())
    onerr ar.Free()
    var o := List[str].New(&ar)
    B1(&ar)
    B2(&ar, mut o)
    try o.Push(try ar.Dup("x"))
    fixed.Reset() // <- E0501
    _ = o.Len()
    ar.Free()
}
