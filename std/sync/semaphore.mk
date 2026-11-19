// Why: limiting concurrent use of a resource (connections, memory, pool
// slots) needs a counting semaphore whose large requests are not starved by
// a stream of small ones.
//
// What: sync.Semaphore: New, Acquire(n), TryAcquire(n) and Release(n), a
// counting, weighted semaphore with a bounded wait. No allocation.
//
// How: one word holds the free permits and three mode bits; waiters queue in
// a FIFO of stack cells (mem_wait.mk). Normal mode lets callers barge; a
// waiter that has waited 1 ms switches to handoff mode, where permits go to
// it first.

// Algorithm: Go sync.Mutex's two modes (Go 1.9, starvation mode) applied
// to tokio's batch_semaphore (tokio/src/sync/batch_semaphore.rs). One word
// w: bit 0 Q (the queue holds a cell), bit 1 W (a waiter woken to retry is
// on its way), bit 2 S (handoff mode), the count of free permits in the
// other bits. The queue is a FIFO of cells on the waiters' stacks, under a
// spin lock (core.SpinCore, mem_wait.mk).
//
//   Normal mode (S clear): permits are in the count, and any Acquire or
//   TryAcquire that finds n there takes them with one Cas (barging), queued
//   waiters or not. Release adds to the count with one Cas; when Q is set
//   and W clear it then pops the first cell, sets W and wakes it to retry.
//   The woken waiter retries under the lock: it takes its permits, or goes
//   back to the front of the queue. W keeps one woken waiter at a time,
//   and it is handed on while permits are left (no lost wakeup, S5).
//   Handoff mode (S set): a woken waiter that loses its retry after
//   waiting SemStarveNanos (1 ms, Go's starvationThresholdNs) since it
//   first queued goes to the front marked starving, with S set and the
//   count given to it. While S is set the count is 0: Release gives its
//   permits to the starving cell at the front, partially until it has its
//   request (tokio's assignment), and Acquire and TryAcquire never barge.
//   When the first cell is not starving, or the queue is empty, S clears
//   and what is left becomes the count, with a retry wake when the queue
//   is not empty.
//
// Invariants (the blocks of tests_waits.mk check each, and say which):
//   S1 Q, S and the queue change only under the lock; Q is set exactly
//      while the queue holds a cell. W is set by the lock holder for the
//      cell it pops to retry, and cleared only by that cell's owner.
//   S2 While S is set the count is 0 and only the lock holder writes w:
//      every other writer Cas-es against a value it read with S clear.
//      A starving cell is the front of the queue and exists only while S
//      is set; only it holds part of its request (need below n). There is
//      at most one: a cell starves only while it holds W, and nobody is
//      given W while S is set. SemGiveLocked's loop is written for a
//      starving prefix, which is therefore one cell long.
//   S3 Bounded wait: a waiter that has waited 1 ms and lost a retry is
//      served before every waiter queued after it, and no request barges
//      past it; so a large request is never starved by small ones.
//   S4 No permit is lost: a permit is in the count, given to a starving
//      cell (its request less its need), or held by a caller. A cancelled
//      waiter still queued unlinks itself and gives what it was given to
//      the next starving waiters, under the lock; one already unlinked was
//      either given everything, and returns ok (it was done before the
//      cancel landed), or woken to retry, and hands W on.
//   S5 No lost wakeup: whenever Q is set, S clear and the count not 0,
//      either W is set or the first cell needs more than the count. Every
//      change that could break it wakes: a Release that adds to the count
//      with W clear and Q set; a woken waiter that takes its permits with
//      some left, or leaves on Canceled, hands W on; a cancelled waiter
//      that leaves the queue in normal mode wakes the next one (SemPassW
//      refuses when W is set or the count is 0); handoff mode ending with
//      permits left wakes the first cell.
//
// Memory ordering (raw.Atomic is seq_cst only). A permit carries a
// happens-before edge from its Release to the Acquire that takes it: in
// normal mode the Release's Cas is the release and the Acquire's Cas that
// reads it (or a later value of w's modification order, all Cas) the
// acquire; in handoff mode the lock's edges carry it to the waker, and the
// cell's Swap(CellWoken) and its owner's Load (mem_wait.mk) to the owner.

import std.core
import std.raw

/// A count of permits that tasks take and give back, to bound how many do
/// something at once. A free permit goes to whichever task finds it, but a
/// waiter that has waited 1 ms and still finds too few starves: from then
/// on permits go to the waiters in arrival order, and nobody takes one past
/// them, so smaller requests never starve a large one. It holds at most
/// 2^60 - 1 free permits. It allocates nothing, and each waiter waits in a
/// cell on its own task's stack.
export struct Semaphore {
    w: raw.Atomic[int]
    q: core.SpinCore[WaitQ]
}

