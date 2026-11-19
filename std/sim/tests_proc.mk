// Why: std.proc's waits must be testable under a Sim: a deadline
// interrupts a child's wait, a pipe's read and write and a Pty's read, and
// the same call can be made again.
// What: the SD8-Sim-* blocks on simulated pipes, the cat child, cut waits,
// Ptys and detached children, and a block showing a pipe's or child's
// stream takes no FailNext fault (it is no file or socket); helper PrReq.
// How: the child is cat (proc.mk's model). Kill and Detach go through the
// native Io, as Child.Kill and Child.Detach do, and reach the Sim through
// rt.ForeignOps.

import std.core
import std.rt
import std.time

// PrReq is a spawn request with the three streams as given and no Pty.
PrReq(i: u32, o: u32, e: u32): core.IoSpawnReq {
    return core.IoSpawnReq(stdio: [i, o, e], src: [0; 3], env_mode: core.IoEnvInherit, group: false, pty: 0,
        pipes: [0; 3], pid: 0, job: 0)
}

// PrArgs is the argument list of a simulated spawn; only "missing"
// matters.
const PrArgs: [1]str = ["cat"]
const PrMissing: [1]str = ["missing"]

// PrSpawn spawns cat on io with s; the child's handle, 0 on failure.
PrSpawn(io: Io, s: mut core.IoSpawnReq): uint {
    r := rt.Spawn(io, PrArgs[..], PrArgs[0..0], "", s)
    return if r.err == core.SysErr.None { Trunc[uint](r.value) } else { 0 }
}

// PrReadN reads exactly n bytes of h into buf from the start, or fewer
// when the stream ends or a read fails; the count.
PrReadN(io: Io, h: uint, buf: mut []u8, n: int): int {
    var got := 0
    for got < n {
        r := core.IoRead(io, h, mut buf[got..n], -1)
        if r.err != core.SysErr.None || r.value == 0 {
            break
        }
        got += Trunc[int](r.value)
    }
    return got
}

// PrKill kills the child h as std.proc's Child.Kill does: through the
// native Io, masked.
PrKill(h: uint, pid: i64): core.IoResult {
    return rt.ChildSignal(core.IoMasked(rt.NativeIo()), h, false, pid, 0, 9)
}

// PrKillCode is ChildWait's value for a killed child on this target.
PrKillCode(): i64 {
    return if Target.os == core.Os.Windows { 1 } else { -9 }
}

test "SD8-Sim-Pipe a simulated pipe carries bytes, ends after its writer closes, holds 64 KiB, and a write after its reader closed is Closed" (sys: &System) {
    var s := Sim.New(sys.arena, 7) else {
        Assert(false)
        return
    }
    io := s.Io()
    var ends: [2]uint = [0; 2]
    Assert(rt.PipeOpen(io, mut ends).err == core.SysErr.None)
    Assert(rt.IsForeign(ends[0]) && rt.IsForeign(ends[1]))
    w := core.IoWrite(io, ends[1], "hello".AsBytes(), -1)
    Assert(w.err == core.SysErr.None && w.value == 5)
    var buf: [8]u8 = [0; 8]
    Assert(PrReadN(io, ends[0], mut buf[..], 5) == 5 && buf[0] == b'h' && buf[4] == b'o')
    rt.Abandon(ends[1])
    r := core.IoRead(io, ends[0], mut buf[..], -1)
    Assert(r.err == core.SysErr.None && r.value == 0)
    rt.Abandon(ends[0])
    // A pipe holds at most 64 KiB unread: a larger write is short, and a
    // write to a full pipe with nothing in the run to drain it is a
    // deadlock, Canceled with nothing written.
    Assert(rt.PipeOpen(io, mut ends).err == core.SysErr.None)
    big := sys.arena.Slice[u8](100_000) else {
        s.Free()
        Assert(false)
        return
    }
    w2 := core.IoWrite(io, ends[1], big[..], -1)
    Assert(w2.err == core.SysErr.None && w2.value == 65_536)
    w3 := core.IoWrite(io, ends[1], big[..], -1)
    Assert(w3.err == core.SysErr.Canceled && w3.value == 0)
    rt.Abandon(ends[0])
    w4 := core.IoWrite(io, ends[1], "x".AsBytes(), -1)
    Assert(w4.err == core.SysErr.Closed)
    rt.Abandon(ends[1])
    s.Free()
}

