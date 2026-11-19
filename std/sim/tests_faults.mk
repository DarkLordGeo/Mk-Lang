// Why: a Sim is only useful for crash testing if its injected faults and
// its durability rules are exact: a fault changes nothing, and a restart
// keeps the synced bytes and nothing else.
// What: blocks on FailNext for every Op and Fault, sparse writes up to the
// largest file (EFBIG past it), synced bytes across restarts against a
// model, and crash points late in a run; helpers such as WriteAllErr.
// How: each block makes its own Sim, asserts on error names, and frees it.

import std.fs
import std.net
import std.time

// The error name of a failed call, "" when it succeeded (a
// block asserts on the name, so a call that should fail and does not is
// caught).
WriteAllErr(io: Io, p: str, data: str): str from p, data {
    fs.WriteAll(io, p, data.AsBytes()) else e {
        return SfName(e)
    }
    return ""
}

ReadErr(a: &Arena, io: Io, p: str): str from a, p {
    _ = fs.ReadText(a, io, p) else e {
        return SfName(e)
    }
    return ""
}

RemoveErr(io: Io, p: str): str {
    fs.Remove(io, p) else e {
        return SfName(e)
    }
    return ""
}

PutErr(io: Io, key: str, value: str): str from key, value {
    Put(io, key, value) else e {
        return SfName(e)
    }
    return ""
}

// FsWant and NetWant are the names fs and net give a fault's SysErr.
FsWant(f: Fault): str {
    return match f {
        .NoSpace { "NoSpace" }
        .Denied { "Denied" }
        .Reset { "Os" }
        .Refused { "Os" }
    }
}

NetWant(f: Fault): str {
    return match f {
        .NoSpace { "other" }
        .Denied { "other" }
        .Reset { "Reset" }
        .Refused { "Refused" }
    }
}

// FaultAt is the k-th Fault.
FaultAt(k: int): Fault {
    if k == 0 {
        return Fault.NoSpace
    }
    if k == 1 {
        return Fault.Denied
    }
    if k == 2 {
        return Fault.Reset
    }
    return Fault.Refused
}

// FileFault checks one file Op with fault f: the armed call fails with f's
// name and changes nothing, and the same call then succeeds.
FileFault(a: &Arena, s: mut Sim, op: Op, f: Fault): fs.Error|OutOfMemory!void {
    io := s.Io()
    try Put(io, "t", "ab")
    match op {
        .Open {
            s.FailNext(op, f)
            Assert(ReadErr(a, io, "t") == FsWant(f))
            Assert(try fs.ReadText(a, io, "t") == "ab")
        }
        .Read {
            s.FailNext(op, f)
            Assert(ReadErr(a, io, "t") == FsWant(f))
            Assert(try fs.ReadText(a, io, "t") == "ab")
        }
        .Write {
            s.FailNext(op, f)
            Assert(WriteAllErr(io, "t", "xy") == FsWant(f))
            // WriteAll's create truncated "t" before its write failed: the
            // failed write itself stored nothing.
            Assert(try fs.ReadText(a, io, "t") == "")
            try fs.WriteAll(io, "t", "xy".AsBytes())
            Assert(try fs.ReadText(a, io, "t") == "xy")
        }
        .Sync {
            s.FailNext(op, f)
            Assert(PutErr(io, "t", "cd") == FsWant(f))
            // The failed sync made nothing durable: after a restart the file
            // holds the bytes synced before (a truncation's data change is
            // durable at a sync, as a write's).
            io2 := s.Restart()
            Assert(try fs.ReadText(a, io2, "t") == "ab")
            try Put(io2, "t", "cd")
            Assert(try fs.ReadText(a, io2, "t") == "cd")
        }
        _ {
        }
    }
}

