// std.fs's surface, in the run directory: a tree made with
// MakeDirAll, a file opened with options (create-new, append, read-write), Seek, Size, Truncate
// and ReadAt; a lock taken, tried from a second handle and released; a read-only mapping of the
// locked file; a recursive walk; Copy, CreateTemp, Rename and RemoveAll; StatLink of a file;
// Info's kind and modified time, set with SetModified; Cwd and Absolute; and the errors of a
// missing file, an existing directory and a create-new over a file. Paths are printed relative
// and names in sorted order, so the output is the same on every target. Permission bits and
// symbolic links are left out: they differ on the drive a Linux run directory sits on.
import std.fs
import std.time
import mapped

// KindName names a file kind.
KindName(k: fs.Kind): str {
    return match k {
        fs.Kind.File { "file" }
        fs.Kind.Dir { "dir" }
        fs.Kind.Link { "link" }
        fs.Kind.Other { "other" }
        _ {
            Panic("fs.Kind gained a member this match does not handle")
        }
    }
}

// Show prints the kind of p and, unless it is a directory, whose size differs between file
// systems, its size; or the error.
Show(w: mut Writer, io: Io, p: str): WriteError!void {
    info := fs.Stat(io, p) else e {
        try Print(w, "stat {p}: {e}\n", (p: p, e: e))
        return
    }
    if info.kind == fs.Kind.Dir {
        try Print(w, "stat {p}: dir\n", (p: p))
        return
    }
    try Print(w, "stat {p}: {k}, {n} bytes\n", (p: p, k: KindName(info.kind), n: info.size))
}

// Walked prints every entry under root as its depth, kind and base name, sorted.
Walked(w: mut Writer, a: &Arena, h: &Heap, io: Io, root: str): WriteError!void {
    var names := List[str].New(a)
    var walker := fs.Walk(h, io, root) else e {
        try Print(w, "walk {root}: {e}\n", (root: root, e: e))
        return
    }
    defer walker.Close()
    for {
        entry := walker.Next(a, io) else e {
            try Print(w, "walk {root}: {e}\n", (root: root, e: e))
            break
        }
        we := entry else {
            break
        }
        base := we.path.Base()
        name := base.Str() else {
            "(not UTF-8)"
        }
        line := a.Fmt("{d} {k} {name}", (d: we.depth, k: KindName(we.kind), name: name)) else {
            break
        }
        names.Push(line) else {
            break
        }
    }
    names.Sort(fn(x: &str, y: &str): bool {
        return *x < *y
    })
    try Print(w, "walk {root}: {n} entries\n", (root: root, n: names.Len()))
    for name in names {
        try Print(w, "  {name}\n", (name: name))
    }
}

// Options shows OpenWith's presets and fields: create-new, append, then read and write at
// positions.
Options(w: mut Writer, io: Io): fs.Error|WriteError!void {
    var o := fs.CreateNew
    o.write = true
    var f := try fs.OpenWith(io, "work/log.txt", o)
    onerr f.Abandon()
    try f.WriteAll(io, "one\n"[..].AsBytes())
    try f.Close(io)
    match fs.OpenWith(io, "work/log.txt", o) {
        ok(again) {
            again.Abandon()
            try Print(w, "create-new over a file: opened\n")
        }
        err(e) {
            try Print(w, "create-new over a file: {e}\n", (e: e))
        }
    }
    var g := try fs.OpenWith(io, "work/log.txt", fs.Append)
    onerr g.Abandon()
    try g.WriteAll(io, "two\nthree\n"[..].AsBytes())
    try g.Close(io)

    var h := try fs.OpenWith(io, "work/log.txt", fs.ReadWrite)
    onerr h.Abandon()
    size := try h.Size(io)
    end := try h.Seek(io, -6, fs.Whence.End)
    var buf: [16]u8 = [0; 16]
    n := try h.Read(io, mut buf[..])
    tail := str.From(buf[..n]) else {
        "(not UTF-8)"
    }
    var three: [3]u8 = [0; 3]
    at := try h.ReadAt(io, 4, mut three[..])
    mid := str.From(three[..at]) else {
        "(not UTF-8)"
    }
    try h.Truncate(io, 4)
    after := try h.Size(io)
    try h.Close(io)
    try Print(w, "options: size {size}, seek to {end}, tail {t}, at 4 {mid}, truncated to {after}\n", (size: size, end: end, t: tail.Trim(), mid: mid, after: after))
}

