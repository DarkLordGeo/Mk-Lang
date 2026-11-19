// Why: a crontab is the daemon's whole configuration, and every problem in
// it must be reported with its line before anything runs.
//
// What: Crontab, Entry, Spec, Schedule, ZoneDef, Var, Problem and Host, with
// Parse, Lookup and Find.
//
// How: read line by line into an arena, in the format below.

// A crontab: variables, zone lines and entries, read into an arena.
//
//   # comment
//   NAME=value                 passed to every job; `$NAME` in a command
//   CRON_TZ=Europe/Berlin      the zone of the entries that follow
//   CRON_TZ=local              the host's zone (the default)
//   CRON_TZFILE=zones/x.tzif   a zone from a TZif file, beside the crontab
//   30 2 * * *  backup  timeout=10m  --  tar -czf /backup/home.tgz /home
//   @every 5s   probe   input=ping   --  check-alive | tee probe.log
//
// Problems are collected, each with its line, so `check` reports them all.

import std.time

/// What a crontab line asked for, or why it could not be read.
export struct Problem {
    export line: int
    export message: str
}

/// The time fields of a five-field schedule as bit sets: bit v of
/// `minutes` is set when minute v matches. Weekday 7 is folded into 0.
export struct Spec {
    export minutes: u64
    export hours: u64
    export days: u64
    export months: u64
    export weekdays: u64
    export any_day: bool
    export any_weekday: bool
}

/// When an entry runs.
export enum Schedule {
    Cron(spec: Spec)
    Every(nanos: i64)
    Reboot
}

/// Where a zone came from.
export enum Source {
    Named
    Local
    File
}

/// A zone the crontab named, with the name it prints as.
export struct ZoneDef {
    export name: str
    export zone: time.Zone
    export source: Source
}

/// A `NAME=value` line.
export struct Var {
    export name: str
    export value: str
}

/// One job: its schedule, the zone it is read in (an index into
/// `Crontab.zones`), its options and its command, a pipeline of one or two
/// stages (`then` is empty for one).
export struct Entry {
    export name: str
    export line: int
    export schedule: Schedule
    /// The schedule as written, such as `30 2 * * *` or `@every 5s`
    export when: str
    export zone: int
    export timeout: ?time.Duration
    export detach: bool
    export input: ?str
    export argv: []str
    export then: []str
}

/// A crontab as read, with every problem found in it.
export struct Crontab {
    export path: str
    export vars: List[Var]
    export zones: List[ZoneDef]
    export entries: List[Entry]
    export problems: List[Problem]
}

/// Where the crontab is read from, and what its commands may name.
export struct Host {
    /// The directory of the crontab, for CRON_TZFILE paths
    export dir: str
    /// The directory of the running cron executable, `$CRON_DIR`
    export cron_dir: str
    /// The lock file the daemon takes, `$CRON_LOCK`
    export lock: str
}

/// Reads `text`, the crontab at `path`. Zone lines load their zones with
/// `io`. Every problem is in the result's `problems`; only memory can
/// fail the call. O(n) in the text plus the zones it loads; everything it
/// returns lives in `a`.
export Parse(a: &Arena, io: Io, path: str, text: str, host: Host): OutOfMemory!Crontab from a {
    var tab := Crontab(
        path: try a.Dup(path),
        vars: List[Var].New(a),
        zones: List[ZoneDef].New(a),
        entries: List[Entry].New(a),
        problems: List[Problem].New(a),
    )
    try tab.vars.Push(Var(name: "CRON_DIR", value: try a.Dup(host.cron_dir)))
    try tab.vars.Push(Var(name: "CRON_LOCK", value: try a.Dup(host.lock)))
    try tab.vars.Push(Var(name: "CRON_TAB", value: tab.path))
    var zone := -1
    var n := 0
    for raw in text.Lines() {
        n += 1
        line := raw.Trim()
        if line.Len() == 0 || line.StartsWith("#") {
            continue
        }
        if eq := Assignment(line) {
            name := line[..eq]
            value := line[eq + 1..].Trim()
            if name == "CRON_TZ" || name == "CRON_TZFILE" {
                if z := try LoadZone(a, io, mut tab, &host, n, name, value) {
                    zone = z
                }
                continue
            }
            try tab.vars.Push(Var(name: try a.Dup(name), value: try a.Dup(value)))
            continue
        }
        if zone < 0 {
            zone = try LocalZone(a, io, mut tab, n)
        }
        try ReadEntry(a, mut tab, n, line, zone)
    }
    return tab
}

/// The offset of `=` when `line` is `NAME=value` with NAME a shell-style
/// name; none otherwise.
Assignment(line: str): ?int {
    eq := line.Find("=") else {
        return none
    }
    if eq == 0 {
        return none
    }
    for i in 0..eq {
        c := line.Byte(i)
        letter := (c >= b'A' && c <= b'Z') || (c >= b'a' && c <= b'z') || c == b'_'
        digit := c >= b'0' && c <= b'9'
        if !letter && !(digit && i > 0) {
            return none
        }
    }
    return some(eq)
}

/// Adds a problem at `line`.
Report(tab: mut Crontab, line: int, message: str into tab): OutOfMemory!void {
    try tab.problems.Push(Problem(line: line, message: message))
}

/// The value of a variable, the last one set winning; none when unset.
export Lookup(tab: &Crontab, name: str): ?str from tab {
    var found: ?str = none
    for v in &tab.vars {
        if v.name == name {
            found = some(v.value)
        }
    }
    return found
}

/// Finds an entry by name.
export Find(tab: &Crontab, name: str): ?&Entry from tab {
    for e in &tab.entries {
        if e.name == name {
            return some(e)
        }
    }
    return none
}
