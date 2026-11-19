// Why: a heap container's Free must release each element that owns
// something and skip the walk for elements that own nothing; both questions
// need unsafe intrinsics, kept here so the container files hold none.
//
// What: CollNeedsRelease (whether T has anything to release) and
// CollRelease (give one value up with an allocator), as in std.core.
//
// How: both ask raw.IsLinear and raw.HasFree, which fold to constants per
// instance, so each is a constant or one call.

import std.raw

// CollNeedsRelease is whether a value of T has anything to release: T is
// linear, or implements Free. Any other T owns nothing a drop must give
// back, so a container of it frees its storage without
// walking its elements.
CollNeedsRelease[T](): bool {
    unsafe "HasFree only asks whether T has an impl of Free; it reads and writes nothing" {
        return raw.IsLinear[T]() || raw.HasFree[T]()
    }
}

// CollRelease gives x up with a: the Free of T when T implements it, else a
// drop. check admits a container's Free only when each element type is not
// linear or is Free; a linear T without Free that reaches here anyway
// panics with code 11 and never reaches raw.Release, which mono prunes by
// the folded HasFree.
CollRelease[T](x: T, a: &Alloc) {
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