// Locked takes an exclusive lock, tries it from a second handle, maps the file while it holds
// the lock, and releases it.
Locked(w: mut Writer, io: Io): fs.Error|WriteError!void {
    var data: [256]u8 = [0; 256]
    for i in 0..256 {
        data[i] = Cast[u8](i)
    }
    try fs.WriteAll(io, "work/blob.bin", data[..])
    f := try fs.OpenWith(io, "work/blob.bin", fs.ReadWrite)
    onerr f.Abandon()
    try f.Lock(io, fs.LockMode.Exclusive)
    g := try fs.Open(io, "work/blob.bin")
    onerr g.Abandon()
    busy := try g.TryLock(io, fs.LockMode.Shared)
    whole := try mapped.Sum(io, &f, 0, 256)
    part := try mapped.Sum(io, &f, 200, 56)
    empty := try mapped.Sum(io, &f, 0, 0)
    try f.Unlock(io)
    free := try g.TryLock(io, fs.LockMode.Shared)
    try g.Unlock(io)
    try g.Close(io)
    try f.Close(io)
    try Print(w, "lock: second handle while held {busy}, after unlock {free}\n", (busy: busy, free: free))
    try Print(w, "map: whole {whole}, from 200 {part}, empty {empty}\n", (whole: whole, part: part, empty: empty))
}

// Staged writes text to a temporary file made in dir, closes it and renames it to dst, and
// reports whether the temporary file's path was under dir.
Staged(a: &Arena, io: Io, dir: str, dst: str, text: str): fs.Error|OutOfMemory!bool {
    (file, path) := try fs.CreateTemp(a, io, dir, "scratch-")
    var f := file
    onerr f.Abandon()
    try f.WriteAll(io, text.AsBytes())
    try f.Close(io)
    try fs.Rename(io, path, dst)
    return path.StartsWith(dir)
}

export Main(sys: &System): WriteError!void {
    a := sys.arena
    io := sys.io
    var out := sys.Stdout()
    fs.RemoveAll(io, "work") else e {
        try Print(mut out, "remove work: {e}\n", (e: e))
        return
    }
    fs.MakeDirAll(io, "work/a/b/c") else e {
        try Print(mut out, "make dirs: {e}\n", (e: e))
        return
    }
    fs.MakeDirAll(io, "work/a/b") else e {
        try Print(mut out, "make dirs again: {e}\n", (e: e))
    }
    fs.MakeDir(io, "work/a") else e {
        try Print(mut out, "make an existing dir: {e}\n", (e: e))
    }
    match fs.Open(io, "work/missing.txt") {
        ok(f) {
            f.Abandon()
            try Print(mut out, "open a missing file: opened\n")
        }
        err(e) {
            try Print(mut out, "open a missing file: {e}\n", (e: e))
        }
    }
    Options(mut out, io) else e {
        try Print(mut out, "options: {e}\n", (e: e))
    }
    Locked(mut out, io) else e {
        try Print(mut out, "lock: {e}\n", (e: e))
    }

    copied := fs.Copy(io, "work/blob.bin", "work/a/b/c/blob.copy") else e {
        try Print(mut out, "copy: {e}\n", (e: e))
        return
    }
    try Print(mut out, "copy: {copied} bytes\n", (copied: copied))
    fs.WriteAll(io, "work/a/note.txt", "kept\n"[..].AsBytes()) else e {
        try Print(mut out, "write note: {e}\n", (e: e))
    }
    fs.Rename(io, "work/a/note.txt", "work/a/b/note.txt") else e {
        try Print(mut out, "rename: {e}\n", (e: e))
    }
    t := time.FromUnix(1_700_000_000, 0)
    fs.SetModified(io, "work/a/b/note.txt", t) else e {
        try Print(mut out, "set modified: {e}\n", (e: e))
    }
    info := fs.StatLink(io, "work/a/b/note.txt") else e {
        try Print(mut out, "stat link: {e}\n", (e: e))
        return
    }
    try Print(mut out, "stat link of a file: {k}, {n} bytes, modified {m}\n", (k: KindName(info.kind), n: info.size, m: info.modified))
    try Show(mut out, io, "work/a/b")
    try Show(mut out, io, "work/a/b/c/blob.copy")

    under := Staged(a, io, "work/a", "work/a/temp.txt", "temporary\n") else e {
        try Print(mut out, "create temp: {e}\n", (e: e))
        return
    }
    try Print(mut out, "create temp: in its directory {under}\n", (under: under))
    try Walked(mut out, a, sys.heap, io, "work")

    cwd := fs.Cwd(a, io) else e {
        try Print(mut out, "cwd: {e}\n", (e: e))
        return
    }
    abs := fs.Absolute(a, io, "work/a") else e {
        try Print(mut out, "absolute: {e}\n", (e: e))
        return
    }
    try Print(mut out, "absolute: under the working directory {under}, ends in the name {name}\n", (under: abs.StartsWith(cwd) && abs.Len() > cwd.Len(), name: abs.EndsWith("a")))

    fs.RemoveAll(io, "work/a") else e {
        try Print(mut out, "remove all: {e}\n", (e: e))
    }
    fs.RemoveAll(io, "work/a") else e {
        try Print(mut out, "remove all again: {e}\n", (e: e))
    }
    try Walked(mut out, a, sys.heap, io, "work")
    fs.RemoveAll(io, "work") else e {
        try Print(mut out, "remove work: {e}\n", (e: e))
    }
    try Show(mut out, io, "work")
}
