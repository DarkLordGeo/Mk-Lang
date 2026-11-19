// Why: a deadline must cut rt.TermWait (Term.Read's wait) under a Sim and
// the wait must be callable again losing no key, with a seed replaying a
// key stream the same on every run.
// What: the SD8-Sim-Term-* blocks on the Sim's one terminal behind rt's
// terminal calls: open, Windows key records, deadlines, replay and main's
// waits; helpers TmOpen and TmReader.
// How: input is queued with Sim.TermInput and read through rt's calls.
// The std.term end of it is std/term/tests_sim.mk.

import std.core
import std.raw
import std.rt
import std.time

// TmOpen opens the Sim's terminal on io: t[0] the input, t[1] the output.
TmOpen(io: Io, t: mut [2]uint): bool {
    return rt.TermOpen(io, t).err == core.SysErr.None && t[0] != 0 && t[1] != 0
}

// TmUnit is the UTF-16 unit of the KEY_EVENT record at r[k * 20], or the
// byte r[k] when records is false.
TmUnit(r: []u8, k: int, records: bool): u32 {
    if !records {
        return u32(r[k])
    }
    at := k * KeyRecord
    return u32(r[at + 14]) | (u32(r[at + 15]) << 8)
}

// TmKeys is how many keys of this target's form n bytes hold.
TmKeys(n: int): int {
    return if Target.os == core.Os.Windows { n / KeyRecord } else { n }
}

test "SD8-Sim-Term-Open term.Open's calls under a Sim: two foreign handles, 80 by 24, modes recorded, output discarded, no restore record, input kept across a close" (sys: &System) {
    var s := Sim.New(sys.arena, 5) else {
        Assert(false)
        return
    }
    io := s.Io()
    s.TermInput("x".AsBytes(), time.Millis(0)) else {
        Assert(false)
    }
    var t: [2]uint = [0; 2]
    Assert(TmOpen(io, mut t))
    Assert(rt.IsForeign(t[0]) && rt.IsForeign(t[1]) && t[0] != t[1])
    var w := core.IoWords(a: 0, b: 0, c: 0, d: 0)
    Assert(rt.TermSizeOf(io, t[0], t[1], mut w).err == core.SysErr.None && w.a == 80 && w.b == 24)
    // The standard streams are no terminal under a Sim.
    Assert(rt.IsTerminal(io, 0).err == core.SysErr.Unsupported)
    // Raw over the mode as opened, then NoEcho, then the first saved mode
    // back; each old mode is the target's length, byte 0 the mode.
    var old: [64]u8 = [0; 64]
    var old2: [64]u8 = [0; 64]
    var empty: [0]u8 = []
    m := rt.TermMode(io, t[0], t[1], core.IoTermRaw, empty[..], mut old[..])
    Assert(m.err == core.SysErr.None && m.value == i64(ModeBytes()) && old[0] == 0)
    m2 := rt.TermMode(io, t[0], t[1], core.IoTermNoEcho, empty[..], mut old2[..])
    Assert(m2.err == core.SysErr.None && old2[0] == 1)
    // IoTermGet reads the mode (NoEcho) and keeps it.
    for _ in 0..2 {
        g := rt.TermMode(io, t[0], t[1], core.IoTermGet, empty[..], mut old2[..])
        Assert(g.err == core.SysErr.None && g.value == i64(ModeBytes()) && old2[0] == 2)
    }
    m3 := rt.TermMode(io, t[0], t[1], core.IoTermSaved, old[0..ModeBytes()], mut old2[..])
    Assert(m3.err == core.SysErr.None && old2[0] == 2)
    Assert(rt.TermMode(io, t[0], t[1], core.IoTermSaved, old[0..3], mut old2[..]).err == core.SysErr.Other)
    Assert(rt.TermMode(io, t[1], t[0], core.IoTermRaw, empty[..], mut old2[..]).err == core.SysErr.Closed)
    // The runtime's restore registry takes no simulated terminal.
    Assert(rt.TermRegisterOpen(t[0], t[1], old[0..ModeBytes()], true) == -1)
    wr := core.IoWrite(io, t[1], "\x1b[?1049h hello".AsBytes(), -1)
    Assert(wr.err == core.SysErr.None && wr.value == 14)
    var b: [4]u8 = [0; 4]
    Assert(core.IoRead(io, t[0], mut b[..], -1).err == core.SysErr.Unsupported)
    Assert(core.IoClose(io, t[0]).err == core.SysErr.None)
    Assert(core.IoClose(io, t[1]).err == core.SysErr.None)
    // A second Open finds the input queued before the first.
    Assert(TmOpen(io, mut t))
    Assert(rt.TermWait(io, t[0]).err == core.SysErr.None)
    var r: [40]u8 = [0; 40]
    n := rt.TermRead(io, t[0], mut r[..])
    Assert(n.err == core.SysErr.None && TmKeys(Trunc[int](n.value)) == 1 && TmUnit(r[..], 0, Target.os == core.Os.Windows) == u32(b'x'))
    Assert(rt.TermRead(io, t[0], mut r[..]).value == 0)
    rt.Abandon(t[0])
    rt.Abandon(t[1])
    s.Free()
}

