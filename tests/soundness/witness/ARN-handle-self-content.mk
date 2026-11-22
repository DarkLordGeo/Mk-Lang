// verdict: reject E0505
// note: twin of ARN-handle-self-ok: here the heap's loan is stored into the arena's contents by a real Push of `sys.heap` into a list made in it, so a later store into another list of the same arena reaches the heap, and a frame loan stored there is E0505. Accepted, the rule that drops what an allocator holds by its making would also drop what was stored into its memory
B1(a: &Arena): void *a into a {
}

B2(a: &Arena into o, o: mut List[str]): void {
}

Stash(l: mut List[&i32], x: &i32 into l) {
}

export Main(sys: &System): OutOfMemory!void {
    n: i32 = 1
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var o := List[str].New(&ar)
    B1(&ar)
    B2(&ar, mut o) // <- E0505
    var l := List[&Heap].New(&ar)
    try l.Push(sys.heap)
    var l2 := List[&i32].New(&ar)
    Stash(mut l2, &n) // <- E0505
    _ = l.Len()
}
