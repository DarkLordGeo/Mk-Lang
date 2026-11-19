// Why: the daemon needs the next instant each schedule fires in its zone,
// across daylight-saving transitions.
//
// What: Next, Fire, Fit, DayMatches and Resolve.
//
// How: as in cronie and Vixie cron, a local time a transition skipped runs
// once, at the transition; a local time a transition repeated runs once, at
// its first occurrence. An entry fires at most once at any instant, so
// `0 2 * * *` and `0 3 * * *` landing on one transition are two entries
// firing together, never one twice.

import src.crontab
import std.time

/// How a fire's local time sat against the zone's transitions.
export enum Fit {
    Exact
    Skipped
    Repeated
}

/// One fire: the instant, and how its local time was resolved.
export struct Fire {
    export at: time.Time
    export fit: Fit
}

/// How far ahead a schedule is searched: eight years and a day, so a
/// schedule of February 29 is found across a century year.
const SearchDays: i64 = 8 * 366 + 1

/// The first fire of `spec` in `z` strictly after `after`; none when it
/// never fires within SearchDays (`0 0 30 2 *`). O(days searched) day
/// tests, then O(1440) local times on a matching day.
export Next(spec: &crontab.Spec, z: &time.Zone, after: time.Time): ?Fire {
    local := after.In(z)
    first := DaysFromCivil(local.year, local.month, local.day)
    // a repeated hour can put an earlier wall time after `after`, so the
    // first day is searched from three hours before its wall time
    from := local.hour * 60 + local.minute - 180
    for k in 0..SearchDays {
        days := first + k
        day := CivilFromDays(days)
        if !DayMatches(spec, &day, Weekday(days)) {
            continue
        }
        for h in 0..24 {
            if spec.hours & (u64(1) << Cast[u64](h)) == 0 {
                continue
            }
            for m in 0..60 {
                if spec.minutes & (u64(1) << Cast[u64](m)) == 0 {
                    continue
                }
                if k == 0 && h * 60 + m < from {
                    continue
                }
                f := Resolve(z, &day, h, m) else {
                    continue
                }
                if f.at.UnixNanos() > after.UnixNanos() {
                    return some(f)
                }
            }
        }
    }
    return none
}

/// crontab(5): when both day fields are restricted, a day matches either;
/// otherwise it matches the restricted one.
DayMatches(spec: &crontab.Spec, day: &Day, weekday: int): bool {
    if spec.months & (u64(1) << Cast[u64](day.month)) == 0 {
        return false
    }
    by_day := spec.days & (u64(1) << Cast[u64](day.day)) != 0
    by_weekday := spec.weekdays & (u64(1) << Cast[u64](weekday)) != 0
    if spec.any_day && spec.any_weekday {
        return true
    }
    if spec.any_day {
        return by_weekday
    }
    if spec.any_weekday {
        return by_day
    }
    return by_day || by_weekday
}

/// The instant of a local time in `z`, by the rules above; none when the
/// date is outside `time.Time`'s range.
export Resolve(z: &time.Zone, day: &Day, hour: int, minute: int): ?Fire {
    t := time.FromDate(day.year, day.month, day.day, hour, minute, 0, 0, z) else e {
        wall := DaysFromCivil(day.year, day.month, day.day) * 86400 + i64(hour) * 3600 + i64(minute) * 60
        return match e {
            time.Skipped { some(Fire(at: time.FromUnix(Transition(z, wall), 0), fit: Fit.Skipped)) }
            time.Repeated { some(Fire(at: time.FromUnix(FirstOccurrence(z, wall), 0), fit: Fit.Repeated)) }
            _ { none }
        }
    }
    return some(Fire(at: t, fit: Fit.Exact))
}

/// The zone's offset at the instant `unix`, in seconds east of UTC.
export Offset(z: &time.Zone, unix: i64): i64 {
    return i64(time.FromUnix(unix, 0).In(z).offset_seconds)
}

/// The two offsets in force half a day either side of a wall time.
Around(z: &time.Zone, wall: i64): (before: i64, after: i64) {
    return (before: Offset(z, wall - 43200), after: Offset(z, wall + 43200))
}

/// The instant a forward transition skipped `wall` (seconds of local time
/// counted as if it were UTC): the first instant whose wall time is at or
/// past it, found by bisection between the two offsets' readings.
Transition(z: &time.Zone, wall: i64): i64 {
    (before, after) := Around(z, wall)
    var lo := wall - Max(before, after)
    var hi := wall - Min(before, after)
    for hi - lo > 1 {
        mid := lo + (hi - lo) / 2
        if mid + Offset(z, mid) >= wall {
            hi = mid
        } else {
            lo = mid
        }
    }
    return hi
}

/// The earlier of the two instants a backward transition gives `wall`.
FirstOccurrence(z: &time.Zone, wall: i64): i64 {
    (before, after) := Around(z, wall)
    a := wall - before
    b := wall - after
    early := Min(a, b)
    late := Max(a, b)
    if early + Offset(z, early) == wall {
        return early
    }
    return late
}

Min(a: i64, b: i64): i64 {
    return if a < b { a } else { b }
}

Max(a: i64, b: i64): i64 {
    return if a > b { a } else { b }
}
