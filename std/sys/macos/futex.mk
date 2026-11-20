// Why: carriers and pool threads park and wake on a counting semaphore, and
// on macOS the primitive to build it on is the kernel's address wait.
//
// What: SemInit, SemPost and SemWait (with a timeout in nanoseconds) over
// Sem's count word and sleepers word.
//
// How: Linux's design (std/sys/linux/futex.mk: the count taken by
// compare-and-swap, and the sleepers word, a Dekker pair with the count, so
// SemPost makes no call when no thread can be waiting), with __ulock_wait
// and __ulock_wake in place of FUTEX_WAIT and FUTEX_WAKE:
// UL_COMPARE_AND_WAIT compares the word with 0 inside the kernel, as
// FUTEX_WAIT does, so no wake is lost. These are the calls libc++'s
// std::atomic wait makes on macOS. The wait's timeout is in microseconds (0
// waits forever), so a wait of under a microsecond waits one.

import std.raw

// Darwin's ulock operations (<sys/ulock.h>).
const UlCompareAndWait: u32 = 1
const UlfWakeAll: u32 = 0x100
const UlfNoErrno: u32 = 0x0100_0000

const MaxI64: i64 = 0x7FFF_FFFF_FFFF_FFFF
// MaxSemCount bounds a semaphore's count on every target (Windows
// CreateSemaphoreW's maximum).
const MaxSemCount: u32 = 0x7FFF_FFFF
// MaxWaitUs is the longest single wait, in microseconds (the argument is a
// u32); a longer timeout waits again for the rest.
const MaxWaitUs: i64 = 0xFFFF_FFFF

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
/// timeout whose deadline would pass i64's largest value.
/// O(1) system calls besides the wait; allocates nothing.
export SemWait(s: &Sem, timeout_ns: i64): bool {
    deadline := Deadline(timeout_ns)
    var w := raw.Null[u8]()
    unsafe "the semaphore word's address, for the kernel's wait" {
        w = raw.PtrCast[raw.Atomic[u32], u8](raw.Of(&s.word))
    }
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
        var us: u32 = 0
        if deadline >= 0 {
            left := deadline - Now()
            if left <= 0 {
                return false
            }
            var u := (left + 999) / 1000
            if u > MaxWaitUs {
                u = MaxWaitUs
            }
            us = Trunc[u32](u)
        }
        // The Dekker pair: announce, then reread the word; the Add's edge is
        // to SemPost's sleepers.Load.
        _ = s.sleepers.Add(1)
        if s.word.Load() == 0 {
            unsafe "a wait on the semaphore word, compared with 0 in the kernel" {
                _ = __ulock_wait(UlCompareAndWait | UlfNoErrno, w, 0, us)
            }
        }
        // Undo this waiter's own announcement (wrapping add of -1).
        _ = s.sleepers.Add(0xFFFF_FFFF)
    }
}

// Deadline is the monotonic time at which a wait of timeout_ns ends, or -1
// when it never ends: a negative timeout, or one whose end is past i64's
// largest value (saturated, never overflowing).
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

/// SemPost adds n counts, saturating at MaxSemCount, and wakes the waiters
/// when one may sleep: one wake for n == 1, every waiter otherwise (the
/// ulock wakes one or all; the woken that find no count sleep again).
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
    // The poster's half of the Dekker pair: ordered after the Cas above.
    if s.sleepers.Load() == 0 {
        return
    }
    op: u32 = if n == 1 { UlCompareAndWait | UlfNoErrno } else { UlCompareAndWait | UlfNoErrno | UlfWakeAll }
    unsafe "a wake of the semaphore word, which is only woken, never written" {
        _ = __ulock_wake(op, raw.PtrCast[raw.Atomic[u32], u8](raw.Of(&s.word)), 0)
    }
}

// WaitWord waits while the u32 at w equals v (a wake or a spurious return
// ends it), for ThreadJoin and ThreadStart's handshake.
WaitWord(w: *raw.Atomic[u32], v: u32) {
    unsafe "w is a live word the waker writes and then wakes" {
        _ = __ulock_wait(UlCompareAndWait | UlfNoErrno, raw.PtrCast[raw.Atomic[u32], u8](w), u64(v), 0)
    }
}

// WakeWord wakes every waiter on the u32 at w.
WakeWord(w: *raw.Atomic[u32]) {
    unsafe "w is a live word its waiters compare" {
        _ = __ulock_wake(UlCompareAndWait | UlfNoErrno | UlfWakeAll, raw.PtrCast[raw.Atomic[u32], u8](w), 0)
    }
}
