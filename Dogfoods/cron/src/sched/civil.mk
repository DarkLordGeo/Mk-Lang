// Why: walking a schedule day by day needs a day number, which std.time's
// date conversions do not give.
//
// What: Day, DaysFromCivil, CivilFromDays, Weekday and DaysInMonth.
//
// How: proleptic Gregorian arithmetic after Howard Hinnant's
// "chrono-Compatible Low-Level Date Algorithms" (days_from_civil and
// civil_from_days).

/// A calendar day.
export struct Day {
    export year: i32
    export month: int
    export day: int
}

/// Days from 1970-01-01 to the given day; negative before it.
export DaysFromCivil(year: i32, month: int, day: int): i64 {
    y := if month <= 2 { i64(year) - 1 } else { i64(year) }
    era := if y >= 0 { y / 400 } else { (y - 399) / 400 }
    yoe := y - era * 400
    mp := if month > 2 { i64(month) - 3 } else { i64(month) + 9 }
    doy := (153 * mp + 2) / 5 + i64(day) - 1
    doe := yoe * 365 + yoe / 4 - yoe / 100 + doy
    return era * 146097 + doe - 719468
}

/// The day `days` after 1970-01-01.
export CivilFromDays(days: i64): Day {
    z := days + 719468
    era := if z >= 0 { z / 146097 } else { (z - 146096) / 146097 }
    doe := z - era * 146097
    yoe := (doe - doe / 1460 + doe / 36524 - doe / 146096) / 365
    doy := doe - (365 * yoe + yoe / 4 - yoe / 100)
    mp := (5 * doy + 2) / 153
    day := doy - (153 * mp + 2) / 5 + 1
    month := if mp < 10 { mp + 3 } else { mp - 9 }
    year := if month <= 2 { yoe + era * 400 + 1 } else { yoe + era * 400 }
    return Day(year: Cast[i32](year), month: Cast[int](month), day: Cast[int](day))
}

/// The weekday of a day number, 0 for Sunday; 1970-01-01 was a Thursday.
export Weekday(days: i64): int {
    w := (days + 4) % 7
    return Cast[int](if w < 0 { w + 7 } else { w })
}

/// The number of days in a month.
export DaysInMonth(year: i32, month: int): int {
    return match month {
        2 {
            leap := (year % 4 == 0 && year % 100 != 0) || year % 400 == 0
            if leap { 29 } else { 28 }
        }
        4 | 6 | 9 | 11 { 30 }
        _ { 31 }
    }
}

test "DaysFromCivil and CivilFromDays agree across eras and leap days" {
    Assert(DaysFromCivil(1970, 1, 1) == 0)
    Assert(DaysFromCivil(2000, 3, 1) == 11017)
    for n in -800_000..800_000 {
        if n % 997 != 0 {
            continue
        }
        d := CivilFromDays(i64(n))
        Assert(DaysFromCivil(d.year, d.month, d.day) == i64(n))
    }
}

test "Weekday names 2026-03-29 a Sunday" {
    Assert(Weekday(DaysFromCivil(2026, 3, 29)) == 0)
    Assert(Weekday(DaysFromCivil(1969, 12, 31)) == 3)
}

test "DaysInMonth knows the century rule" {
    Assert(DaysInMonth(2000, 2) == 29)
    Assert(DaysInMonth(2100, 2) == 28)
    Assert(DaysInMonth(2028, 2) == 29)
}
