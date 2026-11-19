// Why: a fire must print the same way in `next`, the log and the summary.
//
// What: Show.
//
// How: its local time with the offset, the instant in UTC, and what a
// transition did to it.

import std.time

/// `2026-03-29 03:00 +02:00 (2026-03-29T01:00:00Z)`, with `, skipped
/// time run at the transition` or `, repeated time run once` when a
/// transition moved it. Allocates the line in `a`.
export Show(a: &Arena, f: &Fire, z: &time.Zone): OutOfMemory!str from a {
    d := f.at.In(z)
    // the instant the local fields name, which is the fire's own
    utc := d.Time()
    offset := try FormatOffset(a, i64(d.offset_seconds))
    note := match f.fit {
        .Exact { "" }
        .Skipped { ", skipped time run at the transition" }
        .Repeated { ", repeated time run once" }
    }
    return try a.Fmt(
        "{d.year}-{d.month:02}-{d.day:02} {d.hour:02}:{d.minute:02}:{d.second:02} {offset} ({utc}){note}",
        (d, offset, utc, note),
    )
}
