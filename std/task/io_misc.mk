// Why: the native Io's entries that are not files or sockets (clock, sleep,
// randomness, yield, signals, address waits) all go through the scheduler
// rather than the poller or the pool, so they live together.
//
// What: NativeNow, NativeSleep, NativeRandom, NativeYield, NativeSignal with
// its scheduler side (SignalPoll, SignalMark, SignalFromMark, SignalWaitFrom),
// NativeWait and NativeWake. The socket entries are in io_net.mk.
//
// How: std.sys's signal handler only counts deliveries and wakes the poller;
// SignalPoll turns a moved count into a generation-word advance that wakes
// the tasks waiting on it. In a program with no task, NativeWait spins and
// sleeps on the calling thread.

import std.core
import std.raw
import std.rt
import std.sys

NativeNow(io: Io): core.IoResult {
    return NoErr(sys.Now())
}

// NativeSleep: the cancellation check comes first, even for nanos <= 0.
NativeSleep(io: Io, nanos: i64): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    if nanos > 0 {
        // The result is Canceled when the cancel won the race with the
        // timer, else None; level-triggered: the next
        // cancellable operation sees the flag.
        return ErrOf(SleepFor(nanos, Cancellable(io)))
    }
    return NoErr(0)
}

// NativeRandom: an unusable OS source is fatal, std has no error for it.
NativeRandom(io: Io, buf: mut []u8): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    if !sys.Random(buf) {
        rt.Fatal("cannot read the OS random source")
    }
    return NoErr(0)
}

// NativeYield: off a task stack it is panic 16, like every suspending
// entry; YieldNow's MustCurrent raises it.
NativeYield(io: Io): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    YieldNow()
    return NoErr(0)
}

// The scheduler side of signal waits. std.sys counts deliveries (its
// handler only adds to a counter and calls PollWake, both async-signal-safe,
// so it never touches a bucket lock a carrier may hold); this side turns a
// moved count into a wake: after every PollWait return, and on the timer
// tick, SignalPoll compares each watched signal's count with the count it
// last published and, when it moved, advances that signal's generation word
// and wakes every task parked on it. One word per signal, static data.
struct SigWait {
    // generation: advanced (then woken) once per published movement of the count.
    generation: [5]raw.Atomic[u32]
    // seen: the count the last advance published; only grows (Cas).
    seen: [5]raw.Atomic[u64]
    // watched: bit s once some task's SignalWatch(s) succeeded; SignalPoll
    // does nothing while it is 0.
    watched: raw.Atomic[u32]
    pad: u32
    // raised: deliveries raised by SignalRaise (tests only), added to the OS
    // count, so a block can deliver without an OS signal.
    raised: [5]raw.Atomic[u64]
}

var Sig: SigWait = SigWait(generation: [raw.Atomic[u32].New(0), raw.Atomic[u32].New(0), raw.Atomic[u32].New(0),
    raw.Atomic[u32].New(0), raw.Atomic[u32].New(0)],
    seen: [raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
        raw.Atomic[u64].New(0)],
    watched: raw.Atomic[u32].New(0), pad: 0,
    raised: [raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0), raw.Atomic[u64].New(0),
        raw.Atomic[u64].New(0)])

// SigGen is the address of signal s's generation word, for WaitWord.
SigGen(s: int): *u32 {
    unsafe "a static word" {
        return raw.PtrCast[raw.Atomic[u32], u32](raw.Of(&Sig.generation[s]))
    }
}

// SignalNow is signal s's delivery count: the OS count plus test raises.
// Both only grow, so the sum only grows (u64: no wrap in any process life).
SignalNow(s: int): u64 {
    // Edge: SignalRaise's Add precedes its PollWake, so a poll after that
    // wake reads at least this count (edge: raise -> SignalPoll).
    return sys.SignalCount(Trunc[u32](s)) +% Sig.raised[s].Load()
}

// SignalRaise delivers signal s as the OS handler does (count, then
// PollWake), from any thread, a foreign one included: tests only.
SignalRaise(s: int) {
    // seq_cst Add before PollWake: the poller loop's SignalNow after the
    // wake returns reads at least this count (edge: raise -> SignalPoll).
    _ = Sig.raised[s].Add(1)
    sys.PollWake()
}

