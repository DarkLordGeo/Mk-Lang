// Why: copy, move and tree sizes are the file manager's real work, shared by
// the interactive panes and the commands.
// What: CopyTree, Move, TreeUsage, and the IntoItself error.
// How: trees are walked with fs.Walk; a move tries fs.Rename and falls back
// to copy then remove across volumes; a copy into itself is refused before
// anything is written.

import std.fs

/// The destination of a copy or move lies inside its source.
export error IntoItself

/// What fm's file operations fail with: the file system's errors, memory,
/// and a copy or move of a directory into itself.
export type OpError = fs.Error | OutOfMemory | IntoItself

/// What a copy did: the files and links written, the directories made,
/// and the bytes copied.
export struct Copied {
    export files: int
    export dirs: int
    export bytes: i64
}

/// Copies `src` to `dst`. A file is copied with `fs.Copy`; a link is made
/// again with the same target, not followed; a directory is made with
/// `fs.MakeDirAll` and its tree copied entry by entry, walked with
/// `fs.Walk`. A directory whose destination lies inside it fails with
/// `IntoItself` before anything is written. Other errors are the file
/// system's; a failure leaves what was copied before it. O(total size);
/// allocates paths in `a` and the walker's names on `h`.
export CopyTree(a: &Arena, h: &Heap, io: Io, src: str, dst: str): OpError!Copied {
    var done := Copied(files: 0, dirs: 0, bytes: 0)
    info := try fs.StatLink(io, src)
    if info.kind != fs.Kind.Dir {
        try CopyOne(a, io, info.kind, src, dst, mut done)
        return done
    }
    try RefuseInside(a, io, src, dst)
    try fs.MakeDirAll(io, dst)
    done.dirs += 1
    var w := try fs.Walk(h, io, src)
    onerr w.Close()
    for e := try w.Next(a, io) {
        if e.depth == 0 {
            continue
        }
        // a walk of src names every entry as src followed by the rest of its path
        p := e.path.Str() else {
            fail fs.InvalidPath
        }
        target := try a.Fmt("{dst}{rest}", (dst, rest: p[src.Len()..]))
        if e.kind == fs.Kind.Dir {
            try fs.MakeDirAll(io, target)
            done.dirs += 1
            continue
        }
        entry := try fs.StatLink(io, p)
        try CopyOne(a, io, entry.kind, p, target, mut done)
    }
    w.Close()
    return done
}

CopyOne(a: &Arena, io: Io, kind: fs.Kind, src: str, dst: str, done: mut Copied): fs.Error|OutOfMemory!void {
    if kind == fs.Kind.Link {
        target := try fs.ReadLink(a, io, src)
        try fs.Symlink(io, target, dst)
        done.files += 1
        return
    }
    n := try fs.Copy(io, src, dst)
    done.files += 1
    done.bytes += n
}

RefuseInside(a: &Arena, io: Io, src: str, dst: str): OpError!void {
    s := try fs.Absolute(a, io, src)
    d := try fs.Absolute(a, io, dst)
    if d == s {
        fail IntoItself
    }
    if d.StartsWith(s) && d.Len() > s.Len() {
        b := d.Byte(s.Len())
        if b == b'/' || b == b'\\' {
            fail IntoItself
        }
    }
}

/// Moves `src` to `dst` with `fs.Rename`. When the rename fails with an
/// OS error (another volume, say), it copies with `CopyTree` and then
/// removes `src` with `fs.RemoveAll`. A directory moved into itself fails
/// with `IntoItself` first. Errors are the rename's when it is not an OS
/// error, else the copy's and the removal's. O(1) for a rename, else as
/// `CopyTree`; allocates as `CopyTree` does.
export Move(a: &Arena, h: &Heap, io: Io, src: str, dst: str): OpError!void {
    try RefuseInside(a, io, src, dst)
    fs.Rename(io, src, dst) else e {
        match e {
            fs.Os {}
            _ { fail e }
        }
        _ = try CopyTree(a, h, io, src, dst)
        try fs.RemoveAll(io, src)
    }
}

/// The size of a tree: the bytes of its files and links, and how many of
/// each kind it holds, the root included.
export struct Usage {
    export bytes: i64
    export files: int
    export dirs: int
}

/// Adds up the tree under `root`, walked with `fs.Walk`; links are counted
/// as entries and not followed. Errors are the walk's: a directory that
/// cannot be read fails it, and a cancel ends it with `Canceled`. O(n) for
/// n entries; allocates each entry's path in a scratch arena on `h`, reset
/// every 4,096 entries, and the walker's names on `h`.
export TreeUsage(h: &Heap, io: Io, root: str): fs.Error|OutOfMemory!Usage {
    var u := Usage(bytes: 0, files: 0, dirs: 0)
    var scratch := try Arena.New(h)
    defer scratch.Free()
    var w := try fs.Walk(h, io, root)
    onerr w.Close()
    var n := 0
    for {
        if n == 4096 {
            scratch.Reset()
            n = 0
        }
        e := try w.Next(&scratch, io) else {
            break
        }
        n += 1
        if e.kind == fs.Kind.Dir {
            u.dirs += 1
            continue
        }
        u.files += 1
        info := if p := e.path.Str() {
            try fs.StatLink(io, p)
        } else {
            try fs.StatPath(io, &e.path)
        }
        u.bytes += info.size
    }
    w.Close()
    return u
}
