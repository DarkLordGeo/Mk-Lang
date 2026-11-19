// Why: fm shows file times in a zone the user picks with `--zone`.
// What: ZoneChoice, ParseZone, LoadZone and WriteWhen.
// How: `local`, `utc` or `+HH:MM` is parsed into a ZoneChoice; LoadZone turns
// it into a time.Zone once, and WriteWhen formats each time in it.

import std.time

/// The zone fm shows file times in, chosen by `--zone`.
export enum ZoneChoice {
    Local
    Utc
    Fixed(offset_seconds: i32)
}

/// A `--zone` value that is not `local`, `utc` or `+HH:MM`; `text` is its
/// length in bytes.
export error BadZone(text: int)

/// Reads `--zone`'s value: `local`, `utc`, or an offset `+HH:MM` or
/// `-HH:MM` up to 23:59. Fails with `BadZone` holding the value's length
/// for anything else. O(1); allocates nothing.
export ParseZone(s: str): BadZone!ZoneChoice {
    if s == "local" {
        return ZoneChoice.Local
    }
    if s == "utc" || s == "UTC" {
        return ZoneChoice.Utc
    }
    if s.Len() != 6 || s.Byte(3) != b':' {
        fail BadZone(text: s.Len())
    }
    sign := s.Byte(0)
    if sign != b'+' && sign != b'-' {
        fail BadZone(text: s.Len())
    }
    hours := s[1..3].ParseInt() else {
        fail BadZone(text: s.Len())
    }
    minutes := s[4..6].ParseInt() else {
        fail BadZone(text: s.Len())
    }
    if hours < 0 || hours > 23 || minutes < 0 || minutes > 59 {
        fail BadZone(text: s.Len())
    }
    var offset := Trunc[i32](hours * 3600 + minutes * 60)
    if sign == b'-' {
        offset = -offset
    }
    return ZoneChoice.Fixed(offset_seconds: offset)
}

/// Makes the zone `c` names: the system's local zone (`time.Zone.Local`),
/// UTC, or a fixed offset. Fails as `Zone.Local` does when the system's
/// zone cannot be read. O(size of the zone's data); allocates it in `a`.
export LoadZone(a: &Arena, io: Io, c: ZoneChoice): time.Error|OutOfMemory!time.Zone from a {
    return match c {
        .Local { try time.Zone.Local(a, io) }
        .Utc { time.Zone.Utc() }
        .Fixed(offset_seconds) { time.Zone.Fixed(offset_seconds) }
    }
}

/// Writes `t` in `z` as `YYYY-MM-DD HH:MM:SS +HH:MM`, the civil time and
/// the zone's offset at that instant. Errors are `w`'s. O(log of the
/// zone's transitions); allocates nothing.
export WriteWhen(w: mut Writer, t: time.Time, z: &time.Zone): WriteError!void {
    d := t.In(z)
    var off := d.offset_seconds
    sign := if off < 0 { '-' } else { '+' }
    if off < 0 {
        off = -off
    }
    oh := off / 3600
    om := off % 3600 / 60
    try Print(
        w,
        "{d.year:04}-{d.month:02}-{d.day:02} {d.hour:02}:{d.minute:02}:{d.second:02} {sign}{oh:02}:{om:02}",
        (d, sign, oh, om),
    )
}
