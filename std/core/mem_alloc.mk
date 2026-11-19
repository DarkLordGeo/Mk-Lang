// Why: every heap container takes its memory from an allocator the caller
// names, so allocation needs one small handle type the compiler knows, and a
// way for a program to plug in its own allocator.
//
// What: AllocTable (alloc, resize, free), Alloc, Heap with Heap.Alloc,
// MakeAlloc (std.mem's Custom), and the raw calls Alloc.Bytes and
// Alloc.FreeBytes.
//
// How: an Alloc is a context pointer and a table of three entries; its owner
// identity is its own address, which the containers record and compare on
// grow and free. Alloc and Heap are compiler-known: their kind (unique,
// neither Send nor Share) comes from their identity, not their fields. ctx
// is a pointer, so an allocator over evaluation memory runs at compile time.

import std.raw

/// The three entries of an allocator, each taking the allocator's context
/// pointer first: alloc(ctx, size, align) returns a block or null on
/// failure; resize(ctx, p, old, new, align) grows or shrinks a block in
/// place or returns false; free(ctx, p, size, align) gives a block back.
/// ctx is a pointer, not an integer, so an allocator over compile-time
/// memory also runs at compile time.
export struct AllocTable {
    /// alloc(ctx, size, align) returns a block, or null on failure.
    export alloc: fn(*u8, int, int): *u8
    /// resize(ctx, p, old, new, align) grows or shrinks the block in place, or
    /// returns false.
    export resize: fn(*u8, *u8, int, int, int): bool
    /// free(ctx, p, size, align) gives the block back.
    export free: fn(*u8, *u8, int, int)
}

/// An allocator: a context pointer and a table of entries. Containers take
/// a &Alloc and record its address, which is the allocator's identity: a
/// block goes back only to the Alloc it came from. Alloc is unique and
/// neither Send nor Share, whatever its fields.
export struct Alloc {
    ctx: *u8
    table: &AllocTable
}

/// The process heap, in static memory and never released. Main receives it
/// as sys.heap; Heap.Alloc gives it to the containers that take a &Alloc.
export struct Heap {
    own: Alloc
}

/// The heap as an Alloc, the same one on every call, for HeapList, HeapMap,
/// Box, Chan and the other containers that take a &Alloc. O(1); allocates
/// nothing.
///
/// The result holds no loan on the Heap: the heap is static memory that is
/// never released, so the &Alloc is valid for the whole program.
export Heap.Alloc(&self): &Alloc {
    p := raw.Of(&self.own)
    unsafe "every Heap is never released: the process heap is static memory, an evaluation's lives in a page freed only when the evaluation ends; so own outlives every caller" {
        return &*p
    }
}

/// Internal to std: the body of std.mem's Custom, which programs call. An Alloc
/// over a program's own table and context. table must outlive every block the
/// Alloc hands out; as for every Alloc, its identity is its address. O(1);
/// allocates nothing.
export MakeAlloc(ctx: *u8, table: &AllocTable): Alloc from table {
    return Alloc(ctx: ctx, table: table)
}

/// A raw block of n bytes aligned to align (a power of two), or null when
/// the allocator fails: one call of the table's alloc entry. The block is
/// uninitialized; give it back with FreeBytes and the same n and align.
/// Callable only in an unsafe block (E1101). O(1) besides the entry.
export Alloc.Bytes(&self, n: int, align: int): *u8 {
    f := self.table.alloc
    return f(self.ctx, n, align)
}

/// Gives back a block that Bytes returned, with the n and align it was
/// asked for: one call of the table's free entry. Callable only in an
/// unsafe block (E1101). O(1) besides the entry; it allocates nothing
/// itself.
export Alloc.FreeBytes(&self, p: *u8, n: int, align: int) {
    f := self.table.free
    f(self.ctx, p, n, align)
}