// The queue's raw pointers are cells on parked tasks' stacks, reached only
// under the lock; every other field is atomic.
unsafe impl Send for Semaphore {}
unsafe impl Share for Semaphore {}

const SemQ: int = 1
const SemW: int = 2
const SemS: int = 4
const SemOne: int = 8

// SemMax is the most permits a Semaphore holds: the count's 60 bits.
const SemMax: int = 0x0FFF_FFFF_FFFF_FFFF

// SemStarveNanos is Go's starvationThresholdNs.
const SemStarveNanos: i64 = 1_000_000

// SemSpins bounds the spin on w before the first queueing, while nobody is
// queued (parking_lot's rule: spinning is useless behind parked waiters;
// sync_mutex.mk's 100).
const SemSpins = 100

/// A semaphore holding permits free permits. It allocates nothing, so it
/// can initialize a `var` global. A permits count below 0 or above
/// 2^60 - 1 panics with code 8.
export Semaphore.New(permits: int): Semaphore {
    if permits < 0 || permits > SemMax {
        raw.Fail(8, "Semaphore.New: permits below 0 or above 2^60 - 1")
    }
    return Semaphore(w: raw.Atomic[int].New(permits * SemOne), q: core.SpinNew(WaitQNew()))
}

/// Waits until n permits are free, and takes them. A waiter that has
/// waited 1 ms is served before every later one. The task sees every write
/// made before the Release calls that gave its permits back.
///
/// A cancellation point first, then Canceled from a cancelled wait. A
/// cancelled Acquire holds no permit: those it was given while it waited
/// go to the next waiters. An n of 0 takes nothing; an n below 0 panics
/// with code 8. A request for more permits than will ever be released
/// waits until cancelled, and while it is first in the queue so does every
/// request queued behind it. It allocates nothing.
export Semaphore.Acquire(&self, io: Io, n: int): Canceled!void {
    // The cancellation point's fast test inline (core.CancelClear: a native
    // Io while no scope is cancelled), so such a call pays no call for it.
    if !core.CancelClear(io) {
        try core.SyncCancel(io)
    }
    if n <= 0 {
        if n < 0 {
            raw.Fail(8, "Semaphore.Acquire: n below 0")
        }
        return
    }
    if SemTake(&self.w, n) {
        return
    }
    try SemAcquireSlow(self, io, n)
}

// SemTake takes n permits from the count with one Cas when S is clear and
// the count holds n: the barging path of Acquire and TryAcquire. Acquire
// edge: the Cas reads the Release (or the chain of Cas after it) that made
// these permits free. n above SemMax never fits, and the comparison comes
// before the product, so nothing overflows.
SemTake(w: &raw.Atomic[int], n: int): bool {
    for {
        v := w.Load()
        if (v & SemS) != 0 || (v >> 3) < n {
            return false
        }
        // Edge: the Cas that takes n permits reads the Release's Cas that added
        // them (the acquire edge above).
        if w.Cas(v, v - n * SemOne) {
            return true
        }
    }
}

// SemNow is the monotonic clock in nanoseconds (IoTable.now), read only on
// the parking path, never under the lock: once when a task first queues,
// once per retry wake.
SemNow(io: Io): i64 {
    return core.IoNow(io.NoCancel()).value
}

