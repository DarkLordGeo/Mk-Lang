// Why: the calendar, parsers, zones and durations need edge cases pinned in
// Mk itself, beside the Go oracle tests against Go's time.
// What: tests of the RFC examples and edges, with helpers RefusesRfc3339,
// RefusesHttpDate, RefusesDuration, CodeOf and FromDateCode.
// How: run by `mk test --std=std.time`.

// RefusesRfc3339, RefusesHttpDate and RefusesDuration are whether the parser
// refuses s.
RefusesRfc3339(s: str): bool {
    _ = ParseRfc3339(s) else {
        return true
    }
    return false
}

RefusesHttpDate(s: str): bool {
    _ = ParseHttpDate(s) else {
        return true
    }
    return false
}

RefusesDuration(s: str): bool {
    _ = ParseDuration(s) else {
        return true
    }
    return false
}

// CodeOf numbers the members of Error and OutOfMemory a test expects.
CodeOf(e: Error | OutOfMemory): int {
    match e {
        Skipped {
            return 1
        }
        Repeated {
            return 2
        }
        OutOfRange {
            return 3
        }
        BadName {
            return 4
        }
        BadData {
            return 5
        }
        _ {
            return 9
        }
    }
}

// FromDateCode is CodeOf of FromDate's error at the local time given, 0
// when it succeeds.
FromDateCode(y: i32, mo: int, d: int, h: int, mi: int, z: &Zone): int {
    _ = FromDate(y, mo, d, h, mi, 0, 0, z) else e {
        return CodeOf(e)
    }
    return 0
}

test "the calendar round-trips and knows its anchors" {
    Assert(DaysFromCivil(1970, 1, 1) == 0)
    Assert(DaysFromCivil(2000, 3, 1) == 11_017)
    Assert(DaysFromCivil(1969, 12, 31) == -1)
    Assert(Weekday(0) == 4)
    var d: i64 = -800_000
    for d < 800_000 {
        c := CivilFromDays(d)
        Assert(DaysFromCivil(c.year, c.month, c.day) == d)
        d += 37
    }
}

test "RFC 3339's examples parse and Time formats them back" (sys: &System) {
    t := ParseRfc3339("1985-04-12T23:20:50.52Z") else {
        Assert(false)
        return
    }
    Assert(t.UnixNanos() == 482_196_050_520_000_000)
    s := sys.arena.Fmt("{t}", (t)) else {
        Assert(false)
        return
    }
    Assert(s == "1985-04-12T23:20:50.52Z")
    u := ParseRfc3339("1996-12-19T16:39:57-08:00") else {
        Assert(false)
        return
    }
    Assert(u.Unix() == 851_042_397)
    // Second 60, a missing offset and a comma fraction are not RFC 3339.
    Assert(RefusesRfc3339("1990-12-31T23:59:60Z"))
    Assert(RefusesRfc3339("1990-12-31T23:59:59"))
    Assert(RefusesRfc3339("1990-12-31T23:59:59,5Z"))
    // Past Time's range.
    Assert(RefusesRfc3339("2262-04-11T23:47:16.854775808Z"))
}

test "HTTP dates in their three forms" (sys: &System) {
    want: i64 = 784_111_777
    for s in ["Sun, 06 Nov 1994 08:49:37 GMT", "Sunday, 06-Nov-94 08:49:37 GMT", "Sun Nov  6 08:49:37 1994"] {
        t := ParseHttpDate(s) else {
            Assert(false)
            return
        }
        Assert(t.Unix() == want)
    }
    f := FormatHttpDate(sys.arena, FromUnix(want, 0)) else {
        Assert(false)
        return
    }
    Assert(f == "Sun, 06 Nov 1994 08:49:37 GMT")
    Assert(RefusesHttpDate("Sun, 06 Nov 1994 08:49:37 UTC"))
}

// RFC 850's two-digit year has a fixed pivot, Go's: 69 to 99 are the 1900s
// and 00 to 68 the 2000s, where RFC 9110 5.6.7 asks for the 50-year rule
// against the current year (ParseHttpDate's doc says why).
test "RFC 850's two-digit year reads 69 to 99 as the 1900s and 00 to 68 as the 2000s" {
    Assert(HttpYear("Sunday, 06-Nov-68 08:49:37 GMT") == 2068)
    Assert(HttpYear("Thursday, 06-Nov-69 08:49:37 GMT") == 1969)
    Assert(HttpYear("Friday, 06-Nov-70 08:49:37 GMT") == 1970)
    Assert(HttpYear("Saturday, 06-Nov-99 08:49:37 GMT") == 1999)
    Assert(HttpYear("Monday, 06-Nov-00 08:49:37 GMT") == 2000)
}

