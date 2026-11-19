// Why: one value that must outlive every arena (a node of a recursive type,
// the state of an erased connection) needs its own allocation, released with
// the allocator it came from.
//
// What: Box[T] with New, Ref, Mut, Take and its Free impl, and OwnerCheck,
// the allocator check the heap containers share.
//
// How: a Box records the allocator it was made with and panics with code 12
// when Take or Free is given another (one compare, on free only). marker is
// [0]&T, not [0]T, so ?Box[Node] inside Node is not a layout cycle (E0907);
// the same holds for loans.

import std.raw

/// One value of T on an allocator, owned by the Box. The Box records its
/// allocator, and Take and Free panic (code 12) when given a different one.
/// Linear: it ends in Free or Take. Send when T is Send, Share when T is
/// Share.
export linear view struct Box[T] {
    ptr: *T
    owner: *Alloc
    marker: [0]&T
}

unsafe impl[T: Send] Send for Box[T] {}
unsafe impl[T: Share] Share for Box[T] {}

/// A Box holding x, on the allocator a. x moves in only on success: OutOfMemory
/// when a cannot supply the bytes of a T, and then the caller still owns x.
/// O(1) plus one allocation from a; a zero-size T needs no memory.
export Box[T].New(a: &Alloc, x: T): OutOfMemory!Box[T] from a, x {
    size := SizeOfT[T]()
    var p := Dangling[T]()
    if size > 0 {
        q := AllocBytes(a, size, AlignOfT[T]())
        if raw.IsNull(q) {
            fail OutOfMemory
        }
        p = BytesAs[T](q)
    }
    SlotWrite(p, 0, x)
    return Box[T](ptr: p, owner: raw.Of(a), marker: [])
}

/// The boxed value. O(1).
export Box[T].Ref(&self): &T {
    return SlotRef(self.ptr, 0)
}

/// The boxed value, writable. O(1).
export Box[T].Mut(mut self): mut T {
    return SlotMut(self.ptr, 0)
}

/// Ends the Box and gives its value back: the memory goes back to a, which
/// must be the Box's allocator (code 12 otherwise). Works for a Box of any
/// T. O(1) plus one free to a.
export Box[T].Take(self, a: &Alloc): T {
    Box(ptr, owner, marker) := self
    OwnerCheck(a, owner, "Box.Take")
    Discard(marker)
    x := SlotRead(ptr, 0)
    FreeSlots(a, ptr, 1)
    return x
}

/// Releases the value with a, then the memory; a must be the Box's
/// allocator (code 12 otherwise). The call compiles only when T is not
/// linear or is Free (E0701); a Box of any other T gives its value back
/// with Take. O(1) plus the value's own Free.
impl[T] Free for Box[T] {
    Free(self, a: &Alloc) {
        Box(ptr, owner, marker) := self
        OwnerCheck(a, owner, "Box.Free")
        Discard(marker)
        ReleaseValue(SlotRead(ptr, 0), a)
        FreeSlots(a, ptr, 1)
    }
}

// OwnerCheck panics with code 12 unless a is the allocator recorded at New:
// one compare, on grow and free only.
OwnerCheck(a: &Alloc, owner: *Alloc, op: str) {
    if !raw.Same(raw.Of(a), owner) {
        raw.Fail(12, op)
    }
}