// SemAcquireSlow spins while nobody is queued, then queues and parks; a
// retry wake takes the permits or queues again at the front.
SemAcquireSlow(s: &Semaphore, io: Io, n: int): Canceled!void {
    w := &s.w
    for _ in 0..SemSpins {
        // A load orders nothing: the Cas that acts on it does (SemTake's
        // acquire edge).
        v := w.Load()
        if (v & (SemQ | SemS)) != 0 {
            break
        }
        if (v >> 3) >= n && w.Cas(v, v - n * SemOne) {
            return
        }
    }
    var cell := WaiterNew(io, n)
    c := raw.OfMut(mut cell)
    WSetSince(c, SemNow(io))
    var held := false
    var starving := false
    for {
        lk := core.SpinLock(&s.q)
        var wake := raw.Null[Waiter]()
        var took := false
        for {
            // Under the lock Q and S hold still (S1); count and W may
            // change by other tasks' Cas, so each decision is a Cas on the
            // value it read.
            // Edge: each decision is a Cas on the value it read, ordered
            // against the lock-free Cas on w.
            v := w.Load()
            if (v & SemS) == 0 && (v >> 3) >= n {
                var nv := v - n * SemOne
                if held {
                    nv = nv & ~SemW
                }
                // Acquire edge: as SemTake's.
                if w.Cas(v, nv) {
                    took = true
                    if held && (nv & SemQ) != 0 && (nv >> 3) > 0 {
                        // Permits are left for the next waiter: W goes on (S5).
                        wake = SemPassW(s, lk)
                    }
                    break
                }
                continue
            }
            if held && starving {
                // Handoff mode: S set, W cleared, the count given to this
                // cell, which goes to the front marked starving (S2, S3).
                // The Cas orders against every lock-free Cas on w, so no
                // permit is taken or added between the read and the switch.
                // Edge: the Cas to Q and S orders against every lock-free Cas
                // on w (S2, S3).
                if w.Cas(v, SemQ | SemS) {
                    WSetNeed(c, n - (v >> 3))
                    WSetStarving(c, true)
                    QPushFront(WaitQAt(lk), c)
                    break
                }
                continue
            }
            var nv := v | SemQ
            if held {
                nv = nv & ~SemW
            }
            // The Cas that sets Q (or clears W) is the point after which a
            // Release that adds to the count takes the lock and finds this
            // cell (S5).
            // Edge: the Cas that sets Q is the point after which a Release
            // finds this cell (S5).
            if nv == v || w.Cas(v, nv) {
                if held {
                    QPushFront(WaitQAt(lk), c)
                } else {
                    QPush(WaitQAt(lk), c)
                }
                break
            }
        }
        core.SpinUnlock(lk)
        if took {
            WakeList(wake)
            return
        }
        if WaitCell(io, c) {
            try SemCancel(s, io, c, n)
            return
        }
        // Woken: given everything in handoff mode (need 0), or popped to
        // retry with W, its need untouched.
        if WNeed(c) == 0 {
            return
        }
        WRearm(c)
        held = true
        starving = SemNow(io) - WSince(c) >= SemStarveNanos
    }
}

// SemCancel settles c, whose wait was cancelled, under the lock: still
// queued, it unlinks itself and gives its partial grant on (S4); popped,
// it waits for its waker's Swap with no cancellation (W1), then returns ok
// when it was given everything, or hands W on when it was woken to retry.
SemCancel(s: &Semaphore, io: Io, c: *Waiter, n: int): Canceled!void {
    lk := core.SpinLock(&s.q)
    if WQueued(c) {
        QRemove(WaitQAt(lk), c)
        var wake := raw.Null[Waiter]()
        // Under the lock S holds still (S1): this Load orders nothing
        // beyond the lock's acquire edge.
        if (s.w.Load() & SemS) != 0 {
            wake = SemGiveLocked(s, lk, n - WNeed(c))
        } else if raw.IsNull(WaitQAt(lk).head) {
            SemClear(&s.w, SemQ)
        } else {
            // The first cell may have changed, and the new one may fit in
            // the count that held the old one back: wake it to retry
            // unless W is set or the count is 0 (S5).
            wake = SemPassW(s, lk)
        }
        core.SpinUnlock(lk)
        WakeList(wake)
        fail Canceled
    }
    core.SpinUnlock(lk)
    _ = WaitCell(io.NoCancel(), c)
    if WNeed(c) == 0 {
        return
    }
    // Woken to retry, so this task holds W, and leaves: hand it on (S5).
    lk2 := core.SpinLock(&s.q)
    SemClear(&s.w, SemW)
    wake := SemPassW(s, lk2)
    core.SpinUnlock(lk2)
    WakeList(wake)
    fail Canceled
}

// SemClear clears bits of w that only the caller may clear (Q under the
// lock, W by its holder); count and W may change meanwhile, hence the
// loop. Orders nothing beyond the Cas's atomicity: the decisions that read
// these bits take the lock or Cas on w.
SemClear(w: &raw.Atomic[int], bits: int) {
    for {
        // Edge: a Cas on the value read; holders of the bits take the lock or
        // Cas on w.
        v := w.Load()
        if (v & bits) == 0 || w.Cas(v, v & ~bits) {
            return
        }
    }
}

// SemPassW sets W and pops the first cell to retry, when Q is set, W and
// S clear and the count not 0; returns the cell, chained for WakeList, or
// null. The lock is held. The Cas that sets W reads every Cas on w before
// it, so two tasks never both pop for one W. Ordering: the Load and the
// Cas order nothing beyond that atomicity; the retry's own Cas on w is
// the acquire edge for the permits, and the cell's Swap the edge to its
// owner (WakeList).
SemPassW(s: &Semaphore, lk: *core.SpinCore[WaitQ]): *Waiter {
    w := &s.w
    for {
        v := w.Load()
        if (v & (SemQ | SemW | SemS)) != SemQ || (v >> 3) == 0 {
            return raw.Null[Waiter]()
        }
        // Edge: none beyond atomicity: the Cas that sets W reads every Cas on
        // w before it, so one pop per W (S1, S5); the cell's Swap is the edge
        // to its owner.
        if w.Cas(v, v | SemW) {
            break
        }
    }
    h := QPop(WaitQAt(lk))
    WSetWake(h, raw.Null[Waiter]())
    if raw.IsNull(WaitQAt(lk).head) {
        SemClear(w, SemQ)
    }
    return h
}

