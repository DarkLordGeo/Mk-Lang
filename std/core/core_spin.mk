// Why: std.atomic's Spin, a lock for very short critical sections, needs
// unsafe code, and std.atomic is not allowed any; the storage and its lock
// operations live here, in std.core, which is.
//
// What: SpinCore[T] with SpinNew, SpinInto, SpinLock, SpinUnlock, SpinRef,
// SpinMut and SpinStore, and the helpers SpinPauseHint and SpinYield, which
// calls the yield hook MkThreadYield.
//
// How: a lock word (0 free, 1 held) then the value. SpinLock tries a Cas,
// then waits with exponentially growing runs of x86 pause hints (up to
// SpinBackoffMax) and, after SpinYieldTries looks at the word, yields the
// thread before each further try. No Io: the lock never suspends a task.

import std.raw

/// The storage of std.atomic's Spin: a lock word, 0 when free and 1 when
/// held, then the value. It is Send and Share when T is Send, as Mutex is:
/// the lock gives one task at a time a reference to T, so only moving T
/// between tasks needs a bound. It takes no Io: the lock spins and never
/// suspends a task.
export struct SpinCore[T] {
    word: raw.Atomic[u32]
    value: T
}

unsafe impl[T: Send] Send for SpinCore[T] {}
unsafe impl[T: Send] Share for SpinCore[T] {}

/// An unlocked SpinCore holding x. O(1); it allocates nothing, so a `var`
/// global may hold one.
export SpinNew[T](x: T): SpinCore[T] {
    return SpinCore[T](word: raw.Atomic[u32].New(0), value: x)
}

/// The value out of a SpinCore taken by value. Nothing borrows the core, so
/// it is unlocked. O(1).
export SpinInto[T](c: SpinCore[T]): T {
    SpinCore(word: _, value) := c
    return value
}

// SpinBackoffMax bounds the wait between failed Cas calls, in loads.
const SpinBackoffMax = 1024

/// Takes the lock, spinning until it is free, and returns c as a raw pointer
/// for the guard. It never suspends a task and allocates nothing. Uncontended
/// it is one compare-and-swap of 0 to 1, O(1); a waiter may wait without
/// bound, because the lock is not fair.
///
/// A waiter only reads the word until it is 0, so waiters share the cache
/// line instead of taking it from the holder, then tries again after a
/// pause that doubles from 1 to 1,024 CPU pause hints per failed try. From
/// its 64th look at the word on, it yields the thread through the yield
/// hook MkThreadYield before each look, so a spinner whose holder was
/// preempted gives up its CPU. Hosted programs get the OS's yield; a
/// freestanding program only pauses unless it exports its own
/// MkThreadYield.
///
/// The successful compare-and-swap is the acquire: the critical section of
/// the last SpinUnlock happens before this one.
export SpinLock[T](c: &SpinCore[T]): *SpinCore[T] {
    w := &c.word
    if w.Cas(0, 1) {
        return raw.Of(c)
    }
    var backoff := 1
    var tries := 1
    for {
        // A try is one look at the word, a load here or a Cas below. After
        // SpinYieldTries tries the holder is likely off its CPU
        // (preempted, or more spinners than cores), so each later try
        // first gives the thread's CPU away through the yield hook: a
        // waiter behind a holder that keeps the lock yields too.
        // It never suspends a task: the thread stays this task's.
        // Edge: seq_cst loads; the wait only reads, and the Cas below is
        // the acquire that takes the lock.
        for w.Load() != 0 {
            tries += 1
            if tries >= SpinYieldTries {
                SpinYield()
            }
        }
        // Edge: seq_cst; the Cas from 0 takes the lock and acquires the
        // holder's writes, released by the unlock's store of 0.
        if w.Cas(0, 1) {
            return raw.Of(c)
        }
        tries += 1
        if tries >= SpinYieldTries {
            SpinYield()
            continue
        }
        // The wait is pause hints, not loads: a pause leaves the
        // core to its SMT sibling and keeps the waiter off the line, so the
        // holder retakes the lock with the line still local.
        var n := backoff
        for n > 0 {
            SpinPauseHint()
            n -= 1
        }
        if backoff < SpinBackoffMax {
            backoff = backoff * 2
        }
    }
}

// SpinPauseHint is the spin-wait hint: x86_64's pause (Intel SDM, PAUSE;
// Go's procyield and Rust's spin_loop do the same in their spin waits),
// aarch64's yield. std.core is in std/pkg.mk's unsafe_modules.
SpinPauseHint() {
    if Target.arch == Arch.Arm64 {
        unsafe "yield: a spin-wait hint; it reads and writes no memory and no flags" {
            asm("yield", options(nomem, nostack, preserves_flags))
        }
    } else {
        unsafe "pause: a spin-wait hint; it reads and writes no memory and no flags" {
            asm("pause", options(nomem, nostack, preserves_flags))
        }
    }
}

// SpinYieldTries is the number of tries (looks at the word: each load of
// the wait and each Cas, the first Cas included) after which SpinLock
// yields the thread before each further try.
const SpinYieldTries = 64

// MkThreadYield is the yield hook, reached without an import (the
// pattern MkPageAlloc follows): the support pack's weak default only pauses
// (internal/codegen/support.go, yieldHook), hosted std.rt exports the OS's
// yield (std/rt/yield.mk), and a freestanding program may export its own.
extern MkThreadYield()

// SpinYield gives the thread's CPU to another ready thread, if the hook
// can; it never suspends a task.
SpinYield() {
    unsafe "MkThreadYield takes no argument and touches no Mk memory: the weak default is one pause, std.rt's one system call" {
        MkThreadYield()
    }
}

// SpinCoreAt is the SpinCore a guard holds by raw pointer.
SpinCoreAt[T](p: *SpinCore[T]): &SpinCore[T] {
    unsafe "p is the live SpinCore of the Spin whose guard holds it: the guard's loan keeps it alive" {
        return &*p
    }
}

/// Releases the lock. The store of 0 is a release: it publishes the
/// critical section to the next lock. It is not sequentially consistent, so
/// it orders no later load of this thread, and no caller may use it as one
/// half of a store-load (Dekker) pair. O(1).
export SpinUnlock[T](p: *SpinCore[T]) {
    SpinCoreAt(p).word.StoreOrd(0, 2)
}

/// The value under the held lock, shared. The guard's loan on the Spin keeps
/// it alive and the lock keeps it exclusive. O(1).
export SpinRef[T](p: *SpinCore[T]): &T {
    return &SpinCoreAt(p).value
}

/// The value under the held lock, writable. The guard's loan keeps it alive
/// and the lock keeps it exclusive. O(1).
export SpinMut[T](p: *SpinCore[T]): mut T {
    unsafe "the lock is held by the one guard that holds p, so this is the only reference to the value" {
        return mut (*p).value
    }
}

/// Replaces the value under the held lock and drops the old one. O(1);
/// allocates nothing. std.atomic's SpinGuard.Store, which calls it, is
/// E0701 at the call for a linear T, so the code 8 panic here is reached
/// only by a std module that passes a linear T directly.
export SpinStore[T](p: *SpinCore[T], x: T) {
    if raw.IsLinear[T]() {
        raw.Fail(8, "SpinGuard.Store would drop a linear value")
    }
    unsafe "the lock is held by the one guard that holds p; the old value is plain, read once and forgotten" {
        q := raw.Of(&(*p).value)
        raw.Forget(raw.Read(q))
        raw.Write(q, x)
    }
}
