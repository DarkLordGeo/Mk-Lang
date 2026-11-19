// Why: scope deadlines under a Sim must fire on the simulated clock at a
// point the seed fixes, so a failing seed replays exactly; and an
// operation a deadline interrupts must be callable again with nothing lost.
// What: blocks on deadline firing and replay, the exit window over 1,000
// seeds, Expired under a simultaneous Cancel, the simulated wall clock,
// interrupted-and-repeated reads, accepts and receives, stream writes
// from simulated tasks, tape deadlines and main alone in a Sim.
// How: each block runs seeded Sims, often twice per seed, and compares the
// outcomes with each other or with an uninterrupted run.

import std.core
import std.net
import std.raw
import std.rt
import std.time

// DlTicker sleeps 1 ms at a time until cancelled, counting its sleeps and
// mixing the clock at each into d.
DlTicker(io: Io, n: &raw.Atomic[u64], d: &raw.Atomic[u64]) {
    for {
        io.Sleep(time.Millis(1)) else {
            return
        }
        _ = n.Add(1)
        _ = d.Add(n.Load() *% 2654435761)
    }
}

// DlFireOnce runs one Sim of seed: a scope whose deadline is 5.5 ms, two
// tickers and a body that sleeps an hour. The result mixes the tickers'
// counts, the body's Canceled and Expired, and the simulated time the
// scope took; 0 when the deadline did not cancel the body.
DlFireOnce(a: &Arena, seed: u64): u64 {
    var s := Sim.New(a, seed) else {
        return 0
    }
    io := s.Io()
    n := raw.Atomic[u64].New(0)
    d := raw.Atomic[u64].New(0)
    var cut := false
    var exp := false
    t0 := time.Now(io)
    scope sc := io.Scope() {
        sc.CancelAfter(core.DurationOf(5_500_000))
        sc.go DlTicker(io, &n, &d)
        sc.go DlTicker(io, &n, &d)
        io.Sleep(time.Seconds(3_600)) else {
            cut = true
        }
        exp = sc.Expired()
    }
    took := time.Now(io).Since(t0).Nanos()
    s.Free()
    if !cut || !exp || took < 5_500_000 || took > 60_000_000 {
        return 0
    }
    return (n.Load() << 32) ^ d.Load() ^ Cast[u64](took)
}

test "SIM: a deadline fires on the simulated clock, at the same point on every replay" (sys: &System) {
    for seed in 1..6 {
        first := DlFireOnce(sys.arena, Cast[u64](seed))
        Assert(first != 0)
        for _ in 0..10 {
            Assert(DlFireOnce(sys.arena, Cast[u64](seed)) == first)
        }
    }
}

// DlQuick ends at once.
DlQuick(io: Io, n: &raw.Atomic[u64]) {
    _ = n.Add(1)
}

test "SIM: a deadline firing in its scope's exit window, over 1,000 seeds" (sys: &System) {
    for seed in 0..1_000 {
        var s := Sim.New(sys.arena, Cast[u64](seed)) else {
            Assert(false)
            return
        }
        io := s.Io()
        n := raw.Atomic[u64].New(0)
        k := Cast[i64](seed % 7)
        scope sc := io.Scope() {
            sc.go DlQuick(io, &n)
            sc.CancelAfter(core.DurationOf(k * 1_000))
            if seed % 3 == 0 {
                io.Yield() else {}
            }
        }
        Assert(n.Load() == 1)
        s.Free()
    }
}

// DlCancelAt sleeps us microseconds, then cancels sc.
DlCancelAt(io: Io, sc: &Scope, us: i64) {
    io.NoCancel().Sleep(core.DurationOf(us * 1_000)) else {}
    sc.Cancel()
}

// DlRaceRun: the scope's deadline at 10 us and a task's Cancel at us
// microseconds of the simulated clock, just before, at or just after it;
// 1 when Expired, 2 when not, 0 on a failure.
DlRaceRun(a: &Arena, seed: u64, us: i64): int {
    var s := Sim.New(a, seed) else {
        return 0
    }
    io := s.Io()
    var e1 := false
    var e2 := false
    scope sc := io.Scope() {
        sc.CancelAfter(core.DurationOf(10_000))
        sc.go DlCancelAt(io, &sc, us)
        io.Sleep(time.Seconds(1)) else {}
        e1 = sc.Expired()
        io.NoCancel().Yield() else {}
        e2 = sc.Expired()
    }
    s.Free()
    if e1 != e2 {
        return 0
    }
    return if e1 { 1 } else { 2 }
}

