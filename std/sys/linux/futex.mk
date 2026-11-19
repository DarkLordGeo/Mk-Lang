// Why: carriers and pool threads park and wake on a counting semaphore, and
// on Linux the futex is the primitive to build it on.
//
// What: SemInit, SemPost and SemWait (with a timeout in nanoseconds) over
// Sem's count word and sleepers word.
//
// How: the count is one futex word taken by compare-and-swap. A sleepers
// count, a Dekker pair with the count, lets SemPost skip FUTEX_WAKE when no
// thread can be waiting, so a spinning pool thread costs no system call.

import std.raw

const FutexWaitPrivate: uint = 128
const FutexWakePrivate: uint = 129
const MaxI64: i64 = 0x7FFF_FFFF_FFFF_FFFF
// MaxSemCount bounds a semaphore's count on both targets (Windows
// CreateSemaphoreW's maximum).
const MaxSemCount: u32 = 0x7FFF_FFFF

// The count's edges: SemPost's Cas that adds happens before the waiter's
// Cas that takes the count (sequentially consistent), so whatever the
// poster wrote before SemPost is visible after SemWait returns true.
//
// The sleepers word lets SemPost skip FUTEX_WAKE when no thread can be in
// FUTEX_WAIT: a pool thread that spins (PoolSpin) takes its job in
// SemWait's first Load, and without the word each such job would cost the
// poster a futex system call that wakes nobody. It is a Dekker pair, every
// access sequentially consistent:
//   waiter: sleepers.Add(1); v := word.Load(); if v == 0 FUTEX_WAIT(word, 0)
//   poster: word.Cas(v, v + n);                if sleepers.Load() > 0 FUTEX_WAKE
// In the single total order of these four accesses, either the waiter's Add
// precedes the poster's Load (the poster sees a sleeper and wakes), or the
// poster's Cas precedes the waiter's Load (the waiter sees the count and
// does not wait). A waiter that passed its Load with 0 and has not yet
// entered the kernel is covered by the futex itself: FUTEX_WAIT compares the
// word with 0 under the futex bucket lock, so a post that lands first makes
// it return at once, and one that lands after finds it queued. So no wake is
// lost. The word counts sleepers, it is never a flag cleared on wake (a flag
// loses a wake when two threads sleep and one wake clears it for both): each
// waiter undoes its own Add after the wait returns, whatever woke it, and a
// waiter that was woken but lost the count to another thread announces
// itself again before it sleeps again.

/// SemInit makes s a semaphore with count 0, before it is shared (its
/// Stores need no edge); always true here (the Windows variant can fail).
/// O(1); allocates nothing.
export SemInit(s: mut Sem): bool {
    s.word.Store(0)
    s.sleepers.Store(0)
    s.h = 0
    return true
}

/// SemWait takes one count, waiting up to timeout_ns nanoseconds; false when
/// the time passed first. A negative timeout waits forever, and so does a
/// timeout whose deadline would pass i64's largest value
/// (Deadline): Now() + timeout_ns would overflow, which panics, and no
/// monotonic clock reaches that deadline (the Windows
/// variant caps its milliseconds the same way).
/// O(1) system calls besides the wait; allocates nothing.
export SemWait(s: &Sem, timeout_ns: i64): bool {
    deadline := Deadline(timeout_ns)
    w := raw.Addr(raw.Of(&s.word))
    for {
        // Edge: the Cas that takes a count reads the Cas of the SemPost that
        // added it, so the poster's writes before its post happen before this
        // return; the Load only seeds the Cas.
        v := s.word.Load()
        if v > 0 {
            if s.word.Cas(v, v - 1) {
                return true
            }
            continue
        }
        var ts: [2]i64 = [0; 2]
        var tsp: uint = 0
        if deadline >= 0 {
            left := deadline - Now()
            if left <= 0 {
                return false
            }
            ts[0] = left / 1_000_000_000
            ts[1] = left % 1_000_000_000
            tsp = raw.Addr(raw.Data(ts[..]))
        }
        // The Dekker pair (above SemInit): announce, then reread the word; the
        // Add's edge is to SemPost's sleepers.Load.
        _ = s.sleepers.Add(1)
        if s.word.Load() == 0 {
            unsafe "system call; a futex wait on the semaphore word" {
                _ = Syscall(Nrs().futex, w, FutexWaitPrivate, 0, tsp, 0, 0)
            }
        }
        // Undo this waiter's own announcement (wrapping add of -1); it
        // creates no edge anything relies on: a stale count only costs a
        // poster one FUTEX_WAKE that wakes nobody.
        _ = s.sleepers.Add(0xFFFF_FFFF)
    }
}

// Deadline is the CLOCK_MONOTONIC time at which a wait of timeout_ns ends,
// or -1 when it never ends: a negative timeout, or one whose end is past
// i64's largest value (saturated, never overflowing). Now() is never
// negative, so MaxI64 - now cannot overflow either.
Deadline(timeout_ns: i64): i64 {
    if timeout_ns < 0 {
        return -1
    }
    now := Now()
    return if timeout_ns > MaxI64 - now { -1 } else { now + timeout_ns }
}

// Left is the time from now to deadline (Deadline's answer), at least 0;
// -1 (forever) stays -1.
Left(deadline: i64): i64 {
    if deadline < 0 {
        return -1
    }
    l := deadline - Now()
    return if l < 0 { 0 } else { l }
}

/// SemPost adds n counts, saturating at MaxSemCount (2^31 - 1, the Windows
/// semaphore's maximum, so both targets give one answer), and wakes up to n
/// waiters. The saturating Cas keeps the u32 word from wrapping (a post of
/// 2^32 - 1 and then 1 would leave it at 0), and the wake count is capped
/// because FUTEX_WAKE reads it as an int (SemBig checks both). The Cas that
/// adds is the count's edge, and the sleepers Load after it is the poster's
/// half of the Dekker pair (both above); with no sleeper announced no
/// FUTEX_WAKE is made.
/// O(1) system calls; allocates nothing.
export SemPost(s: &Sem, n: u32) {
    if n == 0 {
        return
    }
    for {
        // Edge: none for the Load, which only seeds the Cas.
        v := s.word.Load()
        if v >= MaxSemCount {
            break
        }
        var add := n
        if add > MaxSemCount - v {
            add = MaxSemCount - v
        }
        // Edge: the Cas that adds is the count's release, which a waiter's Cas
        // that takes it acquires.
        if s.word.Cas(v, v + add) {
            break
        }
    }
    // The poster's half of the Dekker pair (above SemInit): this Load
    // is ordered after the Cas above, and its edge is from SemWait's Add.
    if s.sleepers.Load() == 0 {
        return
    }
    var wake := n
    if wake > MaxSemCount {
        wake = MaxSemCount
    }
    unsafe "system call; s.word is the caller's live futex word, only woken, never written" {
        _ = Syscall(Nrs().futex, raw.Addr(raw.Of(&s.word)), FutexWakePrivate, uint(wake), 0, 0, 0)
    }
}
