// Why: a cancel that lands at an I/O edge must neither lose bytes nor leak a
// handle nor leave a system call waiting on a dead task.
// What: RT-Os-Foreign (std/rt/directio.mk), RT-Os-Proc-WriteCancel, and the
// lookup blocks RT-Os-Resolve-* and RT-Resolve-* (std/sys/windows/resolve.mk).
// How: cancels land before, during and after each operation; run by the
// scheduler harness at 1 to 8 carriers, dev and release, both Linux backends.

import std.core
import std.raw
import std.rt
import std.sys

// XfSeen counts the foreign closes XfClose saw and keeps the last handle.
struct XfCount {
    n: raw.Atomic[u32]
    last: raw.Atomic[u64]
}

var XfSeen: XfCount = XfCount(n: raw.Atomic[u32].New(0), last: raw.Atomic[u64].New(0))

// XfClose is the test's ForeignOps.close: the record must still be valid
// and foreign when it runs.
XfClose(h: uint) {
    unsafe "h is the live record CloseRuntimeHandle is closing" {
        Assert((*rt.Rec(h)).kind == rt.KindForeign && (*rt.Rec(h)).os == 0x5EED)
    }
    _ = XfSeen.n.Add(1)
    XfSeen.last.Store(u64(h))
}

// XfSignal is the test's ForeignOps.signal: these records are no
// children.
XfSignal(h: uint, sig: u32): core.IoResult {
    return core.IoResult(value: 0, err: core.SysErr.Unsupported, os: 0)
}

const XfOps = rt.ForeignOps(close: XfClose, signal: XfSignal)

// XfNew makes a foreign record, released or not, with ops or with none.
XfNew(released: bool, ops: bool): uint {
    h := rt.NewSocketHandle(0, 0)
    Assert(h != 0)
    unsafe "h is the record NewSocketHandle just made; no OS handle is behind it" {
        r := rt.Rec(h)
        (*r).kind = rt.KindForeign
        (*r).async = false
        (*r).released = released
        (*r).os = 0x5EED
        if ops {
            (*r).dir = raw.PtrCast[rt.ForeignOps, u8](raw.Of(&XfOps))
        }
    }
    return h
}

test "RT-Os-Foreign a foreign record's close reaches its ops once, by close and by Abandon, never the OS" (sys: &System) {
    base := XfSeen.n.Load()
    var want := base
    for round in 0..50 {
        for k in 0..4 {
            h := XfNew(k >= 2, true)
            if k % 2 == 0 {
                Assert(NativeClose(sys.io, h).err == core.SysErr.None)
            } else {
                rt.Abandon(h)
            }
            want += 1
            Assert(XfSeen.n.Load() == want)
            Assert(XfSeen.last.Load() == u64(h))
        }
        // No ops: the record is freed and nothing is called; os 0x5EED
        // is never given to the OS (a close of it would fail).
        g := XfNew(false, false)
        Assert(NativeClose(sys.io, g).err == core.SysErr.None)
        rt.Abandon(XfNew(true, false))
        Assert(XfSeen.n.Load() == want)
        if round == 0 {
            Assert(want == base + 4)
        }
    }
}

// XwChild is the Proc-WriteCancel block's child: it reads its standard input
// to the end, 16 KiB at a time with a 0.2 ms pause after each read (a slow
// reader, so the parent's large write stays pending), then writes the count
// it read as 8 little-endian bytes to its standard output and exits 0.
XwChild(io: Io): never {
    var buf: [16384]u8 = [0; 16384]
    var n: u64 = 0
    for {
        r := sys.ReadSync(sys.StdHandle(0), mut buf[..], -1)
        if r.err != core.SysErr.None || r.value <= 0 {
            break
        }
        n += Trunc[u64](r.value)
        _ = NativeSleep(io, 200_000)
    }
    var out: [8]u8 = [0; 8]
    for i in 0..8 {
        out[i] = Trunc[u8](n >> Trunc[u64](8 * i))
    }
    _ = sys.WriteSync(sys.StdHandle(1), out[..], -1)
    sys.Exit(0)
}

// XwWrite writes the n bytes at p to h until all went or a write failed,
// as a feeder does: sent counts the bytes the writes answered; st 1 when
// all went, 2 when a write was Canceled, 3 any other error.
XwWrite(io: Io, h: uint, p: uint, n: int, sent: &raw.Atomic[u64], st: &raw.Atomic[u32]) {
    var k := 0
    for k < n {
        var w := core.IoResult(value: 0, err: core.SysErr.None, os: 0)
        unsafe "p holds n bytes for the caller's scope" {
            w = NativeWrite(io, h, raw.Slice(raw.FromAddr[u8](p + Trunc[uint](k)), n - k), -1)
        }
        if w.err == core.SysErr.Canceled {
            st.Store(2)
            return
        }
        if w.err != core.SysErr.None || w.value <= 0 {
            st.Store(3)
            return
        }
        k += Trunc[int](w.value)
        sent.Store(Trunc[u64](k))
    }
    st.Store(1)
}

