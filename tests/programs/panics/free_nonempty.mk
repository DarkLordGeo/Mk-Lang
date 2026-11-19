// Source: FreeEmpty of a non-empty container (types.PanicFreeNonEmpty; the reference:
// "`FreeEmpty` panics unless the container is empty"; Free of linear elements that are not Free is
// E0701, and a Box is Free, so Free of a list of them frees each).
// Site: std's raw.Fail call in HeapList.FreeEmpty, with its message after the code's text (raw.Fail
// reports the call's site); the .err moves when std/core/coll_heap.mk does.
linear struct Tok {
    n: int
}

export Main(sys: &System) {
    a := sys.heap.Alloc()
    var list := HeapList[Tok].New(a)
    t := Tok(n: sys.args.Len())
    list.Push(a, t) else {
        Tok(n: _) := t
        list.FreeEmpty(a)
        Panic("out of memory")
    }
    list.FreeEmpty(a)
}
