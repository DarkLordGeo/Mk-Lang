// Why: Every task updates server counters, and reading them must never wait.
//
// What: Traffic, Stats, the flag bits and the Running and Draining states.
//
// How: Each counter is one atomic word; Connected raises the peak with a weak
// compare-exchange loop. Traffic is the one compound record, kept under a spin
// lock.

import std.atomic

/// Bytes and frames written to clients. Kept under a spin lock: every
/// update is a few additions, shorter than any suspension would be.
export struct Traffic {
    export frames: i64
    export bytes: i64
    export lines: i64
}

/// Server flags, one bit each, in `Stats.flags`.
export const FlagMotdSet: i64 = 1
export const FlagDraining: i64 = 2
export const FlagBotsReady: i64 = 4

/// The values of `Stats.state`.
export const Running: i64 = 0
export const Draining: i64 = 1

/// Counters every task updates. Each one is a single atomic word, so readers
/// never wait; `traffic` is the one compound record.
export struct Stats {
    /// Room messages accepted.
    export messages: atomic.AtomicInt
    /// Clients dropped for falling behind.
    export dropped: atomic.AtomicInt
    /// Frames queued and never written, counted at shutdown.
    export undelivered: atomic.AtomicInt
    /// Messages refused by the rate limit.
    export limited: atomic.AtomicInt
    /// Set once shutdown has begun.
    export shutting: atomic.AtomicBool
    /// The next connection number; only uniqueness matters, so Relaxed.
    export next_id: atomic.Atomic[i64]
    /// Connections open now.
    export online: atomic.Atomic[i64]
    /// Web requests holding a bridge now; shutdown waits for them as it
    /// does for connections, so each gets the closing notice.
    export web: atomic.Atomic[i64]
    /// The most connections open at once.
    export peak: atomic.Atomic[i64]
    /// `FlagMotdSet` and the others.
    export flags: atomic.Atomic[i64]
    /// `Running` or `Draining`; moves once, by compare-exchange.
    export state: atomic.Atomic[i64]
    export traffic: atomic.Spin[Traffic]
}

/// Zeroed counters. O(1).
export Stats.New(): Stats {
    return Stats(
        messages: atomic.AtomicInt.New(0),
        dropped: atomic.AtomicInt.New(0),
        undelivered: atomic.AtomicInt.New(0),
        limited: atomic.AtomicInt.New(0),
        shutting: atomic.AtomicBool.New(false),
        next_id: atomic.Atomic[i64].New(1),
        online: atomic.Atomic[i64].New(0),
        web: atomic.Atomic[i64].New(0),
        peak: atomic.Atomic[i64].New(0),
        flags: atomic.Atomic[i64].New(0),
        state: atomic.Atomic[i64].New(Running),
        traffic: atomic.Spin[Traffic].New(Traffic(frames: 0, bytes: 0, lines: 0)),
    )
}

/// Counts a connection in and raises `peak` to match. Returns the
/// connection's number. Lock-free: a weak compare-exchange loop.
export Stats.Connected(&self): i64 {
    id := self.next_id.FetchAdd(1, atomic.Relaxed)
    now := self.online.FetchAdd(1, atomic.AcqRel) + 1
    var seen := self.peak.Load(atomic.Acquire)
    for seen < now {
        r := self.peak.CompareExchangeWeak(seen, now, atomic.AcqRel, atomic.Acquire)
        if r.ok {
            break
        }
        seen = r.value
    }
    return id
}

/// Counts a connection out.
export Stats.Disconnected(&self) {
    _ = self.online.FetchSub(1, atomic.AcqRel)
}

/// Adds one write of `frames` frames and `bytes` bytes.
export Stats.Wrote(&self, frames: int, bytes: int) {
    var g := self.traffic.Lock()
    t := g.Mut()
    t.frames += i64(frames)
    t.bytes += i64(bytes)
    t.lines += 1
    g.Unlock()
}

