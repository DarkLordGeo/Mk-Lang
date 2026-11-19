// Why: a condition variable loses a wake-up that comes before the wait; a
// permit-based wait keeps it, so a task can wait for an event safely.
//
// What: sync.Notify: New, NotifyOne (wake one waiter or store one permit),
// NotifyAll (wake every queued waiter) and Wait. No allocation.
//
// How: one word (empty, a permit, or waiters queued) and a FIFO of waiter
// cells (mem_wait.mk) under a spin lock, as tokio's Notify without futures.

// Algorithm: tokio's Notify (tokio/src/sync/notify.rs) without futures:
// one word, 0 (empty), 1 (a permit is stored) or 2 (waiters are queued),
// and a FIFO of cells (mem_wait.mk) under a spin lock. NotifyOne: 0 to 1
// by a Cas, nothing at 1 (a permit does not accumulate), at 2 the first
// waiter is unlinked and woken. NotifyAll: at 2 every queued waiter is
// unlinked and woken; it stores no permit. Wait: 1 to 0 by a Cas and
// return, else under the lock 0 to 2 (or 2 kept), queue, park.
//
// Invariants (the blocks of tests_waits.mk check each, and say which):
//   N1 The word is 2 exactly while the queue holds a cell; it enters and
//      leaves 2 only under the lock, so outside the lock only 0 and 1 are
//      written (by Cas).
//   N2 No lost wakeup: Wait moves the word to 2 by a Cas from the value it
//      read under the lock, so a permit stored between its read and its
//      write fails the Cas and is consumed instead of overwritten; a
//      NotifyOne that sees 2 takes the lock and finds the cell (N1).
//   N3 One notification wakes one waiter: a NotifyOne unlinks exactly one
//      cell, and a popped waiter has consumed it and returns ok even when
//      its wait was cancelled meanwhile (it was done before the cancel
//      landed). A cancelled waiter
//      still queued unlinks itself and takes nothing.
//   N4 NotifyAll wakes exactly the waiters queued when it took the lock;
//      a Wait that starts after it waits for the next notification.
//
// Memory ordering (raw.Atomic is seq_cst only): a notification is a
// release from the notifier to the waiter it reaches. Through the permit:
// the NotifyOne's Cas(0, 1) and the Wait's Cas(1, 0) that reads it. Through
// a cell: the lock's edges, then the cell's state Swap(CellWoken) and its
// owner's Load (mem_wait.mk).

import std.core
import std.raw

/// A signal one task sends and another waits for, which cannot be lost.
/// NotifyOne wakes the first waiter or, when nobody waits, stores one
/// permit for the next Wait; NotifyAll wakes every task waiting at that
/// moment. Use it with a flag or a Mutex in place of a condition variable.
/// It allocates nothing, and each waiter waits in a cell on its own task's
/// stack.
export struct Notify {
    w: raw.Atomic[u32]
    q: core.SpinCore[WaitQ]
}

unsafe impl Send for Notify {}
unsafe impl Share for Notify {}

const NotifyEmpty: u32 = 0
const NotifyPermit: u32 = 1
const NotifyWaiting: u32 = 2

/// A Notify with no permit and no waiter. It allocates nothing, so it can
/// initialize a `var` global.
export Notify.New(): Notify {
    return Notify(w: raw.Atomic[u32].New(NotifyEmpty), q: core.SpinNew(WaitQNew()))
}