test "SIM: Expired under a simultaneous Cancel is identical on replay, and both causes occur" (sys: &System) {
    // The canceller's offset (8 to 12 us around the 10 us deadline) and
    // the seed decide which of the deadline and the Cancel sets the flag
    // first (exactly one cause); both must occur over the
    // seeds, and each seed's outcome must be the same on every replay.
    var expired := 0
    var cancelled := 0
    for seed in 0..40 {
        us := 8 + Cast[i64](seed % 5)
        first := DlRaceRun(sys.arena, Cast[u64](seed), us)
        Assert(first != 0)
        if first == 1 {
            expired += 1
        } else {
            cancelled += 1
        }
        for _ in 0..5 {
            Assert(DlRaceRun(sys.arena, Cast[u64](seed), us) == first)
        }
    }
    Assert(expired > 0 && cancelled > 0)
}

test "SIM: the simulated wall clock starts at 2026-01-01T00:00:00Z and moves with the clock" (sys: &System) {
    var s := Sim.New(sys.arena, 9) else {
        Assert(false)
        return
    }
    io := s.Io()
    w0 := rt.WallClock(io)
    Assert(w0.err == core.SysErr.None)
    Assert(w0.value >= 1_767_225_600_000_000_000 && w0.value < 1_767_225_601_000_000_000)
    s.Advance(time.Seconds(3_600))
    w1 := rt.WallClock(io)
    Assert(w1.value - w0.value >= 3_600_000_000_000 && w1.value - w0.value < 3_601_000_000_000)
    s.Free()
}

// DlSendLater sleeps ms milliseconds, then sends v.
DlSendLater(io: Io, c: &Chan[int], ms: i64, v: int) {
    io.Sleep(time.Millis(ms)) else {
        return
    }
    c.Send(io, v) else {}
}

// DlRecvLater sleeps ms milliseconds, then receives one value into out.
DlRecvLater(io: Io, c: &Chan[int], ms: i64, out: &raw.Atomic[u64]) {
    io.Sleep(time.Millis(ms)) else {
        return
    }
    v := c.Recv(io) else {
        return
    }
    out.Store(Cast[u64](v))
}

// DlUnlockLater holds m for ms milliseconds.
DlUnlockLater(io: Io, m: &Mutex[int], held: &raw.Atomic[u32], ms: i64) {
    var g := m.Lock(io) else {
        return
    }
    held.Store(1)
    io.Sleep(time.Millis(ms)) else {}
    g.Unlock()
}

// DlTryRecv receives one value: some(v), or none when Canceled.
DlTryRecv(io: Io, c: &Chan[int]): ?int {
    v := c.Recv(io) else {
        return none
    }
    return some(v)
}

// DlTryLock locks and unlocks m: false when the lock was Canceled.
DlTryLock(io: Io, m: &Mutex[int]): bool {
    var g := m.Lock(io) else {
        return false
    }
    g.Unlock()
    return true
}

// DlTryAccept accepts one connection and closes it: false when Canceled
// or failed.
DlTryAccept(io: Io, l: &net.Listener): bool {
    c := l.Accept(io) else {
        return false
    }
    c.Close()
    return true
}

// DlTryRead reads once into buf: the count, or -1 when the read failed.
DlTryRead(io: Io, c: &net.Stream, buf: mut []u8): int {
    k := c.Read(io, buf) else {
        return -1
    }
    return k
}

// DlRepeatRun interrupts each repeatable line once by a 1 ms deadline,
// then calls it again outside the deadline's scope: Chan.Recv (the value
// arrives at 5 ms), Chan.Send on a full channel (room at 5 ms; the value
// stays with the sender), Mutex.Lock (released at 5 ms), Io.Sleep. The
// result mixes what the second calls delivered; 0 when a first call was not
// Canceled, or a second call failed.
DlRepeatRun(a: &Arena, seed: u64): u64 {
    var s := Sim.New(a, seed) else {
        return 0
    }
    io := s.Io()
    c := Chan[int].New(a.Alloc(), 1) else {
        s.Free()
        return 0
    }
    out := raw.Atomic[u64].New(0)
    // The lines run on a task of the Sim, which holds its baton.
    scope r := io.Scope() {
        r.go DlRepeatBody(io, &c, &out)
    }
    c.Free(a.Alloc())
    s.Free()
    return out.Load()
}

