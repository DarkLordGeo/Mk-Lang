// Why: under a Sim a deadline fires only at a choice, so an accept or a
// receive may be readied by an event the clock passed after the deadline;
// it must still answer as natively, by the instant of the cut.
// What: the SIM-Accept-* and SIM-Recv-* blocks: a task's accept and stream
// receive end Canceled with the scope Expired when the connection or bytes
// came after the deadline that cut the task's own scope chain, and take
// what came before it; main's accept is cut by its deadline or an
// ancestor's. Helpers AcUs and AcAccept.
// How: seeded runs (8 seeds, often in two modes) with a listener and a
// dialler timed around the deadline. Run by mk test --std=std.sim.

import std.core
import std.net
import std.raw
import std.time

// AcUs is the simulated microseconds since t0.
AcUs(io: Io, t0: time.Instant): u64 {
    return Cast[u64](time.Now(io).Since(t0).Nanos() / 1000)
}

// AcAccept is one Listener.Accept in a scope with a dl_us deadline (none
// for 0): 1 accepted, 2 Canceled with the scope Expired, 3 Canceled with
// it not Expired, 4 another error.
AcAccept(io: Io, l: &net.Listener, dl_us: i64): u64 {
    var code: u64 = 0
    scope d := io.Scope() {
        if dl_us > 0 {
            d.CancelAfter(time.Micros(dl_us))
        }
        c := l.Accept(io) else e {
            canceled := match e {
                Canceled { true }
                _ { false }
            }
            code = if !canceled { 4 } else if d.Expired() { 2 } else { 3 }
            return code
        }
        c.Close()
        code = 1
    }
    return code
}

// AcSpin reads the clock until it is past us since t0: main moves the
// clock with no choice.
AcSpin(io: Io, t0: time.Instant, us: u64) {
    for AcUs(io, t0) <= us {
    }
}

// AcTask accepts once under a 1,000 us deadline, then once with none; the
// codes go to r1 and r2.
AcTask(io: Io, l: &net.Listener, r1: &raw.Atomic[u64], r2: &raw.Atomic[u64]) {
    r1.Store(AcAccept(io, l, 1000))
    r2.Store(AcAccept(io, l, 0))
}

// AcLate runs one Sim of seed: a task accepts on l under a 1,000 us
// deadline while main sleeps 900 us and then, in mode 0, dials l2 (a
// network event that readies the task; its round trip moves the clock
// with no choice) and at once l; in mode 1 reads the clock past 1,100 us
// (no event, no choice) and dials l. True when the task's accept held:
// Canceled with its scope Expired whenever l's connection was queued after
// the deadline, and the next accept took it.
AcLate(sys: &System, seed: u64, mode: int): bool {
    var s := Sim.New(sys.arena, seed) else {
        return false
    }
    defer s.Free()
    io := s.Io()
    l := net.Listen(io, "127.0.0.1:7200") else {
        return false
    }
    defer l.Close()
    l2 := net.Listen(io, "127.0.0.1:7201") else {
        return false
    }
    defer l2.Close()
    t0 := time.Now(io)
    r1 := raw.Atomic[u64].New(0)
    r2 := raw.Atomic[u64].New(0)
    var dialled: u64 = 0
    var ok := true
    scope t := io.Scope() {
        t.go AcTask(io, &l, &r1, &r2)
        io.Sleep(time.Micros(900)) else {}
        if mode == 1 {
            AcSpin(io, t0, 1100)
        } else {
            c2 := net.Dial(io, "127.0.0.1:7201") else {
                ok = false
                return false
            }
            c2.Close()
        }
        dialled = AcUs(io, t0)
        c := net.Dial(io, "127.0.0.1:7200") else {
            ok = false
            return false
        }
        c.Close()
    }
    if !ok || r2.Load() == 4 {
        return false
    }
    if dialled <= 1000 {
        return r1.Load() == 1 || r2.Load() == 1
    }
    return r1.Load() == 2 && r2.Load() == 1
}

test "SIM-Accept-Late a task's accept is Canceled with its scope Expired when the connection is queued after its deadline, also when an event readied it before the choice that fired the deadline, on 8 seeds in 2 modes" (sys: &System) {
    for mode in 0..2 {
        for seed in 0..8 {
            Assert(AcLate(sys, Cast[u64](seed), mode))
        }
    }
}

