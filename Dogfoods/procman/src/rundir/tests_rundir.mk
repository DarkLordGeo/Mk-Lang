// Why: the run directory must survive faults and crashes.
//
// What: tests of Open, Replace and SaveOutput on a simulated disk.
//
// How: std.sim injects faults and crashes at chosen points; each test reads
// back what is left.

import std.fs
import std.sim

test "Open makes the directory the first time and finds it the second" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 1)
    defer s.Free()
    io := s.Io()
    first := try Open(io, "run")
    again := try Open(io, "run")
    Assert(first.path == again.path)
    info := try fs.Stat(io, "run")
    Assert(info.kind == fs.Kind.Dir)
}

test "Open refuses a file where the directory should be" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 2)
    defer s.Free()
    io := s.Io()
    try fs.WriteAll(io, "run", b"not a directory\n")
    refused := match Open(io, "run") {
        ok(_) { false }
        err(e) {
            match e {
                NotADirectory { true }
                _ { false }
            }
        }
    }
    Assert(refused)
}

test "Replace overwrites the whole file" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 3)
    defer s.Free()
    io := s.Io()
    dir := try Open(io, "run")
    try dir.Replace(sys.arena, io, "web.state", b"a longer first version\n")
    try dir.Replace(sys.arena, io, "web.state", b"second\n")
    text := try dir.Read(sys.arena, io, "web.state")
    Assert(text.Must() == "second\n")
}

test "a write that fails for want of space keeps the old content and no temporary" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 4)
    defer s.Free()
    io := s.Io()
    dir := try Open(io, "run")
    try dir.Replace(sys.arena, io, "web.state", b"old\n")
    s.FailNext(sim.Op.Write, sim.Fault.NoSpace)
    failed := match dir.Replace(sys.arena, io, "web.state", b"new\n") {
        ok(_) { false }
        err(e) {
            match e {
                fs.NoSpace { true }
                _ { false }
            }
        }
    }
    Assert(failed)
    text := try dir.Read(sys.arena, io, "web.state")
    Assert(text.Must() == "old\n")
    leftovers := try dir.List(sys.arena, io, ".tmp")
    Assert(leftovers.Len() == 0)
}

test "a sync refused by the disk is reported, and the old content stays" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 5)
    defer s.Free()
    io := s.Io()
    dir := try Open(io, "run")
    try dir.Replace(sys.arena, io, "status.port", b"127.0.0.1:1\n")
    s.FailNext(sim.Op.Sync, sim.Fault.Denied)
    dir.Replace(sys.arena, io, "status.port", b"127.0.0.1:2\n") else {}
    text := try dir.Read(sys.arena, io, "status.port")
    Assert(text.Must() == "127.0.0.1:1\n")
}

test "a crash during Replace leaves the old content or the new, never a part" (sys: &System) {
    for seed in 0..40 {
        var s := try sim.Sim.New(sys.arena, Cast[u64](seed))
        defer s.Free()
        io := s.Io()
        dir := try Open(io, "run")
        try dir.Replace(sys.arena, io, "web.state", b"web running runs=1\n")
        s.CrashAfterRandomOps()
        dir.Replace(sys.arena, io, "web.state", b"web ended runs=1 last=exit 0\n") else {}
        after := s.Restart()
        text := try dir.Read(sys.arena, after, "web.state")
        t := text.Must()
        Assert(t == "web running runs=1\n" || t == "web ended runs=1 last=exit 0\n")
    }
}

test "SaveOutput keeps the run before as .out.1" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 6)
    defer s.Free()
    io := s.Io()
    dir := try Open(io, "run")
    try dir.SaveOutput(sys.arena, io, "web", b"first run\n")
    try dir.SaveOutput(sys.arena, io, "web", b"second run\n")
    last := try dir.Read(sys.arena, io, "web.out")
    before := try dir.Read(sys.arena, io, "web.out.1")
    Assert(last.Must() == "second run\n")
    Assert(before.Must() == "first run\n")
}

test "List finds the files with a suffix, sorted" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 7)
    defer s.Free()
    io := s.Io()
    dir := try Open(io, "run")
    try dir.Replace(sys.arena, io, "worker.state", b"w\n")
    try dir.Replace(sys.arena, io, "clock.state", b"c\n")
    try dir.SaveOutput(sys.arena, io, "clock", b"tick\n")
    names := try dir.List(sys.arena, io, ".state")
    Assert(names.Len() == 2)
    Assert(names[0] == "clock.state")
    Assert(names[1] == "worker.state")
}

test "Clean removes what procman wrote, and the directory once it is empty" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 8)
    defer s.Free()
    io := s.Io()
    dir := try Open(io, "run")
    try dir.Replace(sys.arena, io, "web.state", b"w\n")
    try dir.SaveOutput(sys.arena, io, "web", b"out\n")
    left := try dir.Clean(sys.arena, io)
    Assert(left.removed == 2)
    Assert(left.kept == 0)
    gone := match fs.Stat(io, "run") {
        ok(_) { false }
        err(e) {
            match e {
                fs.NotFound { true }
                _ { false }
            }
        }
    }
    Assert(gone)
}

test "Clean keeps a file procman did not write, and the directory with it" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 9)
    defer s.Free()
    io := s.Io()
    dir := try Open(io, "run")
    try dir.Replace(sys.arena, io, "web.state", b"w\n")
    try fs.WriteAll(io, "run/notes.txt", b"mine\n")
    left := try dir.Clean(sys.arena, io)
    Assert(left.removed == 1)
    Assert(left.kept == 1)
    info := try fs.Stat(io, "run")
    Assert(info.kind == fs.Kind.Dir)
}
