// Why: OpenWith, Seek, Lock, Stat, links, Walk, MakeDirAll, RemoveAll, Copy,
// CreateTemp and Cwd need direct cases on both targets.
// What: test blocks for them, run by mk test --std=std.fs.
// How: each block works on paths named mk_tfs2_<id> in the working
// directory, removes them before and after, and ends with no leak.

import std.core
import std.time

// T2Bytes writes n bytes, i % 251 at i, to p.
T2Bytes(io: Io, p: str, n: int) {
    var b: [4096]u8 = [0; 4096]
    for i in 0..n {
        b[i] = Trunc[u8](i % 251)
    }
    WriteAll(io, p, b[0..n]).Must()
}

test "OpenWith CreateNew makes a new file and fails with Exists on an existing one" (sys: &System) {
    p := "mk_tfs2_new"
    TfsGone(sys.io, p)
    var f := OpenWith(sys.io, p, CreateNew).Must()
    f.WriteAll(sys.io, "abc".AsBytes()).Must()
    f.Close(sys.io).Must()
    var got := ""
    g := OpenWith(sys.io, p, CreateNew) else e {
        got = TfsName(sys.arena, e)
        Assert(got == "Exists")
        Assert(ReadText(sys.arena, sys.io, p).Must() == "abc")
        Remove(sys.io, p).Must()
        return
    }
    g.Abandon()
    Assert(false)
}

test "OpenWith Append writes every Write at the end, ReadWrite keeps the bytes" (sys: &System) {
    p := "mk_tfs2_app"
    TfsGone(sys.io, p)
    WriteAll(sys.io, p, "head".AsBytes()).Must()
    var f := OpenWith(sys.io, p, Append).Must()
    f.WriteAll(sys.io, "-one".AsBytes()).Must()
    // A Seek to the start does not move where an append write lands.
    Assert(f.Seek(sys.io, 0, Whence.Start).Must() == 0)
    f.WriteAll(sys.io, "-two".AsBytes()).Must()
    f.Close(sys.io).Must()
    Assert(ReadText(sys.arena, sys.io, p).Must() == "head-one-two")
    var g := OpenWith(sys.io, p, ReadWrite).Must()
    g.WriteAll(sys.io, "HE".AsBytes()).Must()
    g.Close(sys.io).Must()
    Assert(ReadText(sys.arena, sys.io, p).Must() == "HEad-one-two")
    var o := ReadWrite
    o.truncate = true
    var h := OpenWith(sys.io, p, o).Must()
    Assert(h.Size(sys.io).Must() == 0)
    h.Close(sys.io).Must()
    Remove(sys.io, p).Must()
}

test "OpenWith refuses option sets no target opens alike, before any call" (sys: &System) {
    p := "mk_tfs2_bad"
    TfsGone(sys.io, p)
    var no_access := DefaultOptions
    no_access.read = false
    var no_write := DefaultOptions
    no_write.create = true
    var both := Append
    both.truncate = true
    for o in [no_access, no_write, both] {
        f := OpenWith(sys.io, p, o) else e {
            Assert(TfsName(sys.arena, e) == "Unsupported")
            continue
        }
        f.Abandon()
        Assert(false)
    }
    // Nothing was created.
    _ = Stat(sys.io, p) else e {
        Assert(TfsName(sys.arena, e) == "NotFound")
        return
    }
    Assert(false)
}

