// Why: long paths, links, failed copies and racing removals are where the
// targets differ most.
// What: test blocks for std.fs's long paths, links, copies and removals,
// run by mk test --std=std.fs.
// How: each block works on paths named mk_tm9_<id> in the working directory,
// removes them before and after, and ends with no leak.

import std.atomic
import std.core
import std.raw

// Tm9Rep is the first n bytes of "abcde/" repeated, in a, with sep for
// each '/'.
Tm9Rep(a: &Arena, n: int, sep: u8): str from a {
    unit := "abcde/".AsBytes()
    b := a.Slice[u8](n).Must()
    for i in 0..n {
        b[i] = if i % 6 == 5 { sep } else { unit[i % 6] }
    }
    return str.From(b[..]).Must()
}

// Tm9Sep is the target's separator.
Tm9Sep(): u8 {
    return if Target.os == core.Os.Windows { 92 } else { 47 }
}

test "Absolute of a relative path of 1,023, 1,024 and 1,200 units is the working directory joined with it" (sys: &System) {
    io := sys.io
    wd := Cwd(sys.arena, io).Must()
    sep := if Target.os == core.Os.Windows { "\\" } else { "/" }
    for n in [1023, 1024, 1200] {
        got := Absolute(sys.arena, io, Tm9Rep(sys.arena, n, 47)).Must()
        // Cleaned: no trailing separator, and each '/' the target's.
        m := if n % 6 == 0 { n - 1 } else { n }
        want := sys.arena.Fmt("{wd}{sep}{r}", (wd: wd, sep: sep, r: Tm9Rep(sys.arena, m, Tm9Sep()))).Must()
        Assert(got == want)
    }
}

test "Symlink takes a link past 1,024 units and a relative directory target past 1,024 units, and makes directory links" (sys: &System) {
    io := sys.io
    root := "mk_tm9_link"
    RemoveAll(io, root).Must()
    // d is root/abcde/.../abcde, 1,211 units. macOS takes no path or link
    // target of 1,024 bytes or more (PATH_MAX, the kernel's limit on every
    // path argument), so there rel is 1,000 bytes and d 1,012.
    reln := if Target.os == core.Os.Macos { 1000 } else { 1199 }
    rel := Tm9Rep(sys.arena, reln, 47)
    d := sys.arena.Fmt("{root}/{rel}", (root: root, rel: rel)).Must()
    MakeDirAll(io, d).Must()
    WriteAll(io, sys.arena.Fmt("{d}/mark", (d: d)).Must(), "m".AsBytes()).Must()
    // A long link to a short relative target: d's parent.
    l1 := sys.arena.Fmt("{d}/l1", (d: d)).Must()
    Symlink(io, "..", l1) else e {
        // Windows without the privilege to make links.
        Assert(TfsName(sys.arena, e) == "Unsupported" && Target.os == core.Os.Windows)
        RemoveAll(io, root).Must()
        return
    }
    Assert(ReadLink(sys.arena, io, l1).Must() == "..")
    Assert(Stat(io, l1).Must().kind == Kind.Dir)
    // A short link to a long relative target, d from root: stored as
    // written ('/' as '\' on Windows), and a directory link, so a name
    // under it opens.
    l2 := "mk_tm9_link/l2"
    Symlink(io, rel, l2).Must()
    Assert(ReadLink(sys.arena, io, l2).Must() == Tm9Rep(sys.arena, reln, Tm9Sep()))
    Assert(StatLink(io, l2).Must().kind == Kind.Link)
    Assert(Stat(io, l2).Must().kind == Kind.Dir)
    Assert(ReadText(sys.arena, io, "mk_tm9_link/l2/mark").Must() == "m")
    // RemoveAll removes the links, never what they name, then the tree.
    RemoveAll(io, l2).Must()
    Assert(Stat(io, sys.arena.Fmt("{d}/mark", (d: d)).Must()).Must().size == 1)
    RemoveAll(io, root).Must()
    _ = Stat(io, root) else e {
        Assert(TfsName(sys.arena, e) == "NotFound")
    }
}

// Tm9LinkAgrees: Symlink(t, p) answers ok exactly when a link at p exists
// after it; it is true when the call failed. p is removed after.
Tm9LinkAgrees(a: &Arena, io: Io, t: str, p: str): bool {
    Remove(io, p) else {}
    var made := true
    Symlink(io, t, p) else {
        made = false
    }
    var there := true
    _ = StatLink(io, p) else e {
        Assert(TfsName(a, e) == "NotFound")
        there = false
    }
    Remove(io, p) else {}
    Assert(made == there)
    return !made
}

