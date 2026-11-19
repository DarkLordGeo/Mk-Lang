// Why: a program must find zones on a system with no zone database (Windows,
// containers), and give the same answers on every target.
//
// What: tzdata.Database, the zones built into a program that names it, and
// its lookup Find.
//
// How: the data (zones.mk) is compiled TZif generated from the pinned
// zoneinfo.zip of the Go release std.time is tested against, so it is the
// same on every target and under std.sim. It is reached only through
// Database's lookup function, so a binary that does not name Database links
// none of it.

import std.time

/// The IANA time-zone database built into the program, for Zone.Load's
/// embedded argument: Zone.Load(a, io, "Europe/Paris", some(tzdata.Database)).
/// It is compiled TZif of one IANA release, the same on every target and
/// under std.sim, so a program that uses it finds every zone on a system
/// with no zone database (Windows, a small container). It is linked only
/// into a binary that names Database.
export const Database: time.Database = time.Database(find: Find)

// Find is the TZif bytes of the zone name, none when the database has no
// such zone: a binary search over the sorted names, O(log n) comparisons
// of at most 255 bytes; allocates nothing.
Find(name: str): ?[]u8 {
    b := name.AsBytes()
    t := NameText.AsBytes()
    var lo := 0
    var hi := NameEnd.Len()
    for lo < hi {
        mid := lo + (hi - lo) / 2
        start := if mid == 0 { 0 } else { int(NameEnd[mid - 1]) }
        c := Compare(t[start..int(NameEnd[mid])], b)
        if c == 0 {
            k := int(NameZone[mid])
            zs := if k == 0 { 0 } else { int(ZoneEnd[k - 1]) }
            return some(ZoneData[zs..int(ZoneEnd[k])])
        }
        if c < 0 {
            lo = mid + 1
        } else {
            hi = mid
        }
    }
    return none
}

// Compare orders byte strings as Go's sort does: -1, 0 or 1.
Compare(x: []u8, y: []u8): int {
    n := if x.Len() < y.Len() { x.Len() } else { y.Len() }
    for i in 0..n {
        if x[i] != y[i] {
            return if x[i] < y[i] { -1 } else { 1 }
        }
    }
    if x.Len() == y.Len() {
        return 0
    }
    return if x.Len() < y.Len() { -1 } else { 1 }
}
