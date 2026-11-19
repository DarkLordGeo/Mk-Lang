// Why: the simulated file system (disk.mk) keys its nodes by path, so two
// spellings of one path must reduce to the same bytes on every target.
//
// What: IsSep, CleanPath, ParentLen, BytesEq and IsRoot.
//
// How: a path is cleaned as Go's path.Clean cleans it, with \ read as /,
// and compared by its cleaned bytes. Pure functions over bytes; none
// allocates.

IsSep(c: u8): bool {
    return c == b'/' || c == b'\\'
}

// CleanPath writes the cleaned form of p into out (Go's path.Clean, with \
// as /) and returns its length, or -1 when it does not fit or p holds a NUL.
CleanPath(p: str, out: mut []u8): int {
    b := p.AsBytes()
    n := b.Len()
    if n == 0 {
        out[0] = b'.'
        return 1
    }
    for i in 0..n {
        if b[i] == 0 {
            return -1
        }
    }
    rooted := IsSep(b[0])
    var o := 0
    var r := 0
    var dotdot := 0
    if rooted {
        out[0] = b'/'
        o = 1
        r = 1
        dotdot = 1
    }
    for r < n {
        if IsSep(b[r]) {
            r += 1
        } else if b[r] == b'.' && (r + 1 == n || IsSep(b[r + 1])) {
            r += 1
        } else if b[r] == b'.' && b[r + 1] == b'.' && (r + 2 == n || IsSep(b[r + 2])) {
            r += 2
            if o > dotdot {
                o -= 1
                for o > dotdot && out[o] != b'/' {
                    o -= 1
                }
            } else if !rooted {
                if o > 0 {
                    if o >= out.Len() {
                        return -1
                    }
                    out[o] = b'/'
                    o += 1
                }
                if o + 2 > out.Len() {
                    return -1
                }
                out[o] = b'.'
                out[o + 1] = b'.'
                o += 2
                dotdot = o
            }
        } else {
            if rooted && o != 1 || !rooted && o != 0 {
                if o >= out.Len() {
                    return -1
                }
                out[o] = b'/'
                o += 1
            }
            for r < n && !IsSep(b[r]) {
                if o >= out.Len() {
                    return -1
                }
                out[o] = b[r]
                o += 1
                r += 1
            }
        }
    }
    if o == 0 {
        out[0] = b'.'
        return 1
    }
    return o
}

// ParentLen is the length of a cleaned path's parent: the bytes before its
// last /, 1 for "/x", and 0 for a name with no / (the parent is ".").
ParentLen(p: []u8): int {
    var i := p.Len() - 1
    for i >= 0 && p[i] != b'/' {
        i -= 1
    }
    if i < 0 {
        return 0
    }
    if i == 0 {
        return 1
    }
    return i
}

BytesEq(a: []u8, b: []u8): bool {
    if a.Len() != b.Len() {
        return false
    }
    for i in 0..a.Len() {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}

// IsRoot: "." and "/" are directories that always exist.
IsRoot(p: []u8): bool {
    return p.Len() == 1 && (p[0] == b'.' || p[0] == b'/')
}
