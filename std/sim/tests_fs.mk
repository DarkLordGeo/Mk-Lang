// Why: under a simulated Io a seed gives one run whatever the target, so a
// std.fs call must answer alike and make the same Sim operations on every
// target. These blocks live here because std.fs's own tests cannot import
// std.sim, whose tests import std.fs (E0205).
// What: fs.Absolute, fs.Cwd and fs.TempDir answer alike on every target,
// and Walk and CreateTemp join with /; helper FsOps counts operations.
// How: each block asserts literal answers, so one text passing on both
// targets is the both-target check. fs.Absolute once took the system's
// path on Windows (Unsupported under a Sim) where Linux cleaned a path
// from `/` with no call; fs.TempDir answered `C:\Windows` on Windows, and
// Walk and CreateTemp joined with `\` there.

import std.fs

// FsOps makes step (0 nothing, 1 fs.Absolute forty times, 2 fs.Cwd and
// fs.TempDir forty times each, 3 one fs.MakeDir, the positive control) in
// a Sim of seed after arming its crash point, then Unlocks a file of it
// until the run ends or cap succeed: how many succeeded. A step that
// counts no Sim operation leaves the count of step 0, which Unlock (one
// operation on every target) makes alike on every target.
FsOps(a: &Arena, seed: u64, step: int, cap: int): int {
    var s := Sim.New(a, seed).Must()
    io := s.Io()
    var f := fs.Create(io, "lock.bin").Must()
    s.CrashAfterRandomOps()
    if step == 1 {
        for _ in 0..10 {
            for p in ["/a/./b/../c//", "a/b", "", "//h/s/x"] {
                _ = fs.Absolute(a, io, p) else {}
            }
        }
    } else if step == 2 {
        for _ in 0..40 {
            _ = fs.Cwd(a, io) else {}
            _ = fs.TempDir(a, io) else {}
        }
    } else if step == 3 {
        // The positive control: one call that makes counted Sim operations.
        fs.MakeDir(io, "d") else {}
    }
    var n := 0
    for n < cap {
        f.Unlock(io) else {
            break
        }
        n += 1
    }
    f.Abandon()
    s.Free()
    return n
}

// M9Abs is the name of fs.Absolute's error for p under io, or its answer.
M9Abs(a: &Arena, io: Io, p: str): str from a {
    return fs.Absolute(a, io, p) else e {
        return a.Fmt("{e}", (e)).Must()
    }
}

test "under std.sim fs.Absolute answers alike on every target, a path from / cleaned with / and any other path Unsupported as fs.Cwd is, and counts no Sim operation" (sys: &System) {
    var s := Sim.New(sys.arena, 1).Must()
    io := s.Io()
    Assert(M9Abs(sys.arena, io, "/a/./b/../c//") == "/a/c")
    Assert(M9Abs(sys.arena, io, "//h/s/x") == "/h/s/x")
    for p in ["a/b", "", "C:x", "\\x"] {
        Assert(M9Abs(sys.arena, io, p) == "Unsupported")
    }
    var cwd := "ok"
    _ = fs.Cwd(sys.arena, io) else e {
        cwd = sys.arena.Fmt("{e}", (e)).Must()
    }
    Assert(cwd == "Unsupported")
    s.Free()
    var checked := 0
    var moved := 0
    for seed in 0..12 {
        u := FsOps(sys.arena, Cast[u64](seed), 0, 2000)
        if u >= 2000 {
            // This seed's crash point is past the cap: not compared.
            continue
        }
        Assert(FsOps(sys.arena, Cast[u64](seed), 1, 2000) == u)
        Assert(FsOps(sys.arena, Cast[u64](seed), 2, 2000) == u)
        // The count sees Sim operations: one MakeDir lowers it.
        if FsOps(sys.arena, Cast[u64](seed), 3, 2000) < u {
            moved += 1
        }
        checked += 1
    }
    Assert(checked >= 3)
    Assert(moved >= 1)
}

test "under std.sim fs.TempDir is /tmp and Walk and CreateTemp join with / on every target" (sys: &System) {
    var s := Sim.New(sys.arena, 1).Must()
    io := s.Io()
    td := fs.TempDir(sys.arena, io).Must()
    Assert(td == "/tmp")
    // A Sim starts empty: the directory is made, then used.
    fs.MakeDirAll(io, td).Must()
    (file: f, path: p) := fs.CreateTemp(sys.arena, io, "", "m9t").Must()
    f.Close(io).Must()
    Assert(p.Len() == 8 + 16 && p[0..8] == "/tmp/m9t")
    Assert(fs.Stat(io, p).Must().kind == fs.Kind.File)
    fs.MakeDirAll(io, "/d/e").Must()
    (file: g, path: q) := fs.CreateTemp(sys.arena, io, "/d", "x").Must()
    g.Close(io).Must()
    Assert(q.Len() == 4 + 16 && q[0..4] == "/d/x")
    fs.Remove(io, q).Must()
    fs.WriteAll(io, "/d/e/f", b"m9").Must()
    var w := fs.Walk(sys.heap, io, "/d").Must()
    var got := ""
    for {
        e := w.Next(sys.arena, io).Must() else {
            break
        }
        d := e.depth
        ps := e.path.Str().Must()
        got = sys.arena.Fmt("{got}{d} {ps};", (got, d, ps)).Must()
    }
    w.Close()
    Assert(got == "0 /d;1 /d/e;2 /d/e/f;")
    // A root that ends with `\`, which the Sim reads as /d: a name is
    // joined after it with `/`, both where the walk lists the name and
    // where it enters the directory (the target's form puts the name one
    // byte earlier on Windows, where `\` ends the root).
    Assert(M9Walk(sys.arena, sys.heap, io, "/d\\") == "0 /d\\;1 /d\\/e;2 /d\\/e/f;")
    // A prefix with `\`, which the Sim reads as `/`, would name a path
    // outside dir (`..\esc` the root's /esc): InvalidPath on every target,
    // as `/` is.
    Assert(M9TempErr(sys.arena, io, "/d", "a\\b") == "InvalidPath")
    Assert(M9TempErr(sys.arena, io, "/d", "..\\esc") == "InvalidPath")
    s.Free()
}

// M9Walk is the walk of root under io, one "depth path;" per entry.
M9Walk(a: &Arena, h: &Heap, io: Io, root: str): str from a {
    var w := fs.Walk(h, io, root).Must()
    var got := ""
    for {
        e := w.Next(a, io).Must() else {
            break
        }
        d := e.depth
        ps := e.path.Str().Must()
        got = a.Fmt("{got}{d} {ps};", (got, d, ps)).Must()
    }
    w.Close()
    return got
}

// M9TempErr is the name of fs.CreateTemp's error for dir and prefix under
// io, or "made" and the path when it made a file (closed and left).
M9TempErr(a: &Arena, io: Io, dir: str, prefix: str): str from a {
    (file: f, path: p) := fs.CreateTemp(a, io, dir, prefix) else e {
        return a.Fmt("{e}", (e)).Must()
    }
    f.Close(io).Must()
    return a.Fmt("made {p}", (p)).Must()
}