const XwBytes: int = 2_097_152

test "RT-Os-Proc-WriteCancel a cancelled write to a child's pipe answers exactly the bytes the child got" (sys: &System) {
    if sys.args.Len() == 3 && PSame(sys.args[2], "wcancel-child") {
        XwChild(sys.io)
    }
    p := rt.HeapAlloc(XwBytes, 16)
    Assert(!raw.IsNull(p))
    unsafe "p holds XwBytes bytes until the HeapFree below" {
        raw.Fill(p, 97, XwBytes)
    }
    var counts: [3]i64 = [0; 3]
    for round in 0..12 {
        var c := PStdio(core.IoStdPipe, core.IoStdPipe, core.IoStdInherit)
        var a: [3]str = ["", sys.args[1], "wcancel-child"]
        r := NativeSpawn(sys.io, a[..], mut c)
        Assert(r.err == core.SysErr.None && c.pipes[0] != 0 && c.pipes[1] != 0)
        sent := raw.Atomic[u64].New(0)
        st := raw.Atomic[u32].New(0)
        // The cancel lands before the write starts (round 0), while the
        // child drains it (most rounds), or after it finished.
        scope s := sys.io.Scope() {
            s.go XwWrite(sys.io, c.pipes[0], raw.Addr(p), XwBytes, &sent, &st)
            _ = NativeSleep(sys.io, i64(round) * 4_000_000)
            s.Cancel()
        }
        g := st.Load()
        Assert(g == 1 || g == 2)
        want := sent.Load()
        if g == 1 {
            Assert(want == u64(XwBytes))
            counts[2] += 1
        } else if want == 0 {
            counts[0] += 1
        } else {
            counts[1] += 1
        }
        PClose(sys.io, c.pipes[0])
        var got: [8]u8 = [0; 8]
        Assert(PRead(sys.io, c.pipes[1], mut got[..]) == 8)
        var n: u64 = 0
        for i in 0..8 {
            n |= u64(got[i]) << Trunc[u64](8 * i)
        }
        // Data wins: every write that moved bytes said so, and the
        // Canceled one moved none, so the child got exactly what the writes
        // answered (else 2 MiB fed again after a cancel could come back as 4).
        if n != want {
            FNote("write-cancel round", i64(round))
            FNote("write-cancel state", i64(g))
            FNote("write-cancel answered", Trunc[i64](want))
            FNote("write-cancel child got", Trunc[i64](n))
        }
        Assert(n == want)
        h := Trunc[uint](r.value)
        Assert(PWait(sys.io, h) == 0)
        PClose(sys.io, c.pipes[1])
        PClose(sys.io, h)
    }
    rt.HeapFree(p, XwBytes, 16)
    FNote("write-cancel rounds canceled at once", counts[0])
    FNote("write-cancel rounds canceled midway", counts[1])
    FNote("write-cancel rounds all written", counts[2])
}

// XrAsk looks name up and stores 1 Canceled, 2 NotFound, 3 an answer, 4
// anything else.
XrAsk(io: Io, name: str, got: &raw.Atomic[u32]) {
    var out: [4]core.IoAddr = [core.IoAddr(ip: [0; 16], port: 0, v6: false, zone: 0); 4]
    r := NativeResolveAll(io, name, mut out[..])
    if r.err == core.SysErr.Canceled {
        got.Store(1)
    } else if r.err == core.SysErr.NotFound {
        got.Store(2)
    } else if r.err == core.SysErr.None {
        got.Store(3)
    } else {
        got.Store(4)
    }
}

// XrHandles is sys.HandleCount (the blocks' parameter is named sys).
XrHandles(): int {
    return sys.HandleCount()
}

// XrRounds runs rounds lookups of fresh .invalid names, each cancelled
// after 0 to 7 ms (before the submission, and while the lookup waits): the
// number that answered Canceled. Any other answer than Canceled or a fast
// NotFound fails. base makes the names differ between calls and runs.
XrRounds(io: Io, rnd: []u8, base: int, rounds: int): int {
    // mkxlk-<16 hex>.invalid, which the system resolver gives up on after
    // about 11 s (measured), so no negative cache answers it.
    var nb: [30]u8 = [0; 30]
    pre := "mkxlk-".AsBytes()
    for i in 0..6 {
        nb[i] = pre[i]
    }
    suf := ".invalid".AsBytes()
    for i in 0..8 {
        nb[22 + i] = suf[i]
    }
    var cancelled := 0
    for round in 0..rounds {
        for i in 0..16 {
            d := (rnd[(round + i) % rnd.Len()] +% Trunc[u8](base + round * 7 + i)) & 15
            if d < 10 {
                nb[6 + i] = 48 + d
            } else {
                nb[6 + i] = 87 + d
            }
        }
        var name := ""
        unsafe "nb outlives every use of the name in this round" {
            name = raw.Str(raw.Data(nb[..]), 30)
        }
        got := raw.Atomic[u32].New(0)
        scope s := io.Scope() {
            s.go XrAsk(io, name, &got)
            _ = NativeSleep(io, i64(rnd[round % rnd.Len()] % 8) * 1_000_000)
            s.Cancel()
        }
        g := got.Load()
        Assert(g == 1 || g == 2)
        if g == 1 {
            cancelled += 1
        }
    }
    return cancelled
}

