// Why: a task waiting for a word to change should sleep, not spin; this is
// the futex-style wait std's own locks and user code build on.
//
// What: Atomic[T].Wait and Atomic[T].Wake.
//
// How: over the runtime's wait on an address (task.WaitWord and WakeWord,
// through std.core's IoWaitWord and IoWakeWord, which hold the unsafe;
// std.atomic is not in std/pkg.mk's unsafe_modules). Any T of 4 bytes, since
// a futex and WaitOnAddress compare bits: `requires SizeOf[T]() == 4`, else
// E0706 at the call. A wait takes an Io, so this file has the io_ prefix (as
// std.math's io_rng.mk) and a freestanding build that never waits leaves it
// unreached; the rest of std.atomic, Spin included, needs no Io. It imports
// std.core and std.raw only. The word and expected's bits pass to std.core
// as addresses (raw.Addr, safe), which std.core reads as u32.

import std.core
import std.raw

/// Parks the task while the word holds expected, until a Wake. Wait may
/// also return early for no reason, so read the word again in a loop. It
/// fails with Canceled when io's scope is or becomes cancelled. Called off
/// a task, it panics with code 16. The check of the word and the park are
/// one step as far as Wake is concerned: a Wake after the word changed is
/// not lost. T is 4 bytes (E0706 otherwise), since the word is compared by
/// its bits. O(1) besides the wait; it allocates nothing.
export Atomic[T].Wait(&self, io: Io, expected: T): Canceled!void
    requires SizeOf[T]() == 4 {
    e := expected
    if core.IoWaitWord(io, raw.Addr(raw.Of(&self.v)), raw.Addr(raw.Of(&e))) {
        fail Canceled
    }
}

/// Wakes one task waiting on the word, or every one when all is true. It
/// may be called from any thread, a foreign one included, and needs no Io.
/// It also wakes tasks waiting under a simulated Io or a foreign Io: it
/// calls each live foreign Io's wake entry from the waking thread. It takes
/// a sequentially consistent fence after waking native waiters and before
/// it wakes waiters on other Io. T is 4 bytes (E0706 otherwise). O(1)
/// besides the wake; it allocates nothing.
export Atomic[T].Wake(&self, all: bool)
    requires SizeOf[T]() == 4 {
    core.IoWakeWord(raw.Addr(raw.Of(&self.v)), all)
}
