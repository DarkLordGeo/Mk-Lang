// Why: the runtime's own shared structures need a lock that works where no
// task may suspend, on carriers and plain threads alike.
//
// What: the runtime spinlock: Lock, TryLock and Unlock on a raw.Atomic[u32].
//
// How: compare-and-swap 0 to 1, else spin with backoff up to 64 loads, then
// yield the thread. Held only for short sections that never suspend and
// never across a Switch.
//
// Unlock is a release store (a plain mov on x86_64, where a seq_cst store is
// an xchg), so a load after an Unlock may be done before it: no code may
// rely on the unlock store alone to order a later load (Dekker's store-load
// pattern). Every caller in std was audited for it: after each Unlock comes
// a value computed under the lock, an RMW (a full barrier itself), or a
// seq_cst store or load whose partner is another seq_cst access, never the
// lock word.

import std.raw
import std.sys

/// Lock takes the spinlock l: a compare-and-swap of 0 to 1, spinning with
/// backoff, then yielding the thread, until it succeeds. Never suspends.
/// O(1) when free; unbounded while another thread holds it. Allocates nothing.
export Lock(l: &raw.Atomic[u32]) {
    var spins := 1
    // Edge: the acquire. A successful compare-and-swap follows the holder's
    // Unlock store in S, so the holder's section happens before this one.
    // The spin's plain loads only wait for 0; they order nothing.
    for !l.Cas(0, 1) {
        for _ in 0..spins {
            if l.Load() == 0 {
                break
            }
        }
        if spins < 64 {
            spins *= 2
        } else {
            sys.ThreadYield()
        }
    }
}

/// TryLock takes the lock only when it is free.
/// O(1): one compare-and-swap; allocates nothing.
export TryLock(l: &raw.Atomic[u32]): bool {
    // Edge: as Lock's compare-and-swap.
    return l.Cas(0, 1)
}

/// Unlock frees the spinlock l, which the caller holds.
/// O(1): one release store; allocates nothing.
export Unlock(l: &raw.Atomic[u32]) {
    // Edge: release (order 2) to the next Lock's or TryLock's successful
    // Cas(0, 1), which reads this 0 and so acquires every write of the
    // section. It orders no later load of this thread (the header).
    l.StoreOrd(0, 2)
}