test "SD8-Sim-Term-Keys TermInput's Windows form: one key-down KEY_EVENT record per UTF-16 unit, an ill-formed sequence one U+FFFD per maximal subpart" (sys: &System) {
    var v := Vec[u8].Empty()
    // e-acute, U+1F600 (a surrogate pair), a lone continuation byte, a
    // lead cut short by a letter, and a lead at the end.
    var text: [11]u8 = [0xC3, 0xA9, 0xF0, 0x9F, 0x98, 0x80, 0x80, 0xE2, 0x82, 0x7A, 0xE2]
    Assert(AppendKeys(mut v, text[..]))
    var want: [7]u32 = [0xE9, 0xD83D, 0xDE00, 0xFFFD, 0xFFFD, 0x7A, 0xFFFD]
    Assert(v.Len() == 7 * KeyRecord)
    r := v.View()
    for k in 0..7 {
        at := k * KeyRecord
        Assert(TmUnit(r, k, true) == want[k])
        Assert(r[at] == 1 && r[at + 1] == 0 && r[at + 4] == 1 && r[at + 8] == 1 && r[at + 10] == 0 && r[at + 16] == 0)
    }
    v.Free()
}

test "SD8-Sim-Term-Deadline main alone: a wait for input due at 100 ms under a 10 ms deadline ends Canceled at 10 ms with the scope Expired and the key kept; the next wait takes it at 100 ms" (sys: &System) {
    for _ in 0..2 {
        var s := Sim.New(sys.arena, 9) else {
            Assert(false)
            return
        }
        io := s.Io()
        var t: [2]uint = [0; 2]
        Assert(TmOpen(io, mut t))
        s.TermInput("k".AsBytes(), time.Millis(100)) else {
            Assert(false)
        }
        start := time.Now(io)
        var expired := false
        var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
        scope sc := io.Scope() {
            sc.CancelAfter(time.Millis(10))
            r = rt.TermWait(io, t[0])
            expired = sc.Expired()
        }
        cut := time.Now(io).Since(start).Nanos()
        Assert(r.err == core.SysErr.Canceled && expired && cut >= 10_000_000 && cut < 10_010_000)
        Assert(rt.TermWait(io, t[0]).err == core.SysErr.None)
        took := time.Now(io).Since(start).Nanos()
        Assert(took >= 100_000_000 && took < 100_010_000)
        var b: [40]u8 = [0; 40]
        n := rt.TermRead(io, t[0], mut b[..])
        Assert(TmKeys(Trunc[int](n.value)) == 1 && TmUnit(b[..], 0, Target.os == core.Os.Windows) == u32(b'k'))
        // Nothing queued: a deadlock, cut at the deadline.
        scope sc2 := io.Scope() {
            sc2.CancelAfter(time.Millis(3))
            r = rt.TermWait(io, t[0])
            expired = sc2.Expired()
        }
        Assert(r.err == core.SysErr.Canceled && expired)
        rt.Abandon(t[0])
        rt.Abandon(t[1])
        s.Free()
    }
}

// TmLog is a reader task's record of one run: each wait's outcome (1 a
// key read, 2 cut) and the keys' units in order.
struct TmLog {
    events: [256]u8
    nev: int
    units: [64]u32
    nunit: int
}

// TmReader reads keys from the terminal input h until want have come, each
// wait cut by a 1 ms deadline (a cut wait loses nothing), logging
// every outcome.
TmReader(io: Io, h: uint, want: int, log: &raw.Atomic[u64], out: mut TmLog) {
    records := Target.os == core.Os.Windows
    for out.nunit < want && out.nev < 256 {
        var r := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
        scope sc := io.Scope() {
            sc.CancelAfter(time.Millis(1))
            r = rt.TermWait(io, h)
        }
        if r.err != core.SysErr.None {
            out.events[out.nev] = 2
            out.nev += 1
            continue
        }
        var b: [200]u8 = [0; 200]
        n := rt.TermRead(io, h, mut b[..])
        out.events[out.nev] = 1
        out.nev += 1
        for k in 0..TmKeys(Trunc[int](n.value)) {
            if out.nunit < 64 {
                out.units[out.nunit] = TmUnit(b[..], k, records)
                out.nunit += 1
            }
        }
    }
    log.Store(Cast[u64](out.nunit))
}