test "Seek, Size and Truncate move and report as Go's os.File does" (sys: &System) {
    io := sys.io
    p := "mk_tfs2_seek"
    T2Bytes(io, p, 100)
    var f := OpenWith(io, p, ReadWrite).Must()
    Assert(f.Size(io).Must() == 100)
    Assert(f.Seek(io, -10, Whence.End).Must() == 90)
    var b: [16]u8 = [0; 16]
    Assert(f.Read(io, mut b[..]).Must() == 10)
    Assert(b[0] == 90)
    Assert(f.Seek(io, -5, Whence.Current).Must() == 95)
    // Before 0 and past i64 fail, and the position stays.
    _ = f.Seek(io, -1, Whence.Start) else e {
        Assert(TfsName(sys.arena, e) == "Os(code: 0)")
    }
    _ = f.Seek(io, 9_223_372_036_854_775_807, Whence.Current) else e {
        Assert(TfsName(sys.arena, e) == "Os(code: 0)")
    }
    Assert(f.Seek(io, 0, Whence.Current).Must() == 95)
    // Past the end, a write extends the file with zeros.
    Assert(f.Seek(io, 120, Whence.Start).Must() == 120)
    f.WriteAll(io, "z".AsBytes()).Must()
    Assert(f.Size(io).Must() == 121)
    Assert(f.ReadAt(io, 110, mut b[0..1]).Must() == 1 && b[0] == 0)
    // A Writer after Seek writes at the sought position.
    Assert(f.Seek(io, 3, Whence.Start).Must() == 3)
    var wb: [8]u8 = [0; 8]
    var w := f.Writer(io, mut wb[..])
    w.Str("XY").Must()
    w.Flush().Must()
    Assert(f.Seek(io, 0, Whence.Current).Must() == 5)
    // Truncate leaves the position where it was.
    f.Truncate(io, 50).Must()
    Assert(f.Size(io).Must() == 50)
    Assert(f.Seek(io, 0, Whence.Current).Must() == 5)
    f.Truncate(io, 200).Must()
    Assert(f.Size(io).Must() == 200)
    Assert(f.ReadAt(io, 150, mut b[0..1]).Must() == 1 && b[0] == 0)
    var neg := "ok"
    f.Truncate(io, -1) else e {
        neg = TfsName(sys.arena, e)
    }
    Assert(neg == "Os(code: 0)")
    f.Close(io).Must()
    data := ReadBytes(sys.arena, io, p).Must()
    Assert(data[2] == 2 && data[3] == 0x58 && data[4] == 0x59 && data[5] == 5)
    Remove(io, p).Must()
}

test "Lock: Exclusive keeps another handle's TryLock out, Shared lets Shared in" (sys: &System) {
    io := sys.io
    p := "mk_tfs2_lock"
    T2Bytes(io, p, 10)
    a := OpenWith(io, p, ReadWrite).Must()
    b := OpenWith(io, p, ReadWrite).Must()
    a.Lock(io, LockMode.Exclusive).Must()
    Assert(!b.TryLock(io, LockMode.Shared).Must())
    Assert(!b.TryLock(io, LockMode.Exclusive).Must())
    a.Unlock(io).Must()
    Assert(b.TryLock(io, LockMode.Shared).Must())
    Assert(a.TryLock(io, LockMode.Shared).Must())
    // b's Shared lock keeps a's conversion out, on both targets.
    Assert(!a.TryLock(io, LockMode.Exclusive).Must())
    a.Unlock(io).Must()
    b.Unlock(io).Must()
    // Unlock with nothing held succeeds.
    b.Unlock(io).Must()
    a.Close(io).Must()
    // Close releases the lock.
    b.Lock(io, LockMode.Exclusive).Must()
    b.Close(io).Must()
    c := OpenWith(io, p, ReadWrite).Must()
    Assert(c.TryLock(io, LockMode.Exclusive).Must())
    c.Close(io).Must()
    Remove(io, p).Must()
}

test "Lock: a deadline ends a waiting Lock with Canceled and nothing taken, and the call repeats" (sys: &System) {
    io := sys.io
    p := "mk_tfs2_lockc"
    T2Bytes(io, p, 10)
    a := OpenWith(io, p, ReadWrite).Must()
    b := OpenWith(io, p, ReadWrite).Must()
    a.Lock(io, LockMode.Exclusive).Must()
    var got := "none"
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(20))
        b.Lock(io, LockMode.Exclusive) else e {
            got = TfsName(sys.arena, e)
        }
    }
    Assert(got == "Canceled")
    // a still holds its lock: its own TryLock takes it again.
    Assert(a.TryLock(io, LockMode.Exclusive).Must())
    a.Unlock(io).Must()
    b.Lock(io, LockMode.Exclusive).Must()
    Assert(!a.TryLock(io, LockMode.Shared).Must())
    b.Close(io).Must()
    a.Close(io).Must()
    Remove(io, p).Must()
}

test "Stat and StatLink report Info's size, time, kind and permissions" (sys: &System) {
    io := sys.io
    d := "mk_tfs2_stat"
    RemoveAll(io, d).Must()
    MakeDir(io, d).Must()
    T2Bytes(io, "mk_tfs2_stat/f", 33)
    fi := Stat(io, "mk_tfs2_stat/f").Must()
    Assert(fi.kind == Kind.File && fi.size == 33)
    Assert(fi.modified.UnixNanos() > 0)
    Assert((fi.permissions.mode & 0o200) != 0)
    di := StatLink(io, d).Must()
    Assert(di.kind == Kind.Dir)
    pi := StatPath(io, &Path.New(sys.arena, "mk_tfs2_stat/f".AsBytes()).Must()).Must()
    Assert(pi.size == 33 && pi.kind == Kind.File)
    RemoveAll(io, d).Must()
    _ = Stat(io, d) else e {
        Assert(TfsName(sys.arena, e) == "NotFound")
        return
    }
    Assert(false)
}

