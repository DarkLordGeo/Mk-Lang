// Why: every conversion between an instant and a calendar date goes through
// day counts, and those must be exact over the whole i64 range.
//
// What: DaysFromCivil, CivilFromDays, IsLeap, DaysIn, Weekday, FloorDiv,
// FloorMod and the constant SecondsPerDay.
//
// How: the proleptic Gregorian calendar by Howard Hinnant's days_from_civil
// and civil_from_days ("chrono-Compatible Low-Level Date Algorithms",
// howardhinnant.github.io/date_algorithms.html). Both are branch-light O(1)
// integer arithmetic over 400-year eras, exact for every i64 day count whose
// year fits i64, and allocate nothing.

// SecondsPerDay is 86,400: leap seconds are not represented.
const SecondsPerDay: i64 = 86_400

// Civil is a proleptic Gregorian date.
struct Civil {
    year: i64
    month: int
    day: int
}

// FloorDiv is a / b rounded toward minus infinity, b > 0.
FloorDiv(a: i64, b: i64): i64 {
    q := a / b
    return if a % b < 0 { q - 1 } else { q }
}

// FloorMod is a - b * FloorDiv(a, b), in 0..b, b > 0.
FloorMod(a: i64, b: i64): i64 {
    r := a % b
    return if r < 0 { r + b } else { r }
}

// DaysFromCivil is the number of days from 1970-01-01 to y-m-d, negative
// before it; m in 1..12, d in 1..31. Hinnant's days_from_civil: the year
// is shifted to start in March, so the leap day is the era's last.
// |y| up to 2^40 cannot overflow.
DaysFromCivil(y: i64, m: int, d: int): i64 {
    yy := if m <= 2 { y - 1 } else { y }
    era := if yy >= 0 { yy / 400 } else { (yy - 399) / 400 }
    yoe := yy - era * 400
    mp := if m > 2 { m - 3 } else { m + 9 }
    doy := Cast[i64]((153 * mp + 2) / 5 + d - 1)
    doe := yoe * 365 + yoe / 4 - yoe / 100 + doy
    return era * 146_097 + doe - 719_468
}

// CivilFromDays is the date days after 1970-01-01. Hinnant's
// civil_from_days, the inverse of DaysFromCivil. |days| up to 2^40.
CivilFromDays(days: i64): Civil {
    z := days + 719_468
    era := if z >= 0 { z / 146_097 } else { (z - 146_096) / 146_097 }
    doe := z - era * 146_097
    yoe := (doe - doe / 1460 + doe / 36_524 - doe / 146_096) / 365
    doy := doe - (365 * yoe + yoe / 4 - yoe / 100)
    mp := (5 * doy + 2) / 153
    d := Cast[int](doy - (153 * mp + 2) / 5 + 1)
    m := Cast[int](if mp < 10 { mp + 3 } else { mp - 9 })
    y := yoe + era * 400
    return Civil(year: if m <= 2 { y + 1 } else { y }, month: m, day: d)
}

// IsLeap is whether y is a Gregorian leap year.
IsLeap(y: i64): bool {
    return y % 4 == 0 && (y % 100 != 0 || y % 400 == 0)
}

// DaysIn is the length of month m (1..12) of year y.
DaysIn(y: i64, m: int): int {
    if m == 2 {
        return if IsLeap(y) { 29 } else { 28 }
    }
    return if m == 4 || m == 6 || m == 9 || m == 11 { 30 } else { 31 }
}

// Weekday of a day count from 1970-01-01 (a Thursday): 0 is Sunday.
Weekday(days: i64): int {
    return Cast[int](FloorMod(days + 4, 7))
}