// AcReader reads c once under a 1,000 us deadline: 1 read bytes, 2
// Canceled with the scope Expired, 3 Canceled only, 4 another error.
AcReader(io: Io, c: &net.Conn, res: &raw.Atomic[u64]) {
    scope d := io.Scope() {
        d.CancelAfter(time.Micros(1000))
        var b: [8]u8 = [0; 8]
        k := c.Read(io, mut b[..]) else e {
            canceled := match e {
                Canceled { true }
                _ { false }
            }
            res.Store(if !canceled { 4 } else if d.Expired() { 2 } else { 3 })
            return
        }
        res.Store(if k > 0 { 1 } else { 4 })
    }
}

// AcRecvLate runs one Sim of seed: a task reads a stream under a 1,000 us
// deadline; main sleeps sleep_us, then sends once the clock is past
// sent_us, reading the clock until then and past 2,000 us, after the
// bytes' arrival (at most 500 us after the send), with no choice between.
// The read's code (AcReader), 0 when the run failed.
AcRecvLate(sys: &System, seed: u64, sleep_us: i64, sent_us: u64): u64 {
    var s := Sim.New(sys.arena, seed) else {
        return 0
    }
    defer s.Free()
    io := s.Io()
    l := net.Listen(io, "127.0.0.1:7200") else {
        return 0
    }
    defer l.Close()
    c := net.Dial(io, "127.0.0.1:7200") else {
        return 0
    }
    defer c.Close()
    srv := l.Accept(io) else {
        return 0
    }
    sc := srv.Conn()
    defer sc.Abandon()
    t0 := time.Now(io)
    res := raw.Atomic[u64].New(0)
    var ok := true
    scope t := io.Scope() {
        t.go AcReader(io, &sc, &res)
        io.Sleep(time.Micros(sleep_us)) else {}
        AcSpin(io, t0, sent_us)
        c.WriteAll(io, "hi".AsBytes()) else {
            ok = false
        }
        AcSpin(io, t0, 2000)
    }
    return if ok { res.Load() } else { 0 }
}

test "SIM-Recv-Late a task's receive is Canceled with its scope Expired when the bytes are sent after its deadline, on 8 seeds" (sys: &System) {
    for seed in 0..8 {
        Assert(AcRecvLate(sys, Cast[u64](seed), 900, 1100) == 2)
    }
}

// AcPoolTask accepts once on l under a dl_us deadline; the code in r.
AcPoolTask(io: Io, l: &net.Listener, dl_us: i64, r: &raw.Atomic[u64]) {
    r.Store(AcAccept(io, l, dl_us))
}

// AcPool runs one Sim of seed: two tasks accept on l, under 1,000 us
// deadlines (in mode 1 the second's is 1,050 us). Main sleeps 900 us and
// dials l (connection 1, before the deadlines), reads the clock past
// 1,100 us, dials again (connection 2, after them) and reads the clock
// past 2,000 us, with no choice between. Natively one task takes
// connection 1 and the other's deadline cuts its wait. True when exactly
// one accepted and the other was Canceled with its scope Expired.
AcPool(sys: &System, seed: u64, mode: int): bool {
    var s := Sim.New(sys.arena, seed) else {
        return false
    }
    defer s.Free()
    io := s.Io()
    l := net.Listen(io, "127.0.0.1:7200") else {
        return false
    }
    defer l.Close()
    t0 := time.Now(io)
    r1 := raw.Atomic[u64].New(0)
    r2 := raw.Atomic[u64].New(0)
    var dl2: i64 = 1000
    if mode == 1 {
        dl2 = 1050
    }
    var ok := true
    scope t := io.Scope() {
        t.go AcPoolTask(io, &l, 1000, &r1)
        t.go AcPoolTask(io, &l, dl2, &r2)
        io.Sleep(time.Micros(900)) else {}
        c1 := net.Dial(io, "127.0.0.1:7200") else {
            ok = false
            return false
        }
        c1.Close()
        AcSpin(io, t0, 1100)
        c2 := net.Dial(io, "127.0.0.1:7200") else {
            ok = false
            return false
        }
        c2.Close()
        AcSpin(io, t0, 2000)
    }
    return ok && r1.Load() + r2.Load() == 3 && (r1.Load() == 1 || r2.Load() == 1)
}

test "SIM-Accept-Pool two tasks accept on one listener under their deadlines: one takes the connection queued before them, the other is Canceled with its scope Expired and never takes the one queued after them, on 8 seeds in 2 modes" (sys: &System) {
    for mode in 0..2 {
        for seed in 0..8 {
            Assert(AcPool(sys, Cast[u64](seed), mode))
        }
    }
}

