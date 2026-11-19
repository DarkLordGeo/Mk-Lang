// Why: \p{..}, \P{..} and (?i) must read the generated tables of tables.mk
// by name and fold case exactly as Go's regexp does.
//
// What: UcdFind, InUcd, Folds and FoldAt, the lookups over tables.mk.
//
// How: names are matched as Go's regexp/syntax matches them (canonicalName:
// _ - and space dropped, the first letter upper case, the rest lower case),
// and the orbits are unicode.SimpleFold's.

// UcdFind is the index of the table named name in UcdNames, or -1.
UcdFind(name: []u8): int {
    var buf: [40]u8 = [0; 40]
    var n := 0
    var first := true
    for c0 in name {
        var c := c0
        if c == b'_' || c == b'-' || c == b' ' {
            continue
        }
        if first {
            if c >= b'a' && c <= b'z' {
                c -= 32
            }
            first = false
        } else if c >= b'A' && c <= b'Z' {
            c += 32
        }
        if n == 40 {
            // Longer than every name in the tables.
            return -1
        }
        buf[n] = c
        n += 1
    }
    key := buf[0..n]
    var lo := 0
    var hi := UcdNames.Len()
    for lo < hi {
        mid := lo + (hi - lo) / 2
        c := ByteOrder(UcdNames[mid].AsBytes(), key)
        if c == 0 {
            return mid
        }
        if c < 0 {
            lo = mid + 1
        } else {
            hi = mid
        }
    }
    return -1
}

// ByteOrder orders byte strings: negative when x < y, 0 when equal.
ByteOrder(x: []u8, y: []u8): int {
    n := if y.Len() < x.Len() { y.Len() } else { x.Len() }
    for i in 0..n {
        if x[i] != y[i] {
            return Cast[int](x[i]) - Cast[int](y[i])
        }
    }
    return x.Len() - y.Len()
}

// InUcd: scalar c is in table k of UcdNames, by binary search of its
// ranges, O(log r).
InUcd(k: int, c: u32): bool {
    first := Cast[int](UcdStart[k])
    var lo := 0
    var hi := Cast[int](UcdCount[k])
    for lo < hi {
        mid := lo + (hi - lo) / 2
        j := first + mid
        if c < UcdRanges[2 * j] {
            hi = mid
        } else if c > UcdRanges[2 * j + 1] {
            lo = mid + 1
        } else {
            return true
        }
    }
    return false
}

// FoldAt is the index of c in FoldKeys, or the index where it would go
// (the first key above c), with found telling which.
FoldAt(c: u32): (i: int, found: bool) {
    var lo := 0
    var hi := FoldKeys.Len()
    for lo < hi {
        mid := lo + (hi - lo) / 2
        k := FoldKeys[mid]
        if k == c {
            return (i: mid, found: true)
        }
        if k < c {
            lo = mid + 1
        } else {
            hi = mid
        }
    }
    return (i: lo, found: false)
}

// Folds: c's simple case-fold orbit holds another scalar.
Folds(c: u32): bool {
    return FoldAt(c).found
}
