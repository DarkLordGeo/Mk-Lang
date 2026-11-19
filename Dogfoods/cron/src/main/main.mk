// Why: cron is a scheduler daemon with a zone per entry; this module is its
// entry point.
//
// What: Main, Options, Ctx and the usage text.
//
// How: `gen cli.Args` parses the command line. Exit codes: 0 done; 1 an I/O
// failure; 2 a bad command line or crontab; 3 another instance holds the
// lock; 4 the memory budget was exceeded.

import std.cli
import std.fs
import src.crontab

struct Options {
    /// Print this help and exit
    export help: bool
    /// next: start from this instant, RFC 3339 (default: now)
    export from: ?str
    /// next: fires to print for each entry (default 5)
    export count: ?int
    /// zone: the year to list (default: this year)
    export year: ?int
    /// run, sim: the lock file (default: cron.lock)
    export lock: ?str
    /// run: wait this long for the lock instead of refusing at once
    export lock_wait: ?str
    /// run, sim: the job log (default: cron.log)
    export log: ?str
    /// run: stop after this long, such as 90s (default: at an interrupt)
    export stop_after: ?str
    /// run: copy every log record to standard error
    export echo: bool
    /// run: jobs that may run at once (default 16)
    export max_jobs: ?int
    /// run, sim: bytes the job table may use (default: no limit)
    export max_memory: ?int
    /// sim: days to simulate (default 365)
    export days: ?int
    /// sim: the simulation's seed (default 1)
    export seed: ?int
    /// sim: a fault to inject: log-write or lock-open
    export fault: ?str
    /// sim: print every fire, not only those at a transition
    export verbose: bool
    /// check|next|run|sim <crontab>, or zone <name>
    export rest: []str
}

gen cli.Args(Options)

const Overview = """
    usage: cron <command> [options] <crontab>

    commands:
      check <crontab>   read the crontab and list its entries and zones
      next <crontab>    print the next fires of each entry
      zone <name>       print a zone's daylight-saving transitions in a year
      run <crontab>     run the jobs as they fall due (the daemon)
      sim <crontab>     run a year of the schedule under a simulated clock

    """

/// Everything a command needs from the process.
struct Ctx {
    sys: &System
    opts: &Options
}

export Main(sys: &System): int {
    opts := Options.Parse(sys.arena, sys.args[1..]) else e {
        BadArgs(sys, e)
        return 2
    }
    if opts.help {
        Usage(sys) else {
            return 1
        }
        return 0
    }
    if opts.rest.Len() != 2 {
        sys.PrintErr("cron: expected a command and one argument; try cron --help\n") else {}
        return 2
    }
    c := Ctx(sys: sys, opts: &opts)
    command := opts.rest[0]
    arg := opts.rest[1]
    return match command {
        "zone" { ZoneCommand(&c, arg) }
        "check" | "next" | "run" | "sim" { WithCrontab(&c, command, arg) }
        _ {
            sys.PrintErr("cron: unknown command {command}; try cron --help\n", (command)) else {}
            2
        }
    }
}

Usage(sys: &System): WriteError!void {
    var out := sys.Stdout()
    try out.Str(Overview)
    try Options.Usage(mut out)
}

/// Reads the crontab and runs `command` over it.
WithCrontab(c: &Ctx, command: str, path: str): int {
    sys := c.sys
    tab := Load(sys, c.opts, path) else {
        return 2
    }
    if tab.problems.Len() > 0 {
        for p in &tab.problems {
            sys.PrintErr("{path}:{p.line}: {p.message}\n", (path, p)) else {}
        }
        return 2
    }
    return match command {
        "check" { Check(c, &tab) }
        "next" { NextCommand(c, &tab) }
        "run" { RunCommand(c, &tab) }
        _ { SimCommand(c, &tab) }
    }
}

/// Reads and parses the crontab at `path`, reporting a file it cannot read.
Load(sys: &System, opts: &Options, path: str): ?crontab.Crontab from sys {
    text := fs.ReadText(sys.arena, sys.io, path) else e {
        sys.PrintErr("cron: {path}: {e}\n", (path, e)) else {}
        return none
    }
    self_path := sys.args[0]
    host := crontab.Host(dir: crontab.DirOf(path), cron_dir: crontab.DirOf(self_path), lock: LockPath(opts))
    tab := crontab.Parse(sys.arena, sys.io, path, text, host) else {
        sys.PrintErr("cron: {path}: out of memory\n", (path)) else {}
        return none
    }
    return some(tab)
}

/// Says which argument the command line parser refused.
BadArgs(sys: &System, e: cli.Error) {
    args := sys.args[1..]
    match e {
        cli.Unknown(arg) {
            word := args[arg]
            sys.PrintErr("cron: unknown option {word}; try cron --help\n", (word)) else {}
        }
        cli.Invalid(arg) {
            word := args[arg]
            sys.PrintErr("cron: bad value for {word}; try cron --help\n", (word)) else {}
        }
        _ { sys.PrintErr("cron: {e}; try cron --help\n", (e)) else {} }
    }
}
