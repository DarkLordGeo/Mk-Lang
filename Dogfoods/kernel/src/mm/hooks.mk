// Why: std.free's Heap needs a page source, and in this kernel that is the
// frame allocator.
//
// What: MkPageAlloc, MkPageFree and HookCalls.
//
// How: the Heap asks for whole runs of bytes and gives them back with the
// same size and alignment; each is turned into pages of the frame
// allocator. A release the allocator refuses panics.

import std.atomic
import std.raw

var Allocations: atomic.AtomicInt = atomic.AtomicInt.New(0)
var Releases: atomic.AtomicInt = atomic.AtomicInt.New(0)

/// How many times the Heap has asked for pages and given them back.
export HookCalls(): (allocations: i64, releases: i64) {
    return (allocations: Allocations.Load(), releases: Releases.Load())
}

PagesFor(size: int): int {
    return (size + int(PageSize) - 1) / int(PageSize)
}

AlignPages(align: int): int {
    return if align <= int(PageSize) { 1 } else { align / int(PageSize) }
}

/// The Heap's page source: `size` bytes aligned to `align`, as whole pages
/// of the frame allocator; null when none are left, which the Heap reports
/// as OutOfMemory.
export extern MkPageAlloc(size: int, align: int): *u8 {
    address := Allocate(PagesFor(size), AlignPages(align)) else {
        return raw.Null[u8]()
    }
    _ = Allocations.Add(1)
    unsafe "the frame allocator just handed out these pages, which lie in the identity-mapped first GiB" {
        return raw.FromAddr[u8](uint(address))
    }
}

/// Takes back what MkPageAlloc gave. A release the allocator refuses means
/// the Heap's bookkeeping and the frame map disagree, so it panics.
export extern MkPageFree(p: *u8, size: int, align: int) {
    var address: u64 = 0
    unsafe "only the pointer's address is read" {
        address = u64(raw.Addr(p))
    }
    Release(address, PagesFor(size)) else {
        Panic("MkPageFree: the Heap gave back pages the frame allocator did not hand out")
    }
    _ = Releases.Add(1)
}