// A link stores its target twice in a reparse buffer of 16 KiB on
// Windows (a substitute and a print name), so a target of 4,092 units or
// more, or a full path of 4,088 or more, fails there (ERROR_INVALID_REPARSE_DATA,
// 4392); CreateSymbolicLinkW's BOOLEAN is its low byte, and the call
// once answered ok for those. On Linux a target is at
// most PATH_MAX - 1 bytes. The lengths either side of Windows' limit are
// judged only for agreement: the answer matches whether a link exists.
test "Symlink answers ok only when the link exists, and fails for a target the reparse buffer cannot hold" (sys: &System) {
    io := sys.io
    wd := Cwd(sys.arena, io).Must()
    sep := if Target.os == core.Os.Windows { "\\" } else { "/" }
    for n in [4091, 4092, 5000] {
        failed := Tm9LinkAgrees(sys.arena, io, Tm9Rep(sys.arena, n, 47), "mk_tm9_lgrel")
        Assert(failed || n != 5000)
    }
    for n in [4087, 4088, 4500] {
        t := sys.arena.Fmt("{wd}{sep}{r}", (wd: wd, sep: sep, r: Tm9Rep(sys.arena, n - wd.Len() - 1, 47))).Must()
        failed := Tm9LinkAgrees(sys.arena, io, t, "mk_tm9_lgabs")
        Assert(failed || n != 4500)
    }
}

// A drive-relative target (`V:abc`: a drive, a colon, no separator) is
// resolved by CreateSymbolicLinkW against the drive's working directory,
// which failed with Os(code: 87) once the result reached 260 units,
// where Go's os.Symlink succeeds. It
// goes through WidePath now, so a long one is stored as its full path.
// Windows only; a working directory without a drive (UNC) returns at once.
test "Symlink takes a drive-relative target of 5, 198 and 300 units and stores the full path it resolves to" (sys: &System) {
    if Target.os != core.Os.Windows {
        return
    }
    io := sys.io
    wd := Cwd(sys.arena, io).Must()
    if wd.Len() < 2 || wd.AsBytes()[1] != 0x3A {
        return
    }
    drive := wd[0..2]
    p := "mk_tm9_lkdrv"
    for n in [5, 198, 300] {
        t := sys.arena.Fmt("{drive}{r}", (drive: drive, r: Tm9Rep(sys.arena, n - 2, 47))).Must()
        Remove(io, p) else {}
        var made := "ok"
        Symlink(io, t, p) else e {
            made = TfsName(sys.arena, e)
        }
        if made == "Unsupported" {
            return
        }
        Assert(made == "ok")
        Assert(StatLink(io, p).Must().kind == Kind.Link)
        Assert(ReadLink(sys.arena, io, p).Must() == Absolute(sys.arena, io, t).Must())
        Remove(io, p).Must()
    }
}

// Tm9Err is the name of Absolute's error for p under io, or "ok".
Tm9Err(a: &Arena, io: Io, p: str): str from a {
    _ = Absolute(a, io, p) else e {
        return TfsName(a, e)
    }
    return "ok"
}

// The Io here is not a Sim and has no seed: std.fs's tests cannot import
// std.sim, whose own tests import std.fs (E0205). It is an InertIo with
// a token bit in its flags, as a Sim's and a Tape's Io carry one, and
// its call entry answers Unsupported, as theirs do for
// IoOpCwd and IoOpAbsolute. The same answers under a Sim, and the counted
// operations, and TempDir, Walk and CreateTemp, are the blocks of
// std/sim/tests_fs.mk.
test "under a simulated Io Absolute answers as on Linux on every target, a path from / cleaned with / and any other path Unsupported, while the native Io keeps the target's form" (sys: &System) {
    io := core.MakeIo(raw.Of(&core.InertIo), 2)
    Assert(Absolute(sys.arena, io, "/a/./b/../c//").Must() == "/a/c")
    Assert(Absolute(sys.arena, io, "/x").Must() == "/x")
    Assert(Absolute(sys.arena, io, "//h/s/x").Must() == "/h/s/x")
    Assert(Absolute(sys.arena, io, "/..").Must() == "/")
    for p in ["a/b", "", "C:x", "\\x"] {
        Assert(Tm9Err(sys.arena, io, p) == "Unsupported")
    }
    var cwd := "ok"
    _ = Cwd(sys.arena, io) else e {
        cwd = TfsName(sys.arena, e)
    }
    Assert(cwd == "Unsupported")
    // The native Io: the root of the working directory's volume on
    // Windows (a drive, or a UNC share), so not `/x` and ending `\x`.
    n := Absolute(sys.arena, sys.io, "/x").Must()
    if Target.os == core.Os.Windows {
        b := n.AsBytes()
        Assert(n != "/x" && n.Len() > 2 && b[n.Len() - 2] == 0x5C && b[n.Len() - 1] == 0x78)
    } else {
        Assert(n == "/x")
    }
}

