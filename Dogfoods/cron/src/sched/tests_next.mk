// Why: daylight-saving transitions are where schedulers go wrong.
//
// What: tests of skipped and repeated local times, Lord Howe's half-hour
// shift, a schedule that never fires, and two times meeting at a transition.
//
// How: builds daily specs with Daily and checks Next against known instants.

import std.time
import std.time.tzdata
import src.crontab

/// A spec of one minute and hour on every day.
Daily(hour: int, minute: int): crontab.Spec {
    return crontab.Spec(
        minutes: u64(1) << Cast[u64](minute),
        hours: u64(1) << Cast[u64](hour),
        days: 0xfffffffe,
        months: 0x1ffe,
        weekdays: 0x7f,
        any_day: true,
        any_weekday: true,
    )
}

test "a local time a spring transition skips resolves to the transition" (sys: &System) {
    z := try time.Zone.Load(sys.arena, sys.io, "Europe/Berlin", some(tzdata.Database))
    f := Resolve(&z, &Day(year: 2026, month: 3, day: 29), 2, 30).Must()
    Assert(f.fit == Fit.Skipped)
    Assert(f.at.UnixNanos() == time.FromUnix(DaysFromCivil(2026, 3, 29) * 86400 + 3600, 0).UnixNanos())
}

test "a local time an autumn transition repeats resolves to its first occurrence" (sys: &System) {
    z := try time.Zone.Load(sys.arena, sys.io, "Europe/Berlin", some(tzdata.Database))
    f := Resolve(&z, &Day(year: 2026, month: 10, day: 25), 2, 30).Must()
    Assert(f.fit == Fit.Repeated)
    // 02:30 at +02:00 is 00:30 UTC; the second 02:30, at +01:00, is 01:30
    Assert(f.at.Unix() == DaysFromCivil(2026, 10, 25) * 86400 + 1800)
}

test "Lord Howe's half-hour transition is found to the second" (sys: &System) {
    z := try time.Zone.Load(sys.arena, sys.io, "Australia/Lord_Howe", some(tzdata.Database))
    f := Resolve(&z, &Day(year: 2026, month: 10, day: 4), 2, 15).Must()
    Assert(f.fit == Fit.Skipped)
    Assert(f.at.Unix() == DaysFromCivil(2026, 10, 3) * 86400 + 15 * 3600 + 1800)
}

test "a schedule that never comes round gives none" (sys: &System) {
    z := time.Zone.Utc()
    var spec := Daily(0, 0)
    spec.days = u64(1) << 31
    spec.months = u64(1) << 2
    spec.any_day = false
    Assert(Next(&spec, &z, time.FromUnix(0, 0)).IsNone())
    _ = sys
}

test "an entry fires once when two of its times meet at a transition" (sys: &System) {
    z := try time.Zone.Load(sys.arena, sys.io, "Europe/Berlin", some(tzdata.Database))
    var spec := Daily(2, 0)
    spec.hours = (u64(1) << 2) | (u64(1) << 3)
    start := time.FromUnix(DaysFromCivil(2026, 3, 29) * 86400, 0)
    first := Next(&spec, &z, start).Must()
    second := Next(&spec, &z, first.at).Must()
    Assert(first.fit == Fit.Skipped)
    // 03:00 is the transition itself, so the next fire is the next day's 02:00
    Assert(second.at.Unix() == DaysFromCivil(2026, 3, 30) * 86400)
}
