// Why: file names from the OS are bytes that may not be UTF-8, so a path
// cannot be a str; the prelude needs a path view that takes it apart without
// allocating.
//
// What: Path with PathOf, AsBytes, Str, Base, Parent and Format, plus the
// UTF-8 repair helpers PathFormat and Utf8Subpart. Path.New and Path.Join
// allocate from an arena, so they are in mem_path.mk.
//
// How: separators are `/` on every target and `\` too on Windows. The root of
// a path is its leading separators, after a drive `X:` on Windows; UNC names
// (\\server\share) are read as a root of separators and their elements. Every
// method but Format is O(n) in the path's length at worst and touches no
// memory but the path's bytes.

/// A file system path: a view over bytes as the OS gives them, which need
/// not be UTF-8. The bytes live in an arena or in a literal's static data,
/// and the Path borrows them. On Windows a Path holds the name's WTF-8, so a
/// name with an unpaired surrogate round-trips.
///
/// In a format hole a Path prints valid UTF-8 as written, and otherwise one
/// U+FFFD for each maximal subpart that is not UTF-8 (Unicode's recommended
/// substitution), so an unpaired surrogate's bytes ED A0 80 print as three.
/// Printing allocates nothing.
export view struct Path {
    bytes: []u8
}

/// The Path over b, without a copy, for std's own modules; programs make
/// paths with Path.New and Path.Join. O(1); allocates nothing.
export PathOf(b: []u8): Path {
    return Path(bytes: b)
}

/// The path's bytes, as the OS gave them. O(1); allocates nothing.
export Path.AsBytes(&self): []u8 from self.bytes {
    return self.bytes
}

/// The path as a str, or none unless its bytes are well-formed UTF-8, as
/// str.From checks them. O(n); allocates nothing.
export Path.Str(&self): ?str from self.bytes {
    s := str.From(self.bytes) else {
        return none
    }
    return some(s)
}

/// The last element of the path. O(n); allocates nothing.
///
/// Trailing separators are dropped first; the path is not otherwise
/// cleaned. "." for an empty path or a drive alone ("C:"), and the root's
/// last separator for a root ("/", "C:\").
export Path.Base(&self): Path from self.bytes {
    b := self.bytes
    r := PathRootLen(b)
    var end := b.Len()
    for end > r && PathIsSep(b[end - 1]) {
        end -= 1
    }
    if end == r {
        if r > 0 && PathIsSep(b[r - 1]) {
            return Path(bytes: b[r - 1..r])
        }
        return Path(bytes: ".".AsBytes())
    }
    var start := end
    for start > r && !PathIsSep(b[start - 1]) {
        start -= 1
    }
    return Path(bytes: b[start..end])
}

/// The parent of the path, or none when it has none. O(n); allocates nothing.
///
/// The path without its last element and the separators before it. none
/// for an empty path, a root, and a single relative element such as "a"
/// ("C:a" has the parent "C:"). Trailing separators are dropped first, so
/// the parent of "a/b/" is "a".
export Path.Parent(&self): ?Path from self.bytes {
    b := self.bytes
    r := PathRootLen(b)
    var end := b.Len()
    for end > r && PathIsSep(b[end - 1]) {
        end -= 1
    }
    if end == r {
        return none
    }
    var s := end
    for s > r && !PathIsSep(b[s - 1]) {
        s -= 1
    }
    if s == r {
        if r == 0 {
            return none
        }
        return some(Path(bytes: b[0..r]))
    }
    var e := s
    for e > r && PathIsSep(b[e - 1]) {
        e -= 1
    }
    return some(Path(bytes: b[0..e]))
}

// The bytes, with each maximal ill-formed subsequence replaced by U+FFFD
// (Unicode §3.9, "U+FFFD Substitution of Maximal Subparts", as Rust's
// Path::display and WHATWG's decoder). Valid text is one Writer.Bytes call
// after one validation pass; otherwise one call per valid run and per
// replacement. No allocation. This impl is Path's Format, as net.Addr has
// one, so a Path formats in Print.
impl Format for Path {
    Format(&self, w: mut Writer): WriteError!void {
        try PathFormat(self.bytes, w)
    }
}

PathFormat(b: []u8, w: mut Writer): WriteError!void {
    if ValidUtf8(b) {
        try w.Bytes(b)
        return
    }
    n := b.Len()
    var start := 0
    var i := 0
    for i < n {
        if b[i] < 0x80 {
            i += 1
            continue
        }
        k := Utf8Subpart(b, i)
        if k > 0 {
            i += k
            continue
        }
        try w.Bytes(b[start..i])
        try w.Bytes(PathReplacement.AsBytes())
        i += 0 - k
        start = i
    }
    try w.Bytes(b[start..n])
}

// U+FFFD in UTF-8.
const PathReplacement = "�"

// Utf8Subpart reads the sequence that starts at b[i], a byte of 0x80 or
// more: its length when it is well formed, else minus the length of its
// maximal subpart (at least 1), by table 3-7 of Unicode §3.9.
Utf8Subpart(b: []u8, i: int): int {
    c := b[i]
    var need := 0
    var lo: u8 = 0x80
    var hi: u8 = 0xBF
    if c >= 0xC2 && c <= 0xDF {
        need = 1
    } else if c == 0xE0 {
        need = 2
        lo = 0xA0
    } else if c >= 0xE1 && c <= 0xEC || c == 0xEE || c == 0xEF {
        need = 2
    } else if c == 0xED {
        need = 2
        hi = 0x9F
    } else if c == 0xF0 {
        need = 3
        lo = 0x90
    } else if c >= 0xF1 && c <= 0xF3 {
        need = 3
    } else if c == 0xF4 {
        need = 3
        hi = 0x8F
    } else {
        return -1
    }
    n := b.Len()
    var k := 1
    for k <= need {
        if i + k >= n {
            return 0 - k
        }
        d := b[i + k]
        if k == 1 {
            if d < lo || d > hi {
                return -1
            }
        } else if d < 0x80 || d > 0xBF {
            return 0 - k
        }
        k += 1
    }
    return need + 1
}

// PathIsSep: `/`, and `\` on Windows.
PathIsSep(c: u8): bool {
    if c == 47 {
        return true
    }
    return Target.os == Os.Windows && c == 92
}

// PathSep is the separator Join writes: `\` on Windows, `/` elsewhere.
PathSep(): u8 {
    if Target.os == Os.Windows {
        return 92
    }
    return 47
}

// PathDriveLen is 2 for a Windows path that starts with a drive `X:`, else 0.
PathDriveLen(b: []u8): int {
    if Target.os != Os.Windows || b.Len() < 2 || b[1] != 58 {
        return 0
    }
    c := b[0] | 0x20
    if c >= 97 && c <= 122 {
        return 2
    }
    return 0
}

// PathRootLen is the length of b's root: the drive, then every separator
// after it.
PathRootLen(b: []u8): int {
    var r := PathDriveLen(b)
    for r < b.Len() && PathIsSep(b[r]) {
        r += 1
    }
    return r
}
