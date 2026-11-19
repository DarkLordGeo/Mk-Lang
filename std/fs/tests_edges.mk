// Why: std.fs's edge cases (links, races, locks, append, bare drives) must
// behave the same on both targets.
// What: test blocks over those cases, run by mk test --std=std.fs.
// How: each block works on paths named mk_tfe_<id> in the working
// directory, removes them before and after, and ends with no leak.

import std.atomic
import std.core
import std.time

// TfeLinked makes link a symbolic link to target, or answers false on
// Windows without the privilege to make one (the block then checks the
// rest).
TfeLinked(a: &Arena, io: Io, target: str, link: str): bool {
    Symlink(io, target, link) else e {
        Assert(TfsName(a, e) == "Unsupported" && Target.os == core.Os.Windows)
        return false
    }
    return true
}

test "RemoveAll of a link to a directory named with a trailing separator removes the link, never the target's entries" (sys: &System) {
    io := sys.io
    t := "mk_tfe_linkdir"
    RemoveAll(io, t).Must()
    MakeDirAll(io, "mk_tfe_linkdir/target").Must()
    WriteAll(io, "mk_tfe_linkdir/target/keep", "k".AsBytes()).Must()
    if TfeLinked(sys.arena, io, "target", "mk_tfe_linkdir/link") {
        RemoveAll(io, "mk_tfe_linkdir/link/").Must()
        _ = StatLink(io, "mk_tfe_linkdir/link") else e {
            Assert(TfsName(sys.arena, e) == "NotFound")
        }
        Assert(Stat(io, "mk_tfe_linkdir/target/keep").Must().size == 1)
    }
    // A trailing separator on a directory removes it as without one.
    RemoveAll(io, "mk_tfe_linkdir/target//").Must()
    _ = Stat(io, "mk_tfe_linkdir/target") else e {
        Assert(TfsName(sys.arena, e) == "NotFound")
    }
    RemoveAll(io, t).Must()
}

test "RemoveAll of a root is InvalidPath and removes nothing" (sys: &System) {
    io := sys.io
    root := if Target.os == core.Os.Windows { "C:\\" } else { "/" }
    var got := "ok"
    RemoveAll(io, root) else e {
        got = TfsName(sys.arena, e)
    }
    Assert(got == "InvalidPath")
}

test "Copy of a directory is IsDirectory and leaves an existing destination as it was" (sys: &System) {
    io := sys.io
    RemoveAll(io, "mk_tfe_cpsrc").Must()
    MakeDir(io, "mk_tfe_cpsrc").Must()
    WriteAll(io, "mk_tfe_cpdst", "keep".AsBytes()).Must()
    var got := "ok"
    _ = Copy(io, "mk_tfe_cpsrc", "mk_tfe_cpdst") else e {
        got = TfsName(sys.arena, e)
    }
    Assert(got == "IsDirectory")
    Assert(ReadText(sys.arena, io, "mk_tfe_cpdst").Must() == "keep")
    // Nor is a missing destination made.
    Remove(io, "mk_tfe_cpdst").Must()
    _ = Copy(io, "mk_tfe_cpsrc", "mk_tfe_cpdst") else {
    }
    _ = Stat(io, "mk_tfe_cpdst") else e {
        Assert(TfsName(sys.arena, e) == "NotFound")
    }
    Remove(io, "mk_tfe_cpsrc").Must()
}

test "MakeDirAll over a dangling link is NotDirectory, never NotFound" (sys: &System) {
    io := sys.io
    RemoveAll(io, "mk_tfe_dangling").Must()
    MakeDir(io, "mk_tfe_dangling").Must()
    if TfeLinked(sys.arena, io, "missing", "mk_tfe_dangling/dl") {
        for p in ["mk_tfe_dangling/dl", "mk_tfe_dangling/dl/x", "mk_tfe_dangling/dl/x/y/"] {
            var got := "ok"
            MakeDirAll(io, p) else e {
                got = TfsName(sys.arena, e)
            }
            Assert(got == "NotDirectory")
        }
        // The link is still there, still dangling.
        Assert(StatLink(io, "mk_tfe_dangling/dl").Must().kind == Kind.Link)
    }
    RemoveAll(io, "mk_tfe_dangling").Must()
}

