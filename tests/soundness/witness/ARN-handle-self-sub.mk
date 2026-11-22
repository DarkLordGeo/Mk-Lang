// verdict: reject E0503
// note: a sub-arena holds its parent's loan by its making; `*a into a` on the sub-arena drops that loan from the store into the sub-arena, but freeing the parent while the sub-arena's list is live is still refused. Accepted, the list would point into freed chunks
B1(a: &Arena): void *a into a {
}

B2(a: &Arena into o, o: mut List[str]): void {
}

export Main(sys: &System): OutOfMemory!void {
    var ar := try Arena.New(sys.heap)
    onerr ar.Free()
    var sub := try ar.Sub()
    onerr sub.Free()
    var o := List[str].New(&sub)
    B1(&sub)
    B2(&sub, mut o)
    try o.Push(try sub.Dup("z"))
    ar.Free() // <- E0503
    _ = o.Len()
    sub.Free()
}