// DlRepeatBody is DlRepeatRun's task; out gets the result.
DlRepeatBody(io: Io, c: &Chan[int], out: &raw.Atomic[u64]) {
    m := Mutex[int].New(0)
    held := raw.Atomic[u32].New(0)
    got := raw.Atomic[u64].New(0)
    var ok := true
    var res: u64 = 0
    var bad: u64 = 0
    scope top := io.Scope() {
        // Chan.Recv.
        top.go DlSendLater(io, c, 5, 41)
        scope d := io.Scope() {
            d.CancelAfter(time.Millis(1))
            if DlTryRecv(io, c).IsSome() {
                if bad == 0 { bad = 250 }
            }
            if !(d.Expired()) && bad == 0 { bad = 252 }
        }
        v := c.Recv(io) else {
            if bad == 0 { bad = 255 }
            0
        }
        res = res * 31 + Cast[u64](v)
        // Chan.Send: fill the channel, then send under a deadline.
        c.Send(io, 1) else {
            if bad == 0 { bad = 261 }
        }
        top.go DlRecvLater(io, c, 5, &got)
        scope d2 := io.Scope() {
            d2.CancelAfter(time.Millis(1))
            c.Send(io, 99) else {
                if !(d2.Expired()) && bad == 0 { bad = 267 }
            }
        }
        c.Send(io, 2) else {
            if bad == 0 { bad = 271 }
        }
        w := c.Recv(io) else {
            if bad == 0 { bad = 274 }
            0
        }
        res = res * 31 + Cast[u64](w)
        // Mutex.Lock.
        top.go DlUnlockLater(io, &m, &held, 5)
        for held.Load() == 0 {
            io.Yield() else {}
        }
        scope d3 := io.Scope() {
            d3.CancelAfter(time.Millis(1))
            if DlTryLock(io, &m) {
                if bad == 0 { bad = 286 }
            }
            if !(d3.Expired()) && bad == 0 { bad = 288 }
        }
        if !(DlTryLock(io, &m)) && bad == 0 { bad = 290 }
        // Io.Sleep.
        scope d4 := io.Scope() {
            d4.CancelAfter(time.Millis(1))
            io.Sleep(time.Millis(10)) else {
                if !(d4.Expired()) && bad == 0 { bad = 295 }
            }
        }
        io.Sleep(time.Millis(1)) else {
            if bad == 0 { bad = 299 }
        }
    }
    res = res * 31 + got.Load()
    out.Store(if ok && bad == 0 { res } else { 100_000 + bad })
}

test "SIM: the repeatable lines interrupted by a deadline and called again deliver what an uninterrupted run does" (sys: &System) {
    // Uninterrupted, the second calls deliver 41; then the receiver task takes
    // the 1 queued before, and the body's own receive the 2 it sent after
    // the interrupted send, whose 99 never arrives.
    want: u64 = (41 * 31 + 2) * 31 + 1
    for seed in 0..20 {
        r := DlRepeatRun(sys.arena, Cast[u64](seed))
        if r != want {
            sys.PrintErr("seed {seed} got {r}\n", (seed: seed, r: r)) else {}
        }
        Assert(r == want)
    }
}

// DlServe accepts one connection and writes "hi" after ms milliseconds.
DlServe(io: Io, l: &net.Listener, ms: i64) {
    c := l.Accept(io) else {
        return
    }
    io.Sleep(time.Millis(ms)) else {}
    c.WriteAll(io, "hi".AsBytes()) else {}
    c.Close()
}

// DlDialLater dials port after ms milliseconds and closes at once.
DlDialLater(io: Io, port: str, ms: i64) {
    io.Sleep(time.Millis(ms)) else {
        return
    }
    c := net.Dial(io, port) else {
        return
    }
    c.Close()
}

// DlNetAccept: the dial comes at 5 ms; a 1 ms deadline interrupts the
// first accept, and the second gets the connection.
DlNetAccept(io: Io, l: &net.Listener): bool {
    var ok := true
    scope top := io.Scope() {
        top.go DlDialLater(io, "127.0.0.1:7200", 5)
        scope d := io.Scope() {
            d.CancelAfter(time.Millis(1))
            if DlTryAccept(io, l) {
                ok = false
            }
            ok = ok && d.Expired()
        }
        ok = ok && DlTryAccept(io, l)
    }
    return ok
}

// DlReadSide dials, has its first read interrupted by a 1 ms deadline, then
// reads the server's "hi" (written at 5 ms).
DlReadSide(io: Io): bool {
    c := net.Dial(io, "127.0.0.1:7200") else {
        return false
    }
    var ok := true
    var buf: [8]u8 = [0; 8]
    scope d := io.Scope() {
        d.CancelAfter(time.Millis(1))
        if DlTryRead(io, &c, mut buf[..]) >= 0 {
            ok = false
        }
        ok = ok && d.Expired()
    }
    var got := 0
    for got < 2 {
        k := DlTryRead(io, &c, mut buf[got..])
        if k <= 0 {
            break
        }
        got += k
    }
    c.Close()
    return ok && got == 2 && buf[0] == 104 && buf[1] == 105
}

DlNetRun(io: Io): bool {
    l := net.Listen(io, "127.0.0.1:7200") else {
        return false
    }
    var ok := DlNetAccept(io, &l)
    scope top := io.Scope() {
        top.go DlServe(io, &l, 5)
        ok = DlReadSide(io) && ok
    }
    l.Close()
    return ok
}

