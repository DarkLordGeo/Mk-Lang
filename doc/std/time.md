# std.time

`std.time` measures time and works with dates. It has three kinds of value: `Instant`, a reading of the monotonic clock for measuring how long something takes; `Duration`, a span of nanoseconds; and `Time`, an instant on the wall clock for dates and timestamps. Zones (`Zone`) turn a `Time` into the local date and back, from the system's zone database or from the one built into `std.time.tzdata`. The parsers read RFC 3339 timestamps, HTTP dates and durations such as `"1.5s"`.

Read the [library guide](README.md) first.

```mk
// verdict: parse
import std.time
```

## Contents

- [The main pieces](#the-main-pieces)
- [Instants and durations](#instants-and-durations)
- [Wall-clock time](#wall-clock-time)
- [Dates and zones](#dates-and-zones)
- [Finding a zone](#finding-a-zone)
- [std.time.tzdata](#stdtimetzdata)
- [Parsing and formatting](#parsing-and-formatting)
- [Errors](#errors)
- [Under a simulated Io](#under-a-simulated-io)
- [Examples](#examples): [timestamps and durations](#timestamps-and-durations), [local time in a zone](#local-time-in-a-zone), [HTTP dates](#http-dates), [measure elapsed time](#measure-elapsed-time)
- [API](#api)

## The main pieces

| Task | Use |
|---|---|
| Measure how long something takes | `Now`, then `Instant.Since` |
| Make a duration | `Nanos`, `Micros`, `Millis`, `Seconds`, `Minutes`, `Hours`, `ParseDuration` |
| Sleep | `io.Sleep(d)` with a `Duration` |
| The date and time now | `Wall`, which returns a `Time` |
| A `Time` from Unix seconds | `FromUnix`; back with `Time.Unix`, `Time.UnixNanos` |
| Move a `Time`, or subtract two | `Time.Add`, `Time.Sub` |
| The civil date of a `Time` in a zone | `Time.In`, which returns a `Date` |
| A `Time` from a civil date in a zone | `FromDate`, or `Date.Time` without checks |
| Get a zone | `Zone.Utc`, `Zone.Fixed`, `Zone.Load`, `Zone.Local`, `Zone.FromTzif` |
| The system zone's name | `LocalName` |
| Parse and print timestamps | `ParseRfc3339` and `{}`; `ParseHttpDate` and `FormatHttpDate` |
| A deadline passed | the error `TimedOut` |

## Instants and durations

An `Instant` is a reading of the monotonic clock: nanoseconds from an origin the runtime fixes at start. Only differences mean anything. The clock never goes backwards, so measure with `Now` and `Since`, never with `Wall`, which can jump.

A `Duration` is a signed count of nanoseconds in an `i64`, about 292 years either way. It is plain, prints with `{}` as `1.5s` or `2h45m0s`, and is what `io.Sleep` and `Scope.CancelAfter` take. Compare durations by their `Nanos()`. `Seconds`, `Millis` and the other constructors, and `Instant.Since`, panic on overflow.

No `Scope` function takes an `Instant`. A deadline at an instant `t` is `s.CancelAfter(t.Since(time.Now(io)))`.

## Wall-clock time

A `Time` is UTC nanoseconds since the Unix epoch in an `i64`, from 1677-09-21T00:12:43.145224192Z to 2262-04-11T23:47:16.854775807Z. It is plain, `Eq`, `Ord` and `Hash`, and prints with `{}` as RFC 3339 in UTC.

- `Add`, `Sub` and `FromUnix` saturate at the ends of the range. `FromDate` and the parsers fail outside it. None of them wraps or panics.
- Leap seconds are not represented: a minute always has 60 seconds, and a second of 60 does not parse.
- `Wall(io)` reads the OS's clock, which can jump when the system's time is set.

## Dates and zones

A `Date` holds the civil fields of an instant in a zone, in the proleptic Gregorian calendar: year, month, day, hour, minute, second and nanoseconds. It also holds the weekday (0 is Sunday), the day of the year (1 is January 1) and the zone's offset from UTC in seconds, east positive.

- `t.In(&z)` gives the `Date` of `t` in `z`.
- `FromDate(year, month, day, hour, minute, second, nanos, &z)` gives the instant whose fields in `z` are those. It fails with `OutOfRange` for a field out of its range. It fails with `Skipped` for a local time that a transition skipped (when clocks go forward), and `Repeated` for one that a transition repeated (when clocks go back).
- `Date.Time` does the same without checks: each field carries into the next, so month 13 is January of the next year.

A `Zone` is the UTC offset in force at each instant. `Zone.Utc` and `Zone.Fixed` need nothing. `Zone.Load`, `Zone.FromTzif` and, on Linux, `Zone.Local` read TZif data (RFC 8536, versions 1 to 4), whose footer's POSIX rule covers instants past the table. `Zone.Local` may instead read a POSIX rule from `TZ` on Linux, and on Windows it reads the system's time-zone calls. Each keeps the zone's transitions in the arena you pass: the `Zone` borrows it (`from a`). A lookup is a binary search over the transitions.

## Finding a zone

`Zone.Load(a, io, name, embedded)` reads the zone named `name`, such as `"Europe/Paris"`.

- A zone name is one or more segments of ASCII letters, digits, `.`, `_`, `+` and `-`, joined by `/`. No segment is `.` or `..`, and the name is at most 255 bytes; else it is `BadName`. The name is checked before any file is opened, so a name from a user cannot reach outside the zone directory.
- It looks in the directory `TZDIR` names, then in `/usr/share/zoneinfo` on Linux, then in `embedded`, a `Database` such as `tzdata.Database`. It fails with `NoDatabase` when it had nowhere to look, and `NotFound` when no place had the zone. It never falls back to UTC.
- A zone file over 1 MiB, one that cannot be read, or one that is not valid TZif is `BadData`.

Windows has no zone database in this form, so a program that loads zones by name on Windows passes `some(tzdata.Database)`.

`Zone.Local(a, io)` is the zone the system is set to.

- On Linux it comes from `TZ` (a zone name, `:name`, or a POSIX rule string), else `/etc/localtime`, else UTC.
- On Windows it comes from the system's time-zone calls, with no registry read.
- `LocalName(a, io)` is that zone's IANA name, such as `"Europe/Paris"`, or `none` when the system names no zone.
- On Windows `LocalName` maps the system's zone to its IANA name through CLDR's table.

## std.time.tzdata

`std.time.tzdata` has one item, `Database`: the IANA time-zone database built into the program, for `Zone.Load`'s `embedded` argument.

```mk
// verdict: ok
import std.time
import std.time.tzdata

Paris(a: &Arena, io: Io): time.Error|OutOfMemory!time.Zone from a {
    return try time.Zone.Load(a, io, "Europe/Paris", some(tzdata.Database))
}
```

- It holds the zones of one IANA release, compiled to TZif, and gives the same answers on every target and under `std.sim`.
- It is a large table, linked only into a binary that names `tzdata.Database`.
- A program can supply its own `time.Database` instead: its `find` function returns the TZif bytes of a zone name, or `none`.

## Parsing and formatting

| Form | Parse | Print |
|---|---|---|
| RFC 3339, `2006-01-02T15:04:05.5Z` or `...+01:00` | `ParseRfc3339` | `{}` of a `Time` (UTC, as many fraction digits as needed) |
| HTTP date, `Sun, 06 Nov 1994 08:49:37 GMT` | `ParseHttpDate` (also RFC 850's and asctime's forms) | `FormatHttpDate` |
| Duration, `300ms`, `-1.5h`, `2h45m` | `ParseDuration` (units `ns`, `us`, `µs` or `μs`, `ms`, `s`, `m`, `h`) | `{}` of a `Duration` |

The parsers fail with `ParseError` and allocate nothing. `FormatHttpDate` allocates its 29 bytes in the arena you pass; printing with `{}` allocates nothing.

There is no layout formatter. `{}` of a `Time` is always UTC, and a `Date` has no `{}`. To print a local time, take `t.In(&z)` and print its fields, as [local time in a zone](#local-time-in-a-zone) does.

## Errors

`time.Error` holds the failures of zones and dates: `BadName`, `BadData`, `NotFound`, `NoDatabase`, `Skipped`, `Repeated` and `OutOfRange`, with the prelude's `Canceled`. Functions that allocate add `OutOfMemory`. It is open, so a `match` on it outside std keeps a `_` arm.

`time.TimedOut` is the error for a deadline that passed. std's own timeouts report it (an HTTP client's limits, a TLS handshake), never a bare `Canceled`. It is a member of `net.Error`, and through it of `tls.Error` and `websocket.Error`; a program can fail with it too, as the [cookbook](../cookbook.md#time-out-a-read-an-idle-connection-and-a-retry) shows.

## Under a simulated Io

Under a `Sim`'s `Io`, nothing reads the system, so a seed's run is the same on every target:

- `Now` and `Wall` read the simulated clock. `Wall` starts at 2026-01-01T00:00:00Z on every seed and moves with the simulated clock.
- `Zone.Load` answers from `embedded` alone, `Zone.Local` is UTC, and `LocalName` is `none`.

## Examples

Each program below runs as written with `mk run`.

### Timestamps and durations

```mk
// verdict: ok
import std.time

export Main(sys: &System): ParseError|WriteError!void {
    start := try time.ParseRfc3339("2026-03-28T22:15:00+01:00") // an offset is allowed
    d := try time.ParseDuration("2h45m")
    end := start.Add(d)
    try sys.Print("start {start}\nend   {end}\n", (start, end)) // {} prints RFC 3339 in UTC
    try sys.Print("span  {span}\n", (span: end.Sub(start)))
    try sys.Print("unix  {u}\n", (u: end.Unix()))
}
```

```text
$ mk run stamps.mk
start 2026-03-28T21:15:00Z
end   2026-03-29T00:00:00Z
span  2h45m0s
unix  1774742400
```

### Local time in a zone

```mk
// verdict: ok
import std.time
import std.time.tzdata

// Local prints the instant that a wall-clock time in z names, or why there is none.
Local(sys: &System, z: &time.Zone, hour: int, minute: int): WriteError!void {
    t := time.FromDate(2026, 3, 29, hour, minute, 0, 0, z) else e {
        try sys.Print("{hour}:{minute:02}: {e}\n", (hour, minute, e))
        return
    }
    try sys.Print("{hour}:{minute:02}: {t}\n", (hour, minute, t))
}

export Main(sys: &System): time.Error|OutOfMemory|ParseError|WriteError!void {
    paris := try time.Zone.Load(sys.arena, sys.io, "Europe/Paris", some(tzdata.Database))
    t := try time.ParseRfc3339("2026-07-14T08:30:00Z")
    d := t.In(&paris)
    try sys.Print("{y}-{m:02}-{day:02} {h:02}:{min:02}, weekday {w}, offset {o}s\n",
        (y: d.year, m: d.month, day: d.day, h: d.hour, min: d.minute, w: d.weekday, o: d.offset_seconds))

    // On 29 March 2026 clocks in Paris go from 02:00 to 03:00.
    try Local(sys, &paris, 1, 30)
    try Local(sys, &paris, 2, 30)
    try Local(sys, &paris, 3, 30)
}
```

```text
$ mk run paris.mk
2026-07-14 10:30, weekday 2, offset 7200s
1:30: 2026-03-29T00:30:00Z
2:30: Skipped
3:30: 2026-03-29T01:30:00Z
```

- The zone lives in `sys.arena`, so `paris` borrows it.
- On Linux the zone comes from `/usr/share/zoneinfo` when it is there, else from `tzdata.Database`.

### HTTP dates

```mk
// verdict: ok
import std.time

export Main(sys: &System): ParseError|OutOfMemory|WriteError!void {
    forms := ["Sun, 06 Nov 1994 08:49:37 GMT", "Sunday, 06-Nov-94 08:49:37 GMT", "Sun Nov  6 08:49:37 1994"]
    for s in forms {
        t := try time.ParseHttpDate(s) // all three name the same instant
        try sys.Print("{t}\n", (t,))
    }
    t := time.FromUnix(1_700_000_000, 0)
    h := try time.FormatHttpDate(sys.arena, t) // 29 bytes in sys.arena
    try sys.Print("{h}\n", (h,))
}
```

```text
$ mk run http.mk
1994-11-06T08:49:37Z
1994-11-06T08:49:37Z
1994-11-06T08:49:37Z
Tue, 14 Nov 2023 22:13:20 GMT
```

- A two-digit year of RFC 850's form from 69 to 99 is in the 1900s, and from 00 to 68 in the 2000s. The parser reads no clock.

### Measure elapsed time

```mk
// verdict: ok
import std.time

export Main(sys: &System): Canceled|WriteError!void {
    start := time.Now(sys.io) // the monotonic clock
    try sys.io.Sleep(time.Millis(20))
    took := time.Now(sys.io).Since(start)
    try sys.Print("slept at least 20ms: {ok}\n", (ok: took.Nanos() >= time.Millis(20).Nanos()))

    wall := time.Wall(sys.io) // the date and time, which can jump
    try sys.Print("after 2020: {ok}\n", (ok: wall.Unix() > 1_577_836_800))
}
```

```text
$ mk run elapsed.mk
slept at least 20ms: true
after 2020: true
```

- `Duration` has no `<` or `>=`; compare the `Nanos()` of two durations.

## API

Generated from the module's doc comments by `mk doc --std std.time` and `mk doc --std std.time.tzdata` ([reading the API](README.md#reading-an-api-section)).

<!-- api:begin -->
Stability: Stable.

### `BadData`

```
export error BadData
```

Zone data that is not TZif (RFC 8536 versions 1 to 4) with a valid POSIX
footer, a zone file over 1 MiB, or one that could not be read.

### `BadName`

```
export error BadName
```

A zone name that is not one or more segments of ASCII letters, digits,
'.', '_', '+' and '-' joined by '/', with no segment "." or "..", at most
255 bytes; checked before any file is opened.

### `Database`

```
export struct Database
```

Zones built into the program: std.time.tzdata's Database, or one of the
program's own.

Fields:

- `find: fn(str): ?[]u8`: The TZif bytes of the zone named name, none when it has no such
  zone. Called only with a name Zone.Load has checked.

### `Date`

```
export struct Date
```

The civil fields of an instant in a zone: the date in the proleptic
Gregorian calendar, the time of day, the weekday (0 is Sunday), the day
of the year (1 is January 1) and the zone's offset from UTC in seconds,
east positive. Leap seconds are not represented: second is 0 to 59.

Fields:

- `year: i32`: The year of the proleptic Gregorian calendar.
- `month: int`: The month, 1 to 12.
- `day: int`: The day of the month, from 1.
- `hour: int`: The hour, 0 to 23.
- `minute: int`: The minute, 0 to 59.
- `second: int`: The second, 0 to 59.
- `nanos: int`: The nanoseconds within the second, 0 to 999999999.
- `weekday: int`: The weekday, 0 (Sunday) to 6.
- `year_day: int`: The day of the year, from 1 (January 1).
- `offset_seconds: i32`: The zone's offset from UTC in seconds, east positive.

### `Date.Time`

```
export Date.Time(self): Time
```

The instant self shows: its fields less its offset. The fields are not
checked: each carries into the next (month 13 is January of the next
year, minute -1 the last minute of the hour before), and the result
saturates at the ends of Time's range, as FromUnix does. It never
panics. It is exact whenever no field exceeds 2^62 in magnitude; past
that, a carry that would leave i64 saturates first. The weekday and the
day of the year are ignored. Use FromDate to have the fields checked.
O(1); allocates nothing.

### `Duration`

```
export struct Duration
```

A signed span of time, a count of nanoseconds in an i64: about 292
years either way. Plain. It is std.core's Duration, so a time.Duration
is what Io.Sleep takes, and it prints with {} as "1.5s".

### `Duration.Nanos`

```
export Duration.Nanos(self): i64
```

The duration in nanoseconds. O(1).

### `Error`

```
export open type Error = BadName | BadData | NotFound | NoDatabase | Skipped | Repeated | OutOfRange | Canceled
```

Every way std.time's zone and calendar functions fail.

### `FormatHttpDate`

```
export FormatHttpDate(a: &Arena, t: Time): OutOfMemory!str from a
```

- Flows: The result borrows from a.
- Errors: OutOfMemory

t as an IMF-fixdate in GMT, "Sun, 06 Nov 1994 08:49:37 GMT", the form
RFC 9110 section 5.6.7 says a sender generates. OutOfMemory when a
cannot hold its 29 bytes. O(1); allocates 29 bytes in a.

### `FromDate`

```
export FromDate(year: i32, month: int, day: int, hour: int, minute: int, second: int, nanos: int, z: &Zone): Error!Time
```

- Errors: Error (Canceled, BadData, BadName, NoDatabase, NotFound, OutOfRange, Repeated, Skipped)

The instant whose civil fields in z are the ones given. Fails with
OutOfRange when a field is outside its range (month 1 to 12, day 1 to
the month's length, hour 0 to 23, minute and second 0 to 59, nanos 0 to
999,999,999) or the instant is outside Time's range; Skipped when a
transition of z skipped that local time; Repeated when one repeated it.
It never wraps or panics. O(log n + k) for a zone of n transitions, k
of them within two days of the local time; allocates nothing.

### `FromUnix`

```
export FromUnix(seconds: i64, nanos: i64): Time
```

The instant seconds and nanos after the epoch; nanos may be any i64 and
is carried into seconds. Saturates at the ends of Time's range. O(1).

### `Hours`

```
export Hours(n: i64): Duration
```

The Duration of n hours. Panics (code 1) when n * 3.6e12 leaves i64.
O(1).

### `Instant`

```
export struct Instant
```

A point on the monotonic clock, in nanoseconds from an origin the
runtime fixes at start, for measuring how long something takes. Only
differences mean anything: an Instant is never a wall-clock time, and
the clock never goes backwards. Use Wall for the date and time.

### `Instant.Since`

```
export Instant.Since(self, earlier: Instant): Duration
```

The time from earlier to self, negative when earlier is later. Panics
(code 1) when the difference leaves i64, which two readings of one
monotonic clock never do. O(1).

### `LocalName`

```
export LocalName(a: &Arena, io: Io): ?str from a
```

- Flows: The result borrows from a.

The IANA name of the zone the system is set to, as "Europe/Paris", for
display or to pass to another program. On Windows the system's zone is
mapped to its IANA name through CLDR's table of Windows zones. none when
the system names no zone CLDR or the zone database knows (TZ set to "",
a path, a POSIX rule or a name with no readable TZif file, a copied
/etc/localtime), or io is simulated.

On Windows O(log n) over CLDR's 142 Windows zones; on Linux, when TZ
names a zone, O(n) in its file, read and checked as Zone.Local reads
it, so a cancelled scope or a lack of memory there gives none. It
allocates the name in a, and the zone file in a sub-arena of a, freed
before it returns.

### `Micros`

```
export Micros(n: i64): Duration
```

The Duration of n microseconds. Panics (code 1) when n * 1e3 leaves
i64. O(1).

### `Millis`

```
export Millis(n: i64): Duration
```

The Duration of n milliseconds. Panics (code 1) when n * 1e6 leaves i64.
O(1).

### `Minutes`

```
export Minutes(n: i64): Duration
```

The Duration of n minutes. Panics (code 1) when n * 6e10 leaves i64,
beyond about 292 years either way, as Seconds does. O(1).

### `Nanos`

```
export Nanos(n: i64): Duration
```

The Duration of n nanoseconds; every i64 is one. O(1).

### `NoDatabase`

```
export error NoDatabase
```

Zone.Load had nowhere to look: TZDIR unset or not a directory, no
/usr/share/zoneinfo directory (or a target other than Linux), and no
embedded Database.

### `NotFound`

```
export error NotFound
```

No zone of that name in any place Zone.Load looked.

### `Now`

```
export Now(io: Io): Instant
```

The monotonic clock now. It never fails: the system's clock always
answers, and an Io with no clock reads as the origin, 0. Under a Sim it
is the simulated clock. O(1): one clock read; allocates nothing.

### `OutOfRange`

```
export error OutOfRange
```

A field outside its range (month 1 to 12, day 1 to the month's length,
hour 0 to 23, minute and second 0 to 59, nanos 0 to 999,999,999), or an
instant outside Time's range.

### `ParseDuration`

```
export ParseDuration(s: str): ParseError!Duration
```

- Errors: ParseError

The Duration s writes: an optional sign, then one or more of a decimal
number with an optional fraction and a unit, "ns", "us" (or "µs", "μs"),
"ms", "s", "m" or "h", as "300ms", "-1.5h" or "2h45m"; "0" alone is
zero. ParseError when s is not one, has no unit, or leaves Duration's
range. A fraction is scaled in f64, which is exact to the nanosecond
for every unit up to an hour. O(n); allocates nothing.

### `ParseHttpDate`

```
export ParseHttpDate(s: str): ParseError!Time
```

- Errors: ParseError

The instant an HTTP date names, in any of the three forms RFC 9110
section 5.6.7 accepts: IMF-fixdate ("Sun, 06 Nov 1994 08:49:37 GMT"),
RFC 850's form (the full day name, then the day, month name and
two-digit year joined by hyphens, then the time and "GMT") or asctime's
("Sun Nov  6 08:49:37 1994", the day padded with a space or a zero).

RFC 850's year 69 to 99 is in the 1900s and 00 to 68 in the 2000s. RFC
9110 asks instead for the most recent past year with those digits when
the date would be more than 50 years ahead, which needs the current
date. This function reads no clock, so a caller that needs the RFC's
reading moves a result more than 50 years ahead of its clock back 100
years. ParseError when s is none of these, a field is out of range, or
the instant is outside Time's range. O(n); allocates nothing.

### `ParseRfc3339`

```
export ParseRfc3339(s: str): ParseError!Time
```

- Errors: ParseError

The instant an RFC 3339 date-time names: "2006-01-02T15:04:05Z", with an
optional fraction of any length (digits past the ninth are dropped) and
"Z" or a "+hh:mm" or "-hh:mm" offset; "T" and "Z" may be lower case, as
RFC 3339 section 5.6 allows. ParseError when s is not one, a field is
out of range (second 60 included: leap seconds are not represented), or
the instant is outside Time's range. O(n); allocates nothing.

### `Repeated`

```
export error Repeated
```

A local time that a transition repeated: two instants of the zone show it.

### `Seconds`

```
export Seconds(n: i64): Duration
```

The Duration of n seconds. Panics (code 1) when n * 1e9 leaves i64,
beyond about 292 years either way. O(1).

### `Skipped`

```
export error Skipped
```

A local time that a transition skipped: no instant of the zone shows it.

### `Time`

```
export struct Time
```

An instant on the wall clock, UTC nanoseconds since 1970-01-01T00:00:00Z
in an i64: from 1677-09-21T00:12:43.145224192Z to
2262-04-11T23:47:16.854775807Z. Plain, Eq, Ord and Hash; formats with
{} as RFC 3339. Leap seconds are not represented. Add, Sub and FromUnix
saturate at the ends of the range; FromDate and the parsers fail
outside it. None of them wraps or panics.

### `Time.Add`

```
export Time.Add(self, d: Duration): Time
```

The instant d after self (before it for a negative d), saturated at the
ends of Time's range. O(1).

### `Time.In`

```
export Time.In(self, z: &Zone): Date
```

The civil fields of self in z. O(log n) for a zone of n transitions
(one binary search; an instant past the zone's table adds O(1) for its
rule); allocates nothing.

### `Time.Sub`

```
export Time.Sub(self, t: Time): Duration
```

The time from t to self, negative when t is later, saturated at the
ends of Duration's range. O(1).

### `Time.Unix`

```
export Time.Unix(self): i64
```

Whole seconds since the epoch, rounded toward minus infinity, so the
nanoseconds left over are never negative. O(1).

### `Time.UnixNanos`

```
export Time.UnixNanos(self): i64
```

Nanoseconds since the epoch. O(1).

### `TimedOut`

```
export error TimedOut
```

A deadline passed: std's own timeouts (an HTTP client's connect,
response-header and idle limits, a server's request-header and
keep-alive limits, a TLS handshake, a Close that speaks to the peer)
report it, never a bare Canceled. A member of net.Error, and
through it of tls.Error and websocket.Error.

### `Wall`

```
export Wall(io: Io): Time
```

The wall clock now: the OS's (CLOCK_REALTIME on Linux,
GetSystemTimePreciseAsFileTime on Windows), or a Sim's simulated one,
which starts at 2026-01-01T00:00:00Z on every seed and moves with the
simulated clock. An Io with no clock reads as the epoch. The wall clock
can jump when the system's time is set: measure with Now. O(1): one
clock read; allocates nothing.

### `Zone`

```
export view struct Zone
```

A time zone: the UTC offset in force at each instant. Utc and Fixed need
nothing; a zone read from TZif holds its transitions in the arena it was
read into.

### `Zone.Fixed`

```
export Zone.Fixed(offset_seconds: i32): Zone
```

A zone whose offset from UTC is always offset_seconds, east positive.
O(1); allocates nothing.

### `Zone.FromTzif`

```
export Zone.FromTzif(a: &Arena, name: str, data: []u8): Error|OutOfMemory!Zone from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (Canceled, OutOfMemory, BadData, BadName, NoDatabase, NotFound, OutOfRange, Repeated, Skipped)

The zone that TZif bytes describe (RFC 8536 versions 1 to 4, the POSIX
footer of version 2+ for instants past the table). name is the
program's name for the zone; it is not checked or kept. Fails with
BadData when data is not valid TZif, its footer is not a valid POSIX TZ
string, an offset is outside -24:59:59 to 25:59:59, or data is over
1 MiB; OutOfMemory when a cannot hold 12 bytes per transition. O(n) in
the bytes; allocates the transition table in a.

### `Zone.Load`

```
export Zone.Load(a: &Arena, io: Io, name: str, embedded: ?Database): Error|OutOfMemory!Zone from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (Canceled, OutOfMemory, BadData, BadName, NoDatabase, NotFound, OutOfRange, Repeated, Skipped)

The zone named name, read as TZif: from TZDIR when it is set, then from
/usr/share/zoneinfo on Linux, then from embedded; under a simulated Io
from embedded alone, on every target. Fails with BadName
when name is not a zone name (checked before any file is opened);
NoDatabase when there was nowhere to look (TZDIR unset or not a
directory, no /usr/share/zoneinfo directory or not Linux, and no
embedded Database); NotFound when no place has the zone; BadData when
the first place that has it holds bad data, a file over 1 MiB or one it
cannot read; Canceled when the scope is cancelled; OutOfMemory when a
cannot hold the zone. Never falls back to UTC. O(n) in the zone's bytes;
allocates its transitions in a, and the file's bytes while it is read,
freed before it returns.

### `Zone.Local`

```
export Zone.Local(a: &Arena, io: Io): Error|OutOfMemory!Zone from a
```

- Flows: The result borrows from a.
- Errors: Error|OutOfMemory (Canceled, OutOfMemory, BadData, BadName, NoDatabase, NotFound, OutOfRange, Repeated, Skipped)

The zone the system is set to.

On Linux it comes from TZ (a zone name, ":name", or a POSIX rule
string such as "CET-1CEST,M3.5.0,M10.5.0/3"), else /etc/localtime, else
UTC. A zone file that is missing or cannot be read is UTC; for a zone
name in TZ, TZ is then read as a POSIX rule, and UTC when it is not one.
On Windows it comes from the system's time-zone calls, with no registry
read. Under a simulated Io it is UTC, read from nothing, on every
target.

Fails with BadData when the system's zone file is not valid TZif,
Canceled when the scope is cancelled, and OutOfMemory when a cannot
hold the zone. O(n) in the zone's bytes on Linux; on Windows one call
for the zone in force and one for each year since 1970, and O(years)
work. It allocates its transitions in a.

### `Zone.Utc`

```
export Zone.Utc(): Zone
```

Coordinated Universal Time, offset 0 always. O(1); allocates nothing.

### Module `std.time.tzdata`

Stability: Stable.

#### `Database`

```
export const Database: time.Database
```

The IANA time-zone database built into the program, for Zone.Load's
embedded argument: Zone.Load(a, io, "Europe/Paris", some(tzdata.Database)).
It is compiled TZif of one IANA release, the same on every target and
under std.sim, so a program that uses it finds every zone on a system
with no zone database (Windows, a small container). It is linked only
into a binary that names Database.

<!-- api:end -->
