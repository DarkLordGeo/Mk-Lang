// Why: the same crontab must mean the same instants on Windows, on Linux and
// under simulation.
//
// What: LoadZone for CRON_TZ and CRON_TZFILE lines, LocalZone, Embedded,
// and the path helpers Beside and DirOf.
//
// How: a named zone is looked up in the system's database, then in the one
// compiled into cron (std.time.tzdata); a host whose zone cannot be read
// gets UTC, with a problem reported.

import std.fs
import std.time
import std.time.tzdata

/// The zones compiled into cron, looked in after the system's own: the
/// only zones there are on Windows and under std.sim.
export const Embedded: ?time.Database = some(tzdata.Database)

/// Loads the zone a CRON_TZ or CRON_TZFILE line names and returns its
/// index in `tab.zones`, reusing a zone already loaded under that name;
/// none, with a problem reported, when it cannot be loaded.
LoadZone(a: &Arena into tab, io: Io, tab: mut Crontab, host: &Host, line: int, key: str, value: str): OutOfMemory!?int {
    if key == "CRON_TZ" && value == "local" {
        return some(try LocalZone(a, io, tab, line))
    }
    for i, z in &tab.zones {
        if z.name == value && z.source != Source.Local {
            return some(i)
        }
    }
    if key == "CRON_TZ" {
        zone := time.Zone.Load(a, io, value, Embedded) else e {
            hint := ZoneHint(e)
            try Report(tab, line, try a.Fmt("CRON_TZ={value}: {e} ({hint})", (value, e, hint)))
            return none
        }
        try tab.zones.Push(ZoneDef(name: try a.Dup(value), zone: zone, source: Source.Named))
        return some(tab.zones.Len() - 1)
    }
    path := try Beside(a, host.dir, value)
    data := fs.ReadBytes(a, io, path) else e {
        try Report(tab, line, try a.Fmt("CRON_TZFILE={value}: {e}", (value, e)))
        return none
    }
    zone := time.Zone.FromTzif(a, value, data) else e {
        hint := ZoneHint(e)
        try Report(tab, line, try a.Fmt("CRON_TZFILE={value}: {e} ({hint})", (value, e, hint)))
        return none
    }
    try tab.zones.Push(ZoneDef(name: try a.Dup(value), zone: zone, source: Source.File))
    return some(tab.zones.Len() - 1)
}

/// The host's zone, loaded once. Its name is the IANA name the system
/// gives, or `local` when it gives none. A host whose zone cannot be read
/// gets UTC, with a problem reported.
LocalZone(a: &Arena into tab, io: Io, tab: mut Crontab, line: int): OutOfMemory!int {
    for i, z in &tab.zones {
        if z.source == Source.Local {
            return i
        }
    }
    name := time.LocalName(a, io) else { "local" }
    zone := time.Zone.Local(a, io) else e {
        try Report(tab, line, try a.Fmt("the host's zone: {e}; using UTC", (e)))
        time.Zone.Utc()
    }
    try tab.zones.Push(ZoneDef(name: try a.Dup(name), zone: zone, source: Source.Local))
    return tab.zones.Len() - 1
}

/// `path` taken relative to `dir` unless it is absolute.
export Beside(a: &Arena, dir: str, path: str): OutOfMemory!str from a, path {
    if dir.Len() == 0 || IsAbsolute(path) {
        return path
    }
    return try a.Fmt("{dir}/{path}", (dir, path))
}

/// A path that starts at a root: `/x`, `\x` or a drive `C:`.
IsAbsolute(path: str): bool {
    if path.StartsWith("/") || path.StartsWith("\\") {
        return true
    }
    return path.Len() >= 2 && path.Byte(1) == b':'
}

/// The directory part of `path`, `""` for a bare name.
export DirOf(path: str): str from path {
    var cut := -1
    for i in 0..path.Len() {
        c := path.Byte(i)
        if c == b'/' || c == b'\\' {
            cut = i
        }
    }
    if cut < 0 {
        return ""
    }
    if cut == 0 {
        return path[..1]
    }
    return path[..cut]
}

/// What a zone that cannot be used means to the crontab's author.
export ZoneHint(e: time.Error | OutOfMemory): str {
    return match e {
        time.BadName { "not a zone name such as Europe/Berlin" }
        time.BadData { "not a TZif file" }
        time.NotFound { "in neither the system's zone database nor cron's" }
        time.NoDatabase { "this system has no zone database" }
        time.OutOfRange { "outside the years the zone describes" }
        time.Skipped | time.Repeated { "a local time the zone skips or repeats" }
        Canceled { "canceled" }
        OutOfMemory { "out of memory" }
        _ { "an error this cron does not know" }
    }
}