test "SIM: Listener.Accept and Stream.Read interrupted by a deadline and called again" (sys: &System) {
    var s := Sim.New(sys.arena, 3) else {
        Assert(false)
        return
    }
    io := s.Io()
    out := raw.Atomic[u32].New(0)
    // On a task of the Sim, which holds its baton.
    scope r := io.Scope() {
        r.go DlNetTask(io, &out)
    }
    s.Free()
    Assert(out.Load() == 1)
}

// DlNetTask runs DlNetRun and stores 1 in out when it held.
DlNetTask(io: Io, out: &raw.Atomic[u32]) {
    if DlNetRun(io) {
        out.Store(1)
    }
}

// DlUdpLater binds 127.0.0.1:7301 and sends "hi" from it to the address to
// after ms milliseconds (a Datagram is not Share: the task owns its own).
DlUdpLater(io: Io, to: net.Addr, ms: i64) {
    d := net.Bind(io, "127.0.0.1:7301") else {
        return
    }
    io.Sleep(time.Millis(ms)) else {}
    d.Send(io, "hi".AsBytes(), to) else {}
    d.Close()
}

// DlTryRecvFrom is one Datagram.Recv: the bytes, or -1 on an error.
DlTryRecvFrom(io: Io, d: &net.Datagram, buf: mut []u8): int {
    got := d.Recv(io, buf) else {
        return -1
    }
    return got.n
}

// DlUdpRun: the datagram comes at 5 ms; a 1 ms deadline interrupts the
// first Recv, and the second gets the datagram whole.
DlUdpRun(io: Io): bool {
    a := net.Bind(io, "127.0.0.1:7300") else {
        return false
    }
    to := net.ParseAddr("127.0.0.1:7300") else {
        a.Close()
        return false
    }
    var ok := true
    var buf: [8]u8 = [0; 8]
    scope top := io.Scope() {
        top.go DlUdpLater(io, to, 5)
        scope d := io.Scope() {
            d.CancelAfter(time.Millis(1))
            if DlTryRecvFrom(io, &a, mut buf[..]) >= 0 {
                ok = false
            }
            ok = ok && d.Expired()
        }
        n := DlTryRecvFrom(io, &a, mut buf[..])
        ok = ok && n == 2 && buf[0] == 104 && buf[1] == 105
    }
    a.Close()
    return ok
}

// DlUdpTask runs DlUdpRun and stores 1 in out when it held.
DlUdpTask(io: Io, out: &raw.Atomic[u32]) {
    if DlUdpRun(io) {
        out.Store(1)
    }
}

test "SIM: Datagram.Recv interrupted by a deadline and called again receives the datagram whole" (sys: &System) {
    for seed in 0..20 {
        var s := Sim.New(sys.arena, Cast[u64](seed)) else {
            Assert(false)
            return
        }
        io := s.Io()
        out := raw.Atomic[u32].New(0)
        // On a task of the Sim, which holds its baton.
        scope r := io.Scope() {
            r.go DlUdpTask(io, &out)
        }
        s.Free()
        Assert(out.Load() == 1)
    }
}

// DlStdioTask mixes its turn into acc, then writes a byte to the process's
// standard error through the Sim's stdio entry and yields, three times.
DlStdioTask(io: Io, acc: &raw.Atomic[u64], k: u64) {
    for i in 0..3 {
        acc.Store((acc.Load() % 1_000_000_007) * 37 + k * 10 + Cast[u64](i) + 1)
        _ = core.IoStdio(io, 1, ".".AsBytes())
        io.Yield() else {}
    }
}

// DlStdioRun runs four DlStdioTasks in a Sim of seed: the order they ran
// in, mixed.
DlStdioRun(a: &Arena, seed: u64): u64 {
    var s := Sim.New(a, seed) else {
        return 0
    }
    io := s.Io()
    acc := raw.Atomic[u64].New(0)
    scope sc := io.Scope() {
        for k in 0..4 {
            sc.go DlStdioTask(io, &acc, Cast[u64](k))
        }
    }
    s.Free()
    return acc.Load()
}

test "SIM: a stream write from a simulated task keeps the seed's schedule: each seed's order is the same on replay" (sys: &System) {
    for seed in 1..9 {
        first := DlStdioRun(sys.arena, Cast[u64](seed))
        Assert(first != 0)
        for _ in 0..4 {
            Assert(DlStdioRun(sys.arena, Cast[u64](seed)) == first)
        }
    }
}

// DlStreamHog writes 2 KiB lines to the process's standard error through
// the native Io until stop is set, so a simulated task's write often finds
// the stream's lock held by a thread outside its Sim.
DlStreamHog(io: Io, stop: &raw.Atomic[u32]) {
    var line: [2048]u8 = [32; 2048]
    line[2047] = 10
    for stop.Load() == 0 {
        _ = core.IoStdio(io, 1, line[..])
        io.Yield() else {
            return
        }
    }
}