test "SetModified and SetPermissions are what Stat reads back" (sys: &System) {
    io := sys.io
    p := "mk_tfs2_meta"
    TfsGone(io, p)
    T2Bytes(io, p, 5)
    t := time.FromUnix(1_700_000_000, 123_456_700)
    SetModified(io, p, t).Must()
    // Exact on NTFS (100 ns). On Linux this block runs on the Windows drive
    // through WSL's 9p mount, where the exact compare failed (2026-11-13),
    // so it checks the second; the Linux oracle, in /tmp, checks the
    // nanoseconds.
    got := Stat(io, p).Must().modified
    Assert(got.UnixNanos() == t.UnixNanos() || (Target.os != core.Os.Windows && got.Unix() == t.Unix()))
    SetPermissions(io, p, Permissions(mode: 0o444)).Must()
    Assert((Stat(io, p).Must().permissions.mode & 0o222) == 0)
    // A read-only file refuses a write open.
    f := OpenWith(io, p, ReadWrite) else e {
        Assert(TfsName(sys.arena, e) == "Denied")
        SetPermissions(io, p, Permissions(mode: 0o644)).Must()
        Assert((Stat(io, p).Must().permissions.mode & 0o200) != 0)
        Remove(io, p).Must()
        return
    }
    f.Abandon()
    Assert(false)
}

test "Symlink, ReadLink and StatLink: a link reads back its target and is not followed" (sys: &System) {
    io := sys.io
    l := "mk_tfs2_link"
    TfsGone(io, l)
    TfsGone(io, "mk_tfs2_ltarget")
    T2Bytes(io, "mk_tfs2_ltarget", 7)
    Symlink(io, "mk_tfs2_ltarget", l) else e {
        // Windows without the privilege to make links.
        Assert(TfsName(sys.arena, e) == "Unsupported" && Target.os == core.Os.Windows)
        Remove(io, "mk_tfs2_ltarget").Must()
        return
    }
    Assert(ReadLink(sys.arena, io, l).Must() == "mk_tfs2_ltarget")
    Assert(StatLink(io, l).Must().kind == Kind.Link)
    Assert(Stat(io, l).Must().kind == Kind.File && Stat(io, l).Must().size == 7)
    _ = ReadLink(sys.arena, io, "mk_tfs2_ltarget") else e {
        Assert(TfsName(sys.arena, e) != "ok")
    }
    // RemoveAll of a link removes the link, never its target.
    RemoveAll(io, l).Must()
    Assert(Stat(io, "mk_tfs2_ltarget").Must().size == 7)
    Remove(io, "mk_tfs2_ltarget").Must()
}

test "MakeDirAll makes every parent, succeeds again, and is NotDirectory over a file" (sys: &System) {
    io := sys.io
    RemoveAll(io, "mk_tfs2_mda").Must()
    MakeDirAll(io, "mk_tfs2_mda/a/b//c/").Must()
    Assert(Stat(io, "mk_tfs2_mda/a/b/c").Must().kind == Kind.Dir)
    MakeDirAll(io, "mk_tfs2_mda/a/b/c").Must()
    MakeDirAll(io, "mk_tfs2_mda/a").Must()
    T2Bytes(io, "mk_tfs2_mda/file", 1)
    var g1 := "ok"
    MakeDirAll(io, "mk_tfs2_mda/file") else e {
        g1 = TfsName(sys.arena, e)
    }
    Assert(g1 == "NotDirectory")
    var g2 := "ok"
    MakeDirAll(io, "mk_tfs2_mda/file/x/y") else e {
        g2 = TfsName(sys.arena, e)
    }
    Assert(g2 == "NotDirectory")
    RemoveAll(io, "mk_tfs2_mda").Must()
    // RemoveAll of what is not there succeeds; a last element `.` or `..` is refused.
    RemoveAll(io, "mk_tfs2_mda").Must()
    var g3 := "ok"
    RemoveAll(io, "mk_tfs2_mda/..") else e {
        g3 = TfsName(sys.arena, e)
    }
    Assert(g3 == "InvalidPath")
    var g4 := "ok"
    RemoveAll(io, ".") else e {
        g4 = TfsName(sys.arena, e)
    }
    Assert(g4 == "InvalidPath")
}

