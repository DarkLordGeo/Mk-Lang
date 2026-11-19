// Why: with main alone in a run there is no choice to fire a deadline, so
// main's own waits and clock moves must fire the deadlines they pass, and
// a huge sleep must not overflow the simulated clock.
// What: blocks on a datagram receive of main alone cut by its scope's
// deadline (nothing sent, a datagram in flight), a sleep of the largest
// Duration on the three paths of SimSleep, and main's clock moves (a
// yield, a now, a wall read, a dial's round trip, Sim.Advance) firing the
// deadlines they pass. Helper SdLoop.
// How: each block reads the simulated clock and the scope's Expired after
// the cut; the largest sleep's wake saturates as the native timer's does.

import std.core
import std.raw
import std.rt
import std.time

// SdLoop is 127.0.0.1:port.
SdLoop(port: i64): core.IoAddr {
    var ip: [16]u8 = [0; 16]
    ip[0] = 127
    ip[3] = 1
    return core.IoAddr(ip: ip, port: Trunc[u16](port), v6: false, zone: 0)
}

test "main alone in a Sim: a datagram receive with nothing sent ends Canceled at its scope's 20 ms deadline, and the scope is Expired" (sys: &System) {
    var w := Sim.New(sys.arena, 5) else {
        Assert(false)
        return
    }
    io := w.Io()
    u := core.IoUdp(io, SdLoop(7310))
    Assert(u.err == core.SysErr.None)
    start := time.Now(io)
    var expired := false
    var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    var b: [8]u8 = [0; 8]
    var from := SdLoop(0)
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(20))
        r = core.IoRecvFrom(io, Trunc[uint](u.value), mut b[..], mut from)
        expired = s.Expired()
    }
    took := time.Now(io).Since(start).Nanos()
    rt.Abandon(Trunc[uint](u.value))
    w.Free()
    Assert(r.err == core.SysErr.Canceled && expired && took >= 20_000_000 && took < 20_010_000)
}

test "main alone in a Sim: a datagram receive whose datagram arrives after its scope's 5 us deadline ends Canceled at the deadline, the scope Expired; the next receive takes the datagram" (sys: &System) {
    var w := Sim.New(sys.arena, 3) else {
        Assert(false)
        return
    }
    io := w.Io()
    u := core.IoUdp(io, SdLoop(7311))
    Assert(u.err == core.SysErr.None)
    v := core.IoUdp(io, SdLoop(7312))
    Assert(v.err == core.SysErr.None)
    // The datagram arrives 20 to 500 us after the send (net.mk's latency).
    Assert(core.IoSendTo(io, Trunc[uint](v.value), "hi".AsBytes(), SdLoop(7311)).value == 2)
    start := time.Now(io)
    var expired := false
    var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    var b: [8]u8 = [0; 8]
    var from := SdLoop(0)
    scope s := io.Scope() {
        s.CancelAfter(time.Micros(5))
        r = core.IoRecvFrom(io, Trunc[uint](u.value), mut b[..], mut from)
        expired = s.Expired()
    }
    took := time.Now(io).Since(start).Nanos()
    Assert(r.err == core.SysErr.Canceled && expired && took < 20_000)
    r2 := core.IoRecvFrom(io, Trunc[uint](u.value), mut b[..], mut from)
    Assert(r2.err == core.SysErr.None && r2.value == 2 && b[0] == b'h' && b[1] == b'i' && from.port == 7312)
    rt.Abandon(Trunc[uint](v.value))
    rt.Abandon(Trunc[uint](u.value))
    w.Free()
}

// SdHugeSleep sleeps the largest Duration in a scope with a 1 ms deadline:
// 1 when the sleep ended Canceled at the deadline with the scope Expired,
// else 0.
SdHugeSleep(io: Io): u32 {
    start := time.Now(io)
    var slept := true
    var expired := false
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(1))
        io.Sleep(time.Nanos(0x7FFF_FFFF_FFFF_FFFF)) else {
            slept = false
        }
        expired = s.Expired()
    }
    took := time.Now(io).Since(start).Nanos()
    if !slept && expired && took >= 1_000_000 && took < 1_500_000 {
        return 1
    }
    return 0
}