test "SD8-Sim-Cat a simulated child copies stdin to stdout and exits 0 when stdin closes; missing is NotFound" (sys: &System) {
    var s := Sim.New(sys.arena, 11) else {
        Assert(false)
        return
    }
    io := s.Io()
    var q := PrReq(core.IoStdPipe, core.IoStdPipe, core.IoStdPipe)
    ch := PrSpawn(io, mut q)
    Assert(ch != 0 && q.pid >= 100 && q.pipes[0] != 0 && q.pipes[1] != 0 && q.pipes[2] != 0)
    w := core.IoWrite(io, q.pipes[0], "hello".AsBytes(), -1)
    Assert(w.err == core.SysErr.None && w.value == 5)
    var buf: [8]u8 = [0; 8]
    Assert(PrReadN(io, q.pipes[1], mut buf[..], 5) == 5 && buf[0] == b'h' && buf[4] == b'o')
    rt.Abandon(q.pipes[0])
    Assert(core.IoRead(io, q.pipes[1], mut buf[..], -1).value == 0)
    Assert(core.IoRead(io, q.pipes[2], mut buf[..], -1).value == 0)
    r := rt.ChildWait(io, ch, 0)
    Assert(r.err == core.SysErr.None && r.value == 0)
    rt.Abandon(ch)
    rt.Abandon(q.pipes[1])
    rt.Abandon(q.pipes[2])
    // stdin not a pipe: cat reads an empty input and ends at once.
    var q2 := PrReq(core.IoStdNull, core.IoStdPipe, core.IoStdJoin)
    ch2 := PrSpawn(io, mut q2)
    Assert(ch2 != 0 && q2.pipes[0] == 0 && q2.pipes[2] == 0)
    Assert(core.IoRead(io, q2.pipes[1], mut buf[..], -1).value == 0)
    r2 := rt.ChildWait(io, ch2, 0)
    Assert(r2.err == core.SysErr.None && r2.value == 0)
    rt.Abandon(ch2)
    rt.Abandon(q2.pipes[1])
    var q3 := PrReq(core.IoStdPipe, core.IoStdPipe, core.IoStdPipe)
    m := rt.Spawn(io, PrMissing[..], PrArgs[0..0], "", mut q3)
    Assert(m.err == core.SysErr.NotFound && q3.pipes[0] == 0)
    s.Free()
}

// PrLate sleeps ms milliseconds of the run's clock, then writes data to h.
PrLate(io: Io, h: uint, ms: i64, data: str) {
    io.NoCancel().Sleep(time.Millis(ms)) else {}
    _ = core.IoWrite(io.NoCancel(), h, data.AsBytes(), -1)
}

// PrKiller sleeps ms milliseconds of the run's clock, then kills the
// child h through the native Io.
PrKiller(io: Io, h: uint, pid: i64, ms: i64) {
    io.NoCancel().Sleep(time.Millis(ms)) else {}
    _ = PrKill(h, pid)
}

