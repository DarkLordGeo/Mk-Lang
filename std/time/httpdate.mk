// Why: HTTP carries dates in headers (Date, Expires, Last-Modified), in three
// historical forms a recipient must accept.
//
// What: FormatHttpDate and ParseHttpDate (RFC 9110 section 5.6.7).
//
// How: FormatHttpDate writes IMF-fixdate; ParseHttpDate reads IMF-fixdate and
// the two obsolete forms, RFC 850's and asctime's. HTTP-date is
// case-sensitive, and the weekday is checked for form but not against the
// date, as Go's net/http.ParseTime does. RFC 850's two-digit year takes Go's
// fixed pivot, not RFC 9110's 50-year rule, which needs a clock. One pass,
// no recursion, no allocation.

// ShortDays and ShortMonths are RFC 9110's day-name and month.
const ShortDays: [7]str = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]
const LongDays: [7]str = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"]
const ShortMonths: [12]str = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]

/// t as an IMF-fixdate in GMT, "Sun, 06 Nov 1994 08:49:37 GMT", the form
/// RFC 9110 section 5.6.7 says a sender generates. OutOfMemory when a
/// cannot hold its 29 bytes. O(1); allocates 29 bytes in a.
export FormatHttpDate(a: &Arena, t: Time): OutOfMemory!str from a {
    var buf: [29]u8 = [0; 29]
    b := mut buf[..]
    secs := FloorDiv(t.nanos, NanosPerSecond)
    d := DateOf(secs, 0, 0)
    PutName(b, 0, ShortDays[d.weekday])
    b[3] = b','
    b[4] = b' '
    Put2(b, 5, d.day)
    b[7] = b' '
    PutName(b, 8, ShortMonths[d.month - 1])
    b[11] = b' '
    Put4(b, 12, int(d.year))
    b[16] = b' '
    PutClock(b, 17, &d)
    PutName(b, 25, " GMT")
    // Every byte written is ASCII, so the bytes are UTF-8.
    return try a.Dup(str.From(buf[..]).Must())
}

// PutName copies the bytes of s to b[i..].
PutName(b: mut []u8, i: int, s: str) {
    for k, c in s.AsBytes() {
        b[i + k] = c
    }
}

// PutClock writes "hh:mm:ss" at b[i..].
PutClock(b: mut []u8, i: int, d: &Date) {
    Put2(b, i, d.hour)
    b[i + 2] = b':'
    Put2(b, i + 3, d.minute)
    b[i + 5] = b':'
    Put2(b, i + 6, d.second)
}

// NameAt is the index of the name of names at s[i..], none when none is.
NameAt(s: []u8, i: int, names: []str): ?int {
    for k, n in names {
        nb := n.AsBytes()
        if s.Len() - i >= nb.Len() && Same(s[i..i + nb.Len()], nb) {
            return some(k)
        }
    }
    return none
}

// Same is whether a and b hold the same bytes.
Same(a: []u8, b: []u8): bool {
    if a.Len() != b.Len() {
        return false
    }
    for i in 0..a.Len() {
        if a[i] != b[i] {
            return false
        }
    }
    return true
}

// Clock reads "hh:mm:ss" at s[i..] as seconds of the day; none when
// malformed or out of range (second 60 included).
Clock(s: []u8, i: int): ?int {
    h := Digits(s, i, 2) else {
        return none
    }
    m := Digits(s, i + 3, 2) else {
        return none
    }
    sec := Digits(s, i + 6, 2) else {
        return none
    }
    if !Expect(s, i + 2, b':') || !Expect(s, i + 5, b':') || h > 23 || m > 59 || sec > 59 {
        return none
    }
    return some(h * 3600 + m * 60 + sec)
}

