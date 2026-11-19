// Why: a seed must give one run whatever the target, so a std.fs call has
// to make the same Sim operations everywhere; these blocks pin defects
// found where it did not. std.fs's own tests cannot import std.sim, which
// imports std.fs.
// What: blocks on File.TryLock and File.Lock's operation count, FailNext
// and the simulated terminal, and Zone.Load and Zone.Local reading no file.
// How: each block counts Sim operations or arms a fault and checks where
// it lands. File.Lock and TryLock once made one more operation (a release
// before the lock) on Windows only, which moved the Sim's crash points.

import std.core
import std.fs
import std.rt
import std.time
import std.time.tzdata

// DfCount makes call (0 Unlock, 1 TryLock, 2 Lock) on a file of a Sim of
// seed after arming its crash point, until the run ends or cap calls
// succeed: how many succeeded. Each call that counts one Sim operation
// gives the same count as Unlock, which counts one on every target.
DfCount(a: &Arena, seed: u64, call: int, cap: int): int {
    var s := Sim.New(a, seed).Must()
    io := s.Io()
    var f := fs.Create(io, "lock.bin").Must()
    s.CrashAfterRandomOps()
    var n := 0
    for n < cap {
        if call == 0 {
            f.Unlock(io) else {
                break
            }
        } else if call == 1 {
            _ = f.TryLock(io, fs.LockMode.Exclusive) else {
                break
            }
        } else {
            f.Lock(io, fs.LockMode.Exclusive) else {
                break
            }
        }
        n += 1
    }
    f.Abandon()
    s.Free()
    return n
}

test "under std.sim File.TryLock and File.Lock count one Sim operation each, as Unlock does, on every target, so a seed's crash point falls at the same call" (sys: &System) {
    var checked := 0
    for seed in 0..12 {
        u := DfCount(sys.arena, Cast[u64](seed), 0, 2000)
        if u >= 2000 {
            // This seed's crash point is past the cap: not compared.
            continue
        }
        Assert(DfCount(sys.arena, Cast[u64](seed), 1, 2000) == u)
        Assert(DfCount(sys.arena, Cast[u64](seed), 2, 2000) == u)
        checked += 1
    }
    Assert(checked >= 3)
}

// FailNext(Op.Write) is a file's or a socket's write, so a write
// to the simulated terminal (discarded) leaves it for the next file
// write.
test "under std.sim a write to the simulated terminal does not take FailNext(Op.Write); the next file write does" (sys: &System) {
    var s := Sim.New(sys.arena, 1).Must()
    io := s.Io()
    var t: [2]uint = [0; 2]
    Assert(rt.TermOpen(io, mut t).err == core.SysErr.None)
    s.FailNext(Op.Write, Fault.NoSpace)
    w := core.IoWrite(io, t[1], "clear".AsBytes(), -1)
    Assert(w.err == core.SysErr.None && w.value == 5)
    var failed := ""
    fs.WriteAll(io, "f", "x".AsBytes()) else e {
        failed = match e {
            fs.NoSpace { "NoSpace" }
            _ { "other" }
        }
    }
    Assert(failed == "NoSpace")
    // The fault was taken once: the next write succeeds.
    fs.WriteAll(io, "f", "x".AsBytes()).Must()
    _ = core.IoClose(io, t[0])
    _ = core.IoClose(io, t[1])
    s.Free()
}

// The same class for std.time: a simulated Io reads no zone file of the
// host on any target, so Zone.Load and Zone.Local make no Sim operation and
// move no clock, and an armed FailNext(Op.Open) waits for a file's open.

// DfZoneErr is the error name of Zone.Load of Europe/Berlin with no
// embedded database, "" when it succeeded.
DfZoneErr(a: &Arena, io: Io): str {
    _ = time.Zone.Load(a, io, "Europe/Berlin", none) else e {
        return match e {
            time.NoDatabase { "NoDatabase" }
            _ { "other" }
        }
    }
    return ""
}

test "under std.sim Zone.Load and Zone.Local read no file on any target: no Sim operation, the clock unmoved, and FailNext(Op.Open) left for the next file open" (sys: &System) {
    var s := Sim.New(sys.arena, 7).Must()
    io := s.Io()
    s.FailNext(Op.Open, Fault.Denied)
    t0 := time.Now(io)
    z := time.Zone.Load(sys.arena, io, "Europe/Berlin", some(tzdata.Database)).Must()
    local := time.Zone.Local(sys.arena, io).Must()
    Assert(DfZoneErr(sys.arena, io) == "NoDatabase")
    // Only the second Now moved the clock (1 us each).
    Assert(time.Now(io).Since(t0).Nanos() == 1_000)
    Assert(time.FromUnix(1_700_000_000, 0).In(&z).offset_seconds == 3600)
    Assert(time.FromUnix(1_700_000_000, 0).In(&local).offset_seconds == 0)
    Assert(WriteAllErr(io, "f", "x") == "Denied")
    s.Free()
}