// HttpYear is the UTC year of the HTTP date s, 0 when it is refused.
HttpYear(s: str): i32 {
    t := ParseHttpDate(s) else {
        return 0
    }
    utc := Zone.Utc()
    return t.In(&utc).year
}

test "Add, Sub and FromUnix saturate; Date.Time never panics" {
    max := FromUnix(0, 9_223_372_036_854_775_807)
    min := FromUnix(0, -9_223_372_036_854_775_807 - 1)
    Assert(max.Add(Nanos(1)).Eq(&max))
    Assert(min.Add(Nanos(-1)).Eq(&min))
    Assert(max.Sub(min).Nanos() == 9_223_372_036_854_775_807)
    Assert(FromUnix(9_223_372_037, 0).Eq(&max))
    Assert(FromUnix(-9_223_372_037_000, 0).Eq(&min))
    d := Date(year: 2024, month: 9_223_372_036_854_775_807, day: 1, hour: 0, minute: 0, second: 0, nanos: 0, weekday: 0, year_day: 0, offset_seconds: 0)
    Assert(d.Time().Eq(&max))
    e := Date(year: 2024, month: 14, day: 0, hour: -1, minute: 60, second: 0, nanos: 0, weekday: 0, year_day: 0, offset_seconds: 3600)
    // February 0 of 2025 is January 31; -1 h + 60 min is midnight; less
    // the offset of one hour.
    Assert(e.Time().Unix() == DaysFromCivil(2025, 1, 31) * 86_400 - 3600)
}

test "FromDate names a skipped and a repeated local time" {
    r := ParseRule("EST5EDT,M3.2.0,M11.1.0".AsBytes()) else {
        Assert(false)
        return
    }
    z := ZoneOf(r)
    Assert(FromDateCode(2024, 3, 10, 2, 30, &z) == 1)
    Assert(FromDateCode(2024, 11, 3, 1, 30, &z) == 2)
    Assert(FromDateCode(2023, 2, 29, 0, 0, &z) == 3)
    t := FromDate(2024, 7, 4, 12, 0, 0, 0, &z) else {
        Assert(false)
        return
    }
    Assert(t.Unix() == 1_720_108_800)
    Assert(t.In(&z).offset_seconds == -14_400)
}

test "durations format and parse as Go's do" (sys: &System) {
    d := ParseDuration("1h2m3.5s") else {
        Assert(false)
        return
    }
    Assert(d.Nanos() == 3_723_500_000_000)
    s := sys.arena.Fmt("{d}", (d)) else {
        Assert(false)
        return
    }
    Assert(s == "1h2m3.5s")
    Assert(Minutes(2).Nanos() == 120_000_000_000)
    Assert(Hours(-1).Nanos() == -3_600_000_000_000)
    Assert(Micros(7).Nanos() == 7000)
    Assert(RefusesDuration("1d"))
    Assert(RefusesDuration("9223372036854775808ns"))
}

test "the TZif reader refuses bad data and Load checks names first" (sys: &System) {
    _ = Zone.FromTzif(sys.arena, "x", "TZif".AsBytes()) else e {
        Assert(CodeOf(e) == 5)
    }
    for name in ["../etc/passwd", "", "a//b", "a/./b"] {
        _ = Zone.Load(sys.arena, sys.io, name, none) else e {
            Assert(CodeOf(e) == 4)
            continue
        }
        Assert(false)
    }
}

// Digits as one loop for every k: the reference
// its two- and four-digit forms are held to.
TdgDigitsLoop(s: []u8, i: int, k: int): ?int {
    if s.Len() - i < k {
        return none
    }
    var v := 0
    for j in i..i + k {
        c := s[j]
        if c < b'0' || c > b'9' {
            return none
        }
        v = v * 10 + int(c - b'0')
    }
    return some(v)
}

// TdgSame is whether Digits and TdgDigitsLoop agree on s, i and k.
TdgSame(s: []u8, i: int, k: int): bool {
    a := Digits(s, i, k)
    b := TdgDigitsLoop(s, i, k)
    if x := a {
        if y := b {
            return x == y
        }
        return false
    }
    return b.IsNone()
}

