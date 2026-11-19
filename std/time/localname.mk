// Why: programs show and store the local zone's IANA name, which Windows does
// not report and Linux reports only indirectly.
//
// What: time.LocalName, with TzName and IanaOf, a cuttable unit with its
// table (windows_zones.mk).
//
// How: on Windows the key name GetDynamicTimeZoneInformation reports ("W.
// Europe Standard Time") maps to its IANA name through CLDR's windowsZones
// table, territory "001" (no registry read). On Linux it is TZ's zone name
// when Zone.Local reads TZ as a zone file it found, else, with TZ unset, the
// zone /etc/localtime links to, read from the link's path after "zoneinfo/".
// A simulated Io has none.

import std.core
import std.rt

/// The IANA name of the zone the system is set to, as "Europe/Paris", for
/// display or to pass to another program. On Windows the system's zone is
/// mapped to its IANA name through CLDR's table of Windows zones. none when
/// the system names no zone CLDR or the zone database knows (TZ set to "",
/// a path, a POSIX rule or a name with no readable TZif file, a copied
/// /etc/localtime), or io is simulated.
///
/// On Windows O(log n) over CLDR's 142 Windows zones; on Linux, when TZ
/// names a zone, O(n) in its file, read and checked as Zone.Local reads
/// it, so a cancelled scope or a lack of memory there gives none. It
/// allocates the name in a, and the zone file in a sub-arena of a, freed
/// before it returns.
export LocalName(a: &Arena, io: Io): ?str from a {
    if Simulated(io) {
        return none
    }
    if Target.os == Os.Windows {
        var buf: [432]u8 = [0; 432]
        r := rt.TimeZone(io, 0, mut buf[..])
        if r.err != core.SysErr.None {
            return none
        }
        // TimeZoneKeyName: 128 UTF-16 units at 172, NUL-terminated; every
        // key name is ASCII.
        var key: [128]u8 = [0; 128]
        var n := 0
        for n < 128 {
            c := Le16(buf[..], 172 + 2 * n)
            if c == 0 || c > 0x7E {
                break
            }
            key[n] = Trunc[u8](c)
            n += 1
        }
        k := str.From(key[0..n]) else {
            return none
        }
        z := IanaOf(k) else {
            return none
        }
        d := a.Dup(z) else {
            return none
        }
        return some(d)
    }
    var tmp := Arena.Sub(a) else {
        return none
    }
    defer tmp.Free()
    tzv := EnvValue(&tmp, io, "TZ") else {
        return none
    }
    match tzv {
        some(tz) {
            return TzName(a, &tmp, io, tz)
        }
        none {}
    }
    var link: [4096]u8 = [0; 4096]
    l := rt.ReadLink(io, "/etc/localtime".AsBytes(), mut link[..])
    if l.err != core.SysErr.None || l.value <= 0 || l.value >= 4096 {
        return none
    }
    target := str.From(link[0..Cast[int](l.value)]) else {
        return none
    }
    at := target.Find("zoneinfo/") else {
        return none
    }
    name := target[at + 9..]
    if !ValidName(name) {
        return none
    }
    d := a.Dup(name) else {
        return none
    }
    return some(d)
}

// TzName is the zone name in TZ's value tz, copied into a, when Zone.Local
// reads tz as that zone's file: a ValidName after an optional ':' whose
// file FindZoneFile finds and FromTzif reads. None otherwise, a cancel or
// a lack of memory included. Reads the file into tmp.
TzName(a: &Arena, tmp: &Arena, io: Io, tz: str): ?str from a {
    s := if tz.StartsWith(":") { tz[1..] } else { tz }
    if !ValidName(s) {
        return none
    }
    r := FindZoneFile(tmp, io, s) else {
        return none
    }
    if r.how != Found {
        return none
    }
    _ = Zone.FromTzif(tmp, s, r.data) else {
        return none
    }
    d := a.Dup(s) else {
        return none
    }
    return some(d)
}

// IanaOf is the IANA name of a Windows zone key name, none when CLDR has
// no such zone: a binary search over the sorted names.
IanaOf(win: str): ?str {
    w := win.AsBytes()
    t := WinText.AsBytes()
    var lo := 0
    var hi := WinEnd.Len()
    for lo < hi {
        mid := lo + (hi - lo) / 2
        start := if mid == 0 { 0 } else { int(WinEnd[mid - 1]) }
        c := CompareBytes(t[start..int(WinEnd[mid])], w)
        if c == 0 {
            from := if mid == 0 { 0 } else { int(IanaEnd[mid - 1]) }
            return some(IanaText[from..int(IanaEnd[mid])])
        }
        if c < 0 {
            lo = mid + 1
        } else {
            hi = mid
        }
    }
    return none
}

// CompareBytes orders byte strings as Go's sort does: -1, 0 or 1.
CompareBytes(x: []u8, y: []u8): int {
    n := if x.Len() < y.Len() { x.Len() } else { y.Len() }
    for i in 0..n {
        if x[i] != y[i] {
            return if x[i] < y[i] { -1 } else { 1 }
        }
    }
    if x.Len() == y.Len() {
        return 0
    }
    return if x.Len() < y.Len() { -1 } else { 1 }
}
