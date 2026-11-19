// Why: the kernel hands out physical pages, and a fresh boot should hand
// out the same addresses every time.
//
// What: FrameMap, Allocate, Release, AddFree, Usage and the release errors.
//
// How: one bit per 4 KiB page of the identity-mapped first GiB, set while
// the page is free, behind a spin lock. A run is found first-fit from the
// lowest address, skipping whole words with no free page.

import std.atomic
import src.arch

/// The size of one page.
export const PageSize: u64 = 4096

/// The lowest address the frame allocator manages: 16 MiB. Memory below it
/// is the ISA DMA zone and the image; keeping it out also gives every boot
/// the same addresses whatever the image's size.
export const PoolStart: u64 = 0x1000000

const MaxPages: int = 262144
const Words: int = 4096

/// The address is not a multiple of PageSize.
export error Misaligned(address: u64)
/// The address lies past the memory the allocator manages.
export error OutOfRange(address: u64)
/// One of the pages is already free.
export error NotAllocated(address: u64)

/// Why Release refused to give pages back.
export type ReleaseError = Misaligned | OutOfRange | NotAllocated

struct FrameMap {
    free: [4096]u64
    total: int
    used: int
}

var Frames: atomic.Spin[FrameMap] = atomic.Spin[FrameMap].New(FrameMap(free: [0; 4096], total: 0, used: 0))

FrameMap.IsFree(&self, page: int): bool {
    return self.free[page / 64] & (u64(1) << Cast[u64](page % 64)) != 0
}

FrameMap.Mark(mut self, page: int, free: bool) {
    bit := u64(1) << Cast[u64](page % 64)
    if free {
        self.free[page / 64] |= bit
    } else {
        self.free[page / 64] &= ~bit
    }
}

// The first page of the lowest run of n free pages starting at a multiple
// of align. O(pages) in the worst case; a word with no free page costs one
// test.
FrameMap.Find(&self, n: int, align: int): ?int {
    var page := 0
    for page + n <= MaxPages {
        if self.free[page / 64] == 0 {
            page = AlignUp((page / 64 + 1) * 64, align)
            continue
        }
        var run := 0
        for run < n && self.IsFree(page + run) {
            run += 1
        }
        if run == n {
            return some(page)
        }
        page = AlignUp(page + run + 1, align)
    }
    return none
}

AlignUp(x: int, align: int): int {
    return (x + align - 1) / align * align
}

/// Marks the whole pages inside [start, end) free, clipped to the mapped
/// first GiB. Returns how many pages it added. O(pages).
export AddFree(start: u64, end: u64): int {
    first := Cast[int]((start + PageSize - 1) / PageSize)
    limit := if end > arch.MappedEnd { arch.MappedEnd } else { end }
    last := Cast[int](limit / PageSize)
    if first >= last {
        return 0
    }
    var guard := Frames.Lock()
    map := guard.Mut()
    var added := 0
    for page in first..last {
        if !map.IsFree(page) {
            map.Mark(page, true)
            added += 1
        }
    }
    map.total += added
    guard.Unlock()
    return added
}

/// Takes `pages` contiguous free pages whose first address is a multiple of
/// `align` pages, and returns that address; none when no run fits or the
/// request is not a positive number of pages. O(pages managed); allocates
/// nothing.
export Allocate(pages: int, align: int): ?u64 {
    if pages < 1 || pages > MaxPages || align < 1 {
        return none
    }
    var guard := Frames.Lock()
    map := guard.Mut()
    start := map.Find(pages, align) else {
        guard.Unlock()
        return none
    }
    for page in start..start + pages {
        map.Mark(page, false)
    }
    map.used += pages
    guard.Unlock()
    return some(Cast[u64](start) * PageSize)
}

/// Gives back `pages` pages from `address`. Fails, changing nothing, when
/// the address is not page-aligned, lies outside the managed memory, or
/// any of the pages is already free. O(pages).
export Release(address: u64, pages: int): ReleaseError!void {
    if address % PageSize != 0 {
        fail Misaligned(address: address)
    }
    first := address / PageSize
    if pages < 1 || first + Cast[u64](pages) > Cast[u64](MaxPages) {
        fail OutOfRange(address: address)
    }
    start := Cast[int](first)
    var guard := Frames.Lock()
    map := guard.Mut()
    for page in start..start + pages {
        if map.IsFree(page) {
            guard.Unlock()
            fail NotAllocated(address: Cast[u64](page) * PageSize)
        }
    }
    for page in start..start + pages {
        map.Mark(page, true)
    }
    map.used -= pages
    guard.Unlock()
}

/// The pages the allocator manages and how many are handed out.
export Usage(): (total: int, used: int) {
    guard := Frames.Lock()
    map := guard.Ref()
    usage := (total: map.total, used: map.used)
    guard.Unlock()
    return usage
}