/// Takes n permits and returns true if they are free and no waiter
/// starves; otherwise returns false and takes nothing. It never waits,
/// takes no Io and is not a cancellation point. An n of 0 is true; an n
/// below 0 panics with code 8. O(1); allocates nothing.
export Semaphore.TryAcquire(&self, n: int): bool {
    if n <= 0 {
        if n < 0 {
            raw.Fail(8, "Semaphore.TryAcquire: n below 0")
        }
        return true
    }
    return SemTake(&self.w, n)
}

/// Gives n permits back. They go to the free count, waking a waiter to
/// try again, or, while a waiter starves, to the starving waiters first,
/// in arrival order. Takes no Io and is not a cancellation point. More
/// than 2^60 - 1 free permits panics with code 1 (the overflow panic); an
/// n below 0 panics with code 8. It allocates nothing.
export Semaphore.Release(&self, n: int) {
    if n <= 0 {
        if n < 0 {
            raw.Fail(8, "Semaphore.Release: n below 0")
        }
        return
    }
    w := &self.w
    for {
        // A load orders nothing: the Cas below is the release edge, or
        // the lock's edges in handoff mode.
        v := w.Load()
        if (v & SemS) != 0 {
            lk := core.SpinLock(&self.q)
            // Under the lock S holds still (S1); this Load orders nothing
            // beyond the lock's acquire edge.
            if (w.Load() & SemS) == 0 {
                core.SpinUnlock(lk)
                continue
            }
            wake := SemGiveLocked(self, lk, n)
            core.SpinUnlock(lk)
            WakeList(wake)
            return
        }
        if (v >> 3) > SemMax - n {
            raw.Fail(1, "Semaphore.Release: more than 2^60 - 1 permits")
        }
        // Release edge: the Cas publishes this task's writes to the
        // Acquire whose Cas takes these permits.
        if w.Cas(v, v + n * SemOne) {
            if (v & (SemQ | SemW)) == SemQ {
                lk := core.SpinLock(&self.q)
                wake := SemPassW(self, lk)
                core.SpinUnlock(lk)
                WakeList(wake)
            }
            return
        }
    }
}

// SemGiveLocked gives n permits in handoff mode: to the starving cells at
// the front, in order, partially to the last one reached; cells whose need
// reaches 0 are unlinked. Then, when the queue is empty, what is left
// becomes the count and S clears; when the first cell is not starving, S
// clears with what is left as the count and, if any is left, that cell is
// popped to retry with W set; else S stays. Returns the cells to wake,
// chained for WakeList. The lock is held and S is set (S2: the lock holder
// is w's only writer, so plain Stores do).
SemGiveLocked(s: &Semaphore, lk: *core.SpinCore[WaitQ], n: int): *Waiter {
    var rem := n
    var first := raw.Null[Waiter]()
    var last := raw.Null[Waiter]()
    for rem > 0 {
        h := WaitQAt(lk).head
        if raw.IsNull(h) || !WStarving(h) {
            break
        }
        need := WNeed(h)
        if need > rem {
            WSetNeed(h, need - rem)
            rem = 0
            break
        }
        rem -= need
        WSetNeed(h, 0)
        _ = QPop(WaitQAt(lk))
        WSetWake(h, raw.Null[Waiter]())
        if raw.IsNull(last) {
            first = h
        } else {
            WSetWake(last, h)
        }
        last = h
    }
    h := WaitQAt(lk).head
    if !raw.IsNull(h) && WStarving(h) {
        return first
    }
    if rem > SemMax {
        raw.Fail(1, "Semaphore.Release: more than 2^60 - 1 permits")
    }
    if raw.IsNull(h) {
        // Release edge: this Store clears S with the count left; the next
        // Acquire's Cas that reads it acquires these permits.
        s.w.Store(rem * SemOne)
        return first
    }
    if rem == 0 {
        s.w.Store(SemQ)
        return first
    }
    // The first cell did not starve: back to normal mode, and it retries
    // for the permits left (S5).
    _ = QPop(WaitQAt(lk))
    WSetWake(h, raw.Null[Waiter]())
    var nv := SemW | rem * SemOne
    if !raw.IsNull(WaitQAt(lk).head) {
        nv = nv | SemQ
    }
    // Release edge: as above; W stands for h.
    s.w.Store(nv)
    if raw.IsNull(last) {
        first = h
    } else {
        WSetWake(last, h)
    }
    return first
}