// SignalPoll publishes every watched signal whose count moved: exactly one
// caller wins the seen Cas for a movement and advances and wakes; another
// carrier that read the same count finds seen equal and does nothing. On a
// carrier's system stack (c its carrier) or a task's; never parks.
SignalPoll(c: *Carrier) {
    // watched is seq_cst: NativeSignal stores its bit before it reads the
    // count baseline, and a delivery counted after that baseline is
    // followed by a PollWait return whose SignalPoll loads watched after
    // the count's Add (Add -> PollWake -> PollWait return), so it sees the
    // bit (edge: watch -> poll).
    w := Sig.watched.Load()
    if w == 0 {
        return
    }
    for s in 0..5 {
        if (w & (Trunc[u32](1) << Trunc[u32](s))) != 0 {
            now := SignalNow(s)
            for {
                // Edge: the Cas below orders the publications; this load only picks the
                // value to compare.
                seen := Sig.seen[s].Load()
                if now <= seen {
                    break
                }
                // seq_cst Cas, then the gen Add, then the bucket lock in
                // WakeWordOn: a waiter that read the old gen either reads the
                // new one under the bucket lock (WaitWord's recheck) or is
                // queued before the wake takes the lock (edge: advance ->
                // waiter).
                if Sig.seen[s].Cas(seen, now) {
                    _ = Sig.generation[s].Add(1)
                    WakeWordOn(c, SigGen(s), true)
                    break
                }
            }
        }
    }
}

// NativeSignal is the signal entry: the cancellation check, then SignalWatch(s) (first
// use installs the handler), then a wait, cancellable unless io is NoCancel,
// until s's count moves from its value at the call. None on a delivery,
// Canceled when the cancel won, Other when s is not a signal or the OS
// refused the watch (proc.WaitSignal's panic 8). Off a task stack it is panic
// 16, before any handler is installed. No allocation; the waiter is the
// task's own WaitRec. A marked s (core.IoSigMark, IoSigFrom) is
// SignalMark's or SignalFromMark's, before any of that.
NativeSignal(io: Io, s: u32): core.IoResult {
    if s >= core.IoSigFrom {
        if (s & core.IoSigMark) != 0 && (s & core.IoSigFrom) == 0 {
            return SignalMark(s & 0xF)
        }
        if (s & core.IoSigMark) == 0 {
            return SignalFromMark(io, s & 0xF, u64((s >> 4) & 0x3FF_FFFF))
        }
    }
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    _ = MustCurrent()
    if s > 4 {
        return ErrOf(core.SysErr.Other)
    }
    if !SignalWatchOn(s) {
        // Resize and Continue (3, 4) are Linux's (SIGWINCH, SIGCONT); a
        // Windows console reports a resize as an input record.
        return ErrOf(if s > 2 { core.SysErr.Unsupported } else { core.SysErr.Other })
    }
    // The baseline is read after the watched bit is set (SignalPoll's edge).
    i := int(s)
    return ErrOf(SignalWaitFrom(i, SignalNow(i), Cancellable(io)))
}

// SignalMark is core.IoSignalMark natively: s's watch as NativeSignal
// installs it, then the count, read after the watched bit is set
// (SignalPoll's edge), as value. No cancellation check, no wait and no task
// needed: Fill takes it on its own task before the size compare, so a
// delivery after the compare moves the count past the mark whenever the
// signal task's wait starts. The watch comes before
// the count: a delivery before the OS watch exists takes the default
// action and moves no count. That order matters only to a caller with no
// later check of its own (Fill's compare, after both, sees a resize that
// came before the watch).
SignalMark(s: u32): core.IoResult {
    if s > 4 {
        return ErrOf(core.SysErr.Other)
    }
    if !SignalWatchOn(s) {
        return ErrOf(if s > 2 { core.SysErr.Unsupported } else { core.SysErr.Other })
    }
    return core.IoResult(value: Trunc[i64](SignalNow(int(s))), err: core.SysErr.None, os: 0)
}

