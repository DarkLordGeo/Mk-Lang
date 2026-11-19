// Why: both panes and the `ls` command list directories the same way, with
// paths that work on Windows and Linux.
// What: Entry, ListDir (directories first), Join, Parent, BaseName, Clean,
// IndexOf and Mark.
// How: paths are joined with `/`, which both targets accept, and cleaned
// lexically; a name that is not UTF-8 shows U+FFFD.

import std.fs
import std.time

/// One entry of a directory as fm shows it. `name` is printable: a name
/// that is not UTF-8 holds U+FFFD where its bytes are not.
export struct Entry {
    export name: str
    export kind: fs.Kind
    export size: i64
    export modified: time.Time
}

/// Joins `dir` and `name` with `/`, which both targets accept; a `dir`
/// that already ends in a separator gets none. O(n); allocates the path
/// in `a`.
export Join(a: &Arena, dir: str, name: str): OutOfMemory!str from a {
    if dir.EndsWith("/") || dir.EndsWith("\\") {
        return try a.Fmt("{dir}{name}", (dir, name))
    }
    return try a.Fmt("{dir}/{name}", (dir, name))
}

/// The directory that holds `p`: `p` up to its last separator, or `none`
/// for a root (`/`, `C:\`) or a path with no separator. O(n); allocates
/// nothing.
export Parent(p: str): ?str {
    var end := p.Len()
    for end > 0 && IsSep(p.Byte(end - 1)) {
        end -= 1
    }
    var i := end
    for i > 0 && !IsSep(p.Byte(i - 1)) {
        i -= 1
    }
    if i == 0 {
        return none
    }
    var cut := i - 1
    for cut > 0 && IsSep(p.Byte(cut - 1)) {
        cut -= 1
    }
    // a root keeps its separator: "/" and "C:\" name a directory, "" and "C:" do not
    if cut == 0 || (cut == 2 && p.Byte(1) == b':') {
        cut += 1
    }
    if cut >= end {
        return none
    }
    return some(p[..cut])
}

/// The last element of `p`, without trailing separators. O(n); allocates
/// nothing.
export BaseName(p: str): str {
    var end := p.Len()
    for end > 0 && IsSep(p.Byte(end - 1)) {
        end -= 1
    }
    var i := end
    for i > 0 && !IsSep(p.Byte(i - 1)) {
        i -= 1
    }
    return p[i..end]
}

IsSep(b: u8): bool {
    return b == b'/' || b == b'\\'
}

/// The mark fm puts after a name: `/` for a directory, `@` for a link,
/// `?` for anything that is neither a file nor these, and nothing for a
/// file. O(1).
export Mark(k: fs.Kind): str {
    return match k {
        .File { "" }
        .Dir { "/" }
        .Link { "@" }
        .Other { "?" }
        _ {
            Panic("fs.Kind gained a member this match does not handle")
        }
    }
}

/// Lists the entries of `dir` one level deep, directories first and each
/// group in byte order of the names. It walks `dir` with `fs.Walk`, calls
/// `SkipDir` on every subdirectory, and reads each entry with
/// `fs.StatLink`, which does not follow links, so a link is listed as a
/// link. Errors are the walk's and the stat's. O(n log n) for n entries;
/// allocates the list and the names in `a`, and the walker's names on `h`.
export ListDir(a: &Arena, h: &Heap, io: Io, dir: str): fs.Error|OutOfMemory!List[Entry] from a {
    var out := List[Entry].New(a)
    var w := try fs.Walk(h, io, dir)
    onerr w.Close()
    for e := try w.Next(a, io) {
        if e.depth == 0 {
            continue
        }
        if e.kind == fs.Kind.Dir {
            w.SkipDir()
        }
        base := e.path.Base()
        name := try a.Fmt("{n}", (n: &base))
        info := if p := e.path.Str() {
            try fs.StatLink(io, p)
        } else {
            try fs.StatPath(io, &e.path)
        }
        try out.Push(Entry(name: name, kind: info.kind, size: info.size, modified: info.modified))
    }
    w.Close()
    out.Sort(DirsFirst)
    return out
}

DirsFirst(x: &Entry, y: &Entry): bool {
    xd := x.kind == fs.Kind.Dir
    yd := y.kind == fs.Kind.Dir
    if xd != yd {
        return xd
    }
    return x.name < y.name
}

/// Finds the entry named `name` in `entries`. O(n); allocates nothing.
export IndexOf(entries: []Entry, name: str): ?int {
    for i, e in entries {
        if e.name == name {
            return some(i)
        }
    }
    return none
}

/// `path` with its `.` elements dropped and each `..` taken with the element
/// before it, lexically, as Go's `filepath.Clean` does; a leading `/` or
/// drive (`C:`) is kept, a `..` at a root is dropped, and the result's
/// separators are `/`. An empty result is `.`. O(n); allocates the result
/// in `a`.
export Clean(a: &Arena, path: str): OutOfMemory!str from a {
    // the elements are views of a copy in `a`, so the list holds nothing of the caller's
    p := try a.Dup(path)
    var parts := List[str].New(a)
    var start := 0
    var prefix := ""
    if p.Len() >= 2 && p.Byte(1) == b':' {
        prefix = p[..2]
        start = 2
    }
    rooted := start < p.Len() && IsSep(p.Byte(start))
    for i in start..p.Len() + 1 {
        if i < p.Len() && !IsSep(p.Byte(i)) {
            continue
        }
        part := p[start..i]
        start = i + 1
        if part.Len() == 0 || part == "." {
            continue
        }
        if part == ".." {
            n := parts.Len()
            if n > 0 && parts[n - 1] != ".." {
                _ = parts.Pop()
                continue
            }
            if rooted {
                continue
            }
        }
        try parts.Push(part)
    }
    var t := Text.New(a)
    try t.Push(prefix)
    if rooted {
        try t.Push("/")
    }
    for i, part in parts {
        if i > 0 {
            try t.Push("/")
        }
        try t.Push(part)
    }
    if t.Str().Len() == 0 {
        return "."
    }
    return t.Done()
}
