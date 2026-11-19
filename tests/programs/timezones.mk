// std.time: Time from Unix seconds, RFC 3339 and
// HTTP dates both ways, saturating Add and Sub, civil dates in UTC, a fixed offset and named
// zones from the embedded database and from TZif bytes, the daylight-saving gaps FromDate
// refuses, a zone name refused before any file is opened, durations parsed and printed, and the
// wall and local clocks read without printing them. Every zone comes from std.time.tzdata, so
// the output is the same on every target.
import std.time
import std.time.tzdata

// Show prints t in z as civil fields, with the weekday, the day of the year and the offset.
Show(w: mut Writer, label: str, t: time.Time, z: &time.Zone): WriteError!void {
    d := t.In(z)
    try Print(w, "{label}: {d.year}-{d.month}-{d.day} {d.hour}:{d.minute}:{d.second} offset {d.offset_seconds} weekday {d.weekday} day {d.year_day}\n", (label: label, d: d))
}

// Civil asks FromDate for a local time in z and prints the instant or the error.
Civil(w: mut Writer, label: str, hour: int, minute: int, month: int, day: int, z: &time.Zone): WriteError!void {
    t := time.FromDate(2026, month, day, hour, minute, 0, 0, z) else e {
        try Print(w, "{label}: {e}\n", (label: label, e: e))
        return
    }
    try Print(w, "{label}: {t}\n", (label: label, t: t))
}

// Named loads name from the embedded database and prints the error, if any.
Named(w: mut Writer, a: &Arena, io: Io, name: str): WriteError!void {
    _ = time.Zone.Load(a, io, name, some(tzdata.Database)) else e {
        try Print(w, "load {name}: {e}\n", (name: name, e: e))
        return
    }
    try Print(w, "load {name}: ok\n", (name: name))
}

export Main(sys: &System): WriteError!void {
    a := sys.arena
    io := sys.io
    var out := sys.Stdout()
    t := time.FromUnix(1_700_000_000, 123_456_789)
    try Print(mut out, "unix: {t}, seconds {s}, nanos {n}\n", (t: t, s: t.Unix(), n: t.UnixNanos()))
    later := t.Add(time.Hours(36))
    try Print(mut out, "add 36h: {later}, back {b}\n", (later: later, b: later.Sub(t)))
    top := time.FromUnix(9_223_372_036, 854_775_807)
    try Print(mut out, "saturating add: {s}\n", (s: top.Add(time.Hours(1))))

    utc := time.Zone.Utc()
    try Show(mut out, "utc", t, &utc)
    india := time.Zone.Fixed(19_800)
    try Show(mut out, "fixed +05:30", t, &india)
    ny := time.Zone.Load(a, io, "America/New_York", some(tzdata.Database)) else e {
        try Print(mut out, "load America/New_York: {e}\n", (e: e))
        return
    }
    summer := time.FromUnix(1_720_000_000, 0)
    try Show(mut out, "new york, winter", t, &ny)
    try Show(mut out, "new york, summer", summer, &ny)
    try Civil(mut out, "new york 2026-03-08 02:30", 2, 30, 3, 8, &ny)
    try Civil(mut out, "new york 2026-11-01 01:30", 1, 30, 11, 1, &ny)
    try Civil(mut out, "new york 2026-11-01 03:30", 3, 30, 11, 1, &ny)

    bytes := tzdata.Database.find("Europe/Berlin") else {
        try Print(mut out, "Europe/Berlin is not in the embedded database\n")
        return
    }
    berlin := time.Zone.FromTzif(a, "Europe/Berlin", bytes) else e {
        try Print(mut out, "from tzif: {e}\n", (e: e))
        return
    }
    try Show(mut out, "berlin from tzif", summer, &berlin)
    try Named(mut out, a, io, "../etc/passwd")
    try Named(mut out, a, io, "Mars/Olympus_Mons")
    try Named(mut out, a, io, "Asia/Kolkata")
    _ = time.Zone.FromTzif(a, "Broken/Zone", "TZif2 but not really"[..].AsBytes()) else e {
        try Print(mut out, "from tzif, broken bytes: {e}\n", (e: e))
    }

    for s in ["2026-11-13T06:50:00Z", "2026-11-13T07:50:00.5+01:00", "2026-13-01T00:00:00Z"] {
        p := time.ParseRfc3339(s) else e {
            try Print(mut out, "rfc 3339 {s}: {e}\n", (s: s, e: e))
            continue
        }
        try Print(mut out, "rfc 3339 {s}: {p}\n", (s: s, p: p))
    }
    http := time.FormatHttpDate(a, t) else {
        try Print(mut out, "format http date: out of memory\n")
        return
    }
    back := time.ParseHttpDate(http) else e {
        try Print(mut out, "http date {http}: {e}\n", (http: http, e: e))
        return
    }
    try Print(mut out, "http date: {http}, parsed back {back}\n", (http: http, back: back))
    for s in ["1h30m", "250ms", "-2m3.5s", "1x"] {
        d := time.ParseDuration(s) else e {
            try Print(mut out, "duration {s}: {e}\n", (s: s, e: e))
            continue
        }
        try Print(mut out, "duration {s}: {d}, {n} ns\n", (s: s, d: d, n: d.Nanos()))
    }

    epoch := time.FromUnix(1_767_225_600, 0)
    try Print(mut out, "wall clock after 2026-01-01: {b}\n", (b: time.Wall(io).Sub(epoch).Nanos() > 0))
    _ = time.Zone.Local(a, io) else e {
        try Print(mut out, "local zone: {e}\n", (e: e))
        return
    }
    try Print(mut out, "local zone: read\n")
}
