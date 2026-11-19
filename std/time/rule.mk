// Why: past a zone file's last transition, the offsets come from the POSIX TZ
// rule in its footer, and the same rule reader serves a TZ variable.
//
// What: Rule, ParseRule, the scanner RuleScan and Rule.At.
//
// How: the POSIX TZ rule of a TZif footer (RFC 8536 section 3.3, POSIX.1-2017
// section 8.3 "TZ", with RFC 8536's extensions: hours -167 to 167 in a
// rule's time, and permanent daylight time as "J365/25"). The reader is a
// one-pass scan with no recursion and no allocation; a rule is evaluated for
// an instant's UTC year as Go's time.tzset does, so its offsets agree with
// the oracle's.

// The kinds of Rule.
const RuleNone: u8 = 0
const RuleFixed: u8 = 1
const RuleDst: u8 = 2

// The forms of a RuleDate: Jn, n and Mm.w.d.
const DateJulian: u8 = 0
const DateZero: u8 = 1
const DateMonth: u8 = 2

// RuleDate is one end of daylight time: the date, and the local time of
// day in seconds at which it changes (default 02:00:00).
struct RuleDate {
    form: u8
    day: int
    month: int
    week: int
    secs: i64
}

// Rule is a parsed footer: none, a fixed offset (std only), or standard
// and daylight offsets (seconds east of UTC) with the dates between which
// daylight time is in force.
struct Rule {
    kind: u8
    std: i32
    dst: i32
    start: RuleDate
    end: RuleDate
}

// NoRule is the rule of a zone with no footer.
NoRule(): Rule {
    d := RuleDate(form: DateMonth, day: 0, month: 1, week: 1, secs: 0)
    return Rule(kind: RuleNone, std: 0, dst: 0, start: d, end: d)
}

// RuleScan is the reader's position in a footer.
struct RuleScan {
    s: []u8
    i: int
}

// ParseRule reads a POSIX TZ string; none when it is not one. An empty
// string is NoRule. O(n); allocates nothing; no recursion.
ParseRule(s: []u8): ?Rule {
    var r := NoRule()
    if s.Len() == 0 {
        return some(r)
    }
    var p := RuleScan(s: s, i: 0)
    if !p.Name() {
        return none
    }
    std := p.Offset(24) else {
        return none
    }
    r.std = Cast[i32](-std)
    r.kind = RuleFixed
    if p.i == s.Len() {
        return some(r)
    }
    if !p.Name() {
        return none
    }
    r.kind = RuleDst
    r.dst = r.std + 3600
    if p.i < s.Len() && s[p.i] != b',' {
        dst := p.Offset(24) else {
            return none
        }
        r.dst = Cast[i32](-dst)
    }
    if p.i == s.Len() {
        // POSIX leaves the dates to the implementation; Go and glibc use
        // the United States' since 2007.
        r.start = RuleDate(form: DateMonth, day: 0, month: 3, week: 2, secs: 7200)
        r.end = RuleDate(form: DateMonth, day: 0, month: 11, week: 1, secs: 7200)
        return some(r)
    }
    if s[p.i] != b',' {
        return none
    }
    p.i += 1
    r.start = p.Date() else {
        return none
    }
    if p.i >= s.Len() || s[p.i] != b',' {
        return none
    }
    p.i += 1
    r.end = p.Date() else {
        return none
    }
    if p.i != s.Len() {
        return none
    }
    return some(r)
}

// Name reads a zone abbreviation: three or more letters, or "<", three or
// more letters, digits, '+' or '-', then ">".
RuleScan.Name(mut self): bool {
    s := self.s
    if self.i < s.Len() && s[self.i] == b'<' {
        var j := self.i + 1
        for j < s.Len() && s[j] != b'>' {
            c := s[j]
            if !IsAlpha(c) && !IsDigit(c) && c != b'+' && c != b'-' {
                return false
            }
            j += 1
        }
        if j >= s.Len() || j - self.i - 1 < 3 {
            return false
        }
        self.i = j + 1
        return true
    }
    var j := self.i
    for j < s.Len() && IsAlpha(s[j]) {
        j += 1
    }
    if j - self.i < 3 {
        return false
    }
    self.i = j
    return true
}

// Offset reads [+-]hh[:mm[:ss]] with hh at most max, as seconds; none when
// malformed.
RuleScan.Offset(mut self, max: int): ?i64 {
    s := self.s
    var neg := false
    if self.i < s.Len() && (s[self.i] == b'+' || s[self.i] == b'-') {
        neg = s[self.i] == b'-'
        self.i += 1
    }
    h := self.Num(1, 3, max) else {
        return none
    }
    var secs := Cast[i64](h) * 3600
    if self.i < s.Len() && s[self.i] == b':' {
        self.i += 1
        m := self.Num(2, 2, 59) else {
            return none
        }
        secs += Cast[i64](m) * 60
        if self.i < s.Len() && s[self.i] == b':' {
            self.i += 1
            sec := self.Num(2, 2, 59) else {
                return none
            }
            secs += Cast[i64](sec)
        }
    }
    return some(if neg { -secs } else { secs })
}

