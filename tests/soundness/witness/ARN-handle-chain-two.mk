// verdict: ok
// note: twin of ARN-handle-chain-one with the clauses in two calls: `*a into a` alone stores the arena's handle into the arena itself, which drops what it holds by its making, and nothing chains into o. Rejected, the drop of R11's allocator rules would not hold for `*a into a`
B1(a: &Arena) *a into a {
}

B2(a: &Arena into o, o: mut List[str]) {
}

export Main(sys: &System): OutOfMemory!void {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var o := List[str].New(&ar)
    B1(&ar)
    B2(&ar, mut o)
    try o.Push(try ar.Dup("x"))
}
