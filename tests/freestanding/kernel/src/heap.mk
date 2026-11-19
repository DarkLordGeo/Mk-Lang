// Layer 1 on the freestanding target:
// the kernel supplies the Heap's pages by exporting MkPageAlloc and
// MkPageFree, a bump allocator over the identity-mapped memory from
// KernelEnd to PoolEnd, and free.Heap() allocates through them.

import std.atomic
import std.free
import std.raw

// PoolEnd is 64 MiB: inside the boot map's first GiB and QEMU's 128 MiB.
const PoolEnd: u64 = 0x4000000

var Next: atomic.AtomicInt = atomic.AtomicInt.New(0)

// MkPageAlloc hands out size bytes aligned to align, never reused; null
// when the pool is spent. One CPU runs the kernel, so the bump needs no CAS.
export extern MkPageAlloc(size: int, align: int): *u8 {
    if Next.Load() == 0 {
        Next.Store(Cast[i64](ImageEnd()))
    }
    a := Cast[u64](align)
    start := (Cast[u64](Next.Load()) + a - 1) / a * a
    end := start + Cast[u64](size)
    if end > PoolEnd {
        return raw.Null[u8]()
    }
    Next.Store(Cast[i64](end))
    unsafe "start..end lies in identity-mapped RAM past the image that nothing else uses" {
        return raw.FromAddr[u8](Cast[uint](start))
    }
}

// MkPageFree keeps nothing: the pool is a bump.
export extern MkPageFree(p: *u8, size: int, align: int) {
}

// HeapSum pushes 0 to n-1 onto a HeapList from free.Heap() and returns
// their sum, read back from the list.
HeapSum(n: int): u64 {
    a := free.Heap().Alloc()
    var xs := HeapList[u64].New(a)
    for i in 0..n {
        xs.Push(a, Cast[u64](i)).Must()
    }
    var sum: u64 = 0
    for x in xs.Items() {
        sum += x
    }
    xs.Free(a)
    return sum
}