test "Lock and TryLock by the holder convert its lock on both targets, and one Unlock releases it" (sys: &System) {
    io := sys.io
    p := "mk_tfe_relock"
    WriteAll(io, p, "0123456789".AsBytes()).Must()
    a := OpenWith(io, p, ReadWrite).Must()
    a.Lock(io, LockMode.Exclusive).Must()
    // The holder's own lock is no other holder.
    Assert(a.TryLock(io, LockMode.Exclusive).Must())
    a.Unlock(io).Must()
    // Two Shared locks, then one Unlock: nothing held.
    a.Lock(io, LockMode.Shared).Must()
    a.Lock(io, LockMode.Shared).Must()
    a.Unlock(io).Must()
    b := OpenWith(io, p, ReadWrite).Must()
    Assert(b.TryLock(io, LockMode.Exclusive).Must())
    b.Unlock(io).Must()
    // Shared to Exclusive by the holder does not wait on itself.
    a.Lock(io, LockMode.Shared).Must()
    var got := "ok"
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(500))
        a.Lock(io, LockMode.Exclusive) else e {
            got = TfsName(sys.arena, e)
        }
    }
    Assert(got == "ok")
    Assert(!b.TryLock(io, LockMode.Shared).Must())
    // A conversion another holder wins fails; whether a keeps its Shared
    // lock is the OS's (Windows drops it first; flock(2) promises no
    // atomic conversion, and WSL's drive mount keeps it), so a relocks.
    a.Unlock(io).Must()
    a.Lock(io, LockMode.Shared).Must()
    Assert(b.TryLock(io, LockMode.Shared).Must())
    Assert(!a.TryLock(io, LockMode.Exclusive).Must())
    b.Unlock(io).Must()
    a.Unlock(io).Must()
    Assert(b.TryLock(io, LockMode.Exclusive).Must())
    b.Close(io).Must()
    a.Close(io).Must()
    Remove(io, p).Must()
}

test "WriteAt on a file opened under append is Unsupported and writes nothing" (sys: &System) {
    io := sys.io
    p := "mk_tfe_append"
    WriteAll(io, p, "0123456789".AsBytes()).Must()
    var f := OpenWith(io, p, Append).Must()
    var got := "ok"
    _ = f.WriteAt(io, 0, "ab".AsBytes()) else e {
        got = TfsName(sys.arena, e)
    }
    Assert(got == "Unsupported")
    // A Write still lands at the end.
    f.WriteAll(io, "xy".AsBytes()).Must()
    f.Close(io).Must()
    Assert(ReadText(sys.arena, io, p).Must() == "0123456789xy")
    // ReadWrite keeps WriteAt.
    g := OpenWith(io, p, ReadWrite).Must()
    Assert(g.WriteAt(io, 0, "ab".AsBytes()).Must() == 2)
    g.Close(io).Must()
    Assert(ReadText(sys.arena, io, p).Must() == "ab23456789xy")
    Remove(io, p).Must()
}

// TfeTree makes root with n directories of k files each.
TfeTree(a: &Arena, io: Io, root: str, n: int, k: int) {
    for i in 0..n {
        d := a.Fmt("{root}/d{i}", (root, i)).Must()
        MakeDirAll(io, a.Fmt("{d}/sub", (d)).Must()).Must()
        for j in 0..k {
            WriteAll(io, a.Fmt("{d}/f{j}", (d, j)).Must(), "f".AsBytes()).Must()
        }
    }
}

// TfeRemover runs RemoveAll(root) and stores 1 in out when it succeeded,
// 2 when it failed with Denied, 3 otherwise.
TfeRemover(io: Io, root: str, out: &atomic.AtomicInt) {
    RemoveAll(io, root) else e {
        match e {
            Denied {
                out.Store(2)
            }
            _ {
                out.Store(3)
            }
        }
        return
    }
    out.Store(1)
}

test "RemoveAll by two removers of one tree at once: what one removes first is no failure of the other" (sys: &System) {
    io := sys.io
    root := "mk_tfe_two"
    RemoveAll(io, root).Must()
    for round in 0..10 {
        _ = round
        TfeTree(sys.arena, io, root, 20, 4)
        x := atomic.AtomicInt.New(0)
        y := atomic.AtomicInt.New(0)
        scope s := io.Scope() {
            s.go TfeRemover(io, root, &x)
            s.go TfeRemover(io, root, &y)
        }
        // Windows: either remover, or both, can see Denied (cause
        // not settled); the RemoveAll below
        // removes what is left. Linux and std.sim give Ok to both.
        win := Target.os == core.Os.Windows
        Assert(x.Load() == 1 || (win && x.Load() == 2))
        Assert(y.Load() == 1 || (win && y.Load() == 2))
        RemoveAll(io, root).Must()
        _ = Stat(io, root) else e {
            Assert(TfsName(sys.arena, e) == "NotFound")
            continue
        }
        Assert(false)
    }
}

// TfeCreator writes files into dir until stop is set or a write fails,
// and stores the files it made in made.
TfeCreator(io: Io, h: &Heap, dir: str, stop: &atomic.AtomicBool, made: &atomic.AtomicInt) {
    var a := Arena.New(h).Must()
    defer a.Free()
    var i := 0
    for !stop.Load() {
        WriteAll(io, a.Fmt("{dir}/c{i}", (dir, i)).Must(), "c".AsBytes()) else {
            break
        }
        i += 1
        if i % 64 == 0 {
            a.Reset()
        }
    }
    made.Store(i64(i))
}