// Every string of up to 5 bytes over the digits 0, 5 and 9, the bytes
// either side of the digits ('/' and ':') and 0xFF, at every start up to
// one past the end, for k 0 to 5; then every two- and four-digit value,
// with a byte after it and with the input ending one byte short.
test "Digits gives the loop's value or none for every short, non-digit and digit input" {
    alpha: [6]u8 = [0x2F, 0x30, 0x35, 0x39, 0x3A, 0xFF]
    var s: [5]u8 = [0; 5]
    var total := 1
    for n in 0..6 {
        for code in 0..total {
            var c := code
            for p in 0..n {
                s[p] = alpha[c % 6]
                c /= 6
            }
            for i in 0..n + 2 {
                for k in 0..6 {
                    Assert(TdgSame(s[0..n], i, k))
                }
            }
        }
        total *= 6
    }
    var b: [7]u8 = [0x41; 7]
    for v in 0..100 {
        Put2(mut b[..], 1, v)
        Assert(Digits(b[..], 1, 2).Must() == v)
        Assert(Digits(b[0..3], 1, 2).Must() == v)
        Assert(Digits(b[0..2], 1, 2).IsNone())
    }
    for v in 0..10000 {
        Put4(mut b[..], 1, v)
        Assert(Digits(b[..], 1, 4).Must() == v)
        Assert(Digits(b[0..5], 1, 4).Must() == v)
        Assert(Digits(b[0..4], 1, 4).IsNone())
    }
}

// TdgUnix is the Unix seconds of a UTC date and time of day.
TdgUnix(y: int, mo: int, d: int, h: int, mi: int, s: int): i64 {
    return DaysFromCivil(Cast[i64](y), mo, d) * SecondsPerDay + Cast[i64](h * 3600 + mi * 60 + s)
}

// TdgCopy copies src to the start of dst.
TdgCopy(dst: mut []u8, src: []u8) {
    for i in 0..src.Len() {
        dst[i] = src[i]
    }
}

// TdgDelete is b with its byte at p taken out, in dst; its length.
TdgDelete(dst: mut []u8, b: []u8, p: int): int {
    var m := 0
    for i in 0..b.Len() {
        if i != p {
            dst[m] = b[i]
            m += 1
        }
    }
    return m
}

// TdgParsed is the Unix seconds the parser named by http (ParseHttpDate,
// else ParseRfc3339) reads from b, none when it refuses b.
TdgParsed(http: bool, b: []u8): ?i64 {
    s := str.From(b) else {
        return none
    }
    if http {
        t := ParseHttpDate(s) else {
            return none
        }
        return some(t.Unix())
    }
    t := ParseRfc3339(s) else {
        return none
    }
    return some(t.Unix())
}

// TdgRefusesEdits is whether the parser refuses text with any one digit
// of its first fixed bytes (its fixed-width fields: a fraction's digits
// may be taken out) replaced by '/', ':', 'a' or a space, but for a space
// at pad (asctime's day, which a space pads), with any one of those digits
// taken out, and cut short at any length.
TdgRefusesEdits(http: bool, text: str, fixed: int, pad: int): bool {
    t := text.AsBytes()
    n := t.Len()
    var b: [40]u8 = [0; 40]
    bad: [4]u8 = [0x2F, 0x3A, 0x61, 0x20]
    for p in 0..fixed {
        if t[p] < b'0' || t[p] > b'9' {
            continue
        }
        for c in bad {
            if c == 0x20 && p == pad {
                continue
            }
            TdgCopy(mut b[..], t)
            b[p] = c
            if TdgParsed(http, b[0..n]).IsSome() {
                return false
            }
        }
        m := TdgDelete(mut b[..], t, p)
        if TdgParsed(http, b[0..m]).IsSome() {
            return false
        }
    }
    for m in 0..n {
        if TdgParsed(http, t[0..m]).IsSome() {
            return false
        }
    }
    return true
}

// TdgRfcText is RFC 3339's example with an offset; TdgRfcAt its fields'
// positions: year (4 digits), month, day, hour, minute, second, offset
// hour, offset minute.
const TdgRfcText = "1996-12-19T16:39:57-08:00"
const TdgRfcAt: [8]int = [0, 5, 8, 11, 14, 17, 20, 23]

// TdgRfcMatches writes the fields f into TdgRfcText and is whether
// ParseRfc3339 gives the instant they name, or refuses them when one is
// out of range. The year's range is that of December 19, the month and
// day the other cases keep: 1677 to 2261 in Time's range.
TdgRfcMatches(f: [8]int): bool {
    var b: [25]u8 = [0; 25]
    TdgCopy(mut b[..], TdgRfcText.AsBytes())
    Put4(mut b[..], 0, f[0])
    for j in 1..8 {
        Put2(mut b[..], TdgRfcAt[j], f[j])
    }
    got := TdgParsed(false, b[..])
    ok := f[0] >= 1677 && f[0] <= 2261 && f[1] >= 1 && f[1] <= 12 && f[2] >= 1 && f[2] <= DaysIn(Cast[i64](f[0]), f[1]) && f[3] <= 23 && f[4] <= 59 && f[5] <= 59 && f[6] <= 23 && f[7] <= 59
    if !ok {
        return got.IsNone()
    }
    want := TdgUnix(f[0], f[1], f[2], f[3], f[4], f[5]) + Cast[i64](f[6] * 3600 + f[7] * 60)
    if v := got {
        return v == want
    }
    return false
}

