// Why: an archive is made from a directory tree and unpacked into one, and
// both directions must refuse names an archive cannot hold.
//
// What: Walk, Gather (reads the files and computes BLAKE3 digests), MakeDir,
// Item, and the errors BadName and Changed.
//
// How: the walk is sorted by path to match the manifest's order; a file
// whose size changes while it is read fails with Changed.

import src.archive
import std.fs
import std.hash

/// A name in the tree that an archive cannot hold: not UTF-8, or with a
/// newline, a `\` or a `:` in it. `depth` is how deep it is.
export error BadName(depth: int)
/// A file changed size while it was read.
export error Changed

/// The errors of walking and reading a tree.
export type Error = BadName | Changed | fs.Error | OutOfMemory

/// One path under the root, relative, separated by `/`.
export struct Item {
    export path: str
    export is_dir: bool
    export size: i64
}

/// Every directory and file under `root`, the root itself excluded, sorted by
/// path. Fails with `BadName` for a name an archive cannot hold, and with the
/// file system's errors. O(n log n) in the entries; the list and the paths
/// live in `a`.
export Walk(a: &Arena, io: Io, root: str): Error!List[Item] from a {
    var out := List[Item].New(a)
    try WalkDir(a, io, root, "", 1, mut out)
    out.Sort(fn(x: &Item, y: &Item): bool { return x.path < y.path })
    return out
}

WalkDir(a: &Arena into out, io: Io, root: str, rel: str, depth: int, out: mut List[Item]): Error!void {
    full := if rel.Len() == 0 { root } else { try a.Fmt("{root}/{rel}", (root, rel)) }
    var dir := try fs.Dir.Open(io, full)
    defer dir.Close()
    for {
        entry: fs.Entry = try dir.Next(a, io) else {
            break
        }
        name := entry.name.Str() else {
            fail BadName(depth: depth)
        }
        if name.Has("\n") || name.Has("\\") || name.Has(":") {
            fail BadName(depth: depth)
        }
        path := if rel.Len() == 0 { name } else { try a.Fmt("{rel}/{name}", (rel, name)) }
        try out.Push(Item(path: path, is_dir: entry.is_dir, size: if entry.is_dir { 0 } else { entry.size }))
        if entry.is_dir {
            try WalkDir(a, io, root, path, depth + 1, out)
        }
    }
}

/// Reads every file of `items` under `root`, in order, onto the end of
/// `data`, and gives the manifest entries with their offsets and BLAKE3
/// digests. Fails with `Changed` when a file's size differs from what the
/// walk saw, with `OutOfMemory` when `data` cannot grow in `alloc`, and with
/// the file system's errors. O(n) in the bytes; the entries live in `a`, a
/// 64 KiB read buffer in a sub-arena.
export Gather(a: &Arena, io: Io, alloc: &Alloc, root: str, items: []Item, data: mut HeapList[u8]): Error!List[archive.Entry] from a, items *items into a {
    var scratch := try a.Sub()
    defer scratch.Free()
    buf := try scratch.Slice[u8](65536)
    var out := List[archive.Entry].New(a)
    for it in items {
        var e := archive.Entry(path: it.path, is_dir: it.is_dir, size: 0, offset: i64(data.Len()), digest: [0; 32])
        if !it.is_dir {
            full := try scratch.Fmt("{root}/{p}", (root, p: it.path))
            var f := try fs.Open(io, full)
            onerr f.Abandon()
            var h := hash.Blake3State.New()
            for {
                n := try f.Read(io, buf)
                if n == 0 {
                    break
                }
                h.Bytes(buf[..n])
                // HeapList has no push of a slice, so the bytes go one at a time
                for b in buf[..n] {
                    try data.Push(alloc, b)
                }
            }
            try f.Close(io)
            e.size = i64(data.Len()) - e.offset
            if e.size != it.size {
                fail Changed
            }
            e.digest = h.Sum()
        }
        try out.Push(e)
    }
    return out
}

/// Makes the directory `path`; one that exists already is fine. Fails with
/// the file system's other errors.
export MakeDir(io: Io, path: str): fs.Error!void {
    fs.MakeDir(io, path) else e {
        info := fs.Stat(io, path) else {
            fail e
        }
        if info.kind != fs.Kind.Dir {
            fail e
        }
    }
}
