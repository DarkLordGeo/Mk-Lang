// Why: Walk and RemoveAll must cost one OS refill per directory, no final
// path per entry on Windows and no statx per entry on Linux.
// What: test blocks over those directory costs, on both targets, run by
// mk test --std=std.fs, with the helpers TfdTree, TfdWalk and TfdOn/TfdOff.
// How: std.sys's test points 11 to 14 (testhook.mk) count the costs, each
// block in a child process of its own, over a scaled-down fs-mkrm tree.

import std.core
import std.raw
import std.sys

// TfdNop is the test points' callback: the points only count.
TfdNop(arg: *u8) {
    _ = arg
}

// TfdOn turns point p on and returns its count so far; TfdOff turns it
// off and returns its count.
TfdOn(p: u32): u64 {
    return sys.SysTestHook(p, TfdNop, raw.Null[u8](), true)
}

TfdOff(p: u32): u64 {
    return sys.SysTestHook(p, TfdNop, raw.Null[u8](), false)
}

// TfdRoot is where a block makes its tree named name.
TfdRoot(a: &Arena, io: Io, name: str): str from a, name {
    if Target.os != core.Os.Linux {
        return name
    }
    t := TempDir(a, io).Must()
    return a.Fmt("{t}/{name}", (t, name)).Must()
}

// TfdTree makes the tree under root.
TfdTree(h: &Heap, io: Io, root: str) {
    var a := Arena.New(h).Must()
    defer a.Free()
    for i in 0..5 {
        for j in 0..5 {
            d := a.Fmt("{root}/d{i}/s{j}", (root, i, j)).Must()
            MakeDirAll(io, d).Must()
            for k in 0..4 {
                WriteAll(io, a.Fmt("{d}/f{k}", (d, k)).Must(), "x".AsBytes()).Must()
            }
        }
        a.Reset()
    }
}

// TfdWalk walks root and returns its directories (the root included) * 1e6
// + its other entries.
TfdWalk(h: &Heap, io: Io, root: str): int {
    var a := Arena.New(h).Must()
    defer a.Free()
    var w := Walk(h, io, root).Must()
    defer w.Close()
    var dirs := 0
    var files := 0
    for e := w.Next(&a, io).Must() {
        if e.kind == Kind.Dir {
            dirs += 1
        } else {
            files += 1
        }
    }
    return dirs * 1_000_000 + files
}

test "Walk of an fs-mkrm-shaped tree reads each directory in one fill on Windows and no entry's statx on Linux" (sys: &System) {
    io := sys.io
    root := TfdRoot(sys.arena, io, "mk_tfd_walk")
    RemoveAll(io, root).Must()
    TfdTree(sys.heap, io, root)
    p11 := TfdOn(11)
    p12 := TfdOn(12)
    p13 := TfdOn(13)
    got := TfdWalk(sys.heap, io, root)
    paths := TfdOff(11) - p11
    fills := TfdOff(12) - p12
    stats := TfdOff(13) - p13
    Assert(got == 31 * 1_000_000 + 100)
    if Target.os == core.Os.Windows {
        // One fill per directory, made in its open's job, which also
        // finds the listing's end (14b); one final path, the root's (20).
        Assert(fills <= 31 && paths <= 1)
    } else {
        // The kind comes from d_type; statx only on DT_UNKNOWN (19).
        Assert(stats <= 31 && paths == 0)
    }
    // fs.Dir keeps every entry's lstat facts: each f<k> is 1 byte.
    var d := Dir.Open(io, sys.arena.Fmt("{root}/d3/s4", (root)).Must()).Must()
    var n := 0
    for {
        e := d.Next(sys.arena, io).Must() else {
            break
        }
        Assert(!e.is_dir && e.size == 1)
        n += 1
    }
    d.Close()
    Assert(n == 4)
    RemoveAll(io, root).Must()
}