// T2Tree makes a tree under root: for each of n directories d0..d<n-1>,
// files f0..f<k-1> of i bytes and one nested directory deep with a file.
T2Tree(a: &Arena, io: Io, root: str, n: int, k: int) {
    MakeDirAll(io, root).Must()
    for i in 0..n {
        d := a.Fmt("{root}/d{i}", (root, i)).Must()
        MakeDirAll(io, a.Fmt("{d}/deep", (d)).Must()).Must()
        T2Bytes(io, a.Fmt("{d}/deep/x", (d)).Must(), 3)
        for j in 0..k {
            T2Bytes(io, a.Fmt("{d}/f{j}", (d, j)).Must(), j)
        }
    }
}

test "Walk reports the root, then every entry once, a parent before its children, SkipDir skipping" (sys: &System) {
    io := sys.io
    root := "mk_tfs2_walk"
    RemoveAll(io, root).Must()
    T2Tree(sys.arena, io, root, 4, 3)
    var w := Walk(sys.heap, io, root).Must()
    var n := 0
    var dirs := 0
    var max_depth := 0
    for {
        e := w.Next(sys.arena, io).Must() else {
            break
        }
        if n == 0 {
            Assert(e.depth == 0 && e.kind == Kind.Dir && e.path.Str().Must() == root)
        }
        n += 1
        if e.kind == Kind.Dir {
            dirs += 1
        }
        if e.depth > max_depth {
            max_depth = e.depth
        }
        // Every listed path opens or stats by its bytes.
        info := StatPath(io, &e.path).Must()
        Assert(info.kind == e.kind)
    }
    w.Close()
    // root, 4 d's, 4 deep's, 4 x's, 12 f's.
    Assert(n == 25 && dirs == 9 && max_depth == 3)
    // A root that ends with a separator lists the same entries.
    Assert(T2Count(sys.heap, sys.arena, io, "mk_tfs2_walk/").Must() == 25)
    // SkipDir after each d: only the root and its 4 d's.
    var s := Walk(sys.heap, io, root).Must()
    var m := 0
    for {
        e := s.Next(sys.arena, io).Must() else {
            break
        }
        m += 1
        if e.depth == 1 {
            s.SkipDir()
        }
    }
    s.Close()
    Assert(m == 5)
    // A walk of a file is that file alone.
    var f := Walk(sys.heap, io, "mk_tfs2_walk/d0/f2").Must()
    one := f.Next(sys.arena, io).Must().Must()
    Assert(one.kind == Kind.File && one.depth == 0)
    Assert(f.Next(sys.arena, io).Must().IsNone())
    f.Close()
    RemoveAll(io, root).Must()
    var gone := Walk(sys.heap, io, root) else err {
        Assert(TfsName(sys.arena, err) == "NotFound")
        return
    }
    gone.Close()
    Assert(false)
}

// T2Rest counts the entries w has left, each stat-ed by its bytes, in
// the loop form for a linear iterator.
T2Rest(w: mut Walker, a: &Arena, io: Io): Error|OutOfMemory!int {
    var n := 0
    for e := try w.Next(a, io) {
        _ = try StatPath(io, &e.path)
        n += 1
    }
    return n
}

// T2Count is the number of entries Walk lists under root, root included.
T2Count(h: &Heap, a: &Arena, io: Io, root: str): Error|OutOfMemory!int {
    var w := try Walk(h, io, root)
    defer w.Close()
    return try T2Rest(mut w, a, io)
}

// T2Levels is the depth of the deep tree: 300 levels, a path of about 610
// bytes, past Windows' MAX_PATH (std.sys gives such a path the \\?\ form).
const T2Levels = 300

