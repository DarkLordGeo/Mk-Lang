// Why: Io.Sleep and timeouts in the prelude need a time span type, so
// Duration lives in std.core; std.time aliases it and adds the clock.
//
// What: Duration (Nanos, Format), DurationOf for std.time, and the text
// helpers DurationText, DurationFraction and DurationWhole.
//
// How: a Duration is a signed count of nanoseconds. Format writes Go's
// Duration.String form right to left into a fixed buffer, so it allocates
// nothing; the magnitude is taken as an unsigned negation, so i64's least
// value needs no special case.

/// A span of time, as a signed count of nanoseconds in an i64. Plain, Send
/// and Share.
/// std.time makes and measures durations.
export struct Duration {
    count: i64
}

/// The duration in nanoseconds. O(1).
export Duration.Nanos(self): i64 {
    return self.count
}

/// The Duration of nanos nanoseconds, for std.time; programs write
/// time.Nanos, time.Seconds and the like. O(1).
export DurationOf(nanos: i64): Duration {
    return Duration(count: nanos)
}

/// Writes the duration in the form time.ParseDuration reads: hours, minutes
/// and seconds with the fraction the nanoseconds need ("72h3m0.5s",
/// "-1m30s"), a span under a second in the largest unit below it ("1.5ms",
/// "100µs", "7ns"), and "0s" for zero. One Writer.Bytes call of at most 25
/// bytes; allocates nothing.
impl Format for Duration {
    Format(&self, w: mut Writer): WriteError!void {
        var buf: [32]u8 = [0; 32]
        start := DurationText(mut buf[..], self.count)
        try w.Bytes(buf[start..32])
    }
}

// DurationDigits is where DurationText's writing has reached in its buffer, and
// the value left to write.
struct DurationDigits {
    at: int
    v: u64
}

// DurationText writes d's text so that it ends at b[32] and returns where
// it starts. The magnitude is d's two's complement negation, so i64's
// least value needs no special case.
DurationText(b: mut []u8, d: i64): int {
    var at := 32
    var u := Trunc[u64](d)
    if d < 0 {
        u = 0 -% u
    }
    at -= 1
    b[at] = 115
    if u < 1_000_000_000 {
        at -= 1
        if u == 0 {
            b[at] = 48
            return at
        }
        var prec := 0
        if u < 1000 {
            b[at] = 110
        } else if u < 1_000_000 {
            // U+00B5 MICRO SIGN.
            prec = 3
            at -= 1
            b[at] = 0xC2
            b[at + 1] = 0xB5
        } else {
            prec = 6
            b[at] = 109
        }
        f := DurationFraction(b, at, u, prec)
        at = DurationWhole(b, f.at, f.v)
    } else {
        f := DurationFraction(b, at, u, 9)
        at = DurationWhole(b, f.at, f.v % 60)
        mins := f.v / 60
        if mins > 0 {
            at -= 1
            b[at] = 109
            at = DurationWhole(b, at, mins % 60)
            hours := mins / 60
            if hours > 0 {
                at -= 1
                b[at] = 104
                at = DurationWhole(b, at, hours)
            }
        }
    }
    if d < 0 {
        at -= 1
        b[at] = 45
    }
    return at
}

// DurationFraction writes the prec low decimal digits of v before b[at], without
// trailing zeros and with a '.' when any digit is written; it returns the
// new start and v without those digits.
DurationFraction(b: mut []u8, at: int, v: u64, prec: int): DurationDigits {
    var w := at
    var x := v
    var print := false
    for _ in 0..prec {
        digit := x % 10
        print = print || digit != 0
        if print {
            w -= 1
            b[w] = 48 + Trunc[u8](digit)
        }
        x /= 10
    }
    if print {
        w -= 1
        b[w] = 46
    }
    return DurationDigits(at: w, v: x)
}

// DurationWhole writes v in decimal before b[at] and returns the new start.
DurationWhole(b: mut []u8, at: int, v: u64): int {
    var w := at
    var x := v
    if x == 0 {
        w -= 1
        b[w] = 48
        return w
    }
    for x > 0 {
        w -= 1
        b[w] = 48 + Trunc[u8](x % 10)
        x /= 10
    }
    return w
}
