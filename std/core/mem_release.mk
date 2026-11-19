// Why: moving a value between two places, or out of one, must work for any
// T, linear included, without a copy and without a loan around it; and the
// containers need one function that releases a value they own.
//
// What: Swap and Replace, the Free interface, and the helpers NeedsRelease
// and ReleaseValue.
//
// How: Swap and Replace are bitwise exchanges through raw memory, each
// O(SizeOf[T]()), allocating nothing and unable to fail. ReleaseValue hands
// a T that implements Free to its Free and drops any other; NeedsRelease is
// a per-instance constant, so a container of plain values frees its storage
// without walking it.

import std.raw

/// Exchanges the values of a and b, for any T, linear included.
/// O(SizeOf[T]()); allocates nothing and cannot fail.
export Swap[T](a: mut T, b: mut T) *a into b, *b into a {
    p := raw.OfMut(a)
    q := raw.OfMut(b)
    unsafe "a and b are live mut references, which the loan rules make two distinct places (E0501 refuses a mut borrow of a place already borrowed); each holds an initialized T, and each value moves to the other place, so both stay initialized" {
        t := raw.Read(p)
        raw.Write(p, raw.Read(q))
        raw.Write(q, t)
    }
}

/// Puts x in dst and returns the value that was there, for any T, linear
/// included: the way to take a linear value out of a place.
/// O(SizeOf[T]()); allocates nothing and cannot fail.
export Replace[T](dst: mut T, x: T into dst): T {
    p := raw.OfMut(dst)
    unsafe "dst is a live mut reference to an initialized T; its value moves out to the caller and x moves in, so the place stays initialized" {
        old := raw.Read(p)
        raw.Write(p, x)
        return old
    }
}

/// The interface of a value that owns memory from an allocator and gives it
/// back to a. The prelude's HeapList, HeapMap, TextMap, HeapText, Box and Chan
/// implement it, as do std.coll's HeapDeque and HeapBTree and std.net's
/// ConnSlot. A container's Free is deep: it releases every element with a
/// before its storage. Such a Free compiles only when each element type is not
/// linear or is Free (E0701 at the call, which names FreeEmpty and FreeEach as
/// the alternatives).
export interface Free {
    Free(self, a: &Alloc)
}

// NeedsRelease is whether a value of T has anything to release: a T that
// is linear, or one that implements Free. Any other T owns nothing a drop
// must give back, so a container of it frees its storage
// without walking its elements. A constant per instance.
NeedsRelease[T](): bool {
    unsafe "HasFree only asks whether T has an impl of Free; it reads and writes nothing" {
        return raw.IsLinear[T]() || raw.HasFree[T]()
    }
}

// ReleaseValue gives x up with a: every element, surplus key and old value
// a container releases goes through here, so the release protocol's deep
// Free is this one function. A T that implements Free is given to
// its Free; any other T is dropped. check admits a release only when T is
// not linear or is Free (E0701 at the call). A linear T without Free that
// reaches here anyway panics with code 11 and never reaches
// raw.Release: mono folds HasFree per instance and prunes the call, where a
// Release reachable for such a T would stop the build.
ReleaseValue[T](x: T, a: &Alloc) {
    unsafe "HasFree only asks whether T has an impl of Free; Release then calls that Free with a" {
        if raw.HasFree[T]() {
            raw.Release(x, a)
            return
        }
    }
    if raw.IsLinear[T]() {
        raw.Fail(11, "release of a linear value that is not Free")
    }
    unsafe "T is not linear, so x owns nothing a drop must give back" {
        raw.Forget(x)
    }
}