// Num reads lo to hi decimal digits whose value is at most max.
RuleScan.Num(mut self, lo: int, hi: int, max: int): ?int {
    s := self.s
    var v := 0
    var k := 0
    for self.i < s.Len() && k < hi && IsDigit(s[self.i]) {
        v = v * 10 + int(s[self.i] - b'0')
        self.i += 1
        k += 1
    }
    if k < lo || v > max {
        return none
    }
    return some(v)
}

// Date reads Jn (1 to 365), n (0 to 365) or Mm.w.d, then an optional
// "/time" (RFC 8536: -167 to 167 hours).
RuleScan.Date(mut self): ?RuleDate {
    s := self.s
    if self.i >= s.Len() {
        return none
    }
    var d := RuleDate(form: DateZero, day: 0, month: 0, week: 0, secs: 7200)
    if s[self.i] == b'J' {
        self.i += 1
        n := self.Num(1, 3, 365) else {
            return none
        }
        if n < 1 {
            return none
        }
        d.form = DateJulian
        d.day = n
    } else if s[self.i] == b'M' {
        self.i += 1
        m := self.Num(1, 2, 12) else {
            return none
        }
        if m < 1 || self.i >= s.Len() || s[self.i] != b'.' {
            return none
        }
        self.i += 1
        w := self.Num(1, 1, 5) else {
            return none
        }
        if w < 1 || self.i >= s.Len() || s[self.i] != b'.' {
            return none
        }
        self.i += 1
        wd := self.Num(1, 1, 6) else {
            return none
        }
        d.form = DateMonth
        d.month = m
        d.week = w
        d.day = wd
    } else {
        n := self.Num(1, 3, 365) else {
            return none
        }
        d.day = n
    }
    if self.i < s.Len() && s[self.i] == b'/' {
        self.i += 1
        t := self.Offset(167) else {
            return none
        }
        d.secs = t
    }
    return some(d)
}

IsAlpha(c: u8): bool {
    return (c >= b'a' && c <= b'z') || (c >= b'A' && c <= b'Z')
}

IsDigit(c: u8): bool {
    return c >= b'0' && c <= b'9'
}

// DayOfYear is the zero-based day of year y that d names.
DayOfYear(y: i64, d: &RuleDate): i64 {
    if d.form == DateJulian {
        // Jn counts 1 to 365 and never February 29.
        n := Cast[i64](d.day - 1)
        return if IsLeap(y) && d.day >= 60 { n + 1 } else { n }
    }
    if d.form == DateZero {
        return Cast[i64](d.day)
    }
    // Mm.w.d: weekday d of week w (5 is the last) of month m.
    first := DaysFromCivil(y, d.month, 1)
    wd1 := Weekday(first)
    var day := 1 + (d.day - wd1 + 7) % 7 + 7 * (d.week - 1)
    if day > DaysIn(y, d.month) {
        day -= 7
    }
    return first + Cast[i64](day - 1) - DaysFromCivil(y, 1, 1)
}

// At is the span of UTC seconds around u with one offset, inside u's UTC
// year: Go's tzset, with the spans clamped to the year so that the spans
// of successive years never overlap.
Rule.At(&self, u: i64): Period {
    if self.kind != RuleDst {
        return Period(start: NoStart, end: NoEnd, off: self.std)
    }
    y := CivilFromDays(FloorDiv(u, SecondsPerDay)).year
    abs := DaysFromCivil(y, 1, 1) * SecondsPerDay
    next := DaysFromCivil(y + 1, 1, 1) * SecondsPerDay
    var s := abs + DayOfYear(y, &self.start) * SecondsPerDay + self.start.secs - Cast[i64](self.std)
    var e := abs + DayOfYear(y, &self.end) * SecondsPerDay + self.end.secs - Cast[i64](self.dst)
    var outer := self.std
    var inner := self.dst
    if e < s {
        Swap(mut s, mut e)
        Swap(mut outer, mut inner)
    }
    if u < s {
        return Period(start: abs, end: if s < next { s } else { next }, off: outer)
    }
    if u >= e {
        return Period(start: if e > abs { e } else { abs }, end: next, off: outer)
    }
    return Period(start: if s > abs { s } else { abs }, end: if e < next { e } else { next }, off: inner)
}
