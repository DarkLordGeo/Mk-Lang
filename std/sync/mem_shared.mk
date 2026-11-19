// Why: some values have no single owner (a cache entry, a config read by
// many tasks); they need a heap box that lives until its last handle goes.
//
// What: sync.Shared[T], a reference-counted heap value: New, Clone, Ref and
// Free. This mem_ file holds its unsafe blocks.
//
// How: the box holds an atomic count and the value; every handle points at
// it. New allocates from the process Heap (`&Heap`, never `&Alloc` or
// `&Arena`, because Clone's `from self.ptr` steps past the allocator's loan),
// Clone adds one, Free subtracts one and the last Free drops the value (a
// linear T panics) and frees the box. A cycle of Shared values leaks, with
// no safety consequence.

import std.raw
import std.core

/// A handle on a value of type T in a reference-counted box on the heap,
/// for a value with no single owner, such as a cache entry or a
/// configuration many tasks read. Every handle reaches the same value,
/// read-only; put a Mutex or RwLock inside for a value that changes. The
/// box lives until the last handle is freed.
///
/// Linear: end each handle with Free. Send and Share when T is Send and
/// Share. A type may hold a Shared of itself, as
/// `Ring { next: Mutex[?sync.Shared[Ring]] }` does, with no layout cycle.
/// A cycle of Shared values is never freed: it leaks memory, and nothing
/// unsafe follows.
export linear view struct Shared[T] {
    ptr: *SharedBox[T]
    marker: [0]&T
}

struct SharedBox[T] {
    count: raw.Atomic[i64]
    value: T
}

unsafe impl[T: Send + Share] Send for Shared[T] {}
unsafe impl[T: Send + Share] Share for Shared[T] {}

/// A first handle on a new box holding x. x is consumed only on success:
/// OutOfMemory when h cannot give the box, and the caller keeps x. O(1);
/// allocates one box in h, an 8-byte count beside the value.
export Shared[T].New(h: &Heap, x: T): OutOfMemory!Shared[T] from h, x {
    size := core.SizeOfT[SharedBox[T]]()
    q := core.AllocBytes(h.Alloc(), size, core.AlignOfT[SharedBox[T]]())
    if raw.IsNull(q) {
        fail OutOfMemory
    }
    p := BoxAt[T](q)
    unsafe "p is a fresh allocation of one SharedBox, holding no value" {
        raw.Write(p, SharedBox[T](count: raw.Atomic[i64].New(1), value: x))
    }
    return Shared[T](ptr: p, marker: [])
}

/// One more handle on the same box. The count's overflow panics with code
/// 1, which takes far more handles than a program can make. The count's
/// change orders nothing: the new handle comes from a live one, which
/// already keeps the box alive. Not a cancellation point. O(1); allocates
/// nothing.
export Shared[T].Clone(&self): Shared[T] from self.ptr {
    c := CountAt(self.ptr)
    if c.Add(1) == MaxCount {
        _ = c.Add(-1)
        raw.Fail(1, "Shared.Clone: reference count overflow")
    }
    return Shared[T](ptr: self.ptr, marker: [])
}

/// The shared value, for reading, borrowed from this handle. O(1);
/// allocates nothing.
export Shared[T].Ref(&self): &T {
    return ValueAt(self.ptr)
}

/// Ends this handle. The last handle drops the value and frees the box
/// into h, which must be the Heap New took it from; any other handle only
/// lowers the count. Each Free is a release of its handle's uses of the
/// value, and the last one an acquire of them all, so the task that frees
/// the box sees every other handle's use as done. A last Free of a box
/// holding a linear T panics with code 8, since the value would be dropped
/// unended. O(1) besides dropping the value; allocates nothing.
export Shared[T].Free(self, h: &Heap) {
    Shared(ptr, marker) := self
    unsafe "a zero-length marker holds no value" {
        raw.Forget(marker)
    }
    if CountAt(ptr).Add(-1) != 1 {
        return
    }
    if raw.IsLinear[T]() {
        raw.Fail(8, "Shared.Free of the last handle would drop a linear value")
    }
    unsafe "the last handle: the box holds an initialized value nobody else reaches" {
        raw.Forget(raw.Read(ptr))
    }
    q := BoxBytes(ptr)
    core.FreeBytes(h.Alloc(), q, core.SizeOfT[SharedBox[T]](), core.AlignOfT[SharedBox[T]]())
}

// MaxCount is the largest count; Clone of a box at it would wrap.
const MaxCount: i64 = 9223372036854775807

// BoxAt and BoxBytes are pointer casts; nothing is dereferenced.
BoxAt[T](q: *u8): *SharedBox[T] {
    unsafe "a pointer cast of a fresh allocation sized and aligned for SharedBox[T]" {
        return raw.PtrCast[u8, SharedBox[T]](q)
    }
}

BoxBytes[T](p: *SharedBox[T]): *u8 {
    unsafe "a pointer cast; nothing is dereferenced" {
        return raw.PtrCast[SharedBox[T], u8](p)
    }
}

// CountAt and ValueAt are the fields of the live box at p: a handle keeps
// the box alive, and the count is changed only atomically.
CountAt[T](p: *SharedBox[T]): &raw.Atomic[i64] {
    unsafe "p is the live box a handle keeps alive" {
        return &(*p).count
    }
}

ValueAt[T](p: *SharedBox[T]): &T {
    unsafe "p is the live box a handle keeps alive; the value is only read through &" {
        return &(*p).value
    }
}
