// Why: the local zone comes from different sources on each OS, and must give
// the same offsets a zone file would.
//
// What: Zone.Local, with ZoneFromTz and LocalFile for Linux and WindowsZone,
// TziTable and TziRule for Windows.
//
// How: on Windows it comes from GetDynamicTimeZoneInformation and
// GetTimeZoneInformationForYear (no registry read): the bias and the standard
// and daylight dates of each record become the POSIX rule they mean, as Go's
// initLocalFromTZI reads them, the record in force for the years to come and
// each year's own record for the years since 1970. On Linux it comes from TZ
// (a zone name, ":name", an absolute path, or a POSIX rule string), else
// /etc/localtime, else UTC, the system's own meaning of a missing or
// unreadable file. A simulated Io sees neither the OS's zone nor the
// process's TZ, so a simulation's local zone is UTC, with no operation on any
// target.

import std.core
import std.rt

// DynamicTzi is the size of DYNAMIC_TIME_ZONE_INFORMATION.
const DynamicTzi: int = 432

/// The zone the system is set to.
///
/// On Linux it comes from TZ (a zone name, ":name", or a POSIX rule
/// string such as "CET-1CEST,M3.5.0,M10.5.0/3"), else /etc/localtime, else
/// UTC. A zone file that is missing or cannot be read is UTC; for a zone
/// name in TZ, TZ is then read as a POSIX rule, and UTC when it is not one.
/// On Windows it comes from the system's time-zone calls, with no registry
/// read. Under a simulated Io it is UTC, read from nothing, on every
/// target.
///
/// Fails with BadData when the system's zone file is not valid TZif,
/// Canceled when the scope is cancelled, and OutOfMemory when a cannot
/// hold the zone. O(n) in the zone's bytes on Linux; on Windows one call
/// for the zone in force and one for each year since 1970, and O(years)
/// work. It allocates its transitions in a.
export Zone.Local(a: &Arena, io: Io): Error|OutOfMemory!Zone from a {
    if Simulated(io) {
        return Zone.Utc()
    }
    if Target.os == Os.Windows {
        return try WindowsZone(a, io)
    }
    var tmp := try a.Sub()
    defer tmp.Free()
    match try EnvValue(&tmp, io, "TZ") {
        some(tz) {
            return try ZoneFromTz(a, io, tz)
        }
        none {}
    }
    return try LocalFile(a, io, "/etc/localtime", "Local")
}

// ZoneFromTz is the zone a TZ value names, as glibc's tzset reads it: empty
// is UTC; a leading ':' is dropped; an absolute path is a TZif file; a zone
// name is read from TZDIR or /usr/share/zoneinfo; anything else, or a name
// with no file there that can be read, is read as a POSIX rule string, and
// one that is not one is UTC.
ZoneFromTz(a: &Arena, io: Io, tz: str): Error|OutOfMemory!Zone from a {
    var s := tz
    if s.StartsWith(":") {
        s = s[1..]
    }
    if s.Len() == 0 {
        return Zone.Utc()
    }
    if s.StartsWith("/") {
        return try LocalFile(a, io, s, s)
    }
    if ValidName(s) {
        var tmp := try a.Sub()
        defer tmp.Free()
        r := try FindZoneFile(&tmp, io, s)
        if r.how == Found {
            return try Zone.FromTzif(a, s, r.data)
        }
    }
    return ZoneOf(RuleOf(s))
}

// LocalFile is the zone of the TZif file at path, named name: BadData when
// the file is not TZif, UTC when it is missing or cannot be read.
LocalFile(a: &Arena, io: Io, path: str, name: str): Error|OutOfMemory!Zone from a {
    var tmp := try a.Sub()
    defer tmp.Free()
    r := try ReadZoneFile(&tmp, io, path)
    if r.how != Found {
        return Zone.Utc()
    }
    return try Zone.FromTzif(a, name, r.data)
}

// RuleOf is the rule of a POSIX rule string, UTC's when s is not one.
RuleOf(s: str): Rule {
    return ParseRule(s.AsBytes()) else {
        NoRule()
    }
}

// ZoneOf is the zone of rule r alone, with no table.
ZoneOf(r: Rule): Zone {
    return Zone(trans: NoSeconds, offs: NoOffsets, first: r.std, rule: r)
}

// Le16 and Le32 read little-endian fields of a Windows record.
Le16(b: []u8, i: int): int {
    return int(b[i]) | (int(b[i + 1]) << 8)
}

Le32(b: []u8, i: int): i32 {
    return Trunc[i32]((u32(b[i]) | (u32(b[i + 1]) << 8) | (u32(b[i + 2]) << 16) | (u32(b[i + 3]) << 24)))
}

