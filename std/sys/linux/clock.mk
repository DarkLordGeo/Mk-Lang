// Why: the runtime, timers and std.time need a monotonic clock, a wall clock
// and a blocking sleep without libc.
//
// What: Now (CLOCK_MONOTONIC), WallNow (CLOCK_REALTIME) and SleepNanos.
//
// How: clock_gettime and nanosleep by direct system call into a two-word
// timespec; SleepNanos repeats nanosleep with the time left after EINTR.
// The vDSO would avoid the system call; it is not used.

import std.raw

/// Now is the monotonic clock in nanoseconds since an arbitrary start.
/// O(1); allocates nothing.
export Now(): i64 {
    var ts: [2]i64 = [0; 2]
    unsafe "system call; ts is a timespec" {
        _ = Syscall(Nrs().clock_gettime, 1, raw.Addr(raw.DataMut(mut ts[..])), 0, 0, 0, 0)
    }
    return ts[0] * 1_000_000_000 + ts[1]
}

/// WallNow is the wall clock in nanoseconds since the Unix epoch (for
/// db.sqlite): clock_gettime(CLOCK_REALTIME). It jumps when the clock is
/// set; Now is the monotonic one. In range of i64 until the year 2262.
/// O(1); allocates nothing.
export WallNow(): i64 {
    var ts: [2]i64 = [0; 2]
    unsafe "system call; ts is a timespec" {
        _ = Syscall(Nrs().clock_gettime, 0, raw.Addr(raw.DataMut(mut ts[..])), 0, 0, 0, 0)
    }
    return ts[0] * 1_000_000_000 + ts[1]
}

/// SleepNanos blocks the calling thread for at least ns nanoseconds
/// (sqlite's xSleep): nanosleep, made again for the
/// time left when a signal interrupts it (EINTR). ns <= 0 returns at once.
/// It blocks a carrier: for std code below the scheduler (a VFS callback),
/// never for a task that can await a timer.
/// O(1) system calls besides the time asleep; allocates nothing.
export SleepNanos(ns: i64) {
    if ns <= 0 {
        return
    }
    var req: [2]i64 = [ns / 1_000_000_000, ns % 1_000_000_000]
    var rem: [2]i64 = [0; 2]
    unsafe "system call; req and rem are timespecs" {
        for {
            r := Syscall(Nrs().nanosleep, raw.Addr(raw.Data(req[..])), raw.Addr(raw.DataMut(mut rem[..])), 0, 0, 0, 0)
            if r != -4 {
                return
            }
            req = rem
        }
    }
}