/// Wakes the first waiter, or stores one permit for the next Wait when
/// nobody waits. Permits do not add up: two calls with nobody waiting
/// store one. The woken waiter sees every write this task made before the
/// call. Takes no Io and is not a cancellation point. O(1); allocates
/// nothing.
export Notify.NotifyOne(&self) {
    w := &self.w
    for {
        // Release edge: the Cas(0, 1) publishes this task's writes to the
        // Wait whose Cas(1, 0) consumes the permit.
        v := w.Load()
        if v == NotifyPermit {
            return
        }
        if v == NotifyEmpty {
            if w.Cas(NotifyEmpty, NotifyPermit) {
                return
            }
            continue
        }
        lk := core.SpinLock(&self.q)
        // Under the lock the word leaves 2 only here (N1); the Load orders
        // nothing beyond the lock's acquire edge.
        if w.Load() != NotifyWaiting {
            core.SpinUnlock(lk)
            continue
        }
        c := QPop(WaitQAt(lk))
        if raw.IsNull(WaitQAt(lk).head) {
            // Orders nothing a waiter reads: it leaves 2 for 0 under the
            // lock, and the woken waiter's edge is its cell's state.
            w.Store(NotifyEmpty)
        }
        WSetWake(c, raw.Null[Waiter]())
        core.SpinUnlock(lk)
        WakeList(c)
        return
    }
}

/// Wakes every task waiting at the moment of the call and stores no
/// permit: a Wait that starts later waits for the next notification. Takes
/// no Io and is not a cancellation point. O(waiters); allocates nothing.
export Notify.NotifyAll(&self) {
    w := &self.w
    // Orders nothing: a waiter queued after this Load is not one this call
    // must wake (N4); the edge to each woken waiter is its cell's state.
    if w.Load() != NotifyWaiting {
        return
    }
    lk := core.SpinLock(&self.q)
    first := QTake(WaitQAt(lk))
    if !raw.IsNull(first) {
        // Leaves 2 under the lock (N1); orders nothing, as in NotifyOne.
        w.Store(NotifyEmpty)
    }
    core.SpinUnlock(lk)
    WakeList(first)
}

/// Waits for a notification and consumes it: a stored permit at once, else
/// the next NotifyOne or NotifyAll.
///
/// A cancellation point first. A cancelled Wait returns Canceled and
/// consumes nothing. A Wait that a NotifyOne reached returns ok even when
/// its scope was cancelled meanwhile, so no notification is lost to a
/// cancel. O(1) besides the wait; allocates nothing.
export Notify.Wait(&self, io: Io): Canceled!void {
    // The cancellation point's fast test inline (core.CancelClear: a native
    // Io while no scope is cancelled), so such a call pays no call for it.
    if !core.CancelClear(io) {
        try core.SyncCancel(io)
    }
    w := &self.w
    // Acquire edge: the Cas(1, 0) reads the NotifyOne's Cas(0, 1).
    if w.Cas(NotifyPermit, NotifyEmpty) {
        return
    }
    var cell := WaiterNew(io, 0)
    c := raw.OfMut(mut cell)
    lk := core.SpinLock(&self.q)
    for {
        // Under the lock. The Cas to 2 orders against a NotifyOne's
        // Cas(0, 1) in w's one modification order (N2); the Cas(1, 0) is
        // the acquire edge from that NotifyOne.
        v := w.Load()
        if v == NotifyPermit {
            if w.Cas(NotifyPermit, NotifyEmpty) {
                core.SpinUnlock(lk)
                return
            }
            continue
        }
        // Edge: the Cas to 2 is under the lock (N1), from the value it read, so
        // a NotifyOne's Cas(0, 1) between the read and here fails it (N2), and
        // a NotifyOne that sees 2 finds the cell.
        if v == NotifyWaiting || w.Cas(NotifyEmpty, NotifyWaiting) {
            break
        }
    }
    QPush(WaitQAt(lk), c)
    core.SpinUnlock(lk)
    if !WaitCell(io, c) {
        return
    }
    lk2 := core.SpinLock(&self.q)
    if WQueued(c) {
        QRemove(WaitQAt(lk2), c)
        if raw.IsNull(WaitQAt(lk2).head) {
            // Edge: none; leaves 2 under the lock (N1).
            w.Store(NotifyEmpty)
        }
        core.SpinUnlock(lk2)
        fail Canceled
    }
    // Popped by a notifier, which sets CellWoken after its unlock (N3).
    core.SpinUnlock(lk2)
    _ = WaitCell(io.NoCancel(), c)
}
