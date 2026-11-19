// Why: the same tree must give the same files in the same order on every
// run and target.
// What: Walk, Filter, Source, Glob, AnyGlob and IgnoreFile
// (`.mgrepignore`).
// How: roots as given, then each directory's entries sorted by their bytes;
// hidden files, globs, depth, size and ignore files filter them, and each
// path that cannot be read is reported and counted.

import std.fs
import std.simd

/// The file a directory may hold to name what a walk skips in it, one glob a line.
export const IgnoreFile: str = ".mgrepignore"

/// Which files a walk yields.
export struct Filter {
    /// Walk into names that start with a dot
    export hidden: bool
    /// Globs on the base name; a file must match one when any are given
    export globs: []str
    /// Directory levels below a root to descend; -1 is no limit
    export max_depth: int
    /// Skip files larger than this many bytes; -1 is no limit
    export max_size: i64
    /// Write paths with this separator instead of the system's; "" keeps them
    export separator: str
}

/// One file to search.
export struct Source {
    /// The path as it is printed
    export name: str
    /// The path as it is opened
    export path: str
    /// The path below the root it was found under, for tables
    export rel: str
    /// Its size when it was listed
    export size: i64
}

/// Lists the files under `roots` that `f` admits into `files`, in a fixed
/// order: roots as given, then each directory's entries sorted by their bytes,
/// so every run lists the same files in the same order. A root that is a file
/// is taken as given. Each path that cannot be read is reported on `err` and
/// counted. O(entries walked); allocates the paths in `a`.
export Walk(a: &Arena into files, io: Io, roots: []str, f: &Filter, files: mut List[Source], err: mut Writer): WriteError|OutOfMemory!int *roots into files, *roots into a {
    var problems := 0
    for root in roots {
        info: fs.Info = fs.Stat(io, root) else e {
            try Report(err, root, e)
            try Hint(a, io, root, err)
            problems += 1
            continue
        }
        name := try Display(a, root, f.separator)
        if info.kind != fs.Kind.Dir {
            base := try Path.New(a, root.AsBytes())
            leaf := base.Base()
            rel := leaf.Str() else { root }
            try files.Push(Source(name: name, path: root, rel: rel, size: info.size))
            continue
        }
        dir := try Path.New(a, root.AsBytes())
        problems += try WalkDir(a, io, &dir, name, "", 0, f, files, err)
    }
    return problems
}

// Says which part of a missing root's path is missing too.
Hint(a: &Arena, io: Io, root: str, err: mut Writer): WriteError|OutOfMemory!void {
    p := try Path.New(a, root.AsBytes())
    parent := p.Parent() else {
        return
    }
    up := parent.Str() else {
        return
    }
    if up.Len() == 0 {
        return
    }
    _ = fs.Stat(io, up) else {
        try err.Str("mgrep: note: the directory ")
        try parent.Format(err)
        try err.Str(" does not exist either\n")
        return
    }
}

Report(err: mut Writer, path: str, e: fs.Error): WriteError!void {
    try Print(err, "mgrep: {path}: {e}\n", (path, e))
}

