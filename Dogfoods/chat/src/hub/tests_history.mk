// Why: The history writer must survive disk faults without losing recent
// messages.
//
// What: Tests of History.Flush and HistoryWriter on a simulated disk.
//
// How: Runs under std.sim with injected write and sync faults, then checks the
// file and the ring.

import std.fs
import std.sim

/// Records `n` messages in `h` from one nick in `#sim`.
RecordSome(h: &History, io: Io, n: int): Canceled!void {
    for i in 0..n {
        text := if i % 2 == 0 { "hello" } else { "héllo again" }
        try h.Record(io, "#sim", "ann", text)
    }
}

/// Closes the log when there is one; a failed close abandons it.
Shut(io: Io, log: ?fs.File) {
    f := log else {
        return
    }
    f.Close(io) else {
        f.Abandon()
    }
}

test "History.Flush writes every pending line to a simulated disk" (sys: &System) {
    for seed in 0..64 {
        var s := try sim.Sim.New(sys.arena, Cast[u64](seed))
        defer s.Free()
        io := s.Io()
        h := History.New()
        var log: ?fs.File = some(try fs.Create(io, "history.log"))
        onerr Shut(io, log)
        try RecordSome(&h, io, 3)
        n := try h.Flush(io, mut log)
        Assert(n == 3)
        again := h.Flush(io, mut log) else { -1 }
        Assert(again == 0)
        Shut(io, log)
        text := try fs.ReadText(sys.arena, io, "history.log")
        Assert(text == "#sim <ann> hello\n#sim <ann> héllo again\n#sim <ann> hello\n")
    }
}

test "a disk fault on write or sync keeps the messages in recent and reports the fault" (sys: &System) {
    ops := [sim.Op.Write, sim.Op.Sync]
    for seed in 0..64 {
        var s := try sim.Sim.New(sys.arena, Cast[u64](seed))
        defer s.Free()
        io := s.Io()
        h := History.New()
        var log: ?fs.File = some(try fs.Create(io, "history.log"))
        onerr Shut(io, log)
        s.FailNext(ops[seed % 2], sim.Fault.NoSpace)
        try RecordSome(&h, io, 2)
        n := h.Flush(io, mut log) else e {
            match e {
                Canceled {
                    Panic("a disk fault is not a cancellation")
                }
                _ {
                    -1
                }
            }
        }
        Assert(n == -1)
        var g := try h.recent.Lock(io)
        Assert(g.Ref().Len() == 2)
        g.Unlock()
        Shut(io, log)
    }
}

test "the history writer reports a disk fault and keeps recent current" (sys: &System) {
    for seed in 0..32 {
        var s := try sim.Sim.New(sys.arena, Cast[u64](seed))
        defer s.Free()
        io := s.Io()
        h := History.New()
        var log: ?fs.File = some(try fs.Create(io, "history.log"))
        onerr Shut(io, log)
        var failed: ?LogError = none
        var written := 0
        s.FailNext(sim.Op.Write, sim.Fault.NoSpace)
        scope w := io.Scope() {
            w.go HistoryWriter(io, &h, mut log, mut failed, mut written)
            try RecordSome(&h, io, 4)
            w.Cancel()
        }
        e := failed else {
            Panic("the fault was not reported")
        }
        match e {
            Canceled {
                Panic("a disk fault is not a cancellation")
            }
            _ {}
        }
        Assert(written == 0)
        var g := try h.recent.Lock(io)
        Assert(g.Ref().Len() == 4)
        g.Unlock()
        Shut(io, log)
    }
}
