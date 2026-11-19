// Why: fs.RemoveAll must remove a link, never what it points to, and must
// refuse names that would leave the directory it holds.
// What: two RT8-RemoveAt blocks over std/rt/removeat.mk's rt.DirOpenAt and
// rt.RemoveAt, with helpers RaJoin, RaMkdir, RaFile and RaRemoveTree.
// How: a tree with a file, a read-only file, nested directories and a link
// to a directory outside it is removed by RemoveAll's walk; each refusal (a
// separator, . or .., an empty name, a link or a file opened as a directory,
// a kind that does not match, a directory not empty, a cancelled scope) is
// checked. Linux runs them in the distribution's /tmp (FlName: on WSL's
// Windows drives, links and permissions follow Windows rules); Windows in TEMP.

import std.core
import std.rt

// RaJoin writes dir, the separator and name into b.
RaJoin(b: mut [512]u8, dir: str, name: str): str from b {
    var n := 0
    for x in dir.AsBytes() {
        b[n] = x
        n += 1
    }
    b[n] = if PWin() { 92 } else { 47 }
    n += 1
    for x in name.AsBytes() {
        b[n] = x
        n += 1
    }
    return str.From(b[0..n]).Must()
}

// RaMkdir makes dir/name.
RaMkdir(io: Io, dir: str, name: str) {
    var b: [512]u8 = [0; 512]
    Assert(NativeMkdir(io, RaJoin(mut b, dir, name)).err == core.SysErr.None)
}

// RaFile makes dir/name holding n bytes.
RaFile(io: Io, dir: str, name: str, n: int) {
    var b: [512]u8 = [0; 512]
    CaWrite(io, RaJoin(mut b, dir, name), n)
}

// RaExists: p is there (a link counts, not followed).
RaExists(io: Io, p: str): bool {
    var w := core.IoWords(a: 0, b: 0, c: 0, d: 0)
    return rt.StatEx(io, p.AsBytes(), true, mut w).err == core.SysErr.None
}

// RaRemoveTree removes everything the directory stream d lists from where
// it stands, as fs.RemoveAll walks: a directory is opened inside d,
// emptied, closed and removed; anything else is removed itself. One pass:
// a listing that removes as it reads may skip an entry on some file
// systems, so the caller lists again with a fresh stream (the large-tree
// block). Returns the entries removed.
RaRemoveTree(io: Io, d: uint, depth: int): int {
    Assert(depth < 16)
    var removed := 0
    var name: [512]u8 = [0; 512]
    var st := core.IoStat(kind: 0, size: 0, modified: 0)
    for {
        r := NativeReadDir(io, d, mut name[..], mut st)
        Assert(r.err == core.SysErr.None)
        if r.value == 0 {
            return removed
        }
        nm := name[0..Trunc[int](r.value)]
        if st.kind == core.IoKindDir {
            o := rt.DirOpenAt(io, d, nm)
            Assert(o.err == core.SysErr.None)
            c := Trunc[uint](o.value)
            removed += RaRemoveTree(io, c, depth + 1)
            Assert(NativeClose(io, c).err == core.SysErr.None)
            Assert(rt.RemoveAt(io, d, nm, true).err == core.SysErr.None)
        } else {
            Assert(rt.RemoveAt(io, d, nm, false).err == core.SysErr.None)
        }
        removed += 1
    }
}