// Walks one directory, its entries in byte order, and descends into its subdirectories.
WalkDir(a: &Arena into files, io: Io, dir: &Path, name: str, rel: str, depth: int, f: &Filter, files: mut List[Source], err: mut Writer): WriteError|OutOfMemory!int {
    var problems := 0
    path := dir.Str() else {
        try Print(err, "mgrep: {dir}: a path that is not UTF-8 is skipped\n", (dir))
        return 1
    }
    var entries := List[fs.Entry].New(a)
    var d := fs.Dir.Open(io, path) else e {
        try Report(err, name, e)
        return 1
    }
    defer d.Close()
    for {
        next := d.Next(a, io) else e {
            match e {
                OutOfMemory {
                    fail OutOfMemory
                }
                _ {
                    try Print(err, "mgrep: {name}: listing stopped early\n", (name))
                    problems += 1
                    break
                }
            }
        }
        entry := next else {
            break
        }
        try entries.Push(entry)
    }
    entries.Sort(fn(x: &fs.Entry, y: &fs.Entry): bool { return simd.Compare(x.name.AsBytes(), y.name.AsBytes()) < 0 })
    ignore := try Ignored(a, io, dir)
    for entry in &entries {
        base := entry.name.Base()
        leaf := base.Str() else {
            try Print(err, "mgrep: {name}: an entry whose name is not UTF-8 is skipped\n", (name))
            problems += 1
            continue
        }
        if !f.hidden && leaf.StartsWith(".") {
            continue
        }
        if AnyGlob(ignore[..], leaf) {
            continue
        }
        child := try dir.Join(a, leaf)
        shown := try Joined(a, name, leaf, f.separator)
        below := if rel.Len() == 0 { leaf } else { try Joined(a, rel, leaf, f.separator) }
        if entry.is_dir {
            if f.max_depth < 0 || depth < f.max_depth {
                problems += try WalkDir(a, io, &child, shown, below, depth + 1, f, files, err)
            }
            continue
        }
        if f.globs.Len() > 0 && !AnyGlob(f.globs, leaf) {
            continue
        }
        if f.max_size >= 0 && entry.size > f.max_size {
            continue
        }
        full := child.Str() else {
            continue
        }
        try files.Push(Source(name: shown, path: full, rel: below, size: entry.size))
    }
    return problems
}

// The globs of the ignore file in `dir`, or none when it has none.
Ignored(a: &Arena, io: Io, dir: &Path): OutOfMemory!List[str] from a {
    var out := List[str].New(a)
    p := try dir.Join(a, IgnoreFile)
    path := p.Str() else {
        return out
    }
    body := fs.ReadText(a, io, path) else {
        return out
    }
    for line in body.Lines() {
        g := line.Trim()
        if g.Len() > 0 && !g.StartsWith("#") {
            try out.Push(g)
        }
    }
    return out
}

// A path as printed: `s` with each separator written as `sep`, or as given when `sep` is empty.
Display(a: &Arena, s: str, sep: str): OutOfMemory!str from a, s {
    if sep.Len() == 0 {
        return s
    }
    var out := Text.New(a)
    var from := 0
    for i in 0..s.Len() {
        b := s.Byte(i)
        if b == b'/' || b == b'\\' {
            try out.Push(s[from..i])
            try out.Push(sep)
            from = i + 1
        }
    }
    try out.Push(s[from..])
    return out.Done()
}

// The printed name of `leaf` inside the directory printed as `name`.
Joined(a: &Arena, name: str, leaf: str, sep: str): OutOfMemory!str from a {
    s := if sep.Len() > 0 { sep } else if Target.os == Os.Windows { "\\" } else { "/" }
    if name.EndsWith("/") || name.EndsWith("\\") || name.EndsWith(s) {
        return try a.Fmt("{name}{leaf}", (name, leaf))
    }
    return try a.Fmt("{name}{s}{leaf}", (name, s, leaf))
}

/// Reports whether `name` matches any of the comma-separated globs in `globs`.
export AnyGlob(globs: []str, name: str): bool {
    for g in globs {
        for one in g.Split(",") {
            if one.Len() > 0 && Glob(one, name) {
                return true
            }
        }
    }
    return false
}

/// Reports whether `name` matches `pattern`, where `*` stands for any run of
/// bytes and `?` for one byte. O(pattern length times name length) at worst.
export Glob(pattern: str, name: str): bool {
    var p := 0
    var n := 0
    var star := -1
    var mark := 0
    for n < name.Len() {
        if p < pattern.Len() && (pattern.Byte(p) == b'?' || pattern.Byte(p) == name.Byte(n)) {
            p += 1
            n += 1
        } else if p < pattern.Len() && pattern.Byte(p) == b'*' {
            star = p
            mark = n
            p += 1
        } else if star >= 0 {
            p = star + 1
            mark += 1
            n = mark
        } else {
            return false
        }
    }
    for p < pattern.Len() && pattern.Byte(p) == b'*' {
        p += 1
    }
    return p == pattern.Len()
}