// NetFault checks Accept (with a dial queued) and Connect (with a listener
// bound) with fault f, as FileFault does.
NetFault(s: mut Sim, op: Op, f: Fault): net.Error!void {
    io := s.Io()
    l := try net.Listen(io, "127.0.0.1:7400")
    defer l.Close()
    match op {
        .Accept {
            c := try net.Dial(io, "127.0.0.1:7400")
            defer c.Close()
            s.FailNext(op, f)
            srv := l.Accept(io) else e {
                Assert(SnName(e) == NetWant(f))
                // The queued connection is still there.
                again := try l.Accept(io)
                again.Close()
                return
            }
            srv.Close()
            Assert(false)
        }
        .Connect {
            s.FailNext(op, f)
            c := net.Dial(io, "127.0.0.1:7400") else e {
                Assert(SnName(e) == NetWant(f))
                c2 := try net.Dial(io, "127.0.0.1:7400")
                c2.Close()
                srv := try l.Accept(io)
                srv.Close()
                return
            }
            c.Close()
            Assert(false)
        }
        _ {
        }
    }
}

test "SIM FailNext fails each Op with each Fault once, changing nothing" (sys: &System) {
    ops: [4]Op = [Op.Open, Op.Read, Op.Write, Op.Sync]
    nops: [2]Op = [Op.Accept, Op.Connect]
    for k in 0..4 {
        for i in 0..4 {
            var s := try Sim.New(sys.arena, Cast[u64](100 + k * 4 + i))
            FileFault(sys.arena, mut s, ops[i], FaultAt(k)) else {
                s.Free()
                Assert(false)
                return
            }
            s.Free()
        }
        for i in 0..2 {
            var s := try Sim.New(sys.arena, Cast[u64](200 + k * 2 + i))
            NetFault(mut s, nops[i], FaultAt(k)) else {
                s.Free()
                Assert(false)
                return
            }
            s.Free()
        }
    }
}

// SparseFar writes one byte at 1 TiB and reads it back: a write far into
// a file is one sparse page, and the largest file is a bound, not an
// overflow.
SparseFar(io: Io, f: &fs.File): bool {
    far: i64 = 1 << 40
    n := f.WriteAt(io, far, "x".AsBytes()) else {
        return false
    }
    var b: [2]u8 = [0; 2]
    m := f.ReadAt(io, far, mut b[..]) else {
        return false
    }
    if n != 1 || m != 1 || b[0] != b'x' {
        return false
    }
    k := f.ReadAt(io, far - 1, mut b[..]) else {
        return false
    }
    return k == 2 && b[0] == 0 && b[1] == b'x'
}

// SparseEdge is the error name of an 8-byte write at pos, or its count.
SparseEdge(io: Io, f: &fs.File, pos: i64): str {
    var eight: [8]u8 = [1; 8]
    n := f.WriteAt(io, pos, eight[..]) else e {
        return SfName(e)
    }
    if n == 2 {
        return "2"
    }
    return "another count"
}

test "SIM a write at 1 TiB is sparse, and one past the largest file is EFBIG" (sys: &System) {
    var s := try Sim.New(sys.arena, 31)
    defer s.Free()
    io := s.Io()
    t0 := time.Now(sys.io)
    var f := try fs.Create(io, "big")
    ok := SparseFar(io, &f)
    near := SparseEdge(io, &f, MaxFileSize - 2)
    past := SparseEdge(io, &f, 0x7FFF_FFFF_FFFF_FFFC)
    f.Sync(io) else {
    }
    f.Close(io) else {
        f.Abandon()
    }
    ms := time.Now(sys.io).Since(t0).Nanos() / 1_000_000
    Assert(ok)
    // Across the bound: a short write up to it; past it: EFBIG (Os 27).
    Assert(near == "2")
    Assert(past == "Os")
    info := try fs.Stat(io, "big")
    Assert(info.size == MaxFileSize)
    Assert(ms < 50)
}

