// Why: fs.Copy's failure leaves a partial dst, and the simulated form of
// that case can only be tested here: std.fs's own tests cannot import
// std.sim (E0205).
// What: one block: a Copy whose first write fails with NoSpace leaves dst
// empty, for a new dst and an existing one.
// How: a Sim has no copy call, so fs.Copy runs its read and write loop: dst
// is created or truncated first and nothing removes it, so it holds the
// prefix written, here none. The native forms are std.fs's block on a
// failed copy in std/fs/tests_pathcopy.mk.

import std.fs

test "under std.sim a Copy whose first write fails with NoSpace leaves dst empty, a new dst and an existing one alike" (sys: &System) {
    var s := Sim.New(sys.arena, 1).Must()
    io := s.Io()
    fs.WriteAll(io, "src.txt", b"0123456789").Must()
    fs.WriteAll(io, "old.txt", b"OLD-CONTENT!").Must()
    for dst in ["dst.txt", "old.txt"] {
        s.FailNext(Op.Write, Fault.NoSpace)
        var failed := "ok"
        _ = fs.Copy(io, "src.txt", dst) else e {
            failed = match e {
                fs.NoSpace { "NoSpace" }
                _ { "other" }
            }
        }
        Assert(failed == "NoSpace")
        Assert(fs.Stat(io, dst).Must().size == 0)
    }
    // The fault was taken once: the next copy fills dst.
    Assert(fs.Copy(io, "src.txt", "old.txt").Must() == 10)
    Assert(fs.ReadText(sys.arena, io, "old.txt").Must() == "0123456789")
    s.Free()
}
