// Why: RFC 3339 is the timestamp format of logs, JSON and most protocols.
//
// What: Time.Format's writer PutRfc3339 and ParseRfc3339, for RFC 3339
// section 5.6's date-time.
//
// How: the reader is one left-to-right pass with no recursion and no
// allocation; it never panics on input.

// Put2 writes v (0..99) as two digits at buf[i..].
Put2(buf: mut []u8, i: int, v: int) {
    buf[i] = Trunc[u8](48 + v / 10)
    buf[i + 1] = Trunc[u8](48 + v % 10)
}

// Put4 writes v (0..9999) as four digits at buf[i..].
Put4(buf: mut []u8, i: int, v: int) {
    Put2(buf, i, v / 100)
    Put2(buf, i + 2, v % 100)
}

// PutRfc3339 writes the instant nanos at offset off (seconds east) as RFC
// 3339 into buf and returns the length (at most 35): "Z" for offset 0,
// else "+hh:mm" (the offset's seconds are dropped, as Go's RFC3339 does);
// fraction digits only as the nanoseconds need. Every Time is in years
// 1677 to 2262, so the year is four digits.
PutRfc3339(b: mut []u8, nanos: i64, off: i32): int {
    secs := FloorDiv(nanos, NanosPerSecond)
    ns := Cast[int](FloorMod(nanos, NanosPerSecond))
    d := DateOf(secs + Cast[i64](off), ns, off)
    Put4(b, 0, int(d.year))
    b[4] = b'-'
    Put2(b, 5, d.month)
    b[7] = b'-'
    Put2(b, 8, d.day)
    b[10] = b'T'
    Put2(b, 11, d.hour)
    b[13] = b':'
    Put2(b, 14, d.minute)
    b[16] = b':'
    Put2(b, 17, d.second)
    var n := 19
    if ns != 0 {
        b[n] = b'.'
        var f := ns
        for k in 0..9 {
            b[n + 9 - k] = Trunc[u8](48 + f % 10)
            f /= 10
        }
        n += 10
        for b[n - 1] == b'0' {
            n -= 1
        }
    }
    if off == 0 {
        b[n] = b'Z'
        return n + 1
    }
    var o := int(off)
    b[n] = if o < 0 { b'-' } else { b'+' }
    if o < 0 {
        o = -o
    }
    Put2(b, n + 1, o / 3600)
    b[n + 3] = b':'
    Put2(b, n + 4, o / 60 % 60)
    return n + 6
}

// Digits reads exactly k decimal digits at s[i..]; none when short or not
// digits. Two and four digits, every field of RFC 3339 and of an HTTP date
// but asctime's one-digit day, are straight-line (Digits2: two loads and
// one compare a pair, no loop); any other k is the loop (DigitsN). Four
// digits are two pairs: none when either pair is short or not digits,
// which is when the four are.
Digits(s: []u8, i: int, k: int): ?int {
    if k == 2 {
        return Digits2(s, i)
    }
    if k == 4 {
        if hi := Digits2(s, i) {
            if lo := Digits2(s, i + 2) {
                return some(hi * 100 + lo)
            }
        }
        return none
    }
    return DigitsN(s, i, k)
}

// Digits2 is Digits for two digits. A byte minus '0', wrapping, is at most
// 9 exactly when the byte is a digit.
Digits2(s: []u8, i: int): ?int {
    if s.Len() - i < 2 {
        return none
    }
    x := s[i] -% b'0'
    y := s[i + 1] -% b'0'
    if x > 9 || y > 9 {
        return none
    }
    return some(int(x) * 10 + int(y))
}

// DigitsN is Digits for any k, a digit a step.
DigitsN(s: []u8, i: int, k: int): ?int {
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

// Expect is whether s[i] is c.
Expect(s: []u8, i: int, c: u8): bool {
    return i < s.Len() && s[i] == c
}

/// The instant an RFC 3339 date-time names: "2006-01-02T15:04:05Z", with an
/// optional fraction of any length (digits past the ninth are dropped) and
/// "Z" or a "+hh:mm" or "-hh:mm" offset; "T" and "Z" may be lower case, as
/// RFC 3339 section 5.6 allows. ParseError when s is not one, a field is
/// out of range (second 60 included: leap seconds are not represented), or
/// the instant is outside Time's range. O(n); allocates nothing.
export ParseRfc3339(s: str): ParseError!Time {
    b := s.AsBytes()
    y := Digits(b, 0, 4) else {
        fail ParseError
    }
    if !Expect(b, 4, b'-') || !Expect(b, 7, b'-') || !Expect(b, 13, b':') || !Expect(b, 16, b':') {
        fail ParseError
    }
    if !Expect(b, 10, b'T') && !Expect(b, 10, b't') {
        fail ParseError
    }
    mo := Digits(b, 5, 2) else {
        fail ParseError
    }
    d := Digits(b, 8, 2) else {
        fail ParseError
    }
    h := Digits(b, 11, 2) else {
        fail ParseError
    }
    mi := Digits(b, 14, 2) else {
        fail ParseError
    }
    se := Digits(b, 17, 2) else {
        fail ParseError
    }
    if mo < 1 || mo > 12 || d < 1 || d > DaysIn(Cast[i64](y), mo) || h > 23 || mi > 59 || se > 59 {
        fail ParseError
    }
    var i := 19
    var ns := 0
    if Expect(b, i, b'.') {
        i += 1
        start := i
        for i < b.Len() && b[i] >= b'0' && b[i] <= b'9' {
            if i - start < 9 {
                ns = ns * 10 + int(b[i] - b'0')
            }
            i += 1
        }
        if i == start {
            fail ParseError
        }
        for k in i - start..9 {
            _ = k
            ns *= 10
        }
    }
    var off := 0
    if Expect(b, i, b'Z') || Expect(b, i, b'z') {
        i += 1
    } else if Expect(b, i, b'+') || Expect(b, i, b'-') {
        neg := b[i] == b'-'
        oh := Digits(b, i + 1, 2) else {
            fail ParseError
        }
        if !Expect(b, i + 3, b':') {
            fail ParseError
        }
        om := Digits(b, i + 4, 2) else {
            fail ParseError
        }
        if oh > 23 || om > 59 {
            fail ParseError
        }
        off = oh * 3600 + om * 60
        if neg {
            off = -off
        }
        i += 6
    } else {
        fail ParseError
    }
    if i != b.Len() {
        fail ParseError
    }
    secs := DaysFromCivil(Cast[i64](y), mo, d) * SecondsPerDay + Cast[i64](h * 3600 + mi * 60 + se - off)
    return InRange(secs, ns) else {
        fail ParseError
    }
}

// InRange is the Time of secs and ns (0..10^9), none outside Time's range.
InRange(secs: i64, ns: int): ?Time {
    if secs > 9_223_372_036 || secs < -9_223_372_037 {
        return none
    }
    if secs == 9_223_372_036 && ns > 854_775_807 {
        return none
    }
    if secs == -9_223_372_037 && ns < 145_224_192 {
        return none
    }
    return some(Time(nanos: SecondsNanos(secs, Cast[i64](ns))))
}
