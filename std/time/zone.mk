// Why: converting an instant to local time needs the UTC offset in force at
// that instant, past and future.
//
// What: Zone, Zone.Utc, Zone.Fixed, and the lookups OffsetAt, PeriodAt and
// Resolve.
//
// How: a table of transitions read from TZif (RFC 8536) and the POSIX rule of
// its footer, which gives the offset past the table's last transition. A
// lookup is one binary search over the transitions, as Go's Location.lookup
// and the tz reference code's localsub do, then O(1) for the rule.

/// A time zone: the UTC offset in force at each instant. Utc and Fixed need
/// nothing; a zone read from TZif holds its transitions in the arena it was
/// read into.
export view struct Zone {
    // trans holds the UTC seconds of each transition, strictly increasing;
    // offs[i] is the offset from trans[i] on.
    trans: []i64
    offs: []i32
    // first is the offset before trans[0] (RFC 8536 3.2: time type 0),
    // and everywhere when there is neither a transition nor a rule.
    first: i32
    // rule applies past the last transition, or everywhere when there is
    // no transition (RFC 8536 3.2, 3.3).
    rule: Rule
}

// Period is a span [start, end) of UTC seconds with one offset.
struct Period {
    start: i64
    end: i64
    off: i32
}

// NoSeconds and NoOffsets are the empty table of Utc and Fixed.
const NoSeconds: []i64 = []
const NoOffsets: []i32 = []

// NoStart and NoEnd stand for an open end of a Period.
const NoStart: i64 = -9_223_372_036_854_775_807 - 1
const NoEnd: i64 = 9_223_372_036_854_775_807

/// Coordinated Universal Time, offset 0 always. O(1); allocates nothing.
export Zone.Utc(): Zone {
    return Zone(trans: NoSeconds, offs: NoOffsets, first: 0, rule: NoRule())
}

/// A zone whose offset from UTC is always offset_seconds, east positive.
/// O(1); allocates nothing.
export Zone.Fixed(offset_seconds: i32): Zone {
    return Zone(trans: NoSeconds, offs: NoOffsets, first: offset_seconds, rule: NoRule())
}

// OffsetAt is the offset in force at UTC second u. O(log n).
Zone.OffsetAt(&self, u: i64): i32 {
    n := self.trans.Len()
    if n == 0 {
        return if self.rule.kind == RuleNone { self.first } else { self.rule.At(u).off }
    }
    if u < self.trans[0] {
        return self.first
    }
    i := LastAtOrBefore(self.trans, u)
    if i == n - 1 && self.rule.kind != RuleNone {
        return self.rule.At(u).off
    }
    return self.offs[i]
}

// PeriodAt is the span holding UTC second u and its offset. O(log n).
Zone.PeriodAt(&self, u: i64): Period {
    return self.PeriodOf(self.IndexAt(u), u)
}

// IndexAt is the index of the last transition at or before UTC second u,
// -1 when there is none. O(log n).
Zone.IndexAt(&self, u: i64): int {
    if self.trans.Len() == 0 || u < self.trans[0] {
        return -1
    }
    return LastAtOrBefore(self.trans, u)
}

// PeriodOf is the span holding UTC second u, given i, IndexAt(u). O(1).
Zone.PeriodOf(&self, i: int, u: i64): Period {
    n := self.trans.Len()
    if n == 0 {
        if self.rule.kind == RuleNone {
            return Period(start: NoStart, end: NoEnd, off: self.first)
        }
        return self.rule.At(u)
    }
    if i < 0 {
        return Period(start: NoStart, end: self.trans[0], off: self.first)
    }
    if i < n - 1 {
        return Period(start: self.trans[i], end: self.trans[i + 1], off: self.offs[i])
    }
    if self.rule.kind == RuleNone {
        return Period(start: self.trans[i], end: NoEnd, off: self.offs[i])
    }
    p := self.rule.At(u)
    return Period(start: if p.start < self.trans[i] { self.trans[i] } else { p.start }, end: p.end, off: p.off)
}

// LastAtOrBefore is the greatest i with t[i] <= u; t is sorted, non-empty
// and t[0] <= u. A branch-free-friendly lower bound over half-open [lo, hi).
LastAtOrBefore(t: []i64, u: i64): int {
    var lo := 0
    var hi := t.Len()
    for hi - lo > 1 {
        mid := lo + (hi - lo) / 2
        if t[mid] <= u {
            lo = mid
        } else {
            hi = mid
        }
    }
    return lo
}

// ResolveWindow is how far either side of a local time Resolve looks for
// a period that shows it: offsets are within 26 hours of UTC (tzif.mk
// refuses others), so two days hold every candidate.
const ResolveWindow: i64 = 2 * 86_400

// Resolve is the UTC second whose local time in self is local: Skipped
// when no period shows it, Repeated when two do. It walks the periods from
// local - ResolveWindow to local + ResolveWindow; a period with offset o
// shows local when local - o is inside it. One binary search finds the
// first period, and each next one is the next transition or the rule's
// next span, so the cost is O(log n + k), k the periods in the window.
Zone.Resolve(&self, local: i64): Error!i64 {
    var found := 0
    var at: i64 = 0
    var u := local - ResolveWindow
    end := local + ResolveWindow
    n := self.trans.Len()
    var i := self.IndexAt(u)
    for u <= end {
        p := self.PeriodOf(i, u)
        c := local - Cast[i64](p.off)
        if c >= p.start && c < p.end {
            if found == 0 || c != at {
                found += 1
                at = c
            }
        }
        if p.end == NoEnd {
            break
        }
        u = p.end
        if i < n - 1 && u >= self.trans[i + 1] {
            i += 1
        }
    }
    if found == 0 {
        fail Skipped
    }
    if found > 1 {
        fail Repeated
    }
    return at
}