/// A copy of the traffic so far.
export Stats.Snapshot(&self): Traffic {
    g := self.traffic.Lock()
    t := *g.Ref()
    g.Unlock()
    return t
}

/// Zeroes the traffic counters and returns what they held.
export Stats.ResetTraffic(&self): Traffic {
    var g := self.traffic.Lock()
    t := *g.Ref()
    g.Store(Traffic(frames: 0, bytes: 0, lines: 0))
    g.Unlock()
    return t
}

/// Moves `state` from `Running` to `Draining`. True for the one caller that
/// made the move; the flag word records it too.
export Stats.BeginDrain(&self): bool {
    r := self.state.CompareExchange(Running, Draining, atomic.SeqCst, atomic.SeqCst)
    if !r.ok {
        return false
    }
    _ = self.flags.FetchOr(FlagDraining, atomic.AcqRel)
    self.shutting.Store(true)
    atomic.Fence(atomic.SeqCst)
    return true
}

/// Whether shutdown has begun, as a task polling between waits sees it.
export Stats.Draining(&self): bool {
    return self.state.Load(atomic.SeqCst) == Draining || self.shutting.Load()
}

/// Sets or clears one flag bit. `on` sets it with an or, off clears it with
/// an and of the complement; `Toggle` flips it.
export Stats.Flag(&self, bit: i64, on: bool) {
    if on {
        _ = self.flags.FetchOr(bit, atomic.Release)
    } else {
        _ = self.flags.FetchAnd(~bit, atomic.Release)
    }
}

/// Flips one flag bit and returns whether it is now set.
export Stats.Toggle(&self, bit: i64): bool {
    old := self.flags.FetchXor(bit, atomic.AcqRel)
    return old & bit == 0
}

/// The flag word, read with Acquire so a set bit's writes are visible.
export Stats.FlagWord(&self): i64 {
    return self.flags.Load(atomic.Acquire)
}

/// The final traffic, taking the spin lock apart.
export Stats.Into(self): Traffic {
    Stats(messages: _, dropped: _, undelivered: _, limited: _, shutting: _, next_id: _, online: _, web: _, peak: _, flags: _, state: _, traffic) := self
    return traffic.Into()
}

/// The connection counter, read Relaxed: a figure for a report, not a
/// synchronisation point.
export Stats.Connections(&self): i64 {
    return self.next_id.Load(atomic.Relaxed) - 1
}

/// Resets the connection numbering for a fresh run, Relaxed like the count.
export Stats.Renumber(&self) {
    self.next_id.Store(1, atomic.Relaxed)
    _ = self.peak.Swap(self.online.Load(atomic.Relaxed), atomic.SeqCst)
    self.state.Store(Running, atomic.SeqCst)
    self.messages.Store(0)
    atomic.Fence(atomic.Release)
}

/// One memory order and what it may be used for, as the drill prints them.
export struct OrderUse {
    export name: str
    export load: bool
    export store: bool
    export fence: bool
}

/// The five orders with what each allows. A table the operator can check
/// against the C++20 rules the server's atomics follow.
export Orders(): [5]OrderUse {
    return [
        Use("relaxed", atomic.Relaxed),
        Use("acquire", atomic.Acquire),
        Use("release", atomic.Release),
        Use("acq_rel", atomic.AcqRel),
        Use("seq_cst", atomic.SeqCst),
    ]
}

Use(name: str, o: atomic.Order): OrderUse {
    return OrderUse(name: name, load: o.CanLoad(), store: o.CanStore(), fence: o.CanFence())
}

/// Reads `a` with the order `o`, which must allow a load.
export LoadWith(a: &atomic.Atomic[i64], const o: atomic.Order): i64
    requires o.CanLoad() {
    atomic.Fence(atomic.Acquire)
    return a.Load(o)
}

/// Writes `x` to `a` with the order `o`, which must allow a store.
export StoreWith(a: &atomic.Atomic[i64], x: i64, const o: atomic.Order)
    requires o.CanStore() {
    a.Store(x, o)
    atomic.Fence(atomic.AcqRel)
}
