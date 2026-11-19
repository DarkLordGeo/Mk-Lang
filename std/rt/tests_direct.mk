// Why: a panic's flush and Finish's error line use the direct table, which
// must answer every entry without a scheduler; the spinlock and Fatal sit
// on the same paths.
// What: tests of DirectTable's entries, TryLock, Fatal and Finish's error
// line.
// How: each entry is called directly: write writes, wait returns at once
// (a spurious return), wake does nothing, every other entry is Other.

import std.core
import std.raw

// DOther: r is the direct table's refusal.
DOther(r: core.IoResult): bool {
    return r.err == core.SysErr.Other && r.value == 0 && r.os == 0
}

test "RT-Direct-Table every direct entry answers without suspending: write writes, wait returns, the rest are Other" {
    io := DirectIo()
    // Bit 0 of the flags: NoCancel (the direct table is used while a scope
    // may be cancelled, by the panic path and Finish).
    Assert((core.IoFlags(io) & 1) == 1)
    var b: [4]u8 = [0; 4]
    Assert(DOther(DirectOpen(io, "x", 0)))
    Assert(DOther(DirectRead(io, 0, mut b[..], -1)))
    Assert(DOther(DirectHandle(io, 0)))
    Assert(DOther(DirectRename(io, "a", "b")))
    Assert(DOther(DirectPath(io, "a")))
    Assert(DOther(DirectRecv(io, 0, mut b[..])))
    Assert(DOther(DirectSend(io, 0, b[..])))
    Assert(DOther(DirectNone(io)))
    Assert(DOther(DirectSleep(io, 1)))
    Assert(DOther(DirectRandom(io, mut b[..])))
    Assert(DOther(DirectSignal(io, 0)))
    w := raw.Atomic[u32].New(5)
    unsafe "w lives in this frame" {
        // The word still holds the expected value: a real wait would park;
        // the direct one returns at once.
        r := DirectWait(io, raw.PtrCast[raw.Atomic[u32], u32](raw.Of(&w)), 5)
        Assert(r.err == core.SysErr.None)
        DirectWake(io, raw.PtrCast[raw.Atomic[u32], u32](raw.Of(&w)), true)
    }
    // An empty write through the runtime's standard error handle.
    unsafe "the RtState page" {
        e := (*Proc()).stderr.h
        r := DirectWrite(io, e, b[0..0], -1)
        Assert(r.err == core.SysErr.None && r.value == 0)
    }
    Assert(DOther(OtherResult()))
}

test "RT-Lock-TryLock TryLock takes a free lock once and never a held one" {
    l := raw.Atomic[u32].New(0)
    Assert(TryLock(&l))
    Assert(!TryLock(&l))
    Unlock(&l)
    Assert(TryLock(&l))
    Unlock(&l)
    Lock(&l)
    Assert(!TryLock(&l))
    Unlock(&l)
    Assert(l.Load() == 0)
}

test "RT-Fatal reports its message with no site and exits 101 [exit 101: panic: rt-fatal-check]" {
    // Fatal is what every start failure calls (cannot reserve task stacks,
    // and the like), which no test can provoke; its report is checked here.
    Fatal("rt-fatal-check")
}

test "Finish's error line is the error's name after error: and ends the line" {
    // Finish itself checks for leaks and exits, so the block checks the
    // line it writes when the entry did not write Main's (MainErrorWritten).
    var buf: [64]u8 = [0; 64]
    n := ErrorNameLine(mut buf[..], "src.Bad")
    line := str.From(buf[..n]) else {
        Panic("the line is not UTF-8")
    }
    Assert(line == "error: src.Bad\n")
    var small: [8]u8 = [0; 8]
    Assert(ErrorNameLine(mut small[..], "src.Bad") == 0)
}
