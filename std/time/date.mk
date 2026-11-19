// Why: programs show and accept times as civil fields in a zone, and the
// conversion back must not guess when a transition skipped or repeated a
// local time.
//
// What: Date, Time.In, Date.Time and FromDate.
//
// How: a local time a transition skipped is Skipped and one it repeated is
// Repeated, never a guess; fields or an instant outside their range are
// OutOfRange, never a wrap or a panic. The day arithmetic is civil.mk's.

/// The civil fields of an instant in a zone: the date in the proleptic
/// Gregorian calendar, the time of day, the weekday (0 is Sunday), the day
/// of the year (1 is January 1) and the zone's offset from UTC in seconds,
/// east positive. Leap seconds are not represented: second is 0 to 59.
export struct Date {
    /// The year of the proleptic Gregorian calendar.
    export year: i32
    /// The month, 1 to 12.
    export month: int
    /// The day of the month, from 1.
    export day: int
    /// The hour, 0 to 23.
    export hour: int
    /// The minute, 0 to 59.
    export minute: int
    /// The second, 0 to 59.
    export second: int
    /// The nanoseconds within the second, 0 to 999999999.
    export nanos: int
    /// The weekday, 0 (Sunday) to 6.
    export weekday: int
    /// The day of the year, from 1 (January 1).
    export year_day: int
    /// The zone's offset from UTC in seconds, east positive.
    export offset_seconds: i32
}

/// The instant self shows: its fields less its offset. The fields are not
/// checked: each carries into the next (month 13 is January of the next
/// year, minute -1 the last minute of the hour before), and the result
/// saturates at the ends of Time's range, as FromUnix does. It never
/// panics. It is exact whenever no field exceeds 2^62 in magnitude; past
/// that, a carry that would leave i64 saturates first. The weekday and the
/// day of the year are ignored. Use FromDate to have the fields checked.
/// O(1); allocates nothing.
export Date.Time(self): Time {
    // Nanoseconds, seconds, minutes and hours carry upward by floor
    // division, so each lower field ends in its range.
    var c := FloorDiv(Cast[i64](self.nanos), NanosPerSecond)
    ns := FloorMod(Cast[i64](self.nanos), NanosPerSecond)
    sc := SatAdd(Cast[i64](self.second), c)
    c = FloorDiv(sc, 60)
    mn := SatAdd(Cast[i64](self.minute), c)
    c = FloorDiv(mn, 60)
    hr := SatAdd(Cast[i64](self.hour), c)
    c = FloorDiv(hr, 24)
    dd := SatAdd(SatAdd(Cast[i64](self.day), -1), c)
    // The month as 12q + m, m in 1..12, so month - 1 is never formed.
    mo := Cast[i64](self.month)
    var q := FloorDiv(mo, 12)
    var m := Cast[int](FloorMod(mo, 12))
    if m == 0 {
        m = 12
        q -= 1
    }
    // |q| < 2^60, so the year cannot leave i64; DaysFromCivil is exact to
    // |y| = 2^54, and a year past that is past Time's range however far
    // the days carry back.
    var y := Cast[i64](self.year) + q
    if y > YearLimit {
        y = YearLimit
    }
    if y < -YearLimit {
        y = -YearLimit
    }
    days := SatAdd(DaysFromCivil(y, m, 1), dd)
    if days > DayLimit {
        return Time(nanos: MaxNanos)
    }
    if days < -DayLimit {
        return Time(nanos: MinNanos)
    }
    secs := days * SecondsPerDay + FloorMod(hr, 24) * 3600 + FloorMod(mn, 60) * 60 + FloorMod(sc, 60) - Cast[i64](self.offset_seconds)
    return FromUnix(secs, ns)
}

// YearLimit is the largest year Date.Time hands DaysFromCivil (its era
// product stays below 2^63), and DayLimit a day count well past Time's
// range (about 107,000 days) on which the clock and offset still fit.
const YearLimit: i64 = 1 << 54
const DayLimit: i64 = 1 << 40

/// The civil fields of self in z. O(log n) for a zone of n transitions
/// (one binary search; an instant past the zone's table adds O(1) for its
/// rule); allocates nothing.
export Time.In(self, z: &Zone): Date {
    secs := FloorDiv(self.nanos, NanosPerSecond)
    ns := FloorMod(self.nanos, NanosPerSecond)
    off := z.OffsetAt(secs)
    return DateOf(secs + Cast[i64](off), Cast[int](ns), off)
}

// DateOf is the Date of local seconds since the epoch with offset off.
// Every local second of Time's range is within 2^40 days.
DateOf(local: i64, ns: int, off: i32): Date {
    days := FloorDiv(local, SecondsPerDay)
    sod := Cast[int](FloorMod(local, SecondsPerDay))
    c := CivilFromDays(days)
    yday := Cast[int](days - DaysFromCivil(c.year, 1, 1)) + 1
    return Date(year: Cast[i32](c.year), month: c.month, day: c.day, hour: sod / 3600, minute: sod / 60 % 60, second: sod % 60, nanos: ns, weekday: Weekday(days), year_day: yday, offset_seconds: off)
}

// LocalLow and LocalHigh bound the local seconds FromDate resolves: two
// days past Time's range on each side, so no zone lookup runs outside the
// years 1677 to 2262 and the arithmetic stays far from i64's ends.
const LocalLow: i64 = -9_223_372_037 - 2 * 86_400
const LocalHigh: i64 = 9_223_372_037 + 2 * 86_400

/// The instant whose civil fields in z are the ones given. Fails with
/// OutOfRange when a field is outside its range (month 1 to 12, day 1 to
/// the month's length, hour 0 to 23, minute and second 0 to 59, nanos 0 to
/// 999,999,999) or the instant is outside Time's range; Skipped when a
/// transition of z skipped that local time; Repeated when one repeated it.
/// It never wraps or panics. O(log n + k) for a zone of n transitions, k
/// of them within two days of the local time; allocates nothing.
export FromDate(year: i32, month: int, day: int, hour: int, minute: int, second: int, nanos: int, z: &Zone): Error!Time {
    y := Cast[i64](year)
    if month < 1 || month > 12 || day < 1 || day > DaysIn(y, month) {
        fail OutOfRange
    }
    if hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 59 || nanos < 0 || nanos > 999_999_999 {
        fail OutOfRange
    }
    local := DaysFromCivil(y, month, day) * SecondsPerDay + Cast[i64](hour * 3600 + minute * 60 + second)
    if local < LocalLow || local > LocalHigh {
        fail OutOfRange
    }
    u := try z.Resolve(local)
    if u > 9_223_372_036 || u < -9_223_372_037 {
        fail OutOfRange
    }
    t := SecondsNanos(u, Cast[i64](nanos))
    if t == MaxNanos && (u != 9_223_372_036 || nanos != 854_775_807) {
        fail OutOfRange
    }
    if t == MinNanos && (u != -9_223_372_037 || nanos != 145_224_192) {
        fail OutOfRange
    }
    return Time(nanos: t)
}
