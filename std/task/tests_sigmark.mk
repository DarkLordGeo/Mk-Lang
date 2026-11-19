// Why: a plain signal wait (core.IoSignal) takes its baseline at its call and
// misses a delivery that lands before it; marks close that window.
// What: the RT-Signal-Mark* blocks over core.IoSignalMark and core.IoSignalFrom
// (SmFrom, SmMark).
// How: IoSignalMark takes signal s's delivery count and IoSignalFrom waits
// until the count is past it, at any carrier count. As in tests_signal.mk the
// blocks use Hangup (2), which only SignalRaise moves in a test.

import std.core
import std.raw

// SmFrom waits for Hangup from mark and adds 1 to ok on a delivery, 1 to
// canceled on Canceled.
SmFrom(io: Io, mark: u64, ok: &raw.Atomic[u32], canceled: &raw.Atomic[u32]) {
    r := core.IoSignalFrom(io, 2, mark)
    if r.err == core.SysErr.None {
        _ = ok.Add(1)
    } else if r.err == core.SysErr.Canceled {
        _ = canceled.Add(1)
    }
}

// SmMark is Hangup's mark; the block fails when the Io refuses it.
SmMark(io: Io): u64 {
    m := core.IoSignalMark(io, 2)
    Assert(m.err == core.SysErr.None)
    return Trunc[u64](m.value)
}

test "RT-Signal-Mark a delivery after the mark and before the wait ends the wait at once, which a plain wait misses; a mark is no cancellation point" (sys: &System) {
    io := sys.io
    m := SmMark(io)
    // The mark installs the watch: Hangup's watched bit is set once it
    // returns (IoSignalMark's doc), so every delivery after it is counted.
    Assert((Sig.watched.Load() & (Trunc[u32](1) << 2)) != 0)
    SignalRaise(2)
    // The plain wait takes the count at its call, raise included: it
    // parks until the deadline (the gap of request 17).
    var plain := core.SysErr.None
    scope s := io.Scope() {
        s.CancelAfter(core.DurationOf(50_000_000))
        plain = core.IoSignal(io, 2).err
    }
    Assert(plain == core.SysErr.Canceled)
    // The wait from the mark sees the raise.
    var from := core.SysErr.Canceled
    scope s2 := io.Scope() {
        s2.CancelAfter(core.DurationOf(5_000_000_000))
        from = core.IoSignalFrom(io, 2, m).err
    }
    Assert(from == core.SysErr.None)
    // Three raises after a mark, one wait: it ends.
    m2 := SmMark(io)
    SignalRaise(2)
    SignalRaise(2)
    SignalRaise(2)
    Assert(core.IoSignalFrom(io, 2, m2).err == core.SysErr.None)
    // A mark taken after the raises: nothing since, so the wait parks
    // until the deadline.
    m3 := SmMark(io)
    var none_ := core.SysErr.None
    scope s3 := io.Scope() {
        s3.CancelAfter(core.DurationOf(50_000_000))
        none_ = core.IoSignalFrom(io, 2, m3).err
    }
    Assert(none_ == core.SysErr.Canceled)
    // In a cancelled scope and on a masked Io the mark still answers; the
    // wait is Canceled at once in the cancelled scope.
    var mc := core.IoResult(value: 0, err: core.SysErr.Other, os: 0)
    var fc := core.SysErr.None
    scope s4 := io.Scope() {
        s4.Cancel()
        mc = core.IoSignalMark(io, 2)
        fc = core.IoSignalFrom(io, 2, Trunc[u64](mc.value)).err
    }
    Assert(mc.err == core.SysErr.None)
    Assert(fc == core.SysErr.Canceled)
    Assert(core.IoSignalMark(core.IoMasked(io), 2).err == core.SysErr.None)
    // The marks only grow.
    Assert(Trunc[u64](mc.value) >= m3)
    Assert(m3 >= m2 + 3)
}

test "RT-Signal-MarkBad the marks are Unsupported on an Io that is not the native table's, Other for a signal outside the five, Unsupported for Resize and Continue on Windows" (sys: &System) {
    // HkIo's table is not the native one, though its signal entry is
    // NativeSignal: core answers before any entry.
    Assert(core.IoSignalMark(HkIo(), 2).err == core.SysErr.Unsupported)
    Assert(core.IoSignalFrom(HkIo(), 2, 0).err == core.SysErr.Unsupported)
    Assert(core.IoSignalMark(sys.io, 5).err == core.SysErr.Other)
    Assert(core.IoSignalMark(sys.io, 16).err == core.SysErr.Other)
    Assert(core.IoSignalFrom(sys.io, 5, 0).err == core.SysErr.Other)
    Assert(core.IoSignalFrom(sys.io, 0xFFFF_FFFF, 0).err == core.SysErr.Other)
    if PWin() {
        Assert(core.IoSignalMark(sys.io, 3).err == core.SysErr.Unsupported)
        Assert(core.IoSignalMark(sys.io, 4).err == core.SysErr.Unsupported)
        Assert(core.IoSignalFrom(sys.io, 3, 0).err == core.SysErr.Unsupported)
    }
    // A plain wait's s is unchanged: both mark bits set is no mark.
    Assert(core.IoSignal(sys.io, 0xFFFF_FFFF).err == core.SysErr.Other)
    Assert(core.IoSignal(sys.io, 0xC000_0002).err == core.SysErr.Other)
    // A marked s through the plain wait is refused before any entry (the
    // native entry would answer it as a mark, with err None). Only the
    // mark bit is asserted: the wait bit there would park, not fail.
    Assert(core.IoSignal(sys.io, core.IoSigMark | 2).err == core.SysErr.Other)
}

test "RT-Signal-MarkFrom the whole mark is rebuilt from its low 26 bits and a later count, across a wrap of the low bits" (sys: &System) {
    low: u64 = 0x3FF_FFFF
    // No delivery since the mark.
    Assert(MarkFrom(5, 5) == 5)
    Assert(MarkFrom(0, 0) == 0)
    // Five deliveries since a mark whose low bits were about to wrap.
    mark: u64 = (u64(7) << 26) + low - 1
    Assert(MarkFrom(mark + 5, mark & low) == mark)
    // The largest distance that is exact.
    Assert(MarkFrom(mark + low, mark & low) == mark)
    // A count far above 1 << 26.
    big: u64 = (u64(1) << 40) + 12_345
    Assert(MarkFrom(big + 1_000, big & low) == big)
}

test "RT-Signal-MarkRace 300 rounds: 16 waiters from one mark, raised as they start, all see the raise" (sys: &System) {
    var total: u32 = 0
    for _ in 0..300 {
        ok := raw.Atomic[u32].New(0)
        canceled := raw.Atomic[u32].New(0)
        m := SmMark(sys.io)
        scope s := sys.io.Scope() {
            // A waiter that missed the raise would wait for good: the
            // deadline turns that into a count the block checks.
            s.CancelAfter(core.DurationOf(10_000_000_000))
            for _ in 0..16 {
                s.go SmFrom(sys.io, m, &ok, &canceled)
            }
            SignalRaise(2)
        }
        Assert(canceled.Load() == 0)
        Assert(ok.Load() == 16)
        total += ok.Load()
    }
    Assert(total == 4_800)
}