// SignalFromMark is core.IoSignalFrom natively: NativeSignal's wait with
// the mark as its baseline instead of the count at the call. low is the
// mark's low 26 bits; the count now is at least the mark and, while fewer
// than 1 << 26 deliveries came since, less than 1 << 26 past it, so now
// minus ((now - low) mod 1 << 26) is the mark itself.
SignalFromMark(io: Io, s: u32, low: u64): core.IoResult {
    if Stop(io) {
        return ErrOf(core.SysErr.Canceled)
    }
    _ = MustCurrent()
    if s > 4 {
        return ErrOf(core.SysErr.Other)
    }
    if !SignalWatchOn(s) {
        return ErrOf(if s > 2 { core.SysErr.Unsupported } else { core.SysErr.Other })
    }
    i := int(s)
    return ErrOf(SignalWaitFrom(i, MarkFrom(SignalNow(i), low), Cancellable(io)))
}

// MarkFrom is the whole mark whose low 26 bits are low, given now, a
// count read after the mark was taken: now - ((now - low) mod 1 << 26).
// Exact while now is less than 1 << 26 past the mark.
MarkFrom(now: u64, low: u64): u64 {
    return now -% ((now -% low) & core.IoSigMarkLow)
}

// SignalWatchOn installs the OS watch of s (s <= 4) and sets its watched
// bit; false when the OS refused.
SignalWatchOn(s: u32): bool {
    if !sys.SignalWatch(s) {
        return false
    }
    bit: u32 = Trunc[u32](1) << s
    // Edge: the bit is set after SignalWatch installed the OS watch, so a
    // SignalPoll that sees the bit polls a counter that is live.
    var old := Sig.watched.Load()
    for (old & bit) == 0 && !Sig.watched.Cas(old, old | bit) {
        old = Sig.watched.Load()
    }
    return true
}

// SignalWaitFrom waits until signal i's count is no longer base: None then,
// Canceled when cancellable and the cancel won. Spurious WaitWord returns
// loop.
SignalWaitFrom(i: int, base: u64, cancellable: bool): core.SysErr {
    for {
        // Lost wakeup, argued: the count is read again after the gen load.
        // Were it read only before, a delivery and its SignalPoll advance
        // landing between that read and the gen load would leave this task
        // waiting on the new gen with nobody left to advance it. With the
        // read after, an advance before the gen load means its count Add
        // came before this read too (Add -> seen Cas -> gen Add, all
        // seq_cst), so the read sees the movement; an advance after the gen
        // load changes the word WaitWord rechecks under the bucket lock, or
        // wakes the task queued there. The test "RT-MN-SignalRace" drives the window
        // (the jitter point sits in it).
        // Edge: the gen load, then the count read (comment above): edge advance
        // -> waiter, through WaitWord's compare under the bucket lock.
        g := Sig.generation[i].Load()
        Jitter(JitterSignal)
        if SignalNow(i) != base {
            return core.SysErr.None
        }
        e := WaitWord(SigGen(i), g, cancellable)
        if e == core.SysErr.Canceled {
            return core.SysErr.Canceled
        }
    }
}

// NativeWait backs Mutex.Lock and Chan: cancellable unless io is NoCancel.
NativeWait(io: Io, addr: *u32, expected: u32): core.IoResult {
    if rt.UsesTasks() {
        e := WaitWord(addr, expected, Cancellable(io))
        return ErrOf(e)
    }
    // The fallback of a program with no task: Main is the only Mk code running,
    // so only another OS thread (a c_sources library's) can change
    // the word. As Main parked as a task waits
    // for that thread's wake, Main here waits on the calling thread until
    // the word no longer holds expected: yields first, then sleeps from
    // 1 us doubling to 1 ms, so a wake is seen within 1 ms and a word that
    // never changes (a deadlock) waits for good, as a parked task does. No
    // cancellation: off a task nothing can cancel the wait (Main's Io).
    // Edge: AtomicAt is a seq_cst load; the other thread's store to the
    // word (before its WakeWord) is read by one of these loads, and what it
    // wrote before that store happens before the return.
    var spins := 0
    var nap: i64 = 1_000
    for AtomicAt(addr) == expected {
        if spins < 64 {
            spins += 1
            sys.ThreadYield()
        } else {
            sys.SleepNanos(nap)
            if nap < 1_000_000 {
                nap = nap * 2
            }
        }
    }
    return ErrOf(core.SysErr.None)
}

NativeWake(io: Io, addr: *u32, all: bool) {
    // With no task nobody is parked on any word; an off-task waiter
    // (NativeWait above) rereads the word itself.
    if rt.UsesTasks() {
        WakeWord(addr, all)
    }
}