// AcSleeper sleeps 1 s under a 1,000 us deadline of its own scope.
AcSleeper(io: Io) {
    scope d := io.Scope() {
        d.CancelAfter(time.Micros(1000))
        io.Sleep(time.Micros(1_000_000)) else {}
    }
}

// AcNested accepts once on l in a scope with a 1,000 us deadline inside
// one with a 2,000 us deadline, armed outer first; the code in r.
AcNested(io: Io, l: &net.Listener, r: &raw.Atomic[u64]) {
    scope o := io.Scope() {
        o.CancelAfter(time.Micros(2000))
        r.Store(AcAccept(io, l, 1000))
    }
}

// AcOther runs one Sim of seed: main sleeps 900 us, reads the clock past
// 1,500 us, dials l (the connection is queued at about 1,501 us) and reads
// the clock past 2,500 us, with no choice between, so the choice after
// the dial fires every deadline below at once. The task accepting on l:
// mode 0 and 1, under a 2,000 us deadline, in mode 1 beside another task
// whose own scope's 1,000 us deadline fires at that choice too, so the
// accept takes the connection, which came before its own deadline; mode
// 2, in a scope whose 1,000 us deadline is inside the 2,000 us one
// (AcNested), so the inner one fires first, as natively: it cuts the
// accept, its scope reads Expired, and the connection, after it, is not
// taken. The task's code (AcAccept).
AcOther(sys: &System, seed: u64, mode: int): u64 {
    var s := Sim.New(sys.arena, seed) else {
        return 0
    }
    defer s.Free()
    io := s.Io()
    l := net.Listen(io, "127.0.0.1:7200") else {
        return 0
    }
    defer l.Close()
    t0 := time.Now(io)
    r := raw.Atomic[u64].New(0)
    var ok := true
    scope t := io.Scope() {
        if mode == 2 {
            t.go AcNested(io, &l, &r)
        } else {
            t.go AcPoolTask(io, &l, 2000, &r)
        }
        if mode == 1 {
            t.go AcSleeper(io)
        }
        io.Sleep(time.Micros(900)) else {}
        AcSpin(io, t0, 1500)
        c := net.Dial(io, "127.0.0.1:7200") else {
            ok = false
            return 0
        }
        c.Close()
        AcSpin(io, t0, 2500)
    }
    return if ok { r.Load() } else { 0 }
}

test "SIM-Accept-OwnDeadline a task's accept is weighed against the deadline that cut its own scope chain: another scope's earlier deadline fired at the same choice does not cancel it, and of two nested deadlines fired at one choice the earlier, inner one cuts it, its scope Expired, on 8 seeds" (sys: &System) {
    for seed in 0..8 {
        Assert(AcOther(sys, Cast[u64](seed), 0) == 1)
        Assert(AcOther(sys, Cast[u64](seed), 1) == 1)
        Assert(AcOther(sys, Cast[u64](seed), 2) == 2)
    }
}

// AcPlainTask accepts once on l with no deadline: 1 accepted, 3 Canceled,
// 4 another error.
AcPlainTask(io: Io, l: &net.Listener, r: &raw.Atomic[u64]) {
    r.Store(AcAccept(io, l, 0))
}

// AcCancel runs one Sim of seed: a task accepts on l with no deadline in
// scope u, and in mode 1 another task's own scope has a 1,000 us
// deadline. Main sleeps 900 us, reads the clock past 1,500 us, dials l,
// reads it past 2,600 us, cancels u, reads it past 2,800 us and joins,
// with no choice between the dial and the join. The connection came
// before the cancel, so the task takes it in both modes: an explicit
// Cancel is never weighed. The task's code.
AcCancel(sys: &System, seed: u64, mode: int): u64 {
    var s := Sim.New(sys.arena, seed) else {
        return 0
    }
    defer s.Free()
    io := s.Io()
    l := net.Listen(io, "127.0.0.1:7200") else {
        return 0
    }
    defer l.Close()
    t0 := time.Now(io)
    r := raw.Atomic[u64].New(0)
    var ok := true
    scope t := io.Scope() {
        if mode == 1 {
            t.go AcSleeper(io)
        }
        scope u := io.Scope() {
            u.go AcPlainTask(io, &l, &r)
            io.Sleep(time.Micros(900)) else {}
            AcSpin(io, t0, 1500)
            c := net.Dial(io, "127.0.0.1:7200") else {
                ok = false
                return 0
            }
            c.Close()
            AcSpin(io, t0, 2600)
            u.Cancel()
            AcSpin(io, t0, 2800)
        }
    }
    return if ok { r.Load() } else { 0 }
}

