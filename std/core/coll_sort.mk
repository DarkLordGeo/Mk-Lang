// Why: an in-place stable sort costs more moves than one with scratch space;
// callers that have an allocator can trade Len() / 2 elements of memory for a
// plain merge sort with a fixed O(n log n) bound.
//
// What: Slice[T].SortStableIn and its helpers BufSort and BufMerge.
// SortStable, in place with no allocation, is core_sort.mk's.
//
// How: insertion-sorted base runs (the same blocks as StableSort's BlkIter),
// then each level's pairs of runs merged by moving the shorter run out to the
// scratch buffer and merging it back. Short inputs and zero-size T skip the
// allocation and sort in place.

import std.raw

/// Sorts the elements by less, keeping equal elements in their order, as
/// SortStable does, with a merge through Len() / 2 elements of scratch
/// memory from a, freed before it returns. O(n log n) calls of less and
/// O(n log n) moves on every input. Below 32 elements, and for a zero-size
/// T, it sorts in place and allocates nothing. Fails with OutOfMemory when
/// the scratch cannot be allocated, before any element moves, so the slice
/// is unchanged.
export Slice[T].SortStableIn(mut self, a: &Alloc, const less: fn(&T, &T): bool): OutOfMemory!void {
    s := self.ItemsMut()
    n := s.Len()
    if n < 2 * BlkRun || SizeOfT[T]() == 0 {
        StableSort(s, less)
        return
    }
    // The shorter run of a merge of two runs of s holds at most n / 2.
    h := n / 2
    p := GrowSlots(a, Dangling[T](), 0, 0, h, false)
    if raw.IsNull(p) {
        fail OutOfMemory
    }
    BufSort(s, p, less)
    FreeSlots(a, p, h)
}

// BufSort sorts s, at least 2 * BlkRun long, through buf, n / 2 slots: the
// base runs of StableSort's BlkIter, insertion sorted, then each level's
// pairs of runs merged by BufMerge.
BufSort[T](s: mut []T, buf: *T, const less: fn(&T, &T): bool) {
    var it := BlkIterNew(s.Len())
    for !it.Done() {
        a := it.dec
        b := it.Next()
        PdqInsertion(s, a, b, less)
    }
    for {
        it.Begin()
        for !it.Done() {
            a := it.dec
            m := it.Next()
            b := it.Next()
            BufMerge(s, buf, a, m, b, less)
        }
        if !it.NextLevel() {
            break
        }
    }
}

// BufMerge merges the sorted runs s[a..m] and s[m..b] stably, the shorter
// one moved out to buf (at most n / 2 slots): A forward from the front, B
// backward from the back. Runs already in order cost one call of less. Each
// loop is bounded by the runs' lengths, so a less that is no order still
// leaves a permutation; every slot written held a value moved out of the
// slice or buf, and each value lands once.
BufMerge[T](s: mut []T, buf: *T, a: int, m: int, b: int, const less: fn(&T, &T): bool) {
    if !less(&s[m], &s[m - 1]) {
        return
    }
    p := raw.DataMut(s)
    na := m - a
    nb := b - m
    if na <= nb {
        SlotCopy(buf, SlotAt(p, a), na) // A moves out; a..m are free slots
        var i := 0
        var j := m
        var k := a
        // k < j while i < na: the free slots are k..j.
        for i < na && j < b {
            if less(&s[j], SlotRef(buf, i)) {
                SlotCopy(SlotAt(p, k), SlotAt(p, j), 1)
                j += 1
            } else {
                SlotCopy(SlotAt(p, k), SlotAt(buf, i), 1)
                i += 1
            }
            k += 1
        }
        SlotCopy(SlotAt(p, k), SlotAt(buf, i), na - i) // k..k + na - i are free
    } else {
        SlotCopy(buf, SlotAt(p, m), nb) // B moves out; m..b are free slots
        var i := m - 1
        var j := nb - 1
        var k := b - 1
        // k > i while j >= 0: the free slots are i + 1..k + 1.
        for j >= 0 && i >= a {
            if less(SlotRef(buf, j), &s[i]) {
                SlotCopy(SlotAt(p, k), SlotAt(p, i), 1)
                i -= 1
            } else {
                SlotCopy(SlotAt(p, k), SlotAt(buf, j), 1)
                j -= 1
            }
            k -= 1
        }
        SlotCopy(SlotAt(p, a), buf, j + 1) // a..a + j + 1 are free
    }
}
