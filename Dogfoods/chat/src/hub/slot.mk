// Why: Each connection needs queues that other tasks can fill without waiting
// on a slow client.
//
// What: Msg (a shared frame), Ctl, Slot, and Slot.New, Deliver, Notice, Reset
// and Release.
//
// How: Slots are made once and reused. Room frames take a credit from a bounded
// queue, notices use a separate queue that takes no credit, and a client out of
// credit is asked to leave through its kicker channel.

import std.atomic
import std.sync
import src.proto

/// A shared, reference-counted frame: one allocation per broadcast, one
/// count per recipient.
export type Msg = sync.Shared[proto.Frame]

/// A request to a client's kicker task.
export enum Ctl {
    /// The client fell behind: its queue is full.
    Drop
    /// An operator asked for the client to go.
    Kick
}

/// What the server keeps for one connection. Slots are made once, at start,
/// and reused, so other tasks may hold `&Slot` for the life of the hub.
export struct Slot {
    /// Room traffic, at most `queue` frames: one credit each.
    export out: Chan[Msg]
    /// Server notices, which skip the credit count.
    export note: Chan[Msg]
    /// Requests to end the connection; see `Ctl`.
    export ctl: Chan[Ctl]
    /// Free room in `out`. A sender takes one before it sends, so a send never
    /// waits, and a client with no credit left is too slow.
    export credits: sync.Semaphore
    /// Messages the client may still send before the next refill.
    export rate: sync.Semaphore
    /// Rate permits taken since the last refill.
    export used: atomic.Atomic[i64]
    /// Set once when the client is found too slow, so one `Drop` is sent.
    export slow: atomic.AtomicBool
    /// Set while a connection owns the slot.
    export live: atomic.AtomicBool
}

/// A slot whose queues hold `queue` room frames. Fails with `OutOfMemory`
/// when a channel cannot be made. O(queue); allocates the three channels.
export Slot.New(a: &Alloc, queue: int, burst: int): OutOfMemory!Slot {
    out := try Chan[Msg].New(a, queue)
    onerr out.FreeEmpty(a)
    note := try Chan[Msg].New(a, 4)
    onerr note.FreeEmpty(a)
    ctl := try Chan[Ctl].New(a, 2)
    return Slot(
        out: out,
        note: note,
        ctl: ctl,
        credits: sync.Semaphore.New(queue),
        rate: sync.Semaphore.New(burst),
        used: atomic.Atomic[i64].New(0),
        slow: atomic.AtomicBool.New(false),
        live: atomic.AtomicBool.New(false),
    )
}

/// Frees the slot. Frames still queued were never delivered: each is
/// released and counted in `undelivered`. O(queue).
export Slot.Release(self, a: &Alloc, h: &Heap, undelivered: &atomic.AtomicInt) {
    Slot(out, note, ctl, credits: _, rate: _, used: _, slow: _, live: _) := self
    out.FreeEach(a, fn(m: Msg) {
        _ = undelivered.Add(1)
        m.Free(h)
    })
    note.FreeEach(a, fn(m: Msg) {
        _ = undelivered.Add(1)
        m.Free(h)
    })
    ctl.Free(a)
}

/// Queues a clone of `m` for this slot's client, taking one credit. Returns
/// false, keeping nothing, when the client has no credit left: it is too
/// slow, and the first such call asks its kicker to drop it.
export Slot.Deliver(&self, io: Io, h: &Heap, m: &Msg): bool *m into self {
    if !self.credits.TryAcquire(1) {
        if !self.slow.Swap(true) {
            // ctl holds two requests and only one Drop is sent per connection
            self.ctl.Send(io, Ctl.Drop) else {}
        }
        return false
    }
    c := m.Clone()
    self.out.Send(io, c) else {
        c.Free(h)
        self.credits.Release(1)
        return false
    }
    return true
}

/// Queues a clone of `m` as a notice, which takes no credit and is written
/// before room traffic. Returns false when the notice queue is closed or the
/// wait is cancelled.
export Slot.Notice(&self, io: Io, h: &Heap, m: &Msg): bool *m into self {
    c := m.Clone()
    self.note.Send(io, c) else {
        c.Free(h)
        return false
    }
    return true
}

/// Empties the slot after its connection ended, so the next connection
/// starts clean: queued frames are released, credits and rate permits given
/// back, flags cleared. Returns how many frames were never written. Runs
/// with `io.NoCancel()`, since the slot must be clean whatever happened.
export Slot.Reset(&self, io: Io, h: &Heap): int {
    var lost := 0
    for self.out.Ready() {
        m := self.out.Recv(io) else {
            break
        }
        m.Free(h)
        self.credits.Release(1)
        lost += 1
    }
    for self.note.Ready() {
        m := self.note.Recv(io) else {
            break
        }
        m.Free(h)
        lost += 1
    }
    for self.ctl.Ready() {
        _ = self.ctl.Recv(io) else {
            break
        }
    }
    taken := self.used.Swap(0, atomic.AcqRel)
    self.rate.Release(Cast[int](taken))
    self.slow.Store(false)
    self.live.Store(false)
    return lost
}