// PrCut is one Sim of seed: a child's wait and a stdout read, each cut by
// a 1 ms deadline while a task of the run is due later, then made again;
// a write to a full stdin cut the same way. The clocks of the cuts mixed,
// or 0 on a failure.
PrCut(a: &Arena, seed: u64): i64 {
    var s := Sim.New(a, seed) else {
        return 0
    }
    io := s.Io()
    var ok := true
    var mix: i64 = 0
    var q := PrReq(core.IoStdPipe, core.IoStdPipe, core.IoStdNull)
    ch := PrSpawn(io, mut q)
    if ch == 0 {
        s.Free()
        return 0
    }
    // The read: Canceled at the deadline, the late bytes kept for the next.
    t0 := time.Now(io)
    var buf: [8]u8 = [0; 8]
    scope o := io.Scope() {
        o.go PrLate(io, q.pipes[0], 5, "late")
        scope d := io.Scope() {
            d.CancelAfter(time.Millis(1))
            r := core.IoRead(io, q.pipes[1], mut buf[..], -1)
            ok = ok && r.err == core.SysErr.Canceled
            mix = time.Now(io).Since(t0).Nanos()
            ok = ok && mix >= 1_000_000 && mix < 5_000_000
        }
        ok = ok && PrReadN(io, q.pipes[1], mut buf[..], 4) == 4 && buf[0] == b'l' && buf[3] == b'e'
    }
    // The write: stdin and stdout are one 64 KiB stream; with stdout
    // unread the second write waits, and the deadline ends it with nothing
    // written.
    big := a.Slice[u8](65_536) else {
        s.Free()
        return 0
    }
    ok = ok && core.IoWrite(io, q.pipes[0], big[..], -1).value == 65_536
    t1 := time.Now(io)
    scope o := io.Scope() {
        o.go PrLate(io, q.pipes[0], 5, "")
        scope d := io.Scope() {
            d.CancelAfter(time.Millis(1))
            r := core.IoWrite(io, q.pipes[0], big[..], -1)
            ok = ok && r.err == core.SysErr.Canceled && r.value == 0
            cut := time.Now(io).Since(t1).Nanos()
            ok = ok && cut >= 1_000_000 && cut < 5_000_000
            mix = mix *% 31 +% cut
        }
    }
    var drain: [4096]u8 = [0; 4096]
    var got := 0
    for got < 65_536 {
        n := PrReadN(io, q.pipes[1], mut drain[..], 4096)
        if n == 0 {
            break
        }
        got += n
    }
    ok = ok && got == 65_536
    // The wait: Canceled at the deadline with the child running; the same
    // wait again answers the kill a task sends later.
    t2 := time.Now(io)
    scope o := io.Scope() {
        o.go PrKiller(io, ch, q.pid, 5)
        scope d := io.Scope() {
            d.CancelAfter(time.Millis(1))
            r := rt.ChildWait(io, ch, 0)
            ok = ok && r.err == core.SysErr.Canceled
            cut := time.Now(io).Since(t2).Nanos()
            ok = ok && cut >= 1_000_000 && cut < 5_000_000
            mix = mix *% 31 +% cut
        }
        r := rt.ChildWait(io, ch, 0)
        ok = ok && r.err == core.SysErr.None && r.value == PrKillCode()
    }
    // The child is gone: its stdout ends, a write to its stdin is Closed.
    ok = ok && core.IoRead(io, q.pipes[1], mut buf[..], -1).value == 0
    ok = ok && core.IoWrite(io, q.pipes[0], "x".AsBytes(), -1).err == core.SysErr.Closed
    ok = ok && PrKill(ch, q.pid).err == core.SysErr.None
    rt.Abandon(ch)
    rt.Abandon(q.pipes[0])
    rt.Abandon(q.pipes[1])
    s.Free()
    return if ok { mix } else { 0 }
}

test "SD8-Sim-Cut a child's wait, a stdout read and a full stdin write cut by a deadline under a Sim, then made again; the same on replay" (sys: &System) {
    for seed in 0..8 {
        m := PrCut(sys.arena, Cast[u64](seed))
        Assert(m != 0)
        Assert(PrCut(sys.arena, Cast[u64](seed)) == m)
    }
}

test "SD8-Sim-Pty a simulated Pty echoes what is written, its read is cut by a deadline, ends after its last child ends and reads again for the next" (sys: &System) {
    var s := Sim.New(sys.arena, 5) else {
        Assert(false)
        return
    }
    io := s.Io()
    var p := core.IoPty(read: 0, write: 0, term: 0)
    Assert(rt.PtyOpen(io, 0, 24, mut p).err == core.SysErr.Other)
    Assert(rt.PtyOpen(io, 80, 24, mut p).err == core.SysErr.None)
    Assert(p.term != 0 && rt.IsForeign(p.read) && rt.IsForeign(p.write))
    Assert(rt.PtyResize(io, &p, 100, 30).err == core.SysErr.None)
    Assert(rt.PtyResize(io, &p, 100, 40_000).err == core.SysErr.Other)
    var q := PrReq(core.IoStdInherit, core.IoStdInherit, core.IoStdInherit)
    q.pty = p.term
    ch := PrSpawn(io, mut q)
    Assert(ch != 0 && q.pipes[0] == 0)
    Assert(core.IoWrite(io, p.write, "abc".AsBytes(), -1).value == 3)
    var buf: [8]u8 = [0; 8]
    Assert(PrReadN(io, p.read, mut buf[..], 3) == 3 && buf[0] == b'a' && buf[2] == b'c')
    var cut := false
    scope o := io.Scope() {
        o.go PrKiller(io, ch, q.pid, 5)
        scope d := io.Scope() {
            d.CancelAfter(time.Millis(1))
            r := rt.PtyRead(io, &p, mut buf[..])
            cut = r.err == core.SysErr.Canceled
        }
        // Read again: the end, once the killer ended the only child.
        r := rt.PtyRead(io, &p, mut buf[..])
        Assert(r.err == core.SysErr.None && r.value == 0)
    }
    Assert(cut)
    w := rt.ChildWait(io, ch, 0)
    Assert(w.err == core.SysErr.None && w.value == PrKillCode())
    rt.Abandon(ch)
    // A second child: the Pty reads again; closing the Pty ends its input.
    var q2 := PrReq(core.IoStdInherit, core.IoStdInherit, core.IoStdInherit)
    q2.pty = p.term
    ch2 := PrSpawn(io, mut q2)
    Assert(ch2 != 0)
    Assert(core.IoWrite(io, p.write, "z".AsBytes(), -1).value == 1)
    Assert(PrReadN(io, p.read, mut buf[..], 1) == 1 && buf[0] == b'z')
    Assert(rt.PtyClose(core.IoMasked(rt.NativeIo()), p).err == core.SysErr.None)
    w2 := rt.ChildWait(io, ch2, 0)
    Assert(w2.err == core.SysErr.None && w2.value == 0)
    rt.Abandon(ch2)
    s.Free()
}