test "SIM: a stream write from a simulated task keeps the seed's schedule while a native task holds the stream" (sys: &System) {
    var firsts: [6]u64 = [0; 6]
    for seed in 1..7 {
        firsts[seed - 1] = DlStdioRun(sys.arena, Cast[u64](seed))
    }
    stop := raw.Atomic[u32].New(0)
    scope n := sys.io.Scope() {
        n.go DlStreamHog(sys.io, &stop)
        n.go DlStreamHog(sys.io, &stop)
        for _ in 0..4 {
            for seed in 1..7 {
                Assert(DlStdioRun(sys.arena, Cast[u64](seed)) == firsts[seed - 1])
            }
        }
        stop.Store(1)
    }
}

// DlNativeWriter, a task of a Sim, writes 16 KiB lines to the process's
// standard error through the native Io (as sys.PrintErr does), so its
// write drains on the pool while it holds the stream's lock; then yields
// the Sim's turn. Three times.
DlNativeWriter(io: Io, acc: &raw.Atomic[u64]) {
    var line: [16384]u8 = [32; 16384]
    line[16383] = 10
    for i in 0..3 {
        acc.Store((acc.Load() % 1_000_000_007) * 37 + 90 + Cast[u64](i))
        _ = core.IoStdio(rt.NativeIo(), 1, line[..])
        io.Yield() else {}
    }
}

// DlMixedRun runs two DlStdioTasks and two DlNativeWriters in a Sim of
// seed: the order they ran in, mixed.
DlMixedRun(a: &Arena, seed: u64): u64 {
    var s := Sim.New(a, seed) else {
        return 0
    }
    io := s.Io()
    acc := raw.Atomic[u64].New(0)
    scope sc := io.Scope() {
        sc.go DlStdioTask(io, &acc, 0)
        sc.go DlNativeWriter(io, &acc)
        sc.go DlStdioTask(io, &acc, 1)
        sc.go DlNativeWriter(io, &acc)
    }
    s.Free()
    return acc.Load()
}

test "SIM: simulated tasks writing a stream through the Sim's Io and the native Io neither hang nor leave the seed's order" (sys: &System) {
    for seed in 1..5 {
        first := DlMixedRun(sys.arena, Cast[u64](seed))
        Assert(first != 0)
        for _ in 0..3 {
            Assert(DlMixedRun(sys.arena, Cast[u64](seed)) == first)
        }
    }
}

// DlRandomTask draws 8 bytes in a cancelled scope (out 1 when Canceled),
// then 8 outside it, mixed into out's high half: the draw that was
// Canceled took nothing from the stream.
DlRandomTask(io: Io, out: &raw.Atomic[u64]) {
    var b: [8]u8 = [0; 8]
    var cut := false
    scope c := io.Scope() {
        c.Cancel()
        io.Random(mut b[..]) else {
            cut = true
        }
    }
    io.Random(mut b[..]) else {
        return
    }
    var x: u64 = 0
    for i in 0..8 {
        x = (x << 8) | u64(b[i])
    }
    out.Store(if cut { (x & ~Trunc[u64](1)) | 1 } else { 0 })
}

test "SIM: Io.Random in a cancelled scope is Canceled and draws nothing, as natively" (sys: &System) {
    for seed in 1..6 {
        var s := Sim.New(sys.arena, Cast[u64](seed)) else {
            Assert(false)
            return
        }
        io := s.Io()
        out := raw.Atomic[u64].New(0)
        scope r := io.Scope() {
            r.go DlRandomTask(io, &out)
        }
        s.Free()
        Assert((out.Load() & 1) == 1)
        // The first draw of a fresh Sim of the same seed gives the bytes
        // the second draw above got: the Canceled one drew nothing.
        var t := Sim.New(sys.arena, Cast[u64](seed)) else {
            Assert(false)
            return
        }
        var b: [8]u8 = [0; 8]
        t.Io().Random(mut b[..]) else {
            Assert(false)
        }
        t.Free()
        var x: u64 = 0
        for i in 0..8 {
            x = (x << 8) | u64(b[i])
        }
        Assert((x & ~Trunc[u64](1)) == (out.Load() & ~Trunc[u64](1)))
    }
}

// MnCanceller cancels s after 1 ms of the Sim's clock.
MnCanceller(io: Io, s: &Scope) {
    io.Sleep(time.Millis(1)) else {}
    s.Cancel()
}

// MnPeer writes "late" on c at 2 ms and closes it (the task owns c).
MnPeer(io: Io, c: net.Stream) {
    io.Sleep(time.Millis(2)) else {}
    c.WriteAll(io, "late".AsBytes()) else {}
    c.Close()
}

// MnLate is when the other task's event comes (2 ms): a cut at or after it
// was not made by the cancel.
const MnLate: i64 = 2_000_000

// MnSleeper sleeps 2 ms: the event a choice made too early would jump to.
MnSleeper(io: Io) {
    io.Sleep(time.Millis(2)) else {}
}

