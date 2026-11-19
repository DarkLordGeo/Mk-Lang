// Source: a heap container given the wrong allocator on its free path (types.PanicWrongAllocator;
// the owner check covers every grow and free that takes &Alloc, on
// HeapList, HeapMap, TextMap, HeapText, Box and Chan). The box is made with the heap's allocator
// and freed with the arena's. Site: std's raw.Fail call in OwnerCheck, reached from Box.Free, with
// Box.Free's message after the code's text; the .err moves when
// std/core/mem_box.mk does.
export Main(sys: &System) {
    heap_alloc := sys.heap.Alloc()
    arena_alloc := sys.arena.Alloc()
    b := Box[int].New(heap_alloc, sys.args.Len()) else {
        Panic("out of memory")
    }
    b.Free(arena_alloc)
}
