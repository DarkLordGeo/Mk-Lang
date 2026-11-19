// fs, Mk: std.fs's tree operations against each standard
// library's own (tests/bench-lanes/std-fs2/README.md). The tree is made under
// fs.TempDir in mkbench-fs2-mk: 10 directories d<i>, each with 10
// directories s<j> of 10 one-byte files f<k>, 1,110 entries.
// Usage: fs walk <reps> | fs mkrm <reps> | fs copy <file> <reps>.
//   walk: makes the tree, lists it reps times with fs.Walk (the root not
//         counted), removes it; prints reps, directories, files and name
//         bytes, summed over the walks.
//   mkrm: reps times makes the tree (fs.MakeDirAll and fs.WriteAll) and
//         removes it with fs.RemoveAll; prints reps, entries made and the
//         removals that left the root behind.
//   copy: copies file reps times with fs.Copy onto one destination;
//         prints reps and the bytes copied.

import std.fs

// Make builds the tree under root and returns the entries made.
Make(a: &Arena, io: Io, root: str): fs.Error|OutOfMemory!int {
    var n := 0
    for i in 0..10 {
        for j in 0..10 {
            d := try a.Fmt("{root}/d{i}/s{j}", (root, i, j))
            try fs.MakeDirAll(io, d)
            for k in 0..10 {
                try fs.WriteAll(io, try a.Fmt("{d}/f{k}", (d, k)), "x".AsBytes())
            }
            n += 11
        }
        n += 1
    }
    return n
}

// Tally is what the walks saw: directories, files and name bytes.
struct Tally {
    dirs: int
    files: int
    names: int
}

// Count lists the tree under root once and adds its directories, files
// and name bytes to t, the root left out.
Count(h: &Heap, a: &Arena, io: Io, root: str, t: mut Tally): fs.Error|OutOfMemory!void {
    var w := try fs.Walk(h, io, root)
    defer w.Close()
    for e := try w.Next(a, io) {
        if e.depth == 0 {
            continue
        }
        if e.kind == fs.Kind.Dir {
            t.dirs += 1
        } else {
            t.files += 1
        }
        t.names += e.path.Base().AsBytes().Len()
    }
}

export Main(sys: &System): fs.Error|OutOfMemory|ParseError|WriteError!void {
    io := sys.io
    mode := sys.args[1]
    tmp := try fs.TempDir(sys.arena, io)
    root := try sys.arena.Fmt("{tmp}/mkbench-fs2-mk", (tmp))
    if mode == "copy" {
        n := Cast[int](try sys.args[3].ParseInt())
        dst := try sys.arena.Fmt("{root}-copy", (root))
        var bytes: i64 = 0
        for _ in 0..n {
            bytes += try fs.Copy(io, sys.args[2], dst)
        }
        try fs.Remove(io, dst)
        try sys.Print("{n} {bytes}\n", (n, bytes))
        return
    }
    n := Cast[int](try sys.args[2].ParseInt())
    var scratch := try Arena.New(sys.heap)
    defer scratch.Free()
    // A run killed earlier may have left the tree.
    try fs.RemoveAll(io, root)
    if mode == "walk" {
        _ = try Make(&scratch, io, root)
        scratch.Reset()
        var t := Tally(dirs: 0, files: 0, names: 0)
        for _ in 0..n {
            try Count(sys.heap, &scratch, io, root, mut t)
            scratch.Reset()
        }
        try fs.RemoveAll(io, root)
        try sys.Print("{n} {dirs} {files} {names}\n", (n, dirs: t.dirs, files: t.files, names: t.names))
        return
    }
    var made := 0
    var fails := 0
    for _ in 0..n {
        made += try Make(&scratch, io, root)
        scratch.Reset()
        try fs.RemoveAll(io, root)
        _ = fs.StatLink(io, root) else {
            continue
        }
        fails += 1
    }
    try sys.Print("{n} {made} {fails}\n", (n, made, fails))
}