test "SIM-Accept-Cancel a task's accept readied by a connection before its scope's explicit Cancel takes it, with or without another scope's deadline fired at the same choice, on 8 seeds" (sys: &System) {
    for seed in 0..8 {
        Assert(AcCancel(sys, Cast[u64](seed), 0) == 1)
        Assert(AcCancel(sys, Cast[u64](seed), 1) == 1)
    }
}

// AcDialLater sleeps us microseconds, then dials port.
AcDialLater(io: Io, port: str, us: i64) {
    io.Sleep(time.Micros(us)) else {
        return
    }
    c := net.Dial(io, port) else {
        return
    }
    c.Close()
}

// AcMain runs main's own accepts in one Sim of seed and returns what each
// gave as code * 100_000 + the simulated microseconds it took:
// [0] main alone, a 1 ms deadline, nothing dialled;
// [1] main alone, the 1 ms deadline an ancestor's (its Expired in [2]);
// [3] main beside a task that dials at 5 ms, a 1 ms deadline, then [4]
//     an accept with none;
// [5] as [3] with the deadline an ancestor's (its Expired in [6]), then
//     [7] an accept with none.
AcMain(sys: &System, seed: u64): [8]u64 {
    var o: [8]u64 = [0; 8]
    var s := Sim.New(sys.arena, seed) else {
        return o
    }
    defer s.Free()
    io := s.Io()
    l := net.Listen(io, "127.0.0.1:7200") else {
        return o
    }
    defer l.Close()
    t0 := time.Now(io)
    var b := AcUs(io, t0)
    o[0] = AcAccept(io, &l, 1000) * 100_000 + AcUs(io, t0) - b
    b = AcUs(io, t0)
    scope a := io.Scope() {
        a.CancelAfter(time.Micros(1000))
        o[1] = AcAccept(io, &l, 0) * 100_000 + AcUs(io, t0) - b
        o[2] = if a.Expired() { 1 } else { 0 }
    }
    b = AcUs(io, t0)
    scope t := io.Scope() {
        t.go AcDialLater(io, "127.0.0.1:7200", 5000)
        o[3] = AcAccept(io, &l, 1000) * 100_000 + AcUs(io, t0) - b
        o[4] = AcAccept(io, &l, 0) * 100_000 + AcUs(io, t0) - b
    }
    b = AcUs(io, t0)
    scope t := io.Scope() {
        t.go AcDialLater(io, "127.0.0.1:7200", 5000)
        scope a := io.Scope() {
            a.CancelAfter(time.Micros(1000))
            o[5] = AcAccept(io, &l, 0) * 100_000 + AcUs(io, t0) - b
            o[6] = if a.Expired() { 1 } else { 0 }
        }
        o[7] = AcAccept(io, &l, 0) * 100_000 + AcUs(io, t0) - b
    }
    return o
}

test "SIM-Accept-MainDeadline main's own accept is cut by its deadline, or an ancestor's, at that deadline, alone and beside a task that dials later, which the next accept then takes, on 4 seeds" (sys: &System) {
    for seed in 0..4 {
        o := AcMain(sys, Cast[u64](seed))
        Assert(o[0] == 201_001)
        Assert(o[1] == 301_001 && o[2] == 1)
        Assert(o[3] == 201_001 && o[4] == 105_001)
        Assert(o[5] == 301_001 && o[6] == 1 && o[7] == 105_001)
    }
}

// AcEarly runs one Sim of seed: a task accepts on l under a 5,100 us
// deadline while main sleeps 5,000 us and dials l, so the connection is
// queued at about 5,001 us, before the deadline; main's dial round trip
// then moves the clock past the deadline with no choice between. True
// when the task accepted the connection, as natively.
AcEarly(sys: &System, seed: u64): bool {
    var s := Sim.New(sys.arena, seed) else {
        return false
    }
    defer s.Free()
    io := s.Io()
    l := net.Listen(io, "127.0.0.1:7200") else {
        return false
    }
    defer l.Close()
    r := raw.Atomic[u64].New(0)
    var ok := true
    scope t := io.Scope() {
        t.go AcPoolTask(io, &l, 5100, &r)
        io.Sleep(time.Micros(5000)) else {}
        c := net.Dial(io, "127.0.0.1:7200") else {
            ok = false
            return false
        }
        c.Close()
    }
    return ok && r.Load() == 1
}

test "SIM-Accept-Early a task's accept takes a connection queued before its deadline, also when main's dial then moved the clock past the deadline before a choice fired it, on 8 seeds" (sys: &System) {
    for seed in 0..8 {
        Assert(AcEarly(sys, Cast[u64](seed)))
    }
}