test "Walk and RemoveAll take a deep tree, every entry reopened through OpenPath" (sys: &System) {
    io := sys.io
    root := "mk_tfs2_deep"
    RemoveAll(io, root).Must()
    levels := T2Levels
    var t := Text.New(sys.arena)
    t.Push(root).Must()
    for i in 0..levels {
        _ = i
        t.Push("/d").Must()
    }
    deep := t.Str()
    MakeDirAll(io, deep).Must()
    T2Bytes(io, sys.arena.Fmt("{deep}/leaf", (deep)).Must(), 9)
    var w := Walk(sys.heap, io, root).Must()
    var n := 0
    var files := 0
    for {
        e := w.Next(sys.arena, io).Must() else {
            break
        }
        n += 1
        if e.kind == Kind.File {
            files += 1
            f := OpenPath(io, &e.path, ReadOnly).Must()
            Assert(f.Size(io).Must() == 9 && e.depth == levels + 1)
            f.Close(io).Must()
        }
    }
    w.Close()
    Assert(n == levels + 2 && files == 1)
    RemoveAll(io, root).Must()
    _ = Stat(io, root) else e {
        Assert(TfsName(sys.arena, e) == "NotFound")
        return
    }
    Assert(false)
}

test "RemoveAll removes a tree of files and directories, a read-only file among them" (sys: &System) {
    io := sys.io
    root := "mk_tfs2_rma"
    RemoveAll(io, root).Must()
    T2Tree(sys.arena, io, root, 30, 5)
    SetPermissions(io, "mk_tfs2_rma/d3/f1", Permissions(mode: 0o444)).Must()
    RemoveAll(io, root).Must()
    _ = Stat(io, root) else e {
        Assert(TfsName(sys.arena, e) == "NotFound")
        return
    }
    Assert(false)
}

test "Copy copies every byte and returns the count; a missing source is NotFound" (sys: &System) {
    io := sys.io
    T2Bytes(io, "mk_tfs2_cpa", 3000)
    TfsGone(io, "mk_tfs2_cpb")
    Assert(Copy(io, "mk_tfs2_cpa", "mk_tfs2_cpb").Must() == 3000)
    a := ReadBytes(sys.arena, io, "mk_tfs2_cpa").Must()
    b := ReadBytes(sys.arena, io, "mk_tfs2_cpb").Must()
    Assert(a.Len() == b.Len())
    for i in 0..a.Len() {
        Assert(a[i] == b[i])
    }
    // An existing destination is replaced.
    T2Bytes(io, "mk_tfs2_cpa", 10)
    Assert(Copy(io, "mk_tfs2_cpa", "mk_tfs2_cpb").Must() == 10)
    Assert(Stat(io, "mk_tfs2_cpb").Must().size == 10)
    // A file copied onto itself is Denied and keeps its bytes.
    var self := "none"
    _ = Copy(io, "mk_tfs2_cpa", "mk_tfs2_cpa") else e {
        self = TfsName(sys.arena, e)
    }
    Assert(self == "Denied" && Stat(io, "mk_tfs2_cpa").Must().size == 10)
    Remove(io, "mk_tfs2_cpa").Must()
    Remove(io, "mk_tfs2_cpb").Must()
    _ = Copy(io, "mk_tfs2_cpa", "mk_tfs2_cpb") else e {
        Assert(TfsName(sys.arena, e) == "NotFound")
        return
    }
    Assert(false)
}

test "CreateTemp makes distinct new files with the prefix, TempDir names a directory" (sys: &System) {
    io := sys.io
    d := "mk_tfs2_tmp"
    RemoveAll(io, d).Must()
    MakeDir(io, d).Must()
    (file: f1, path: p1) := CreateTemp(sys.arena, io, d, "pre-").Must()
    (file: f2, path: p2) := CreateTemp(sys.arena, io, d, "pre-").Must()
    Assert(p1 != p2 && p1.Len() == d.Len() + 1 + 4 + 16)
    Assert(Path.New(sys.arena, p1.AsBytes()).Must().Base().Str().Must().StartsWith("pre-"))
    var g := f1
    g.WriteAll(io, "x".AsBytes()).Must()
    g.Close(io).Must()
    f2.Close(io).Must()
    Assert(Stat(io, p1).Must().size == 1)
    RemoveAll(io, d).Must()
    t := TempDir(sys.arena, io).Must()
    Assert(t.Len() > 0)
    Assert(Stat(io, t).Must().kind == Kind.Dir)
    (file: bad, path: _) := CreateTemp(sys.arena, io, d, "a/b") else e {
        Assert(TfsName(sys.arena, e) == "InvalidPath")
        return
    }
    bad.Abandon()
    Assert(false)
}

