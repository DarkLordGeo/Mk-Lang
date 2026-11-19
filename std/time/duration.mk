// Why: durations arrive as text in configuration and flags, and must read
// back exactly what Duration.Format writes.
//
// What: ParseDuration and its unit table UnitOf.
//
// How: Go's time.ParseDuration form and arithmetic exactly, so a duration Go
// writes reads back the same. The text is untrusted: one left-to-right pass,
// no recursion, no allocation.

import std.core

// The units a duration names, in nanoseconds.
UnitOf(u: []u8): ?u64 {
    n := u.Len()
    if n == 1 {
        c := u[0]
        if c == b's' {
            return some(1_000_000_000)
        }
        if c == b'm' {
            return some(60_000_000_000)
        }
        if c == b'h' {
            return some(3_600_000_000_000)
        }
        return none
    }
    if n == 2 && u[1] == b's' {
        c := u[0]
        if c == b'n' {
            return some(1)
        }
        if c == b'u' {
            return some(1000)
        }
        if c == b'm' {
            return some(1_000_000)
        }
        return none
    }
    // U+00B5 MICRO SIGN and U+03BC GREEK SMALL LETTER MU, then 's'.
    if n == 3 && u[2] == b's' && ((u[0] == 0xC2 && u[1] == 0xB5) || (u[0] == 0xCE && u[1] == 0xBC)) {
        return some(1000)
    }
    return none
}

// Half is 2^63, the magnitude of the most negative Duration.
const Half: u64 = 1 << 63

/// The Duration s writes: an optional sign, then one or more of a decimal
/// number with an optional fraction and a unit, "ns", "us" (or "µs", "μs"),
/// "ms", "s", "m" or "h", as "300ms", "-1.5h" or "2h45m"; "0" alone is
/// zero. ParseError when s is not one, has no unit, or leaves Duration's
/// range. A fraction is scaled in f64, which is exact to the nanosecond
/// for every unit up to an hour. O(n); allocates nothing.
export ParseDuration(s: str): ParseError!Duration {
    b := s.AsBytes()
    var i := 0
    var neg := false
    if b.Len() > 0 && (b[0] == b'-' || b[0] == b'+') {
        neg = b[0] == b'-'
        i = 1
    }
    if b.Len() - i == 1 && b[i] == b'0' {
        return core.DurationOf(0)
    }
    if i == b.Len() {
        fail ParseError
    }
    var d: u64 = 0
    for i < b.Len() {
        c := b[i]
        if c != b'.' && (c < b'0' || c > b'9') {
            fail ParseError
        }
        // The integer part; more than 2^63 is out of range.
        var v: u64 = 0
        start := i
        for i < b.Len() && b[i] >= b'0' && b[i] <= b'9' {
            if v > Half / 10 {
                fail ParseError
            }
            v = v * 10 + u64(b[i] - b'0')
            if v > Half {
                fail ParseError
            }
            i += 1
        }
        pre := i != start
        // The fraction: digits past what fits are read and dropped.
        var f: u64 = 0
        var scale := 1.0
        var post := false
        if i < b.Len() && b[i] == b'.' {
            i += 1
            fstart := i
            var over := false
            for i < b.Len() && b[i] >= b'0' && b[i] <= b'9' {
                if !over {
                    if f > (Half - 1) / 10 {
                        over = true
                    } else {
                        y := f * 10 + u64(b[i] - b'0')
                        if y > Half {
                            over = true
                        } else {
                            f = y
                            scale *= 10.0
                        }
                    }
                }
                i += 1
            }
            post = i != fstart
        }
        if !pre && !post {
            fail ParseError
        }
        ustart := i
        for i < b.Len() && b[i] != b'.' && (b[i] < b'0' || b[i] > b'9') {
            i += 1
        }
        unit := UnitOf(b[ustart..i]) else {
            fail ParseError
        }
        if v > Half / unit {
            fail ParseError
        }
        v *= unit
        if f > 0 {
            v += Cast[u64](Cast[f64](f) * (Cast[f64](unit) / scale))
            if v > Half {
                fail ParseError
            }
        }
        d += v
        if d > Half {
            fail ParseError
        }
    }
    if neg {
        return core.DurationOf(0 -% Trunc[i64](d))
    }
    if d > Half - 1 {
        fail ParseError
    }
    return core.DurationOf(Trunc[i64](d))
}