test "RT8-RemoveAt a tree with a read-only file, nested directories and a link to a directory outside it is removed by the walk; the link's target is kept" (sys: &System) {
    io := sys.io
    var rb: [256]u8 = [0; 256]
    var kb: [256]u8 = [0; 256]
    root := FlName(mut rb, "rmat", 0)
    keep := FlName(mut kb, "rmatk", 0)
    Assert(NativeMkdir(io, root).err == core.SysErr.None)
    Assert(NativeMkdir(io, keep).err == core.SysErr.None)
    RaFile(io, keep, "k", 7)
    RaFile(io, root, "f", 100)
    RaFile(io, root, "ro", 10)
    var pb: [512]u8 = [0; 512]
    Assert(rt.SetMode(io, RaJoin(mut pb, root, "ro").AsBytes(), 0o444).err == core.SysErr.None)
    RaMkdir(io, root, "sub")
    var sb: [512]u8 = [0; 512]
    sub := RaJoin(mut sb, root, "sub")
    RaFile(io, sub, "g", 5)
    RaMkdir(io, sub, "deep")
    var db: [512]u8 = [0; 512]
    RaFile(io, RaJoin(mut db, sub, "deep"), "h", 3)
    var lb: [512]u8 = [0; 512]
    link := RaJoin(mut lb, root, "lnk")
    linked := rt.Symlink(io, keep.AsBytes(), link.AsBytes()).err == core.SysErr.None
    if !linked {
        Assert(PWin())
    }
    // The root by its whole path; a link is never opened as a directory.
    o := rt.DirOpenAt(io, 0, root.AsBytes())
    Assert(o.err == core.SysErr.None)
    d := Trunc[uint](o.value)
    if linked {
        Assert(rt.DirOpenAt(io, 0, link.AsBytes()).err == core.SysErr.NotDirectory)
        Assert(rt.DirOpenAt(io, d, "lnk".AsBytes()).err == core.SysErr.NotDirectory)
    }
    Assert(rt.DirOpenAt(io, d, "f".AsBytes()).err == core.SysErr.NotDirectory)
    Assert(rt.DirOpenAt(io, d, "missing".AsBytes()).err == core.SysErr.NotFound)
    // Names that would leave the held directory, refused before any call.
    var bad: [5]str = ["", ".", "..", "sub/deep", "../x"]
    for n in bad[..] {
        Assert(rt.DirOpenAt(io, d, n.AsBytes()).err == core.SysErr.InvalidPath)
        Assert(rt.RemoveAt(io, d, n.AsBytes(), false).err == core.SysErr.InvalidPath)
    }
    var nul: [3]u8 = [97, 0, 98]
    Assert(rt.DirOpenAt(io, d, nul[..]).err == core.SysErr.InvalidPath)
    Assert(rt.RemoveAt(io, d, nul[..], false).err == core.SysErr.InvalidPath)
    // Bytes that are not WTF-8 (a truncated sequence, a lone continuation
    // byte): InvalidPath on Windows, never read past their end; any bytes
    // but NUL and '/' name a Linux entry (here none such exists).
    var trunc: [2]u8 = [97, 0xC3]
    var cont: [2]u8 = [0x80, 97]
    if PWin() {
        Assert(rt.RemoveAt(io, d, trunc[..], false).err == core.SysErr.InvalidPath)
        Assert(rt.RemoveAt(io, d, cont[..], false).err == core.SysErr.InvalidPath)
        Assert(rt.DirOpenAt(io, 0, trunc[..]).err == core.SysErr.InvalidPath)
    } else {
        Assert(rt.RemoveAt(io, d, trunc[..], false).err == core.SysErr.NotFound)
    }
    if PWin() {
        Assert(rt.RemoveAt(io, d, "sub\\deep".AsBytes(), true).err == core.SysErr.InvalidPath)
        Assert(rt.RemoveAt(io, d, "f:s".AsBytes(), false).err == core.SysErr.InvalidPath)
    }
    Assert(rt.RemoveAt(io, 0, "".AsBytes(), false).err == core.SysErr.InvalidPath)
    // The kind must match what the caller listed.
    Assert(rt.RemoveAt(io, d, "sub".AsBytes(), false).err == core.SysErr.IsDirectory)
    Assert(rt.RemoveAt(io, d, "f".AsBytes(), true).err == core.SysErr.NotDirectory)
    Assert(rt.RemoveAt(io, d, "missing".AsBytes(), false).err == core.SysErr.NotFound)
    nonempty := rt.RemoveAt(io, d, "sub".AsBytes(), true)
    Assert(rt.RemoveNotEmpty(nonempty))
    // A cancelled scope removes nothing.
    var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    scope s := io.Scope() {
        s.Cancel()
        r = rt.RemoveAt(io, d, "f".AsBytes(), false)
    }
    Assert(r.err == core.SysErr.Canceled)
    var fb: [512]u8 = [0; 512]
    Assert(RaExists(io, RaJoin(mut fb, root, "f")))
    // Windows: a directory held by DirOpenAt cannot be removed until closed.
    if PWin() {
        var eb: [512]u8 = [0; 512]
        RaMkdir(io, root, "held")
        ho := rt.DirOpenAt(io, d, "held".AsBytes())
        Assert(ho.err == core.SysErr.None)
        Assert(rt.RemoveAt(io, d, "held".AsBytes(), true).err == core.SysErr.Denied)
        Assert(NativeClose(io, Trunc[uint](ho.value)).err == core.SysErr.None)
        Assert(rt.RemoveAt(io, d, "held".AsBytes(), true).err == core.SysErr.None)
        Assert(!RaExists(io, RaJoin(mut eb, root, "held")))
    }
    n := RaRemoveTree(io, d, 0)
    // f, ro, sub, sub/g, sub/deep, sub/deep/h, and the link.
    want := if linked { 7 } else { 6 }
    Assert(n == want)
    Assert(NativeClose(io, d).err == core.SysErr.None)
    // The emptied root by its whole path.
    Assert(rt.RemoveAt(io, 0, root.AsBytes(), true).err == core.SysErr.None)
    Assert(!RaExists(io, root))
    var kk: [512]u8 = [0; 512]
    Assert(RaExists(io, RaJoin(mut kk, keep, "k")))
    Assert(NativeRemove(io, RaJoin(mut kk, keep, "k")).err == core.SysErr.None)
    Assert(NativeRemove(io, keep).err == core.SysErr.None)
}

