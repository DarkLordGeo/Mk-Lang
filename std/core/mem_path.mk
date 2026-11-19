// Why: building a path (from bytes, or by joining a part) needs fresh memory,
// and the allocation-free rest of Path lives in core_path.mk; these two
// constructors take an arena.
//
// What: Path.New, Path.Join and their helper PathAlloc.
//
// How: each copies the bytes into one block of exactly the result's length
// from the arena; an empty result gets the dangling nonnull pointer and holds
// no memory. Join follows Rust's PathBuf::push: a rooted part keeps only the
// path's drive, a part with a drive replaces the path, and otherwise one
// native separator goes between them when needed.

import std.raw

// PathAlloc is n fresh bytes of a (n >= 0), or null when the arena cannot
// give them; for n == 0 the dangling non-null pointer, so an empty Path
// still has a data pointer and holds no memory.
PathAlloc(a: &Arena, n: int): *u8 {
    if n == 0 {
        return DanglingBytes()
    }
    if n > MaxBytes {
        return raw.Null[u8]()
    }
    return Bump(a.hdr, n, 1)
}

/// A Path holding a copy of bytes in a, as one block of exactly bytes.Len()
/// bytes, aligned 1. OutOfMemory leaves a as it was. O(n).
export Path.New(a: &Arena, bytes: []u8): OutOfMemory!Path from a {
    n := bytes.Len()
    p := PathAlloc(a, n)
    if raw.IsNull(p) {
        fail OutOfMemory
    }
    SlotCopy(p, raw.Data(bytes), n) // p is fresh memory for n bytes
    return PathOf(SlotView(p, n))   // in the arena, which outlives the loan on a
}

/// The path with part appended, in a. One block of exactly the result's
/// length from a; O(n).
///
/// - A part with a root and no drive (`/y`, and `\y` on Windows) replaces
///   the path but keeps its drive: `C:\x` joined with `\y` is `C:\y`.
/// - A part with a drive (`D:\y`, `D:y`, Windows only) replaces the whole
///   path.
/// - Otherwise the result is the path, the native separator, then part. No
///   separator is added when the path is empty, already ends with one, or
///   is a drive alone ("C:").
/// - An empty part gives a copy of the path.
///
/// Drives are the only prefixes Path knows: a UNC `\\server\share` prefix
/// is read as a root of separators.
export Path.Join(&self, a: &Arena, part: str): OutOfMemory!Path from a {
    pb := part.AsBytes()
    if PathDriveLen(pb) > 0 {
        return try Path.New(a, pb)
    }
    if PathRootLen(pb) > 0 {
        d := PathDriveLen(self.bytes)
        if d == 0 {
            return try Path.New(a, pb)
        }
        if pb.Len() > MaxBytes - d {
            fail OutOfMemory
        }
        q := PathAlloc(a, d + pb.Len())
        if raw.IsNull(q) {
            fail OutOfMemory
        }
        SlotCopy(q, raw.Data(self.bytes), d) // q is fresh memory for d + pb.Len() bytes
        SlotCopy(SlotAt(q, d), raw.Data(pb), pb.Len())
        return PathOf(SlotView(q, d + pb.Len()))
    }
    b := self.bytes
    m := b.Len()
    sep := if m > 0 && pb.Len() > 0 && !PathIsSep(b[m - 1]) && !(m == 2 && PathDriveLen(b) == 2) { 1 } else { 0 }
    if pb.Len() > MaxBytes - m - sep {
        fail OutOfMemory
    }
    n := m + sep + pb.Len()
    p := PathAlloc(a, n)
    if raw.IsNull(p) {
        fail OutOfMemory
    }
    SlotCopy(p, raw.Data(b), m) // p is fresh memory for n bytes
    if sep == 1 {
        SlotWrite(p, m, PathSep())
    }
    SlotCopy(SlotAt(p, m + sep), raw.Data(pb), pb.Len())
    return PathOf(SlotView(p, n))
}