test "RT-Os-Resolve-Cancel a lookup the system would hold for seconds ends at its cancel" (sys: &System) {
    // Linux answers an .invalid name with no query (RFC 6761), and its
    // nameserver exchange has the Dns-Cancel block.
    if !PWin() {
        return
    }
    var rnd: [24]u8 = [0; 24]
    _ = NativeRandom(sys.io, mut rnd[..])
    one := raw.Atomic[u32].New(0)
    XrAsk(sys.io, "localhost.", &one)
    Assert(one.Load() == 3)
    // No handle count is judged: every cancelled lookup leaves about four
    // handles with the system resolver while its query runs on (a Go probe
    // of GetAddrInfoExW alone, 32 cancels at a time: 163, 287, 413, 544,
    // 668 handles, the same 15 s later; after a 20 s pause 64 more added
    // 16). The runtime's own path makes no handle (a page and the system's list,
    // both freed in ResolveFinish on every answer).
    // The backlog is bounded: the
    // harness runs this block at 1, 2, 4 and 8 carriers, dev and release,
    // and every cancelled lookup goes on in the system resolver for about
    // 11 s. At 32 lookups a run, eight runs left about 256 queries behind,
    // and a later run's lookups (the cancel's completion, or localhost.)
    // waited behind them past the 60 s watchdog.
    // 8 a run (64 in all) still cancel both before the submission and
    // while the lookup waits (0 to 7 ms), on both halves of the handle
    // count.
    t0 := NativeNow(sys.io).value
    c1 := XrRounds(sys.io, rnd[..], 0, 4)
    h0 := XrHandles()
    c2 := XrRounds(sys.io, rnd[..], 1000, 4)
    h1 := XrHandles()
    // 8 lookups the system here holds for about 11 s each are over in well
    // under 10 s (the pool's GetAddrInfoW took the 60 s watchdog).
    // How many were cancelled depends on the network (an upstream that
    // answers NXDOMAIN at once ends them first), so it is printed, not
    // judged: on a development machine every one is.
    Assert(NativeNow(sys.io).value - t0 < 10_000_000_000)
    // A lookup that is not cancelled still answers through the port.
    two := raw.Atomic[u32].New(0)
    XrAsk(sys.io, "localhost.", &two)
    Assert(two.Load() == 3)
    FNote("resolve-cancel rounds cancelled of 8", i64(c1 + c2))
    FNote("resolve-cancel handle drift", i64(h1 - h0))
}

test "RT-Os-Resolve-RoutineNoWait the lookup routine never waits for a cancel inside GetAddrInfoExCancel" (sys: &System) {
    // The routine, run on its own thread while the word
    // is cancelling, returns and leaves the packet to the canceller
    // (XrProbe). A routine that spun until the cancel call returned would
    // deadlock if that call waits for a routine already running. Linux has no
    // routine (0).
    if !PWin() {
        Assert(XrProbe() == 0)
        return
    }
    for _ in 0..20 {
        Assert(XrProbe() == 1)
    }
}

// XrProbe is sys.ResolveProbe (the blocks' parameter is named sys).
XrProbe(): u32 {
    return sys.ResolveProbe()
}

test "RT-Resolve-OneCancel a lookup gets at most one cancel call however many cancels come" (sys: &System) {
    // Two ResolveCancels of a lookup whose routine has not run (Await's own
    // PollCancel and a cancel walk's may both come): a word that went back
    // to armed after the first call would let the second call
    // GetAddrInfoExCancel again on a handle whose lookup may be over.
    if !PWin() {
        Assert(XrCancelProbe() == 0)
        return
    }
    Assert(XrCancelProbe() == 1)
}

test "RT-Resolve-RoutineRace the routine and the cancel's tail post exactly once in every interleaving" (sys: &System) {
    // The routine on its own thread, the cancel's tail on this one, 50,000
    // rounds with delays that shift by round: a routine that returned after
    // a lost Cas (no loop) loses the packet, and the task would hang.
    if !PWin() {
        Assert(XrRaceProbe(1) == 0)
        return
    }
    Assert(XrRaceProbe(50_000) == 1)
}

// XrCancelProbe and XrRaceProbe are sys's probes (the blocks' parameter is
// named sys).
XrCancelProbe(): u32 {
    return sys.ResolveCancelProbe()
}

XrRaceProbe(rounds: int): u32 {
    return sys.ResolveRaceProbe(rounds)
}
