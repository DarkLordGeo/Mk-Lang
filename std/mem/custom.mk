// Why: an embedder that hands Mk its own heap needs an Alloc over its own
// allocator functions.
//
// What: AllocTable (std.core's table of alloc, resize and free entries,
// named here for programs outside std) and Custom, an Alloc over a table and
// a context pointer. Both are Provisional.
//
// How: Custom wraps core.MakeAlloc. Both are unsafe only (E1101): the
// table's entries are trusted to keep AllocTable's contract
// (std/core/mem_alloc.mk).

import std.core

/// A table of a program's own allocator functions, which Custom calls:
/// alloc(ctx, n, align) returns a block or null, resize(ctx, p, old, new,
/// align) resizes in place or returns false, and free(ctx, p, n, align)
/// frees. Build one with `mem.AllocTable(alloc: ..., resize: ..., free:
/// ...)`. Provisional, as Custom is.
export type AllocTable = core.AllocTable

/// An Alloc whose every request is one call of an entry of table, with
/// ctx. table must outlive the Alloc and every block it hands out.
/// Callable only inside unsafe (E1101), since the entries are trusted to
/// keep the table's rules. O(1); allocates nothing.
export Custom(ctx: *u8, table: &AllocTable): Alloc from table {
    return core.MakeAlloc(ctx, table)
}