test "Cwd and Absolute: a relative path joins the working directory, cleaned" (sys: &System) {
    io := sys.io
    wd := Cwd(sys.arena, io).Must()
    Assert(wd.Len() > 0)
    Assert(Absolute(sys.arena, io, "").Must() == wd)
    Assert(Absolute(sys.arena, io, ".").Must() == wd)
    sep := if Target.os == core.Os.Windows { "\\" } else { "/" }
    Assert(Absolute(sys.arena, io, "a/./b/../c//").Must() == sys.arena.Fmt("{wd}{sep}a{sep}c", (wd, sep)).Must())
    if Target.os != core.Os.Windows {
        Assert(Absolute(sys.arena, io, "/x/../../y/.").Must() == "/y")
        Assert(Absolute(sys.arena, io, "//").Must() == "/")
    }
}

test "Walker.Next in a cancelled scope is Canceled, and the next call resumes there" (sys: &System) {
    io := sys.io
    root := "mk_tfs2_wcan"
    RemoveAll(io, root).Must()
    T2Tree(sys.arena, io, root, 2, 2)
    var w := Walk(sys.heap, io, root).Must()
    _ = w.Next(sys.arena, io).Must()
    var got := "none"
    scope s := io.Scope() {
        s.Cancel()
        _ = w.Next(sys.arena, io) else e {
            got = TfsName(sys.arena, e)
        }
    }
    Assert(got == "Canceled")
    n := 1 + T2Rest(mut w, sys.arena, io).Must()
    w.Close()
    // root, and for each of d0 and d1: itself, deep, deep/x, f0, f1.
    Assert(n == 11)
    RemoveAll(io, root).Must()
}

test "RemoveAll, MakeDirAll and Copy cut by a deadline keep what they did, and a second call finishes" (sys: &System) {
    io := sys.io
    a := sys.arena
    root := "mk_tfs2_pfx"
    RemoveAll(io, root).Must()
    // RemoveAll: deadlines from 0 (cancelled at once) to 1 ms land before,
    // inside or after the tree's removal.
    var cut := 0
    for i in 0..6 {
        T2Tree(a, io, root, 20, 4)
        var got := "none"
        scope s := io.Scope() {
            s.CancelAfter(time.Micros(i64(i) * 200))
            RemoveAll(io, root) else e {
                got = TfsName(a, e)
            }
        }
        Assert(got == "none" || got == "Canceled")
        if got == "Canceled" {
            cut += 1
            // What is left is part of the tree: every entry still lists.
            _ = T2Count(sys.heap, a, io, root) else e {
                Assert(TfsName(a, e) == "NotFound")
            }
        }
        RemoveAll(io, root).Must()
        _ = Stat(io, root) else e {
            Assert(TfsName(a, e) == "NotFound")
            continue
        }
        Assert(false)
    }
    Assert(cut >= 1)
    // MakeDirAll: the levels made before the cut stay, the second call
    // makes the rest.
    var t := Text.New(a)
    t.Push(root).Must()
    for j in 0..30 {
        _ = j
        t.Push("/m").Must()
    }
    deep := t.Str()
    cut = 0
    for i in 0..6 {
        var got := "none"
        scope s := io.Scope() {
            s.CancelAfter(time.Micros(i64(i) * 100))
            MakeDirAll(io, deep) else e {
                got = TfsName(a, e)
            }
        }
        Assert(got == "none" || got == "Canceled")
        if got == "Canceled" {
            cut += 1
        }
        MakeDirAll(io, deep).Must()
        Assert(Stat(io, deep).Must().kind == Kind.Dir)
        Assert(T2Count(sys.heap, a, io, root).Must() == 31)
        RemoveAll(io, root).Must()
    }
    Assert(cut >= 1)
    // Copy: an interrupted copy may leave part of dst; the second copies
    // every byte.
    T2Bytes(io, "mk_tfs2_pfxa", 4000)
    TfsGone(io, "mk_tfs2_pfxb")
    var got := "none"
    scope s := io.Scope() {
        s.Cancel()
        _ = Copy(io, "mk_tfs2_pfxa", "mk_tfs2_pfxb") else e {
            got = TfsName(a, e)
        }
    }
    Assert(got == "Canceled")
    Assert(Copy(io, "mk_tfs2_pfxa", "mk_tfs2_pfxb").Must() == 4000)
    x := ReadBytes(a, io, "mk_tfs2_pfxa").Must()
    y := ReadBytes(a, io, "mk_tfs2_pfxb").Must()
    Assert(x.Len() == y.Len())
    for k in 0..x.Len() {
        Assert(x[k] == y[k])
    }
    Remove(io, "mk_tfs2_pfxa").Must()
    Remove(io, "mk_tfs2_pfxb").Must()
}
