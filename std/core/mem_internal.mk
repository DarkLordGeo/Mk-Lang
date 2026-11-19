// Why: std modules outside std.core and the runtime must allocate, free and
// build heaps without seeing the private fields of Alloc and Heap.
//
// What: AllocBytes, ResizeBytes, FreeBytes, MakeHeap, SetHeapTable and
// HeapCtx for them, and the std.core helpers Dangling, DanglingBytes and
// AtomicWord.
//
// How: each is one call of the AllocTable entry or one field access.
// Dangling gives an empty buffer a nonnull, aligned data pointer that is
// never dereferenced.

import std.raw

/// A block of size bytes aligned to align from a, or null when a refuses,
/// for std modules that cannot see Alloc's fields. One call of a's table.
export AllocBytes(a: &Alloc, size: int, align: int): *u8 {
    f := a.table.alloc
    return f(a.ctx, size, align)
}

/// Grows or shrinks the block p from old to new bytes in place; false when it
/// cannot, and then p is unchanged. One call of a's table.
export ResizeBytes(a: &Alloc, p: *u8, old: int, new: int, align: int): bool {
    f := a.table.resize
    return f(a.ctx, p, old, new, align)
}

/// Returns the block p of size bytes to a. One call of a's table.
export FreeBytes(a: &Alloc, p: *u8, size: int, align: int) {
    f := a.table.free
    f(a.ctx, p, size, align)
}

/// The one process Heap over the table t, which must point at a global table.
/// O(1).
export MakeHeap(ctx: *u8, t: *AllocTable): Heap {
    unsafe "t points at a global table, which lives as long as the process" {
        return Heap(own: Alloc(ctx: ctx, table: &*t))
    }
}

/// Points the process heap at the table t, the counting table of test builds.
/// O(1).
export SetHeapTable(h: *Heap, t: *AllocTable) {
    unsafe "h points at the process heap; t at a global table" {
        (*h).own.table = &*t
    }
}

/// The context pointer of the heap's allocator table. O(1).
export HeapCtx(h: &Heap): *u8 {
    return h.own.ctx
}

// Dangling is the data pointer of an empty buffer: never null, because the
// data pointer of every slice and str is nonnull, equal to T's alignment,
// never dereferenced.
Dangling[T](): *T {
    unsafe "an address no allocation holds, used only for zero-length views" {
        return raw.FromAddr[T](Trunc[uint](AlignOfT[T]()))
    }
}

// DanglingBytes is Dangling for u8, whose alignment is 1.
DanglingBytes(): *u8 {
    unsafe "an address no allocation holds, used only for zero-length views" {
        return raw.FromAddr[u8](1)
    }
}

// AtomicWord is the address of a 32-bit atomic as the *u32 IoTable.wait and
// wake take (a lock word, a channel's sequence word): raw.Atomic[u32] is
// laid out as its u32. Nothing is dereferenced here.
AtomicWord(a: &raw.Atomic[u32]): *u32 {
    unsafe "a pointer cast; raw.Atomic[u32] has u32's layout" {
        return raw.PtrCast[raw.Atomic[u32], u32](raw.Of(a))
    }
}
