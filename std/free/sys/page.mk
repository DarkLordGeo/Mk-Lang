// Why: std's layers 0 and 1 get memory pages through std.sys, and a
// freestanding program has no OS to supply them: the program does.
//
// What: std.sys on x86_64-freestanding, selected by std/pkg.mk for os
// "freestanding": PageTable, the Heap's AllocTable over the program's
// MkPageAlloc and MkPageFree.
//
// How: it holds what layers 0 and 1 reach through the hooks and nothing
// else. The hooks' weak defaults in the support pack return null and do
// nothing, and pages never grow in place.

import std.core

// The program's page source, or the support pack's weak defaults.
extern MkPageAlloc(size: int, align: int): *u8
extern MkPageFree(p: *u8, size: int, align: int)

/// The allocator table behind free.Heap: alloc calls the program's
/// MkPageAlloc, free calls its MkPageFree, and resize always refuses, so a
/// block never grows in place. ctx is unused.
export const PageTable = core.AllocTable(alloc: PageAlloc, resize: PageResize, free: PageFree)

PageAlloc(ctx: *u8, size: int, align: int): *u8 {
    unsafe "MkPageAlloc is the program's hook or the weak default, which takes any size and align and returns null on failure" {
        return MkPageAlloc(size, align)
    }
}

// Pages never grow in place.
PageResize(ctx: *u8, p: *u8, old: int, new: int, align: int): bool {
    return false
}

PageFree(ctx: *u8, p: *u8, size: int, align: int) {
    unsafe "p, size and align are what MkPageAlloc returned and was given, as AllocTable's contract requires" {
        MkPageFree(p, size, align)
    }
}