test "ParseRfc3339 refuses a non-digit or short field and reads every value of each field" {
    Assert(TdgParsed(false, TdgRfcText.AsBytes()).Must() == 851_042_397)
    Assert(TdgRefusesEdits(false, TdgRfcText, 25, -1))
    Assert(TdgRefusesEdits(false, "1985-04-12T23:20:50.52Z", 19, -1))
    Assert(TdgRefusesEdits(false, "2006-01-02T15:04:05+07:30", 25, -1))
    f: [8]int = [1996, 12, 19, 16, 39, 57, 8, 0]
    for v in 0..10000 {
        var g := f
        g[0] = v
        Assert(TdgRfcMatches(g))
    }
    for j in 1..8 {
        for v in 0..100 {
            var g := f
            g[j] = v
            Assert(TdgRfcMatches(g))
        }
    }
    // February's last day, of leap years and of common ones.
    for y in [1996, 1997, 2000, 2100] {
        for v in 0..100 {
            var g := f
            g[0] = y
            g[1] = 2
            g[2] = v
            Assert(TdgRfcMatches(g))
        }
    }
}

// The HTTP date forms: IMF-fixdate, RFC 850's and asctime's with a
// two-digit day and with a one-digit day after a space; TdgHttpAt is each
// form's day, year, hour, minute and second positions.
const TdgHttpText: [4]str = ["Sun, 06 Nov 1994 08:49:37 GMT", "Sunday, 06-Nov-94 08:49:37 GMT", "Sun Nov 06 08:49:37 1994", "Sun Nov  6 08:49:37 1994"]
const TdgHttpAt: [4][5]int = [[5, 12, 17, 20, 23], [8, 15, 18, 21, 24], [8, 20, 11, 14, 17], [9, 20, 11, 14, 17]]

// TdgHttpMatches writes the fields f (day, year, hour, minute, second)
// into form's text and is whether ParseHttpDate gives the instant they
// name in November, or refuses them when one is out of range. RFC 850's
// year is two digits, 69 to 99 the 1900s; asctime's one-digit day is 0 to
// 9. The year's range is November's: 1677 to 2261 in Time's range.
TdgHttpMatches(form: int, f: [5]int): bool {
    t := TdgHttpText[form].AsBytes()
    var b: [40]u8 = [0; 40]
    TdgCopy(mut b[..], t)
    at := TdgHttpAt[form]
    if form == 3 {
        b[at[0]] = Trunc[u8](48 + f[0])
    } else {
        Put2(mut b[..], at[0], f[0])
    }
    var y := f[1]
    if form == 1 {
        Put2(mut b[..], at[1], f[1])
        y = if f[1] >= 69 { 1900 + f[1] } else { 2000 + f[1] }
    } else {
        Put4(mut b[..], at[1], f[1])
    }
    for j in 2..5 {
        Put2(mut b[..], at[j], f[j])
    }
    got := TdgParsed(true, b[0..t.Len()])
    ok := y >= 1677 && y <= 2261 && f[0] >= 1 && f[0] <= 30 && f[2] <= 23 && f[3] <= 59 && f[4] <= 59
    if !ok {
        return got.IsNone()
    }
    want := TdgUnix(y, 11, f[0], f[2], f[3], f[4])
    if v := got {
        return v == want
    }
    return false
}

test "ParseHttpDate refuses a non-digit or short field and reads every value of each field" {
    for form in 0..4 {
        Assert(TdgParsed(true, TdgHttpText[form].AsBytes()).Must() == 784_111_777)
        text := TdgHttpText[form]
        Assert(TdgRefusesEdits(true, text, text.Len(), if form == 2 { 8 } else { -1 }))
        f: [5]int = [6, if form == 1 { 94 } else { 1994 }, 8, 49, 37]
        days := if form == 3 { 10 } else { 100 }
        for v in 0..days {
            var g := f
            g[0] = v
            Assert(TdgHttpMatches(form, g))
        }
        years := if form == 1 { 100 } else { 10000 }
        for v in 0..years {
            var g := f
            g[1] = v
            Assert(TdgHttpMatches(form, g))
        }
        for j in 2..5 {
            for v in 0..100 {
                var g := f
                g[j] = v
                Assert(TdgHttpMatches(form, g))
            }
        }
    }
}
