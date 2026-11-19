// Why: timeouts and measurements need a monotonic clock and duration
// constructors that every std module can use.
//
// What: Instant, Now, Instant.Since, Duration (std.core's type) and its
// constructors Seconds, Millis, Minutes, Hours, Micros and Nanos.
//
// How: Instant reads IoTable.now. Duration is declared in std.core so
// Io.Sleep can name it. Every operation is O(1) and allocates nothing.
// Overflow is the language's checked i64 arithmetic: a panic, never a wrap.

import std.core

/// A signed span of time, a count of nanoseconds in an i64: about 292
/// years either way. Plain. It is std.core's Duration, so a time.Duration
/// is what Io.Sleep takes, and it prints with {} as "1.5s".
export type Duration = core.Duration

/// A point on the monotonic clock, in nanoseconds from an origin the
/// runtime fixes at start, for measuring how long something takes. Only
/// differences mean anything: an Instant is never a wall-clock time, and
/// the clock never goes backwards. Use Wall for the date and time.
export struct Instant {
    nanos: i64
}

/// The Duration of n seconds. Panics (code 1) when n * 1e9 leaves i64,
/// beyond about 292 years either way. O(1).
export Seconds(n: i64): Duration {
    return core.DurationOf(n * 1_000_000_000)
}

/// The Duration of n milliseconds. Panics (code 1) when n * 1e6 leaves i64.
/// O(1).
export Millis(n: i64): Duration {
    return core.DurationOf(n * 1_000_000)
}

/// The monotonic clock now. It never fails: the system's clock always
/// answers, and an Io with no clock reads as the origin, 0. Under a Sim it
/// is the simulated clock. O(1): one clock read; allocates nothing.
export Now(io: Io): Instant {
    r := core.IoNow(io)
    return if r.err != core.SysErr.None { Instant(nanos: 0) } else { Instant(nanos: r.value) }
}

/// The time from earlier to self, negative when earlier is later. Panics
/// (code 1) when the difference leaves i64, which two readings of one
/// monotonic clock never do. O(1).
export Instant.Since(self, earlier: Instant): Duration {
    return core.DurationOf(self.nanos - earlier.nanos)
}

/// The Duration of n minutes. Panics (code 1) when n * 6e10 leaves i64,
/// beyond about 292 years either way, as Seconds does. O(1).
export Minutes(n: i64): Duration {
    return core.DurationOf(n * 60_000_000_000)
}

/// The Duration of n hours. Panics (code 1) when n * 3.6e12 leaves i64.
/// O(1).
export Hours(n: i64): Duration {
    return core.DurationOf(n * 3_600_000_000_000)
}

/// The Duration of n microseconds. Panics (code 1) when n * 1e3 leaves
/// i64. O(1).
export Micros(n: i64): Duration {
    return core.DurationOf(n * 1000)
}

/// The Duration of n nanoseconds; every i64 is one. O(1).
export Nanos(n: i64): Duration {
    return core.DurationOf(n)
}
