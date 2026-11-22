// verdict: reject E0505
// note: one call with `a into o` and `*a into a`: by R11's chains the two declare `*a into o`, so the arena's heap loan reaches o, a list in the arena, and the store of the arena into o reaches the heap: E0505. The drop of what an allocator holds by its making covers the store into the allocator itself only; the two-call twin ARN-handle-chain-two is accepted. Accepted, an unsafe body within this signature may leave the arena's source in o's memory
B(a: &Arena into o, o: mut List[str]) *a into a {
}

export Main(sys: &System): OutOfMemory!void {
    var ar := try Arena.New(sys.heap)
    defer ar.Free()
    var o := List[str].New(&ar)
    B(&ar, mut o) // <- E0505
}