// SdTicks sleeps 1 ms n times.
SdTicks(io: Io, n: int) {
    for _ in 0..n {
        io.Sleep(time.Millis(1)) else {
            return
        }
    }
}

// SdHugeTask runs SdHugeSleep on a task of the Sim and stores its answer.
SdHugeTask(io: Io, out: &raw.Atomic[u32]) {
    out.Store(SdHugeSleep(io))
}

test "under a Sim a sleep of the largest Duration does not overflow the clock: main alone, main with a task, and a task each end Canceled at their scope's 1 ms deadline" (sys: &System) {
    // Main alone in the run (SimSleep's last branch).
    var w := Sim.New(sys.arena, 7) else {
        Assert(false)
        return
    }
    io := w.Io()
    Assert(SdHugeSleep(io) == 1)
    w.Free()
    // Main while a task of the run sleeps (MainWait's branch).
    var w2 := Sim.New(sys.arena, 7) else {
        Assert(false)
        return
    }
    io2 := w2.Io()
    var got: u32 = 0
    scope top := io2.Scope() {
        top.go SdTicks(io2, 5)
        got = SdHugeSleep(io2)
    }
    w2.Free()
    Assert(got == 1)
    // A task of the run, which holds the baton (BlockAs's branch).
    var w3 := Sim.New(sys.arena, 7) else {
        Assert(false)
        return
    }
    io3 := w3.Io()
    out := raw.Atomic[u32].New(0)
    scope top := io3.Scope() {
        top.go SdHugeTask(io3, &out)
    }
    w3.Free()
    Assert(out.Load() == 1)
}

// SdYields yields on io until a yield fails, at most cap times: how many
// succeeded.
SdYields(io: Io, cap: int): int {
    var n := 0
    for n < cap {
        io.Yield() else {
            return n
        }
        n += 1
    }
    return n
}

test "main alone in a Sim: a yield loop, loops that read the clock or the wall clock, a dial's round trip and Sim.Advance each fire the scope deadline they pass, so the scope is Expired at its deadline" (sys: &System) {
    var w := Sim.New(sys.arena, 7) else {
        Assert(false)
        return
    }
    io := w.Io()
    // Each yield costs 1 us of the clock; the one that reaches the 1 ms
    // deadline answers Canceled.
    var n := 0
    var expired := false
    start := time.Now(io)
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(1))
        n = SdYields(io, 100_000)
        expired = s.Expired()
    }
    took := time.Now(io).Since(start).Nanos()
    Assert(n < 1_000 && expired && took >= 1_000_000 && took <= 1_002_000)
    // A loop that reads the clock until the scope is Expired.
    var polls := 0
    t1 := time.Now(io)
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(1))
        for !s.Expired() && polls < 100_000 {
            _ = time.Now(io)
            polls += 1
        }
    }
    Assert(polls <= 1_000 && time.Now(io).Since(t1).Nanos() <= 1_002_000)
    // The same with the wall clock.
    polls = 0
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(1))
        for !s.Expired() && polls < 100_000 {
            _ = time.Wall(io)
            polls += 1
        }
    }
    Assert(polls <= 1_000)
    // A dial's round trip (40 us at least) passes a 10 us deadline: the
    // dial returns its connection and the scope is Expired.
    l := core.IoListen(io, "127.0.0.1:7330")
    Assert(l.err == core.SysErr.None)
    var d := core.IoResult(value: 0, err: core.SysErr.Canceled, os: 0)
    expired = false
    scope s := io.Scope() {
        s.CancelAfter(time.Micros(10))
        d = core.IoDial(io, "127.0.0.1:7330")
        expired = s.Expired()
    }
    Assert(d.err == core.SysErr.None && expired)
    rt.Abandon(Trunc[uint](d.value))
    rt.Abandon(Trunc[uint](l.value))
    // Sim.Advance past the deadline.
    expired = false
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(1))
        w.Advance(time.Millis(2))
        expired = s.Expired()
    }
    Assert(expired)
    w.Free()
}