test "RT8-RemoveAt a large tree: 3 levels of 40 entries are removed through held directories, and a directory listed then filled again is not empty" (sys: &System) {
    io := sys.io
    var rb: [256]u8 = [0; 256]
    root := FlName(mut rb, "rmatl", 0)
    Assert(NativeMkdir(io, root).err == core.SysErr.None)
    var made := 0
    for i in 0..40 {
        var nb: [8]u8 = [0; 8]
        nb[0] = 100
        nb[1] = Trunc[u8](48 + i / 10)
        nb[2] = Trunc[u8](48 + i % 10)
        dn := str.From(nb[0..3]).Must()
        RaMkdir(io, root, dn)
        made += 1
        var pb: [512]u8 = [0; 512]
        p := RaJoin(mut pb, root, dn)
        for j in 0..3 {
            var fb: [8]u8 = [0; 8]
            fb[0] = 102
            fb[1] = Trunc[u8](48 + j)
            RaFile(io, p, str.From(fb[0..2]).Must(), j)
            made += 1
        }
    }
    o := rt.DirOpenAt(io, 0, root.AsBytes())
    Assert(o.err == core.SysErr.None)
    d := Trunc[uint](o.value)
    var removed := RaRemoveTree(io, d, 0)
    // A listing that removes as it reads can skip on some file systems: a
    // second stream over the same directory finishes it.
    Assert(NativeClose(io, d).err == core.SysErr.None)
    o2 := rt.DirOpenAt(io, 0, root.AsBytes())
    Assert(o2.err == core.SysErr.None)
    d2 := Trunc[uint](o2.value)
    removed += RaRemoveTree(io, d2, 0)
    Assert(removed == made)
    RaMkdir(io, root, "late")
    // Held, the root cannot be removed on Windows (its pin); closed, it is
    // not empty on both targets.
    if PWin() {
        Assert(rt.RemoveAt(io, 0, root.AsBytes(), true).err == core.SysErr.Denied)
    } else {
        Assert(rt.RemoveNotEmpty(rt.RemoveAt(io, 0, root.AsBytes(), true)))
    }
    Assert(NativeClose(io, d2).err == core.SysErr.None)
    Assert(rt.RemoveNotEmpty(rt.RemoveAt(io, 0, root.AsBytes(), true)))
    var lb: [512]u8 = [0; 512]
    Assert(rt.RemoveAt(io, 0, RaJoin(mut lb, root, "late").AsBytes(), true).err == core.SysErr.None)
    Assert(rt.RemoveAt(io, 0, root.AsBytes(), true).err == core.SysErr.None)
}