// Tm9Held is what p holds: its text, or "absent" when it is NotFound.
Tm9Held(a: &Arena, io: Io, p: str): str from a {
    t := ReadText(a, io, p) else e {
        Assert(TfsName(a, e) == "NotFound")
        return "absent"
    }
    return t
}

// The copy fails once dst is open by an exclusive lock on src, held
// through a second handle: on Windows a lock is LockFileEx and the copy's
// reads meet it (ERROR_LOCK_VIOLATION, Os(code: 33)); on Linux it is
// flock, advisory, so there the copies succeed. The prefix under std.sim
// is the block in std/sim/tests_copy.mk:
// std.fs's tests cannot import std.sim (E0205).
test "a copy that fails once dst is open leaves none of dst's earlier bytes: absent after Copy on Windows when nothing else holds it, empty after the read and write loop; a failure before dst is opened leaves dst as it was" (sys: &System) {
    io := sys.io
    src := "mk_tm9_cpsrc"
    a := "mk_tm9_cpa"
    b := "mk_tm9_cpb"
    for p in [src, a, b] {
        Remove(io, p) else {}
    }
    WriteAll(io, src, "0123456789".AsBytes()).Must()
    WriteAll(io, b, "OLD-CONTENT!".AsBytes()).Must()
    // Before dst is opened: src missing, or src a directory.
    var got := "ok"
    _ = Copy(io, "mk_tm9_cpnone", b) else e {
        got = TfsName(sys.arena, e)
    }
    Assert(got == "NotFound")
    got = "ok"
    _ = Copy(io, ".", b) else e {
        got = TfsName(sys.arena, e)
    }
    Assert(got == "IsDirectory")
    Assert(Tm9Held(sys.arena, io, b) == "OLD-CONTENT!")
    win := Target.os == core.Os.Windows
    want := if win { "Os(code: 33)" } else { "ok" }
    var l := OpenWith(io, src, ReadWrite).Must()
    l.Lock(io, LockMode.Exclusive).Must()
    // The native copy, into a new dst and over an existing one.
    left := if win { "absent" } else { "0123456789" }
    for p in [a, b] {
        got = "ok"
        _ = Copy(io, src, p) else e {
            got = TfsName(sys.arena, e)
        }
        Assert(got == want)
        Assert(Tm9Held(sys.arena, io, p) == left)
    }
    // The read and write loop (an Io with no copy call's path), over an
    // existing dst: truncated, then left holding what it wrote.
    Remove(io, a) else {}
    WriteAll(io, b, "OLD-CONTENT!".AsBytes()).Must()
    prefix := if win { "" } else { "0123456789" }
    for p in [a, b] {
        got = "ok"
        _ = CopyThrough(io, src, p) else e {
            got = TfsName(sys.arena, e)
        }
        Assert(got == want)
        Assert(Tm9Held(sys.arena, io, p) == prefix)
    }
    l.Unlock(io).Must()
    l.Close(io).Must()
    for p in [src, a, b] {
        Remove(io, p).Must()
    }
}

// Tm9Look is the name of Stat's error for p, or "ok".
Tm9Look(a: &Arena, io: Io, p: str): str from a {
    _ = Stat(io, p) else e {
        return TfsName(a, e)
    }
    return "ok"
}

