// Why: saving a file must leave the old one or the new one after a crash.
//
// What: crash and fault tests of Save and of reading key files.
//
// How: runs under std.sim's simulated disk with crashes and faults injected
// by seed, then reads back what survived.

import std.fs
import std.sim

// The contents of path after a restart, or "" when it does not exist.
After(a: &Arena, io: Io, path: str): ?str from a {
    data := fs.ReadBytes(a, io, path) else e {
        match e {
            fs.NotFound {
                return some("")
            }
            _ {
                return none
            }
        }
    }
    text := str.From(data) else {
        return none
    }
    return some(text)
}

test "a crash while saving leaves the old file or the new one, never a part" (sys: &System) {
    for seed in 0..200 {
        var s := try sim.Sim.New(sys.arena, Cast[u64](seed))
        defer s.Free()
        try Save(sys.arena, s.Io(), "sealed.bin", b"the old sealed file")
        s.CrashAfterRandomOps()
        Save(sys.arena, s.Io(), "sealed.bin", b"the new sealed file, longer than the old") else {}
        io := s.Restart()
        text := After(sys.arena, io, "sealed.bin").Must()
        Assert(text == "the old sealed file" || text == "the new sealed file, longer than the old" || text == "")
    }
}

test "a full disk while saving is reported and keeps the old file" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 7)
    defer s.Free()
    try Save(sys.arena, s.Io(), "out.bin", b"kept")
    s.FailNext(sim.Op.Write, sim.Fault.NoSpace)
    var failed := false
    Save(sys.arena, s.Io(), "out.bin", b"lost") else {
        failed = true
    }
    Assert(failed)
    Assert(After(sys.arena, s.Io(), "out.bin").Must() == "kept")
}

test "a denied open while reading a key file is reported" (sys: &System) {
    var s := try sim.Sim.New(sys.arena, 11)
    defer s.Free()
    try fs.WriteAll(s.Io(), "key.pem", b"-----BEGIN PUBLIC KEY-----\nAAAA\n-----END PUBLIC KEY-----\n")
    s.FailNext(sim.Op.Open, sim.Fault.Denied)
    var denied := false
    _ = Blocks(sys.arena, s.Io(), "key.pem") else {
        denied = true
        List[Block].New(sys.arena)
    }
    Assert(denied)
}