// MnTryAccept is one Listener.Accept: the stream, or none on an error.
MnTryAccept(io: Io, l: &net.Listener): ?net.Stream {
    a := l.Accept(io) else {
        return none
    }
    return some(a)
}

// MnDialer dials addr at 2 ms and closes the stream.
MnDialer(io: Io, addr: str) {
    io.Sleep(time.Millis(2)) else {}
    c := net.Dial(io, addr) else {
        return
    }
    c.Close()
}

// MnRun is one Sim of seed with main (not a task of the run) parked in a
// Stream.Read, a Listener.Accept and an Io.Sleep, each in a scope that a
// task of the run cancels at 1 ms while another task is due at 2 ms. The
// clock at each cut, mixed: 0 when an operation was not Canceled, or the
// Read took the late bytes.
MnRun(sys: &System, seed: u64): i64 {
    var w := Sim.New(sys.arena, seed) else {
        return 0
    }
    io := w.Io()
    var mix: i64 = 0
    var ok := true
    l := net.Listen(io, "127.0.0.1:0") else {
        w.Free()
        return 0
    }
    addr := sys.arena.Fmt("127.0.0.1:{p}", (p: l.Port())) else {
        l.Close()
        w.Free()
        return 0
    }
    c := net.Dial(io, addr) else {
        l.Close()
        w.Free()
        return 0
    }
    srv := l.Accept(io) else {
        c.Close()
        l.Close()
        w.Free()
        return 0
    }
    t0 := time.Now(io)
    // The Read: Canceled at the cancel's clock, the late bytes kept.
    scope o := io.Scope() {
        o.go MnPeer(io, c)
        scope s := io.Scope() {
            s.go MnCanceller(io, &s)
            var b: [8]u8 = [0; 8]
            _ = srv.Read(io, mut b[..]) else {
                mix = time.Now(io).Since(t0).Nanos()
                0
            }
            ok = ok && mix > 0 && mix < MnLate
        }
        var b2: [8]u8 = [0; 8]
        var got := 0
        for got < 4 {
            k := srv.Read(io, mut b2[got..]) else { 0 }
            if k == 0 {
                break
            }
            got += k
        }
        ok = ok && got == 4 && b2[0] == 108 && b2[3] == 101
    }
    // The Accept: Canceled at the cancel's clock; the later dial is
    // accepted by the next call.
    t1 := time.Now(io)
    scope o := io.Scope() {
        o.go MnDialer(io, addr)
        var cut: i64 = 0
        scope s := io.Scope() {
            s.go MnCanceller(io, &s)
            if a := MnTryAccept(io, &l) {
                a.Close()
            } else {
                cut = time.Now(io).Since(t1).Nanos()
            }
        }
        ok = ok && cut > 0 && cut < MnLate
        mix = mix *% 31 +% cut
        if a2 := MnTryAccept(io, &l) {
            a2.Close()
        } else {
            ok = false
        }
    }
    // The Sleep: ended at the cancel's clock.
    t2 := time.Now(io)
    scope o := io.Scope() {
        o.go MnSleeper(io)
        var cut: i64 = 0
        scope s := io.Scope() {
            s.go MnCanceller(io, &s)
            io.Sleep(time.Millis(10)) else {
                cut = time.Now(io).Since(t2).Nanos()
            }
        }
        ok = ok && cut > 0 && cut < MnLate
        mix = mix *% 31 +% cut
    }
    // The Sleep under main's own scope deadline: ended at the deadline's
    // clock, not at the other task's event.
    t3 := time.Now(io)
    scope o := io.Scope() {
        o.go MnSleeper(io)
        var cut: i64 = 0
        scope s := io.Scope() {
            s.CancelAfter(time.Millis(1))
            io.Sleep(time.Millis(10)) else {
                cut = time.Now(io).Since(t3).Nanos()
            }
        }
        ok = ok && cut > 0 && cut < MnLate
        mix = mix *% 31 +% cut
    }
    srv.Close()
    l.Close()
    w.Free()
    return if ok { mix } else { 0 }
}

test "SIM: a cancel from a task, or main's own deadline, ends main's parked Read, Accept and Sleep at that clock, the Read takes nothing, the same on replay" (sys: &System) {
    for seed in 0..12 {
        m := MnRun(sys, Cast[u64](seed))
        Assert(m != 0)
        Assert(MnRun(sys, Cast[u64](seed)) == m)
    }
}

// TapeCut sleeps 10 s on io under a 20 ms deadline of a scope opened from
// io; 1 when the sleep was Canceled and the scope Expired, 2 when it was
// Canceled only, 0 when it was not cut.
TapeCut(io: Io): int {
    var cut := false
    var exp := false
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(20))
        io.Sleep(time.Seconds(10)) else {
            cut = true
        }
        exp = s.Expired()
    }
    return if !cut { 0 } else if exp { 1 } else { 2 }
}

