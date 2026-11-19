// Source: a heap container given the wrong allocator (types.PanicWrongAllocator).
// The list is made with the heap's allocator and pushed to with the arena's.
// Site: std's raw.Fail call in OwnerCheck, reached from the growing Push, with Push's message after the code's
// text; the .err moves when std/core/mem_box.mk does.
export Main(sys: &System) {
    heap_alloc := sys.heap.Alloc()
    arena_alloc := sys.arena.Alloc()
    var list := HeapList[int].New(heap_alloc)
    list.Push(arena_alloc, sys.args.Len()) else {
        Panic("out of memory")
    }
    list.Free(heap_alloc)
}
