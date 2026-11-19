// Why: the wall clock (dates, file times, protocols) needs a compact value
// type whose arithmetic cannot wrap.
//
// What: Time, Wall, FromUnix, Time.Unix, UnixNanos, Add and Sub.
//
// How: UTC nanoseconds since the Unix epoch in an i64, from
// 1677-09-21T00:12:43.145224192Z to 2262-04-11T23:47:16.854775807Z. Add, Sub
// and FromUnix saturate at the ends of that range instead of wrapping or
// panicking. Every operation is O(1) and allocates nothing.

import std.core
import std.rt

/// An instant on the wall clock, UTC nanoseconds since 1970-01-01T00:00:00Z
/// in an i64: from 1677-09-21T00:12:43.145224192Z to
/// 2262-04-11T23:47:16.854775807Z. Plain, Eq, Ord and Hash; formats with
/// {} as RFC 3339. Leap seconds are not represented. Add, Sub and FromUnix
/// saturate at the ends of the range; FromDate and the parsers fail
/// outside it. None of them wraps or panics.
export struct Time {
    nanos: i64
}

// MinNanos and MaxNanos are the ends of Time's range.
const MinNanos: i64 = -9_223_372_036_854_775_807 - 1
const MaxNanos: i64 = 9_223_372_036_854_775_807

// NanosPerSecond is 10^9.
const NanosPerSecond: i64 = 1_000_000_000

/// The wall clock now: the OS's (CLOCK_REALTIME on Linux,
/// GetSystemTimePreciseAsFileTime on Windows), or a Sim's simulated one,
/// which starts at 2026-01-01T00:00:00Z on every seed and moves with the
/// simulated clock. An Io with no clock reads as the epoch. The wall clock
/// can jump when the system's time is set: measure with Now. O(1): one
/// clock read; allocates nothing.
export Wall(io: Io): Time {
    r := rt.WallClock(io)
    return if r.err != core.SysErr.None { Time(nanos: 0) } else { Time(nanos: r.value) }
}

/// The instant seconds and nanos after the epoch; nanos may be any i64 and
/// is carried into seconds. Saturates at the ends of Time's range. O(1).
export FromUnix(seconds: i64, nanos: i64): Time {
    s := SatAdd(seconds, FloorDiv(nanos, NanosPerSecond))
    ns := FloorMod(nanos, NanosPerSecond)
    return Time(nanos: SecondsNanos(s, ns))
}

// SecondsNanos is s * 10^9 + ns saturated, ns in 0..10^9. (s + 1) * 10^9
// fits for every negative s that can reach the range, so the low end needs
// no special case.
SecondsNanos(s: i64, ns: i64): i64 {
    if s > 9_223_372_036 {
        return MaxNanos
    }
    if s < -9_223_372_037 {
        return MinNanos
    }
    if s >= 0 {
        return SatAdd(s * NanosPerSecond, ns)
    }
    return SatAdd((s + 1) * NanosPerSecond, ns - NanosPerSecond)
}

// SatAdd is a + b, saturated at the ends of i64.
SatAdd(a: i64, b: i64): i64 {
    r := a +% b
    if ((a ^ r) & (b ^ r)) < 0 {
        return if b > 0 { MaxNanos } else { MinNanos }
    }
    return r
}

// SatSub is a - b, saturated at the ends of i64.
SatSub(a: i64, b: i64): i64 {
    r := a -% b
    if ((a ^ b) & (a ^ r)) < 0 {
        return if a >= 0 { MaxNanos } else { MinNanos }
    }
    return r
}

/// Whole seconds since the epoch, rounded toward minus infinity, so the
/// nanoseconds left over are never negative. O(1).
export Time.Unix(self): i64 {
    return FloorDiv(self.nanos, NanosPerSecond)
}

/// Nanoseconds since the epoch. O(1).
export Time.UnixNanos(self): i64 {
    return self.nanos
}

/// The instant d after self (before it for a negative d), saturated at the
/// ends of Time's range. O(1).
export Time.Add(self, d: Duration): Time {
    return Time(nanos: SatAdd(self.nanos, d.Nanos()))
}

/// The time from t to self, negative when t is later, saturated at the
/// ends of Duration's range. O(1).
export Time.Sub(self, t: Time): Duration {
    return core.DurationOf(SatSub(self.nanos, t.nanos))
}

/// Writes self as RFC 3339 in UTC, "2006-01-02T15:04:05Z", with as many
/// fraction digits as the nanoseconds need (none when they are zero).
/// One Writer.Bytes call of at most 30 bytes; allocates nothing.
impl Format for Time {
    Format(&self, w: mut Writer): WriteError!void {
        var buf: [32]u8 = [0; 32]
        n := PutRfc3339(mut buf[..], self.nanos, 0)
        try w.Bytes(buf[0..n])
    }
}

/// Equal instants. O(1).
impl Eq for Time {
    Eq(&self, other: &Time): bool {
        return self.nanos == other.nanos
    }
}

/// The earlier instant first. O(1).
impl Ord for Time {
    Less(&self, other: &Time): bool {
        return self.nanos < other.nanos
    }
}

/// Hashes the nanoseconds. O(1).
impl Hash for Time {
    Hash(&self, h: mut Hasher) {
        h.U64(Trunc[u64](self.nanos))
    }
}