test "SD8-Sim-Detach a detached simulated child runs on: its stream still copies, and it ends when its stdin closes" (sys: &System) {
    var s := Sim.New(sys.arena, 3) else {
        Assert(false)
        return
    }
    io := s.Io()
    var q := PrReq(core.IoStdPipe, core.IoStdPipe, core.IoStdNull)
    ch := PrSpawn(io, mut q)
    Assert(ch != 0)
    Assert(rt.ChildDetach(core.IoMasked(rt.NativeIo()), ch, 0).err == core.SysErr.None)
    Assert(core.IoWrite(io, q.pipes[0], "go".AsBytes(), -1).value == 2)
    var buf: [4]u8 = [0; 4]
    Assert(PrReadN(io, q.pipes[1], mut buf[..], 2) == 2 && buf[1] == b'o')
    rt.Abandon(q.pipes[0])
    Assert(core.IoRead(io, q.pipes[1], mut buf[..], -1).value == 0)
    rt.Abandon(q.pipes[1])
    s.Free()
}

test "under a Sim a pipe's and a child's stream take no FailNext fault: an armed Reset or Denied waits for the next file operation and the pipe stays whole" (sys: &System) {
    var s := Sim.New(sys.arena, 7) else {
        Assert(false)
        return
    }
    io := s.Io()
    var ends: [2]uint = [0; 2]
    Assert(rt.PipeOpen(io, mut ends).err == core.SysErr.None)
    s.FailNext(Op.Write, Fault.Reset)
    s.FailNext(Op.Read, Fault.Denied)
    w := core.IoWrite(io, ends[1], "hello".AsBytes(), -1)
    Assert(w.err == core.SysErr.None && w.value == 5)
    var buf: [8]u8 = [0; 8]
    Assert(PrReadN(io, ends[0], mut buf[..], 5) == 5 && buf[0] == b'h' && buf[4] == b'o')
    // Not reset: the pipe carries the next bytes both ways.
    Assert(core.IoWrite(io, ends[1], "again".AsBytes(), -1).value == 5)
    Assert(PrReadN(io, ends[0], mut buf[..], 5) == 5 && buf[0] == b'a' && buf[4] == b'n')
    rt.Abandon(ends[1])
    rt.Abandon(ends[0])
    // A child's stdin write and stdout read leave both faults armed too.
    var q := PrReq(core.IoStdPipe, core.IoStdPipe, core.IoStdNull)
    ch := PrSpawn(io, mut q)
    Assert(ch != 0)
    Assert(core.IoWrite(io, q.pipes[0], "hi".AsBytes(), -1).value == 2)
    Assert(PrReadN(io, q.pipes[1], mut buf[..], 2) == 2 && buf[0] == b'h' && buf[1] == b'i')
    rt.Abandon(q.pipes[0])
    Assert(core.IoRead(io, q.pipes[1], mut buf[..], -1).value == 0)
    Assert(rt.ChildWait(io, ch, 0).value == 0)
    rt.Abandon(ch)
    rt.Abandon(q.pipes[1])
    // The next file write takes the Reset, the next file read the Denied.
    Assert(WriteAllErr(io, "f", "x") != "")
    Assert(WriteAllErr(io, "f", "x") == "")
    Assert(ReadErr(sys.arena, io, "f") == "Denied")
    Assert(ReadErr(sys.arena, io, "f") == "")
    s.Free()
}
