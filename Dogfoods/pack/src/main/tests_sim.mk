// Why: pack promises that a crash or an I/O fault never leaves a damaged
// archive behind.
//
// What: crash and fault tests of `pack create`, and the Plant, Leftovers and
// Exists helpers.
//
// How: runs pack on std.sim's simulated disk with faults injected by seed,
// then checks for leftover temporary files and that any archive verifies.

import std.fs
import std.mem
import std.sim

const Tree: []str = ["tree"]
const Archive: []str = ["out.pk"]
const Small: []str = ["tree/docs/b.txt"]

/// A tree of two files and a directory on the simulated disk behind `io`.
Plant(io: Io): fs.Error!void {
    try fs.MakeDir(io, "tree")
    try fs.MakeDir(io, "tree/docs")
    try fs.WriteAll(io, "tree/a.txt", b"alpha\n")
    try fs.WriteAll(io, "tree/docs/b.txt", b"bravo bravo bravo bravo\n")
}

PackOptions(): CreateOptions {
    return CreateOptions(out: "out.pk", sign: none, to: none, cipher: none, gzip: false, mem_limit: none, stats: false, dirs: Tree)
}

/// How many temporary files pack left in the working directory.
Leftovers(a: &Arena, io: Io): int {
    var n := 0
    var dir := fs.Dir.Open(io, ".") else {
        return 0
    }
    defer dir.Close()
    for {
        next := dir.Next(a, io) else {
            break
        }
        entry := next else {
            break
        }
        name := entry.name.Str() else { "" }
        if name.StartsWith(TempPrefix) {
            n += 1
        }
    }
    return n
}

/// Whether `path` exists on the disk behind `io`.
Exists(io: Io, path: str): bool {
    _ = fs.Stat(io, path) else {
        return false
    }
    return true
}

test "a crash while packing leaves no archive, or one that verifies" (sys: &System) {
    var kept := 0
    var lost := 0
    for seed in 0..64 {
        var arena := try Arena.New(sys.heap)
        defer arena.Free()
        var s := try sim.Sim.New(&arena, Cast[u64](seed))
        defer s.Free()
        try Plant(s.Io())
        s.CrashAfterRandomOps()
        counting := mem.Counting.New(sys.heap.Alloc())
        c := Ctx(sys: sys, arena: &arena, io: s.Io(), alloc: counting.Alloc(), limit: 0)
        Create(&c, &PackOptions()) else {}
        io := s.Restart()
        if !Exists(io, "out.pk") {
            lost += 1
        } else {
            kept += 1
            after := Ctx(sys: sys, arena: &arena, io: io, alloc: counting.Alloc(), limit: 0)
            v := VerifyOptions(key: none, signer: none, mem_limit: none, stats: false, archives: Archive)
            var whole := true
            Verify(&after, &v) else {
                whole = false
            }
            Assert(whole)
        }
        Assert(counting.Bytes() == 0)
    }
    // the crash point falls both before and after the rename in 64 seeds
    Assert(kept > 0 && lost > 0)
}

test "a write, sync or open that fails leaves no archive and no temporary file" (sys: &System) {
    ops := [sim.Op.Write, sim.Op.Sync, sim.Op.Open]
    for op in ops {
        var arena := try Arena.New(sys.heap)
        defer arena.Free()
        var s := try sim.Sim.New(&arena, 7)
        defer s.Free()
        try Plant(s.Io())
        s.FailNext(op, sim.Fault.NoSpace)
        counting := mem.Counting.New(sys.heap.Alloc())
        c := Ctx(sys: sys, arena: &arena, io: s.Io(), alloc: counting.Alloc(), limit: 0)
        var failed := false
        Create(&c, &PackOptions()) else {
            failed = true
        }
        Assert(failed)
        Assert(!Exists(s.Io(), "out.pk"))
        Assert(Leftovers(&arena, s.Io()) == 0)
    }
}

test "a denied open of the tree is reported and packs nothing" (sys: &System) {
    var arena := try Arena.New(sys.heap)
    defer arena.Free()
    var s := try sim.Sim.New(&arena, 11)
    defer s.Free()
    try Plant(s.Io())
    s.FailNext(sim.Op.Open, sim.Fault.Denied)
    counting := mem.Counting.New(sys.heap.Alloc())
    c := Ctx(sys: sys, arena: &arena, io: s.Io(), alloc: counting.Alloc(), limit: 0)
    var code := 0
    Create(&c, &PackOptions()) else e {
        code = match e {
            Failed(code: status) { status }
            _ { -1 }
        }
    }
    Assert(code == ExitFiles)
    Assert(!Exists(s.Io(), "out.pk"))
}

test "a write that fails while zlib streams abandons the encoder and leaves no file" (sys: &System) {
    var arena := try Arena.New(sys.heap)
    defer arena.Free()
    var s := try sim.Sim.New(&arena, 13)
    defer s.Free()
    try Plant(s.Io())
    s.FailNext(sim.Op.Write, sim.Fault.NoSpace)
    c := Ctx(sys: sys, arena: &arena, io: s.Io(), alloc: mem.Null(), limit: 0)
    o := ZlibOptions(out: "out.zz", level: some(6), sync_every: some(1), inputs: Small)
    var code := 0
    Zlib(&c, &o) else e {
        code = match e {
            Failed(code: status) { status }
            _ { -1 }
        }
    }
    Assert(code == ExitFiles)
    Assert(!Exists(s.Io(), "out.zz"))
    Assert(Leftovers(&arena, s.Io()) == 0)
}
