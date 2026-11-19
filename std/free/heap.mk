// Why: a freestanding program has no System to hand out a Heap, yet std's
// collections need one.
//
// What: free.Heap, the one freestanding Heap.
//
// How: the Heap lives in static memory, installed once behind atomics,
// over std/free/sys's page table, which calls the program's MkPageAlloc
// and MkPageFree (or the support pack's weak defaults, which return null,
// so every allocation is OutOfMemory).

import std.core
import std.raw
import std.sys

// The Heap's two words (Alloc's context and table), and the state of the
// one-time install: 0 none, 1 installing, 2 ready. Atomics, so a var of it
// is Share and its words change behind &.
struct Slot {
    words: [2]raw.Atomic[u64]
    state: raw.Atomic[u32]
}

var HeapSlot: Slot = Slot(words: [raw.Atomic[u64].New(0), raw.Atomic[u64].New(0)], state: raw.Atomic[u32].New(0))

/// The one Heap of a freestanding program, in static memory, since there
/// is no System to hand one out. Each allocation is one call of the
/// program's `export extern MkPageAlloc(size: int, align: int): *u8`, with
/// the size and alignment asked for, and each free one call of its
/// `MkPageFree(p: *u8, size: int, align: int)`; a block never grows in
/// place. Without those hooks the defaults return null, and every
/// allocation fails with OutOfMemory. The first call sets the Heap up; a
/// kernel makes it once at boot, before it starts another CPU, and a call
/// that races it spins until the first is done. O(1) after the first
/// call; allocates nothing.
export Heap(): &core.Heap {
    // Edge: raw.Atomic is seq_cst. The installer's Store(2) is a release
    // after its write of the words, and a Load that reads 2 acquires it, so
    // every caller that returns sees the whole Heap. The Cas(0, 1) admits
    // one installer, and nobody reads the words before state is 2.
    if HeapSlot.state.Cas(0, 1) {
        h := core.MakeHeap(raw.Null[u8](), raw.Of(&sys.PageTable))
        unsafe "the Cas made this call the only writer, and nobody reads the words before state is 2" {
            raw.Write[core.Heap](raw.PtrCast[raw.Atomic[u64], core.Heap](raw.Of(&HeapSlot.words[0])), h)
        }
        // Edge: the Store of 2 publishes the heap's words, written above, to
        // every Load of 2.
        HeapSlot.state.Store(2)
    }
    for HeapSlot.state.Load() != 2 {
    }
    unsafe "state is 2, so the words hold the installed Heap, which is never written again" {
        return &*raw.PtrCast[raw.Atomic[u64], core.Heap](raw.Of(&HeapSlot.words[0]))
    }
}