test "SIM-Recv-Early a task's receive takes bytes that arrived before its deadline, also when main's clock reads then moved the clock past the deadline before a choice fired it, on 8 seeds" (sys: &System) {
    for seed in 0..8 {
        Assert(AcRecvLate(sys, Cast[u64](seed), 100, 0) == 1)
    }
}

// AcCountReader reads a stream into an 8-byte buffer under a 1,000 us
// deadline: res is the byte count read, or 100 plus AcReader's code.
AcCountReader(io: Io, c: &net.Conn, res: &raw.Atomic[u64]) {
    scope d := io.Scope() {
        d.CancelAfter(time.Micros(1000))
        var b: [8]u8 = [0; 8]
        k := c.Read(io, mut b[..]) else e {
            canceled := match e {
                Canceled { true }
                _ { false }
            }
            res.Store(if !canceled { 104 } else if d.Expired() { 102 } else { 103 })
            return
        }
        res.Store(Cast[u64](k))
    }
}

// AcRecvSplit runs one Sim of seed: a task reads a stream under a 1,000 us
// deadline; main sleeps 100 us and sends "ab" (it arrives before the
// deadline and readies the task), reads the clock past 1,100 us, sends
// "cd" (after the deadline), reads the clock past 2,000 us, after both
// arrivals, with no choice between, and joins. Natively the read woke for
// "ab" and returns it alone. True when the task read 2 bytes and main's
// read after the join then takes "cd".
AcRecvSplit(sys: &System, seed: u64): bool {
    var s := Sim.New(sys.arena, seed) else {
        return false
    }
    defer s.Free()
    io := s.Io()
    l := net.Listen(io, "127.0.0.1:7200") else {
        return false
    }
    defer l.Close()
    c := net.Dial(io, "127.0.0.1:7200") else {
        return false
    }
    defer c.Close()
    srv := l.Accept(io) else {
        return false
    }
    sc := srv.Conn()
    defer sc.Abandon()
    t0 := time.Now(io)
    res := raw.Atomic[u64].New(0)
    var ok := true
    scope t := io.Scope() {
        t.go AcCountReader(io, &sc, &res)
        io.Sleep(time.Micros(100)) else {}
        c.WriteAll(io, "ab".AsBytes()) else {
            ok = false
        }
        AcSpin(io, t0, 1100)
        c.WriteAll(io, "cd".AsBytes()) else {
            ok = false
        }
        AcSpin(io, t0, 2000)
    }
    if !ok || res.Load() != 2 {
        return false
    }
    var b: [8]u8 = [0; 8]
    k := sc.Read(io, mut b[..]) else {
        return false
    }
    return k == 2 && b[0] == b'c' && b[1] == b'd'
}

test "SIM-Recv-ByCut a task's receive readied by bytes that arrived before its deadline takes only those, not the bytes sent after the deadline, which the next read takes, on 8 seeds" (sys: &System) {
    for seed in 0..8 {
        Assert(AcRecvSplit(sys, Cast[u64](seed)))
    }
}

// AcBorn dials a listener of a fresh Sim of seed, nothing accepting, and
// weighs the queued connection with ConnCut at its own queue instant and
// 1 ns before it: true when the first takes it (a connection queued at
// the cut came at or before the deadline) and the second does not.
AcBorn(sys: &System, seed: u64): bool {
    var s := Sim.New(sys.arena, seed) else {
        return false
    }
    defer s.Free()
    io := s.Io()
    l := core.IoListen(io, "127.0.0.1:7202")
    d := core.IoDial(io, "127.0.0.1:7202")
    if l.err != core.SysErr.None || d.err != core.SysErr.None {
        return false
    }
    lh := Trunc[uint](l.value)
    var e := Enter(io)
    if !e.ok {
        return false
    }
    w := WorldOf(&e)
    i := FindHand(&*w, lh, HListen)
    var ok := i >= 0
    if ok {
        p := w.ports.Get(w.handles.Get(i).obj)
        ok = p.backlog.Len() == 1
        if ok {
            born := w.ends.Get(p.backlog.Get(0)).born
            ok = !ConnCut(&*w, lh, born) && ConnCut(&*w, lh, born - 1)
        }
    }
    Leave(&e)
    return ok
}

test "SIM-Accept-AtCut a connection queued at the very instant of the deadline that cut the accept is taken, one queued after it is not, on 4 seeds" (sys: &System) {
    for seed in 0..4 {
        Assert(AcBorn(sys, Cast[u64](seed)))
    }
}