/// The instant an HTTP date names, in any of the three forms RFC 9110
/// section 5.6.7 accepts: IMF-fixdate ("Sun, 06 Nov 1994 08:49:37 GMT"),
/// RFC 850's form (the full day name, then the day, month name and
/// two-digit year joined by hyphens, then the time and "GMT") or asctime's
/// ("Sun Nov  6 08:49:37 1994", the day padded with a space or a zero).
///
/// RFC 850's year 69 to 99 is in the 1900s and 00 to 68 in the 2000s. RFC
/// 9110 asks instead for the most recent past year with those digits when
/// the date would be more than 50 years ahead, which needs the current
/// date. This function reads no clock, so a caller that needs the RFC's
/// reading moves a result more than 50 years ahead of its clock back 100
/// years. ParseError when s is none of these, a field is out of range, or
/// the instant is outside Time's range. O(n); allocates nothing.
export ParseHttpDate(s: str): ParseError!Time {
    b := s.AsBytes()
    // IMF-fixdate: day-name "," SP 2DIGIT SP month SP 4DIGIT SP time " GMT".
    if b.Len() == 29 && Expect(b, 3, b',') {
        _ = NameAt(b, 0, ShortDays[..]) else {
            fail ParseError
        }
        if !Expect(b, 4, b' ') || !Expect(b, 7, b' ') || !Expect(b, 11, b' ') || !Expect(b, 16, b' ') || !Same(b[25..29], " GMT".AsBytes()) {
            fail ParseError
        }
        d := Digits(b, 5, 2) else {
            fail ParseError
        }
        m := NameAt(b, 8, ShortMonths[..]) else {
            fail ParseError
        }
        y := Digits(b, 12, 4) else {
            fail ParseError
        }
        c := Clock(b, 17) else {
            fail ParseError
        }
        return try HttpTime(Cast[i64](y), m + 1, d, c)
    }
    // asctime: day-name SP month SP (2DIGIT / SP 1DIGIT) SP time SP 4DIGIT.
    if b.Len() == 24 && Expect(b, 3, b' ') {
        _ = NameAt(b, 0, ShortDays[..]) else {
            fail ParseError
        }
        m := NameAt(b, 4, ShortMonths[..]) else {
            fail ParseError
        }
        if !Expect(b, 7, b' ') || !Expect(b, 10, b' ') || !Expect(b, 19, b' ') {
            fail ParseError
        }
        day := if Expect(b, 8, b' ') { Digits(b, 9, 1) } else { Digits(b, 8, 2) }
        d := day else {
            fail ParseError
        }
        c := Clock(b, 11) else {
            fail ParseError
        }
        y := Digits(b, 20, 4) else {
            fail ParseError
        }
        return try HttpTime(Cast[i64](y), m + 1, d, c)
    }
    // RFC 850: day-name-l "," SP 2DIGIT "-" month "-" 2DIGIT SP time " GMT".
    w := NameAt(b, 0, LongDays[..]) else {
        fail ParseError
    }
    i := LongDays[w].Len()
    if b.Len() != i + 24 || !Expect(b, i, b',') || !Expect(b, i + 1, b' ') || !Expect(b, i + 4, b'-') || !Expect(b, i + 8, b'-') || !Expect(b, i + 11, b' ') || !Same(b[i + 20..i + 24], " GMT".AsBytes()) {
        fail ParseError
    }
    d := Digits(b, i + 2, 2) else {
        fail ParseError
    }
    m := NameAt(b, i + 5, ShortMonths[..]) else {
        fail ParseError
    }
    yy := Digits(b, i + 9, 2) else {
        fail ParseError
    }
    c := Clock(b, i + 12) else {
        fail ParseError
    }
    return try HttpTime(Cast[i64](if yy >= 69 { 1900 + yy } else { 2000 + yy }), m + 1, d, c)
}

// HttpTime is the UTC instant of a parsed HTTP date, checking the day.
HttpTime(y: i64, m: int, d: int, clock: int): ParseError!Time {
    if d < 1 || d > DaysIn(y, m) {
        fail ParseError
    }
    return InRange(DaysFromCivil(y, m, d) * SecondsPerDay + Cast[i64](clock), 0) else {
        fail ParseError
    }
}
