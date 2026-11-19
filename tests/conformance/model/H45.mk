// verdict: reject E0603
// case: H45
struct Owner {
    arena: Arena
    items: ?List[int]
}

Make(h: &Heap): OutOfMemory!Owner {
    var o := Owner(arena: try Arena.New(h), items: none)
    o.items = some(List[int].New(&o.arena))
    return o // <- E0603
}