test "SIM-Tape-Deadline a Recorder over the native Io arms deadlines on the inner clock; its Replayer answers the same Canceled, and Expired as recorded" (sys: &System) {
    var rec := Recorder.New(sys.io) else {
        Assert(false)
        return
    }
    t0 := time.Now(sys.io)
    Assert(TapeCut(rec.Io()) == 1)
    Assert(time.Now(sys.io).Since(t0).Nanos() < 5_000_000_000)
    log := rec.Log(sys.arena) else {
        rec.Free()
        Assert(false)
        return
    }
    rec.Free()
    var rp := Replayer.New(log) else {
        Assert(false)
        return
    }
    // The replay arms nothing (no clock behind it): the logged fire comes
    // before the sleep's logged Canceled, so the scope is Expired as when
    // it was recorded.
    Assert(TapeCut(rp.Io()) == 1)
    Assert(!rp.Diverged())
    Assert(rp.Done())
    rp.Free()
}

// TapeSimTask runs TapeCut under a Recorder over the Sim's io; out 1 when
// the Sim's clock fired the deadline.
TapeSimTask(io: Io, out: &raw.Atomic[u32]) {
    var rec := Recorder.New(io) else {
        return
    }
    if TapeCut(rec.Io()) == 1 {
        out.Store(1)
    }
    rec.Free()
}

test "SIM-Tape-SimDeadline a Recorder over a Sim arms deadlines on the Sim's clock" (sys: &System) {
    for seed in 1..5 {
        var s := Sim.New(sys.arena, Cast[u64](seed)) else {
            Assert(false)
            return
        }
        io := s.Io()
        out := raw.Atomic[u32].New(0)
        scope r := io.Scope() {
            r.go TapeSimTask(io, &out)
        }
        s.Free()
        Assert(out.Load() == 1)
    }
}

test "SIM-Tape-FreedArmed a Recorder freed while a scope opened from its Io holds an armed deadline still disarms it at the scope's exit" (sys: &System) {
    for k in 0..50 {
        var rec := Recorder.New(sys.io) else {
            Assert(false)
            return
        }
        io := rec.Io()
        scope s := io.Scope() {
            s.CancelAfter(time.Millis(1 + Cast[i64](k % 3)))
            rec.Free()
        }
        // The disarmed record is not on the native heap any more: a later
        // deadline elsewhere fires on its own record only.
        var cut := false
        scope n := sys.io.Scope() {
            n.CancelAfter(time.Millis(5))
            sys.io.Sleep(time.Seconds(10)) else {
                cut = true
            }
        }
        Assert(cut)
    }
}

test "main alone in a Sim, a 1 s Sleep under a 50 ms CancelAfter ends Canceled at 50 ms and the scope is Expired, the same on replay" (sys: &System) {
    for _ in 0..2 {
        var w := Sim.New(sys.arena, 7) else {
            Assert(false)
            return
        }
        io := w.Io()
        start := time.Now(io)
        var slept := true
        var expired := false
        scope s := io.Scope() {
            s.CancelAfter(time.Millis(50))
            io.Sleep(time.Seconds(1)) else {
                slept = false
            }
            expired = s.Expired()
        }
        took := time.Now(io).Since(start).Nanos()
        w.Free()
        Assert(!slept && expired && took >= 50_000_000 && took < 50_010_000)
    }
}

test "main alone in a Sim, a Read of a stream nobody writes under a 20 ms CancelAfter ends Canceled at 20 ms with the scope Expired" (sys: &System) {
    var w := Sim.New(sys.arena, 3) else {
        Assert(false)
        return
    }
    io := w.Io()
    l := rt.ListenWith(io, "127.0.0.1:0", 0, true)
    Assert(l.err == core.SysErr.None)
    var ab: [32]u8 = [0; 32]
    d := core.IoDial(io, SdAddr(mut ab, l.os))
    Assert(d.err == core.SysErr.None)
    start := time.Now(io)
    var expired := false
    var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(20))
        var b: [4]u8 = [0; 4]
        r = core.IoRecv(io, Trunc[uint](d.value), mut b[..])
        expired = s.Expired()
    }
    took := time.Now(io).Since(start).Nanos()
    Assert(r.err == core.SysErr.Canceled && expired && took >= 20_000_000 && took < 20_010_000)
    rt.Abandon(Trunc[uint](d.value))
    rt.Abandon(Trunc[uint](l.value))
    w.Free()
}

// SdAddr writes "127.0.0.1:port" into b and returns it.
SdAddr(b: mut [32]u8, port: i64): str {
    pre := "127.0.0.1:".AsBytes()
    for i in 0..pre.Len() {
        b[i] = pre[i]
    }
    var n := pre.Len()
    var digits: [8]u8 = [0; 8]
    var k := 0
    var p := port
    for {
        digits[k] = Trunc[u8](p % 10) + b'0'
        k += 1
        p = p / 10
        if p == 0 {
            break
        }
    }
    for k > 0 {
        k -= 1
        b[n] = digits[k]
        n += 1
    }
    return str.From(b[0..n]) else { "" }
}