// On Windows CopyFileExW marks the dst it wrote for deletion when it
// fails, so a handle on dst that shares delete (std's own Open does)
// keeps it there, and every call on it is Denied, until that handle
// closes (measured on Windows 11). On Linux the lock is
// advisory and the copy succeeds, so the block checks only that.
test "on Windows a failed copy's dst that another handle holds is Denied to every call, Remove too, until that handle closes" (sys: &System) {
    io := sys.io
    src := "mk_tm9_hsrc"
    dst := "mk_tm9_hdst"
    for p in [src, dst] {
        Remove(io, p) else {}
    }
    WriteAll(io, src, "0123456789".AsBytes()).Must()
    WriteAll(io, dst, "OLD-CONTENT!".AsBytes()).Must()
    var held := Open(io, dst).Must()
    var l := OpenWith(io, src, ReadWrite).Must()
    l.Lock(io, LockMode.Exclusive).Must()
    var got := "ok"
    _ = Copy(io, src, dst) else e {
        got = TfsName(sys.arena, e)
    }
    win := Target.os == core.Os.Windows
    if win {
        Assert(got == "Os(code: 33)")
        Assert(Tm9Look(sys.arena, io, dst) == "Denied")
        var rt := "ok"
        _ = ReadText(sys.arena, io, dst) else e {
            rt = TfsName(sys.arena, e)
        }
        Assert(rt == "Denied")
        var rm := "ok"
        Remove(io, dst) else e {
            rm = TfsName(sys.arena, e)
        }
        Assert(rm == "Denied")
    } else {
        Assert(got == "ok")
    }
    held.Close(io).Must()
    if win {
        Assert(Tm9Look(sys.arena, io, dst) == "NotFound")
    } else {
        Assert(ReadText(sys.arena, io, dst).Must() == "0123456789")
    }
    l.Unlock(io).Must()
    l.Close(io).Must()
    for p in [src, dst] {
        Remove(io, p) else {}
    }
}

// Tm9Unlinker removes TfeTree(root, 20, 4) bottom-up with fs.Remove, each
// d's sub, its four files, then d, then root, taking NotFound as removed
// and going on after any other failure. A directory's removal may also
// meet the OS's not-empty code while the other remover still holds or has
// not yet removed an entry of it. It stores 1 in out when every Remove
// succeeded or was one of those, 2 when one was Denied and none failed
// otherwise, 3 otherwise.
Tm9Unlinker(io: Io, h: &Heap, root: str, out: &atomic.AtomicInt) {
    var a := Arena.New(h).Must()
    defer a.Free()
    var res := 1
    for i in 0..21 {
        var ps := [root, "", "", "", "", ""]
        if i < 20 {
            d := a.Fmt("{root}/d{i}", (root, i)).Must()
            ps = [a.Fmt("{d}/sub", (d)).Must(), a.Fmt("{d}/f0", (d)).Must(), a.Fmt("{d}/f1", (d)).Must(), a.Fmt("{d}/f2", (d)).Must(), a.Fmt("{d}/f3", (d)).Must(), d]
        }
        for k in 0..6 {
            p := ps[k]
            if p.Len() == 0 {
                continue
            }
            dir := k == 0 || k == 5 || i == 20
            Remove(io, p) else e {
                n := TfsName(&a, e)
                if n == "Denied" {
                    if res == 1 {
                        res = 2
                    }
                } else if n != "NotFound" && !(dir && n.StartsWith("Os(")) {
                    res = 3
                }
            }
        }
    }
    out.Store(i64(res))
}

// On Windows a RemoveAll raced by a task
// that removes the tree's entries with fs.Remove can see Denied, and so
// can the task, either or both (measured on Windows 11: RemoveAll
// Denied in 87 of 200 rounds, the task in 161, both in 48); a RemoveAll
// made once both have returned removes what is left (RemoveAll's doc), so
// the root can then be made again. Each round has its own root. Three
// rounds: at one carrier a round takes 3 to 4 s on Windows 11 under load
// and the block must stay well inside a harness's 60 s.
// What Linux answers in the race itself was never measured, so there only
// the last RemoveAll and the root's new MakeDir are judged.
test "a RemoveAll raced by a task removing the tree with Remove can see Denied on Windows, either or both, and a RemoveAll after them removes the rest" (sys: &System) {
    io := sys.io
    win := Target.os == core.Os.Windows
    for round in 0..3 {
        root := sys.arena.Fmt("mk_tm9_race{round}", (round)).Must()
        RemoveAll(io, root).Must()
        TfeTree(sys.arena, io, root, 20, 4)
        x := atomic.AtomicInt.New(0)
        y := atomic.AtomicInt.New(0)
        scope s := io.Scope() {
            s.go TfeRemover(io, root, &x)
            s.go Tm9Unlinker(io, sys.heap, root, &y)
        }
        if win {
            Assert(x.Load() == 1 || x.Load() == 2)
            Assert(y.Load() == 1 || y.Load() == 2)
        }
        RemoveAll(io, root).Must()
        _ = Stat(io, root) else e {
            Assert(TfsName(sys.arena, e) == "NotFound")
            MakeDir(io, root) else e2 {
                Panic(sys.arena.Fmt("round {round}: MakeDir of the removed root: {e2}", (round, e2)).Must())
            }
            Remove(io, root).Must()
            continue
        }
        Assert(false)
    }
}
