// Why: `cron zone` lists a zone's transitions in a year, which std.time does
// not enumerate.
//
// What: Transitions, Change and FormatOffset.
//
// How: samples the offset every hour and bisects each change to the second.

import std.time

/// One change of a zone's offset.
export struct Change {
    export at: time.Time
    export from_offset: i64
    export to_offset: i64
}

/// The transitions of `z` in `year`, in order. A zone whose offset changes
/// twice within an hour shows the net change only; tzdata has no such
/// zone since 1970. O(8,784) offset lookups; the list lives in `a`.
export Transitions(a: &Arena, z: &time.Zone, year: i32): OutOfMemory!List[Change] from a {
    var out := List[Change].New(a)
    start := DaysFromCivil(year, 1, 1) * 86400
    end := DaysFromCivil(year + 1, 1, 1) * 86400
    var t := start
    var off := Offset(z, t)
    for t < end {
        next := t + 3600
        o := Offset(z, next)
        if o != off {
            var lo := t
            var hi := next
            for hi - lo > 1 {
                mid := lo + (hi - lo) / 2
                if Offset(z, mid) == off {
                    lo = mid
                } else {
                    hi = mid
                }
            }
            try out.Push(Change(at: time.FromUnix(hi, 0), from_offset: off, to_offset: o))
            off = o
        }
        t = next
    }
    return out
}

/// Writes an offset as `+02:00` or `-03:30`.
export FormatOffset(a: &Arena, seconds: i64): OutOfMemory!str from a {
    sign := if seconds < 0 { "-" } else { "+" }
    abs := if seconds < 0 { -seconds } else { seconds }
    hours := abs / 3600
    minutes := abs % 3600 / 60
    return try a.Fmt("{sign}{hours:02}:{minutes:02}", (sign, hours, minutes))
}