test "SD8-Sim-MainReadInFlight main alone in a Sim: a receive whose bytes arrive after its scope's 5 us deadline ends Canceled at the deadline, the scope Expired and the bytes kept; the next receive takes them" (sys: &System) {
    var w := Sim.New(sys.arena, 3) else {
        Assert(false)
        return
    }
    io := w.Io()
    l := rt.ListenWith(io, "127.0.0.1:0", 0, true)
    Assert(l.err == core.SysErr.None)
    var ab: [32]u8 = [0; 32]
    d := core.IoDial(io, SdAddr(mut ab, l.os))
    Assert(d.err == core.SysErr.None)
    a := core.IoAccept(io, Trunc[uint](l.value))
    Assert(a.err == core.SysErr.None)
    // The bytes arrive 20 to 500 us after the send (net.mk's latency).
    Assert(core.IoSend(io, Trunc[uint](a.value), "hi".AsBytes()).value == 2)
    var expired := false
    var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    var b: [4]u8 = [0; 4]
    scope s := io.Scope() {
        s.CancelAfter(time.Micros(5))
        r = core.IoRecv(io, Trunc[uint](d.value), mut b[..])
        expired = s.Expired()
    }
    Assert(r.err == core.SysErr.Canceled && expired)
    var got := 0
    for got < 2 {
        r2 := core.IoRecv(io, Trunc[uint](d.value), mut b[got..])
        Assert(r2.err == core.SysErr.None && r2.value > 0)
        got += Trunc[int](r2.value)
    }
    Assert(b[0] == b'h' && b[1] == b'i')
    rt.Abandon(Trunc[uint](a.value))
    rt.Abandon(Trunc[uint](d.value))
    rt.Abandon(Trunc[uint](l.value))
    w.Free()
}

test "under std.sim a scope with no task: its 1 h deadline ends main's 2 h Sleep with Canceled at 1 h, and the scope is Expired" (sys: &System) {
    var w := Sim.New(sys.arena, 1) else {
        Assert(false)
        return
    }
    io := w.Io()
    start := time.Now(io)
    var slept := true
    var expired := false
    scope sc := io.Scope() {
        sc.CancelAfter(time.Hours(1))
        io.Sleep(time.Hours(2)) else {
            slept = false
        }
        expired = sc.Expired()
    }
    took := time.Now(io).Since(start).Nanos()
    w.Free()
    Assert(!slept && expired && took >= 3_600_000_000_000 && took < 3_600_000_010_000)
}

test "SD8-Sim-SignalDeadline a signal wait under a Sim is cut by its scope's 5 ms deadline (Canceled, Expired), main alone and main beside a sleeping task, and in a task beside a ticker" (sys: &System) {
    var w := Sim.New(sys.arena, 2) else {
        Assert(false)
        return
    }
    io := w.Io()
    // Main alone.
    start := time.Now(io)
    var got := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    var expired := false
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(5))
        got = core.IoSignal(io, 15)
        expired = s.Expired()
    }
    took := time.Now(io).Since(start).Nanos()
    Assert(got.err == core.SysErr.Canceled && expired && took >= 5_000_000 && took < 5_010_000)
    // Main waits while a task sleeps 20 ms: the deadline cuts it first.
    var main_cut := false
    scope r := io.Scope() {
        r.go SgSleep(io, 20)
        scope s2 := io.Scope() {
            s2.CancelAfter(time.Millis(5))
            got = core.IoSignal(io, 15)
            main_cut = got.err == core.SysErr.Canceled && s2.Expired()
        }
    }
    Assert(main_cut)
    // A task's wait beside a ticker task.
    var task_cut := raw.Atomic[u32].New(0)
    scope q := io.Scope() {
        q.go SgTicker(io)
        q.go SgWaitTask(io, &task_cut)
    }
    Assert(task_cut.Load() == 1)
    w.Free()
}

// SgSleep sleeps ms milliseconds of the Sim's clock.
SgSleep(io: Io, ms: i64) {
    io.Sleep(time.Millis(ms)) else {}
}

// SgTicker sleeps 1 ms ten times.
SgTicker(io: Io) {
    for _ in 0..10 {
        io.Sleep(time.Millis(1)) else {}
    }
}

// SgWaitTask waits for a signal under a 3 ms deadline: cut becomes 1 when
// the deadline ended it.
SgWaitTask(io: Io, cut: &raw.Atomic[u32]) {
    scope s := io.Scope() {
        s.CancelAfter(time.Millis(3))
        r := core.IoSignal(io, 15)
        if r.err == core.SysErr.Canceled && s.Expired() {
            cut.Store(1)
        }
    }
}