test "RemoveAll of an fs-mkrm-shaped tree reads no final path per entry on Windows and no entry's statx on Linux" (sys: &System) {
    io := sys.io
    root := TfdRoot(sys.arena, io, "mk_tfd_rm")
    RemoveAll(io, root).Must()
    TfdTree(sys.heap, io, root)
    p11 := TfdOn(11)
    p12 := TfdOn(12)
    p13 := TfdOn(13)
    RemoveAll(io, root).Must()
    paths := TfdOff(11) - p11
    fills := TfdOff(12) - p12
    stats := TfdOff(13) - p13
    _ = Stat(io, root) else e {
        Assert(TfsName(sys.arena, e) == "NotFound")
        if Target.os == core.Os.Windows {
            // The root's parent, held by the open-dir entry (which pins
            // nothing), is asked twice: the root's open and its removal.
            // The root keeps its own final path, read once after its open
            // (its parent keeps none, so the path the open used may name
            // another place by then). Every other directory's
            // path is the one its open used (20). Exactly 3: 2 would mean
            // the root kept the path read before its open.
            Assert(fills <= 31 && paths == 3)
        } else {
            Assert(stats <= 31 && paths == 0)
        }
        return
    }
    Assert(false)
}

// TfdPad is the first 190 bytes of TfdLong's names.
const TfdPad = "nnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnnn"

// TfdLong is a name of 200 bytes ending in i's digits.
TfdLong(a: &Arena, i: int): str from a {
    return a.Fmt("{TfdPad}{i:010}", (TfdPad, i)).Must()
}

// TfdIndex is the number the last 10 bytes of b spell.
TfdIndex(b: []u8): int {
    var n := 0
    for c in b[b.Len() - 10..] {
        n = n * 10 + int(c - 48)
    }
    return n
}

test "Walk lists directories of 0, 1, 5 and 120 long names, the last over several fills, every name once" (sys: &System) {
    io := sys.io
    root := TfdRoot(sys.arena, io, "mk_tfd_shapes")
    RemoveAll(io, root).Must()
    var a := Arena.New(sys.heap).Must()
    defer a.Free()
    counts := [0, 1, 5, 120]
    for c in counts {
        d := a.Fmt("{root}/e{c}", (root, c)).Must()
        MakeDirAll(io, d).Must()
        for i in 0..c {
            WriteAll(io, a.Fmt("{d}/{n}", (d, n: TfdLong(&a, i))).Must(), "y".AsBytes()).Must()
        }
        a.Reset()
    }
    // 120 records of about 470 bytes (Windows) or 224 (Linux) take
    // several 16 KiB fills (4 and 2); the end is found after the last. The
    // second round has test point 14 on: each Windows refill asks for
    // 1,024 bytes first, so the rest of every block is chained records.
    var chained: u64 = 0
    for round in 0..2 {
        p14 := if round == 1 { TfdOn(14) } else { 0 }
        for c in counts {
            var seen: [120]bool = [false; 120]
            var n := 0
            var w := Walk(sys.heap, io, a.Fmt("{root}/e{c}", (root, c)).Must()).Must()
            for e := w.Next(&a, io).Must() {
                if e.depth == 1 {
                    Assert(e.kind == Kind.File)
                    k := TfdIndex(e.path.AsBytes())
                    Assert(k < c && !seen[k])
                    seen[k] = true
                    n += 1
                }
            }
            w.Close()
            Assert(n == c)
            a.Reset()
        }
        if round == 1 {
            chained = TfdOff(14) - p14
        }
    }
    Assert(Target.os != core.Os.Windows || chained > 0)
    RemoveAll(io, root).Must()
}

test "Walk and RemoveAll inside directories whose paths are too long to keep read the OS's final path" (sys: &System) {
    io := sys.io
    root := TfdRoot(sys.arena, io, "mk_tfd_long")
    RemoveAll(io, root).Must()
    // 40 levels of 60-byte names: past the 2,048 units a Windows stream
    // keeps (sys/windows/dir.mk DirPathMax), so the deeper levels take
    // GetFinalPathNameByHandleW at each call.
    var t := Text.New(sys.arena)
    t.Push(root).Must()
    for i in 0..40 {
        t.Push(sys.arena.Fmt("/{i:02}ddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", (i)).Must()).Must()
    }
    deep := t.Str()
    MakeDirAll(io, deep).Must()
    WriteAll(io, sys.arena.Fmt("{deep}/leaf", (deep)).Must(), "z".AsBytes()).Must()
    p11 := TfdOn(11)
    got := TfdWalk(sys.heap, io, root)
    walked := TfdOff(11) - p11
    Assert(got == 41 * 1_000_000 + 1)
    q11 := TfdOn(11)
    RemoveAll(io, root).Must()
    removed := TfdOff(11) - q11
    if Target.os == core.Os.Windows {
        Assert(walked > 1 && removed > 2)
    }
    _ = Stat(io, root) else e {
        Assert(TfsName(sys.arena, e) == "NotFound")
        return
    }
    Assert(false)
}
