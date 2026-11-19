// Why: the runtime, timers and std.time need a monotonic clock, a wall
// clock and a blocking sleep on Windows.
//
// What: Now (QueryPerformanceCounter in nanoseconds), WallNow and
// SleepNanos, with the counter's frequency kept in Clock.
//
// How: the counter is scaled to nanoseconds without overflow for any
// frequency below 9.2 GHz; SleepNanos rounds up to whole milliseconds and
// caps each Sleep below INFINITE.

import std.raw

struct ClockState {
    freq: raw.Atomic[i64]
}

var Clock: ClockState = ClockState(freq: raw.Atomic[i64].New(0))

// MaxSleepMs is the longest Sleep, one below INFINITE.
const MaxSleepMs: i64 = 0xFFFF_FFFE

/// Now is the monotonic clock in nanoseconds since an arbitrary start:
/// count / freq seconds plus the remainder scaled, so no product overflows
/// for any frequency below 9.2 GHz.
/// O(1); allocates nothing.
export Now(): i64 {
    unsafe "kernel32; q and c are i64s in this frame" {
        // Edge: none; a cache of QueryPerformanceFrequency, the same value for
        // every thread.
        var f := Clock.freq.Load()
        if f == 0 {
            var q: i64 = 0
            _ = QueryPerformanceFrequency(raw.OfMut(mut q))
            if q <= 0 {
                q = 1
            }
            // A cache of a value fixed at boot: racing threads store the
            // same number, so the store orders nothing (no edge needed).
            Clock.freq.Store(q)
            f = q
        }
        var c: i64 = 0
        _ = QueryPerformanceCounter(raw.OfMut(mut c))
        return (c / f) * 1_000_000_000 + (c % f) * 1_000_000_000 / f
    }
}

/// WallNow is the wall clock in nanoseconds since the Unix epoch (for
/// db.sqlite): GetSystemTimePreciseAsFileTime's 100 ns ticks since 1601,
/// less the 11,644,473,600 s between the epochs. It jumps when the clock is
/// set; Now is the monotonic one. In range of i64 until the year 2262.
/// O(1); allocates nothing.
export WallNow(): i64 {
    var ft: u64 = 0
    unsafe "kernel32; ft is a FILETIME (two u32 words, low first: one u64)" {
        GetSystemTimePreciseAsFileTime(raw.OfMut(mut ft))
    }
    return (Trunc[i64](ft) - 116444736000000000) * 100
}

/// SleepNanos blocks the calling thread for at least ns nanoseconds
/// (sqlite's xSleep): Sleep's milliseconds, rounded up, so a short sleep is
/// never zero, and capped below INFINITE (about 49.7 days); ns <= 0 yields
/// the rest of the time slice (Sleep(0)). It blocks a carrier: for std code
/// below the scheduler (a VFS callback), never for a task that can await a
/// timer.
/// O(1) system calls besides the time asleep; allocates nothing.
export SleepNanos(ns: i64) {
    // The cap is tested before the rounding add, which would overflow for
    // an ns within 1 ms of i64's largest value.
    ms: i64 = if ns <= 0 {
        0
    } else if ns >= MaxSleepMs * 1_000_000 {
        MaxSleepMs
    } else {
        (ns + 999_999) / 1_000_000
    }
    unsafe "a kernel32 call that passes no memory, only values and handles" {
        Sleep(Trunc[u32](ms))
    }
}