// TmTicker sleeps in steps, so the run has a second task whose choices
// interleave with the reader's.
TmTicker(io: Io) {
    for _ in 0..40 {
        io.Sleep(time.Micros(700)) else {
            return
        }
    }
}

// TmRun runs one seed: keys queued at 0, 3, 3, 9 and 20 ms, read by a task
// whose every wait a 1 ms deadline cuts, beside a ticker.
TmRun(sys: &System, seed: u64, out: mut TmLog): bool {
    var s := Sim.New(sys.arena, seed) else {
        return false
    }
    io := s.Io()
    var t: [2]uint = [0; 2]
    if !TmOpen(io, mut t) {
        s.Free()
        return false
    }
    var ok := true
    s.TermInput("ab".AsBytes(), time.Millis(0)) else {
        ok = false
    }
    s.TermInput("c".AsBytes(), time.Millis(3)) else {
        ok = false
    }
    s.TermInput("de".AsBytes(), time.Millis(3)) else {
        ok = false
    }
    s.TermInput("f".AsBytes(), time.Millis(9)) else {
        ok = false
    }
    s.TermInput("gh".AsBytes(), time.Millis(20)) else {
        ok = false
    }
    got := raw.Atomic[u64].New(0)
    scope g := io.Scope() {
        g.go TmReader(io, t[0], 8, &got, out)
        g.go TmTicker(io)
    }
    rt.Abandon(t[0])
    rt.Abandon(t[1])
    s.Free()
    return ok && got.Load() == 8
}

test "SD8-Sim-Term-Replay a task's waits for keys, each cut by a 1 ms deadline, lose no key, and each seed's run is the same twice (seeds 0 to 7)" (sys: &System) {
    keys := "abcdefgh".AsBytes()
    for seed in 0..8 {
        var a := TmLog(events: [0; 256], nev: 0, units: [0; 64], nunit: 0)
        var b := TmLog(events: [0; 256], nev: 0, units: [0; 64], nunit: 0)
        Assert(TmRun(sys, Cast[u64](seed), mut a))
        Assert(TmRun(sys, Cast[u64](seed), mut b))
        Assert(a.nunit == 8 && a.nev == b.nev)
        var cuts := 0
        for k in 0..a.nev {
            Assert(a.events[k] == b.events[k])
            if a.events[k] == 2 {
                cuts += 1
            }
        }
        // The keys at 3, 9 and 20 ms come after cut waits.
        Assert(cuts >= 3)
        for k in 0..8 {
            Assert(a.units[k] == u32(keys[k]) && b.units[k] == a.units[k])
        }
    }
}

// TmSleeper sleeps 20 ms: a task of the run while main waits.
TmSleeper(io: Io) {
    io.Sleep(time.Millis(20)) else {}
}

test "SD8-Sim-Term-MainWaits main waits for input while a task of the run sleeps: a 5 ms deadline cuts the wait at 5 ms, the key kept, and the next wait ends at its arrival, 8 ms" (sys: &System) {
    var s := Sim.New(sys.arena, 4) else {
        Assert(false)
        return
    }
    io := s.Io()
    var t: [2]uint = [0; 2]
    Assert(TmOpen(io, mut t))
    s.TermInput("k".AsBytes(), time.Millis(8)) else {
        Assert(false)
    }
    start := time.Now(io)
    var r1 := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
    var r2 := r1
    var cut: i64 = 0
    var took: i64 = 0
    var expired := false
    scope g := io.Scope() {
        g.go TmSleeper(io)
        scope sc := io.Scope() {
            sc.CancelAfter(time.Millis(5))
            r1 = rt.TermWait(io, t[0])
            expired = sc.Expired()
        }
        cut = time.Now(io).Since(start).Nanos()
        r2 = rt.TermWait(io, t[0])
        took = time.Now(io).Since(start).Nanos()
    }
    Assert(r1.err == core.SysErr.Canceled && expired && cut >= 5_000_000 && cut < 5_010_000)
    Assert(r2.err == core.SysErr.None && took >= 8_000_000 && took < 8_010_000)
    var b: [40]u8 = [0; 40]
    Assert(TmKeys(Trunc[int](rt.TermRead(io, t[0], mut b[..]).value)) == 1)
    rt.Abandon(t[0])
    rt.Abandon(t[1])
    s.Free()
}