// EmptyAt is the count of a write of no bytes at pos, or the error's name.
EmptyAt(io: Io, f: &fs.File, pos: i64): str {
    n := f.WriteAt(io, pos, "".AsBytes()) else e {
        return SfName(e)
    }
    return if n == 0 { "0" } else { "another count" }
}

// EmptyKeeps: after the writes of no bytes, f still holds "ab", reads it
// back and syncs.
EmptyKeeps(io: Io, f: mut fs.File): bool {
    var b: [4]u8 = [0; 4]
    n := f.ReadAt(io, 0, mut b[..]) else {
        return false
    }
    f.Sync(io) else {
        return false
    }
    return n == 2 && b[0] == b'a' && b[1] == b'b'
}

test "SIM a write of no bytes at, past and far past the largest file is 0, and the file still reads and syncs" (sys: &System) {
    var s := try Sim.New(sys.arena, 32)
    defer s.Free()
    io := s.Io()
    var f := try fs.Create(io, "e")
    wrote := f.WriteAt(io, 0, "ab".AsBytes()) else {
        0
    }
    at := EmptyAt(io, &f, MaxFileSize)
    past := EmptyAt(io, &f, MaxFileSize + 1)
    far := EmptyAt(io, &f, 0x7FFF_FFFF_FFFF_FFFF)
    kept := EmptyKeeps(io, mut f)
    f.Close(io) else {
        f.Abandon()
    }
    Assert(wrote == 2)
    Assert(at == "0" && past == "0" && far == "0")
    Assert(kept)
    info := try fs.Stat(io, "e")
    Assert(info.size == 2)
}

// The file model: a small file's live and durable bytes against random
// writes, syncs, truncations and restarts, so the dirty-page sync and
// revert keep exactly what was synced. ModelNext is its xorshift step.
ModelNext(x: mut u64): u64 {
    *x = *x ^ (*x << 13)
    *x = *x ^ (*x >> 7)
    *x = *x ^ (*x << 17)
    return *x
}

// FileModel is the model's state: the live and the durable bytes of the
// file, their sizes, and the generator.
struct FileModel {
    live: [20000]u8
    dur: [20000]u8
    lsize: int
    dsize: int
    x: u64
}

// ModelCheck reads the file whole and compares it with the model's live bytes.
ModelCheck(io: Io, m: &FileModel): bool {
    var got: [20000]u8 = [0; 20000]
    var f := fs.Open(io, "m") else {
        return false
    }
    n := f.ReadAt(io, 0, mut got[..]) else {
        f.Abandon()
        return false
    }
    f.Abandon()
    if n != m.lsize {
        return false
    }
    for k in 0..n {
        if got[k] != m.live[k] {
            return false
        }
    }
    return true
}

// ModelStep is one random write, sync or truncation through g, applied to
// the model too; false when the file and the model part.
ModelStep(io: Io, g: &fs.File, m: mut FileModel, step: int): bool {
    r := ModelNext(mut m.x) % 16
    if r < 10 {
        at := Cast[int](ModelNext(mut m.x) % 16000)
        n := 1 + Cast[int](ModelNext(mut m.x) % 4000)
        var data: [4000]u8 = [0; 4000]
        for k in 0..n {
            data[k] = Trunc[u8](step + k + 1)
        }
        c := g.WriteAt(io, i64(at), data[0..n]) else {
            return false
        }
        if c != n {
            return false
        }
        for k in 0..n {
            m.live[at + k] = data[k]
        }
        if at + n > m.lsize {
            m.lsize = at + n
        }
    } else if r < 14 {
        // A sync through a second handle (fs.File.Sync takes mut self).
        var t := fs.Open(io, "m") else {
            return false
        }
        t.Sync(io) else {
            t.Abandon()
            return false
        }
        t.Abandon()
        for k in 0..m.lsize {
            m.dur[k] = m.live[k]
        }
        m.dsize = m.lsize
    } else {
        // A second Create truncates the file under g.
        t := fs.Create(io, "m") else {
            return false
        }
        t.Abandon()
        for k in 0..m.lsize {
            m.live[k] = 0
        }
        m.lsize = 0
    }
    return ModelCheck(io, &*m)
}

