// Why: a crontab can be checked and its schedule previewed without running
// anything.
//
// What: Check, NextCommand and ZoneCommand (`check`, `next`, `zone`).
//
// How: each parses the crontab and prints from it; none takes the lock.

import std.time
import src.crontab
import src.daemon
import src.sched

/// Lists the crontab's zones and entries.
Check(c: &Ctx, tab: &crontab.Crontab): int {
    sys := c.sys
    n := tab.entries.Len()
    z := tab.zones.Len()
    sys.Print("{tab.path}: entries {n}, zones {z}\n", (tab, n, z)) else {
        return 1
    }
    for zone in &tab.zones {
        source := match zone.source {
            .Named { "named" }
            .Local { "the host's zone" }
            .File { "TZif file" }
        }
        sys.Print("zone {zone.name} ({source})\n", (zone, source)) else {
            return 1
        }
    }
    for e in &tab.entries {
        line := Describe(sys.arena, tab, e) else {
            return 4
        }
        sys.Print("{line}\n", (line)) else {
            return 1
        }
    }
    return 0
}

/// One line about an entry: name, schedule, zone, options and command.
Describe(a: &Arena, tab: &crontab.Crontab, e: &crontab.Entry): OutOfMemory!str from a {
    var t := Text.New(a)
    zone := tab.zones[e.zone].name
    try t.Push(try a.Fmt("{e.name}: {e.when} in {zone}", (e, zone)))
    if d := e.timeout {
        try t.Push(try a.Fmt(", timeout {d}", (d)))
    }
    if e.detach {
        try t.Push(", detached")
    }
    if text := e.input {
        try t.Push(try a.Fmt(", input {text}", (text)))
    }
    try t.Push(" --")
    for w in e.argv {
        try t.Push(try a.Fmt(" {w}", (w)))
    }
    if e.then.Len() > 0 {
        try t.Push(" |")
        for w in e.then {
            try t.Push(try a.Fmt(" {w}", (w)))
        }
    }
    return t.Done()
}

/// Prints the next `--count` fires of each entry after `--from`.
NextCommand(c: &Ctx, tab: &crontab.Crontab): int {
    sys := c.sys
    from := Start(c) else {
        return 2
    }
    count := c.opts.count else { 5 }
    if count < 1 || count > 10_000 {
        sys.PrintErr("cron: --count is from 1 to 10000\n") else {}
        return 2
    }
    for e in &tab.entries {
        zone := &tab.zones[e.zone]
        sys.Print("{e.name} ({e.when}, {zone.name}):\n", (e, zone)) else {
            return 1
        }
        var after := from
        for _ in 0..count {
            f := daemon.Following(&e.schedule, &zone.zone, after) else {
                sys.Print("  never\n") else {
                    return 1
                }
                break
            }
            line := sched.Show(sys.arena, &f, &zone.zone) else {
                return 4
            }
            sys.Print("  {line}\n", (line)) else {
                return 1
            }
            after = f.at
        }
    }
    return 0
}

/// `--from`, or the wall clock.
Start(c: &Ctx): ?time.Time {
    text := c.opts.from else {
        return some(time.Wall(c.sys.io))
    }
    t := time.ParseRfc3339(text) else e {
        c.sys.PrintErr("cron: --from {text}: {e}; write it as 2026-03-29T00:00:00Z\n", (text, e)) else {}
        return none
    }
    return some(t)
}

/// Prints a zone's transitions in `--year`.
ZoneCommand(c: &Ctx, name: str): int {
    sys := c.sys
    a := sys.arena
    io := sys.io
    var shown := name
    zone := if name == "local" {
        shown = time.LocalName(a, io) else { "local" }
        time.Zone.Local(a, io) else e {
            sys.PrintErr("cron: the host's zone: {e}\n", (e)) else {}
            return 1
        }
    } else {
        time.Zone.Load(a, io, name, crontab.Embedded) else e {
            hint := crontab.ZoneHint(e)
            sys.PrintErr("cron: zone {name}: {e} ({hint})\n", (name, e, hint)) else {}
            return 2
        }
    }
    year := if y := c.opts.year {
        if y < 1900 || y > 2200 {
            sys.PrintErr("cron: --year is from 1900 to 2200\n") else {}
            return 2
        }
        Cast[i32](y)
    } else {
        time.Wall(io).In(&zone).year
    }
    changes := sched.Transitions(a, &zone, year) else {
        return 4
    }
    n := changes.Len()
    sys.Print("{shown} in {year}: {n} transitions\n", (shown, year, n)) else {
        return 1
    }
    for ch in &changes {
        before := sched.FormatOffset(a, ch.from_offset) else {
            return 4
        }
        after := sched.FormatOffset(a, ch.to_offset) else {
            return 4
        }
        old := ch.at.Add(time.Seconds(ch.from_offset)).In(&time.Zone.Utc())
        now := ch.at.In(&zone)
        // Lord Howe moves its clocks by half an hour
        shift := time.Seconds(ch.to_offset - ch.from_offset).Nanos() / time.Minutes(1).Nanos()
        sign := if shift > 0 { "+" } else { "" }
        sys.Print(
            "  {ch.at}  {before} -> {after}  local {old.hour:02}:{old.minute:02} becomes {now.hour:02}:{now.minute:02} ({sign}{shift} min)\n",
            (ch, before, after, old, now, sign, shift),
        ) else {
            return 1
        }
    }
    return 0
}
