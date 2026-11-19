// Why: Dir, MakeDir and Stat must agree on names, kinds and sizes.
// What: test blocks for them, run by `mk test --std=std.fs`
// (std/std-missing_blocks_test.go), with the helper TdirList.
// How: each block works under mk_tdir_<id> in the working directory and
// removes it after; the listing against Go's os.ReadDir is
// std/std-missing_dir_test.go.

// TdirList lists p and returns the entries, the sum of their sizes and
// the number that are directories, as count * 1e12 + dirs * 1e9 + sizes.
TdirList(a: &Arena, io: Io, p: str): i64 {
    var d := Dir.Open(io, p) else {
        return -1
    }
    var n: i64 = 0
    var dirs: i64 = 0
    var size: i64 = 0
    for {
        e := d.Next(a, io) else {
            d.Close()
            return -2
        }
        x := e else {
            break
        }
        n += 1
        if x.is_dir {
            dirs += 1
        }
        size += x.size
    }
    d.Close()
    return n * 1_000_000_000_000 + dirs * 1_000_000_000 + size
}

test "Dir: MakeDir, Stat and a listing of files and a directory" (sys: &System) {
    io := sys.io
    root := "mk_tdir_1"
    TfsGone(io, "mk_tdir_1/a")
    TfsGone(io, "mk_tdir_1/b")
    TfsGone(io, "mk_tdir_1/sub")
    TfsGone(io, root)
    MakeDir(io, root).Must()
    MakeDir(io, root) else e {
        Assert(TfsName(sys.arena, e) == "Exists")
    }
    // an empty directory lists nothing
    Assert(TdirList(sys.arena, io, root) == 0)
    WriteAll(io, "mk_tdir_1/a", "hello".AsBytes()).Must()
    WriteAll(io, "mk_tdir_1/b", "hi".AsBytes()).Must()
    MakeDir(io, "mk_tdir_1/sub").Must()
    Assert(TdirList(sys.arena, io, root) == 3 * 1_000_000_000_000 + 1_000_000_000 + 7)
    i := Stat(io, "mk_tdir_1/a").Must()
    Assert(i.kind == Kind.File && i.size == 5 && i.modified.UnixNanos() > 1_600_000_000_000_000_000)
    j := Stat(io, root).Must()
    Assert(j.kind == Kind.Dir)
    TfsGone(io, "mk_tdir_1/a")
    TfsGone(io, "mk_tdir_1/b")
    TfsGone(io, "mk_tdir_1/sub")
    TfsGone(io, root)
}

test "Dir: names come back as Paths" (sys: &System) {
    io := sys.io
    TfsGone(io, "mk_tdir_2/é-x")
    TfsGone(io, "mk_tdir_2")
    MakeDir(io, "mk_tdir_2").Must()
    WriteAll(io, "mk_tdir_2/é-x", "z".AsBytes()).Must()
    var d := Dir.Open(io, "mk_tdir_2").Must()
    e := d.Next(sys.arena, io) else {
        d.Close()
        Assert(false)
        return
    }
    x := e else {
        d.Close()
        Assert(false)
        return
    }
    s := x.name.Str() else {
        d.Close()
        Assert(false)
        return
    }
    Assert(s == "é-x")
    Assert(!x.is_dir && x.size == 1)
    last := d.Next(sys.arena, io) else {
        d.Close()
        Assert(false)
        return
    }
    Assert(last.IsNone())
    d.Close()
    TfsGone(io, "mk_tdir_2/é-x")
    TfsGone(io, "mk_tdir_2")
}

test "Dir: Open and Stat of a missing path are NotFound, Open of a file is NotDirectory" (sys: &System) {
    io := sys.io
    TfsGone(io, "mk_tdir_3")
    d := Dir.Open(io, "mk_tdir_3") else e {
        Assert(TfsName(sys.arena, e) == "NotFound")
        _ = Stat(io, "mk_tdir_3") else f {
            Assert(TfsName(sys.arena, f) == "NotFound")
            WriteAll(io, "mk_tdir_3", "f".AsBytes()).Must()
            g := Dir.Open(io, "mk_tdir_3") else h {
                Assert(TfsName(sys.arena, h) == "NotDirectory")
                MakeDir(io, "mk_tdir_3/x") else k {
                    Assert(TfsName(sys.arena, k) != "Exists")
                }
                TfsGone(io, "mk_tdir_3")
                return
            }
            g.Close()
            Assert(false)
            return
        }
        Assert(false)
        return
    }
    d.Close()
    Assert(false)
}