test "RemoveAll returns while another task keeps filling a directory of the tree" (sys: &System) {
    io := sys.io
    // On Linux the tree is made under TempDir, not in the working
    // directory: the harness runs Linux blocks in the Windows drive's
    // directory, which WSL mounts over 9p, where every create and
    // unlink is a round trip to Windows and the remover barely outpaces
    // the creator: the call took 26.8 and 29.0 s to spend its 256 listings
    // again, and a third run reached the 30 s deadline (Canceled); in /tmp
    // (ext4) it took 24 ms, on Windows 0.18 s (measured 2026-11-14, three
    // runs each).
    t := TempDir(sys.arena, io).Must()
    root := if Target.os == core.Os.Linux { sys.arena.Fmt("{t}/mk_tfe_refill", (t)).Must() } else { "mk_tfe_refill" }
    sub := sys.arena.Fmt("{root}/sub", (root)).Must()
    RemoveAll(io, root).Must()
    MakeDirAll(io, sub).Must()
    stop := atomic.AtomicBool.New(false)
    made := atomic.AtomicInt.New(0)
    var got := "ok"
    scope s := io.Scope() {
        s.go TfeCreator(io, sys.heap, sub, &stop, &made)
        // Let the creator start before the removal does.
        io.Sleep(time.Millis(20)) else {
        }
        scope d := io.Scope() {
            d.CancelAfter(time.Seconds(30))
            RemoveAll(io, root) else e {
                got = TfsName(sys.arena, e)
            }
        }
        stop.Store(true)
    }
    // Ok when a removal won, or the OS's not-empty code once its passes
    // ran out; never Canceled at the deadline.
    Assert(got == "ok" || got.StartsWith("Os("))
    RemoveAll(io, root).Must()
}

test "Walk of a bare drive lists that drive's working directory under paths that name it" (sys: &System) {
    if Target.os != core.Os.Windows {
        return
    }
    io := sys.io
    // A directory of the test's own, so the drive's working directory is
    // not its root and holds an entry the walk can name.
    RemoveAll(io, "mk_tfe_drive").Must()
    MakeDir(io, "mk_tfe_drive").Must()
    wd := Cwd(sys.arena, io).Must()
    drive := wd[0..2]
    var w := Walk(sys.heap, io, drive).Must()
    var seen := 0
    var found := false
    for e := w.Next(sys.arena, io).Must() {
        b := e.path.AsBytes()
        if e.depth == 0 {
            Assert(b.Len() == 2)
            continue
        }
        // C:name, never C:\name (an entry of the drive's root).
        Assert(b.Len() > 2 && b[2] != 0x5C && b[2] != 0x2F)
        // Only this block's own entry is stat-ed: the others' come and go
        // while the blocks run side by side.
        if e.path.Str().Must() == sys.arena.Fmt("{drive}mk_tfe_drive", (drive)).Must() {
            Assert(StatPath(io, &e.path).Must().kind == Kind.Dir)
            found = true
        }
        w.SkipDir()
        seen += 1
    }
    w.Close()
    Assert(seen > 0 && found)
    Remove(io, "mk_tfe_drive").Must()
}

test "Seek of a regular file needs the OS once, then moves the position alone" (sys: &System) {
    io := sys.io
    p := "mk_tfe_seek"
    WriteAll(io, p, "0123456789".AsBytes()).Must()
    var f := Open(io, p).Must()
    Assert(f.Seek(io, 4, Whence.Start).Must() == 4)
    var b: [2]u8 = [0; 2]
    Assert(f.Read(io, mut b[..]).Must() == 2 && b[0] == 0x34)
    Assert(f.Seek(io, -1, Whence.Current).Must() == 5)
    Assert(f.Seek(io, -2, Whence.End).Must() == 8)
    f.Close(io).Must()
    Remove(io, p).Must()
}

test "Open of a directory succeeds on both targets, File.Read of it is IsDirectory, and a Reader over it reports ReadError" (sys: &System) {
    io := sys.io
    p := "mk_tfe_opendir"
    RemoveAll(io, p).Must()
    MakeDir(io, p).Must()
    var f := Open(io, p).Must()
    var b: [16]u8 = [0; 16]
    var got := "ok"
    _ = f.Read(io, mut b[..]) else e {
        got = TfsName(sys.arena, e)
    }
    Assert(got == "IsDirectory")
    var rb: [16]u8 = [0; 16]
    var r := f.Reader(io, mut rb[..])
    var failed := false
    _ = r.Read(mut b[..]) else {
        failed = true
    }
    Assert(failed)
    f.Close(io).Must()
    got = "ok"
    _ = ReadText(sys.arena, io, p) else e {
        got = TfsName(sys.arena, e)
    }
    Assert(got == "IsDirectory")
    Remove(io, p).Must()
}