// ModelLife is one run: Create (a truncation), the durable bytes written back
// and synced, then steps until the generator ends the run.
ModelLife(io: Io, m: mut FileModel, steps: mut int): bool {
    var g := fs.Create(io, "m") else {
        return false
    }
    if m.dsize > 0 {
        _ = g.WriteAt(io, 0, m.dur[0..m.dsize]) else {
            g.Abandon()
            return false
        }
    }
    g.Sync(io) else {
        g.Abandon()
        return false
    }
    // The model's live bytes past its size stay zero: a hole reads so.
    for k in 0..20000 {
        if k < m.dsize {
            m.live[k] = m.dur[k]
        } else {
            m.live[k] = 0
        }
    }
    m.lsize = m.dsize
    for *steps < 400 {
        *steps += 1
        if ModelNext(mut m.x) % 8 == 0 {
            break
        }
        if !ModelStep(io, &g, m, *steps) {
            g.Abandon()
            return false
        }
    }
    g.Abandon()
    return true
}

test "SIM syncs and restarts keep exactly the synced bytes, against a model" (sys: &System) {
    var s := try Sim.New(sys.arena, 32)
    defer s.Free()
    var io := s.Io()
    var m := FileModel(live: [0; 20000], dur: [0; 20000], lsize: 0, dsize: 0, x: 0x9E3779B97F4A7C15)
    var steps := 0
    var lives := 0
    for steps < 400 {
        Assert(ModelLife(io, mut m, mut steps))
        lives += 1
        io = s.Restart()
        // After the power cut the file is its durable bytes.
        for k in 0..20000 {
            if k < m.dsize {
                m.live[k] = m.dur[k]
            } else {
                m.live[k] = 0
            }
        }
        m.lsize = m.dsize
        Assert(ModelCheck(io, &m))
    }
    Assert(lives > 5)
}

// CrashLife puts 300 records, syncing the first sync_from: a crash can
// land after the first 128 operations, so a store whose syncs stop after
// its 200th put loses a returned put for some seed.
CrashLife(io: Io, sync_from: int, acked: mut int): fs.Error!void {
    var f := try fs.Create(io, "log")
    onerr f.Abandon()
    try f.Sync(io)
    for i in 0..300 {
        var rec: [8]u8 = [0; 8]
        for k in 0..8 {
            rec[k] = Trunc[u8](i + 1)
        }
        _ = try f.WriteAt(io, i64(i * 8), rec[..])
        if i < sync_from {
            try f.Sync(io)
        }
        *acked = i + 1
    }
    try f.Close(io)
}

// CrashLost: some put that returned is missing after the crash and restart.
CrashLost(a: &Arena, seed: u64, sync_from: int): bool {
    var s := Sim.New(a, seed) else {
        return false
    }
    s.CrashAfterRandomOps()
    var acked := 0
    CrashLife(s.Io(), sync_from, mut acked) else {
    }
    io := s.Restart()
    if acked == 0 {
        s.Free()
        return false
    }
    var buf: [2400]u8 = [0; 2400]
    var f := fs.Open(io, "log") else {
        s.Free()
        return true
    }
    n := f.ReadAt(io, 0, mut buf[..]) else {
        f.Abandon()
        s.Free()
        return true
    }
    f.Abandon()
    s.Free()
    return n < acked * 8
}

test "SIM a crash point can fall after the first 128 operations" (sys: &System) {
    var found := 0
    var sound := 0
    for seed in 0..500 {
        if CrashLost(sys.arena, Cast[u64](seed), 200) {
            found += 1
        }
        if CrashLost(sys.arena, Cast[u64](seed), 300) {
            sound += 1
        }
    }
    Assert(found > 0)
    Assert(sound == 0)
}