// WindowsZone is the zone the OS is set to: the record in force
// (GetDynamicTimeZoneInformation) as the rule for the years past the
// table, and that zone's rules for each year from AskFirst to this year
// (GetTimeZoneInformationForYear) as the table, so past years keep their
// own rules. UTC when the Io has no such call (a Sim's, an inert one);
// the record in force alone when a year's call fails. AskFirst to this
// year is about 60 calls; the table holds at most three transitions a
// year from TableFirst on, in a.
WindowsZone(a: &Arena, io: Io): OutOfMemory!Zone from a {
    var buf: [432]u8 = [0; 432]
    r := rt.TimeZone(io, 0, mut buf[..])
    if r.err != core.SysErr.None {
        return Zone.Utc()
    }
    now := TziRule(buf[..], 0)
    last := CivilFromDays(FloorDiv(Wall(io).Unix(), SecondsPerDay)).year
    if last < AskFirst {
        return ZoneOf(now)
    }
    var tmp := try a.Sub()
    defer tmp.Free()
    years := Cast[int](last - AskFirst) + 1
    recs := try tmp.Slice[u8](years * TziBytes)
    for k in 0..years {
        q := rt.TimeZone(io, AskFirst + Cast[i64](k), mut recs[k * TziBytes..(k + 1) * TziBytes])
        if q.err != core.SysErr.None {
            return ZoneOf(now)
        }
    }
    return try TziTable(a, AskFirst, recs[..], now)
}

// TziBytes is the size of TIME_ZONE_INFORMATION. TableFirst is the first
// year of Zone.Local's table on Windows and AskFirst the first year it
// asks the OS for: GetTimeZoneInformationForYear answers a year before a
// zone's first rules with those rules, so the years before AskFirst reuse
// AskFirst's record with no call.
const TziBytes: int = 172
const TableFirst: i64 = 1900
const AskFirst: i64 = 1970

// TziTable is the zone of per-year records: recs holds TziBytes for each
// year from first on, and the years from TableFirst to first - 1 reuse
// the first record. Each year's rule is walked over its UTC year (Rule.At)
// and every change of offset is a transition; past the last year, rule
// now. O(years); allocates three transitions a year at most in a.
TziTable(a: &Arena, first: i64, recs: []u8, now: Rule): OutOfMemory!Zone from a {
    count := recs.Len() / TziBytes
    if count == 0 {
        return ZoneOf(now)
    }
    lo := if first < TableFirst { first } else { TableFirst }
    span := Cast[int](first - lo) + count
    trans := try a.Slice[i64](3 * span)
    offs := try a.Slice[i32](3 * span)
    origin := DaysFromCivil(lo, 1, 1) * SecondsPerDay
    start := TziRule(recs[0..TziBytes], lo).At(origin).off
    var cur := start
    var n := 0
    for k in 0..span {
        y := lo + Cast[i64](k)
        i := if y < first { 0 } else { Cast[int](y - first) }
        r := TziRule(recs[i * TziBytes..(i + 1) * TziBytes], y)
        var u := DaysFromCivil(y, 1, 1) * SecondsPerDay
        next := DaysFromCivil(y + 1, 1, 1) * SecondsPerDay
        for u < next {
            p := r.At(u)
            if p.off != cur {
                trans[n] = u
                offs[n] = p.off
                n += 1
                cur = p.off
            }
            if p.end >= next {
                break
            }
            u = p.end
        }
    }
    return Zone(trans: trans[0..n], offs: offs[0..n], first: start, rule: now)
}

// TziRule is the rule of a TIME_ZONE_INFORMATION record (the first 172
// bytes of the dynamic one), applied in year (0: every year): Bias at 0
// (minutes, UTC = local + bias), StandardDate at 68, StandardBias at 84,
// DaylightDate at 152, DaylightBias at 168. A zone with no daylight time
// (StandardDate's month 0) is its Bias alone: StandardBias is ignored
// then, as the record's documentation and Go's initLocalFromTZI say. A
// rule date is month, weekday and week of the month (5 the last), the
// form of a POSIX M date, or, with its year set, that one day of that
// year; its time is the local time before the change, as POSIX's. A date
// that is neither, or an absolute date of another year, keeps the
// standard offset.
TziRule(b: []u8, year: i64): Rule {
    bias := Cast[i64](Le32(b, 0))
    var r := NoRule()
    r.kind = RuleFixed
    if Le16(b, 70) == 0 {
        r.std = Cast[i32](-bias * 60)
        return r
    }
    r.std = Cast[i32](-(bias + Cast[i64](Le32(b, 84))) * 60)
    start := SystemDate(b, 152, year) else {
        return r
    }
    end := SystemDate(b, 68, year) else {
        return r
    }
    r.kind = RuleDst
    r.dst = Cast[i32](-(bias + Cast[i64](Le32(b, 168))) * 60)
    r.start = start
    r.end = end
    return r
}

// SystemDate is the rule date of the SYSTEMTIME at b[i..]: wYear at 0,
// wMonth at 2, wDayOfWeek at 4, wDay (the week, 1 to 5, or the day of the
// month when wYear is set) at 6, then hour, minute and second; none when
// a field is out of range or the date is another year's.
SystemDate(b: []u8, i: int, year: i64): ?RuleDate {
    month := Le16(b, i + 2)
    wd := Le16(b, i + 4)
    day := Le16(b, i + 6)
    if month < 1 || month > 12 || wd > 6 || day < 1 {
        return none
    }
    secs := Cast[i64](Le16(b, i + 8) * 3600 + Le16(b, i + 10) * 60 + Le16(b, i + 12))
    wy := Cast[i64](Le16(b, i))
    if wy != 0 {
        if wy != year || day > DaysIn(year, month) {
            return none
        }
        doy := DaysFromCivil(year, month, day) - DaysFromCivil(year, 1, 1)
        return some(RuleDate(form: DateZero, day: Cast[int](doy), month: month, week: 1, secs: secs))
    }
    return some(RuleDate(form: DateMonth, day: wd, month: month, week: if day > 5 { 5 } else { day }, secs: secs))
}
