// Why: the runtime, timers and std.time need a monotonic clock, a wall clock
// and a blocking sleep.
//
// What: Now (CLOCK_UPTIME_RAW), WallNow (CLOCK_REALTIME) and SleepNanos.
//
// How: clock_gettime and nanosleep from libSystem into a two-word timespec.
// Now uses CLOCK_UPTIME_RAW, the clock mach_absolute_time reads, which
// stops while the machine sleeps, as Linux's CLOCK_MONOTONIC does;
// SleepNanos repeats nanosleep with the time left after EINTR.

import std.raw

// Darwin's clock ids (<time.h>).
const ClockRealtime: u32 = 0
const ClockUptimeRaw: u32 = 8

/// Now is the monotonic clock in nanoseconds since an arbitrary start.
/// O(1); allocates nothing.
export Now(): i64 {
    var ts: [2]i64 = [0; 2]
    unsafe "ts is a timespec" {
        _ = clock_gettime(ClockUptimeRaw, raw.DataMut(mut ts[..]))
    }
    return ts[0] * 1_000_000_000 + ts[1]
}

/// WallNow is the wall clock in nanoseconds since the Unix epoch:
/// clock_gettime(CLOCK_REALTIME). It jumps when the clock is set; Now is
/// the monotonic one. In range of i64 until the year 2262.
/// O(1); allocates nothing.
export WallNow(): i64 {
    var ts: [2]i64 = [0; 2]
    unsafe "ts is a timespec" {
        _ = clock_gettime(ClockRealtime, raw.DataMut(mut ts[..]))
    }
    return ts[0] * 1_000_000_000 + ts[1]
}

/// SleepNanos blocks the calling thread for at least ns nanoseconds:
/// nanosleep, made again for the time left when a signal interrupts it
/// (EINTR). ns <= 0 returns at once. It blocks a carrier: for std code
/// below the scheduler, never for a task that can await a timer.
/// O(1) system calls besides the time asleep; allocates nothing.
export SleepNanos(ns: i64) {
    if ns <= 0 {
        return
    }
    var req: [2]i64 = [ns / 1_000_000_000, ns % 1_000_000_000]
    var rem: [2]i64 = [0; 2]
    unsafe "req and rem are timespecs" {
        for {
            if nanosleep(raw.Data(req[..]), raw.DataMut(mut rem[..])) == 0 || Errno() != 4 {
                return
            }
            req = rem
        }
    }
}
