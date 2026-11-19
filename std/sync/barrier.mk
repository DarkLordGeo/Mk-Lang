// Why: phased parallel work needs n tasks to wait for each other at the end
// of each round, with one of them chosen to do the round's serial step.
//
// What: sync.Barrier: New(n) and Wait, which returns true for the round's
// one leader, the task whose arrival completes the round. Reusable, no
// allocation, no unsafe.
//
// How: a generation barrier: a count and a 64-bit generation under a spin
// lock, the generation's low 32 bits published in a word the others park on.

// Algorithm: a generation barrier, the centralized barrier of
// Mellor-Crummey and Scott ("Algorithms for Scalable Synchronization on
// Shared-Memory Multiprocessors", 1991) as Rust's std::sync::Barrier
// builds it (a count and a generation id under a lock): the count of this
// round's arrivals and a 64-bit generation live under a spin lock, and the
// generation's low 32 bits are published in word, on which the others
// park. The leader resets the count, bumps the generation, stores word and
// wakes every task parked on it.
//
// Invariants (the blocks of tests_waits.mk check each, and say which):
//   B1 count and gen change only under the lock; 0 <= count < n outside
//      it, and word holds gen's low 32 bits.
//   B2 A round completes exactly when its n-th arrival is counted; that
//      arrival is the round's one leader, and every other arrival of the
//      round returns false after it.
//   B3 Cancellation leaves the barrier consistent: a cancelled Wait whose
//      round has not completed takes its arrival back under the lock, so
//      the round still needs n arrivals; one whose round completed first
//      returns false (it was done before the cancel landed).
//   B4 No ABA: a waiter that wakes with word still its round's value
//      re-reads the 64-bit gen under the lock before it waits again.
//
// Memory ordering (raw.Atomic is seq_cst only): every arrival's writes
// happen before every return of its round. Each arrival releases them
// through the lock; the leader acquires them through the lock and
// releases them, with its own, in its Store to word; a waiter acquires
// them in the Load of word that reads that Store (or under the lock, B4).

import std.core
import std.raw

struct BarState {
    count: int
    round: u64
}

/// A reusable meeting point for n tasks. Each round ends when n tasks have
/// called Wait, and the task whose call completed the round is its leader.
/// The barrier is then ready for the next round. It allocates nothing, and
/// each waiter waits in a cell on its own task's stack.
export struct Barrier {
    n: int
    word: raw.Atomic[u32]
    q: core.SpinCore[BarState]
}

/// A barrier for rounds of n tasks. It allocates nothing, so it can
/// initialize a `var` global. An n below 1 panics with code 8.
export Barrier.New(n: int): Barrier {
    if n < 1 {
        raw.Fail(8, "Barrier.New: n below 1")
    }
    return Barrier(n: n, word: raw.Atomic[u32].New(0), q: core.SpinNew(BarState(count: 0, round: 0)))
}

/// Waits until n tasks have called Wait in this round, and returns true in
/// the one whose call completed it (the leader) and false in the others.
/// Every write an arrival made before its Wait is visible to every task
/// after its Wait returns.
///
/// A cancellation point first. A Wait cancelled before its round completes
/// returns Canceled and takes its arrival back, so the round still needs n
/// arrivals; one whose round completed first returns false. It allocates
/// nothing.
export Barrier.Wait(&self, io: Io): Canceled!bool {
    // The cancellation point's fast test inline (core.CancelClear: a native
    // Io while no scope is cancelled), so such a call pays no call for it.
    if !core.CancelClear(io) {
        try core.SyncCancel(io)
    }
    lk := core.SpinLock(&self.q)
    st := core.SpinMut(lk)
    st.count += 1
    if st.count == self.n {
        st.count = 0
        st.round +%= 1
        // Release edge: publishes every arrival of the round (acquired
        // through the lock) to the waiters' Load of word.
        self.word.Store(Trunc[u32](st.round))
        core.SpinUnlock(lk)
        core.SyncWake(io, &self.word, true)
        return true
    }
    g := st.round
    core.SpinUnlock(lk)
    return try BarrierPark(self, io, g)
}

// BarrierPark waits for the round g to complete.
BarrierPark(b: &Barrier, io: Io, g: u64): Canceled!bool {
    g32 := Trunc[u32](g)
    for {
        cancelled := core.SyncWait(io, &b.word, g32)
        // Acquire edge: a Load that reads the leader's Store of a later
        // generation acquires the round's writes.
        if b.word.Load() != g32 {
            return false
        }
        lk := core.SpinLock(&b.q)
        st := core.SpinMut(lk)
        if st.round != g {
            // Acquire edge through the lock: the leader stored gen under it.
            core.SpinUnlock(lk)
            return false
        }
        if cancelled {
            st.count -= 1
            core.SpinUnlock(lk)
            fail Canceled
        }
        core.SpinUnlock(lk)
    }
}
